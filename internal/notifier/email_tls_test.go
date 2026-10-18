package notifier

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/state"
)

// relayCert is a self-signed certificate for name, and a pool that trusts it.
func relayCert(t *testing.T, name string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		DNSNames:              []string{name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

// startTLSRelay is a recording relay that speaks TLS from the first byte,
// as a submission server on port 465 does. It offers STARTTLS and AUTH as
// well, so a client that tried to upgrade a second time would be seen.
func startTLSRelay(t *testing.T, cert tls.Certificate) *recordingRelay {
	t.Helper()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ln := tls.NewListener(raw, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	return serveRelay(t, ln, "250-relay\r\n250-AUTH PLAIN\r\n250 STARTTLS")
}

// senderTo is an email sender whose every connection goes to r, whatever
// host and port the channel names: a test cannot bind port 465, and the
// relay's certificate is for a name that does not resolve.
func senderTo(r *recordingRelay, roots *x509.CertPool) (*EmailSender, *string) {
	dialled := new(string)
	return &EmailSender{
		roots: roots,
		dial: func(ctx context.Context, addr string) (net.Conn, error) {
			*dialled = addr
			var d net.Dialer
			return d.DialContext(ctx, "tcp", net.JoinHostPort(r.host, r.port))
		},
	}, dialled
}

func relayConfig(port string) map[string]string {
	return map[string]string{
		"host": "relay.example.com", "port": port,
		"from": "sg@example.com", "to": "ops@example.com",
		"username": "sg", "password": "hunter2-hunter2",
	}
}

// A relay on port 465 sends nothing until it has seen a TLS handshake. It
// used to be spoken to in plain SMTP like every other port, and every alert
// waited out the deadline for a greeting that never came, then failed as
// "i/o timeout".
func TestEmailOnPort465SpeaksTLSFromTheFirstByte(t *testing.T) {
	for _, port := range []string{"465", "0465"} {
		t.Run(port, func(t *testing.T) {
			cert, roots := relayCert(t, "relay.example.com")
			r := startTLSRelay(t, cert)
			s, dialled := senderTo(r, roots)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := s.Send(ctx, relayConfig(port), Alert{MonitorName: "api", Event: string(state.EventIncidentConfirmed)})
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
			if *dialled != "relay.example.com:"+port {
				t.Errorf("dialled %q", *dialled)
			}
			if got, want := strings.Join(r.envelope(), " | "), "MAIL FROM:<sg@example.com> | RCPT TO:<ops@example.com>"; got != want {
				t.Errorf("envelope = %q, want %q", got, want)
			}

			r.mu.Lock()
			defer r.mu.Unlock()
			authed := false
			for _, l := range r.lines {
				if strings.HasPrefix(l, "STARTTLS") {
					t.Errorf("the client asked to upgrade a session that started in TLS: %q", l)
				}
				// PlainAuth refuses to send a password over a
				// connection it does not know is encrypted.
				if strings.HasPrefix(l, "AUTH PLAIN") {
					authed = true
				}
			}
			if !authed {
				t.Errorf("no AUTH PLAIN reached the relay; lines = %q", r.lines)
			}
		})
	}
}

// The handshake on port 465 is held to the rules STARTTLS is: the
// certificate has to chain to a trusted root and name the host.
func TestEmailOnPort465RefusesACertificateItCannotVerify(t *testing.T) {
	for _, tc := range []struct {
		name     string
		certFor  string
		trust    bool
		contains string
	}{
		{"another name", "other.example.com", true, "other.example.com"},
		{"untrusted", "relay.example.com", false, "unknown authority"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cert, roots := relayCert(t, tc.certFor)
			if !tc.trust {
				roots = x509.NewCertPool()
			}
			r := startTLSRelay(t, cert)
			s, _ := senderTo(r, roots)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := s.Send(ctx, relayConfig("465"), Alert{MonitorName: "api", Event: string(state.EventIncidentConfirmed)})
			if err == nil {
				t.Fatal("delivered over a certificate that should not verify")
			}
			if !strings.Contains(err.Error(), "tls with relay.example.com on port 465") || !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("error = %v", err)
			}
			if len(r.envelope()) != 0 {
				t.Errorf("the envelope was sent: %q", r.envelope())
			}
		})
	}
}

// A relay that wants TLS from the start, reached on another port, shows
// itself only by saying nothing. The error says that, and where such a
// relay is reached, instead of a bare timeout that reads as a slow server.
func TestEmailWithoutAGreetingNamesPort465(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Silent until the client hangs up, as a TLS server
			// waiting for a ClientHello is.
			go func(c net.Conn) {
				_, _ = io.Copy(io.Discard, c)
				_ = c.Close()
			}(conn)
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err = NewEmailSender(nil).Send(ctx, map[string]string{
		"host": host, "port": port, "from": "sg@example.com", "to": "ops@example.com",
	}, Alert{MonitorName: "api", Event: string(state.EventIncidentConfirmed)})
	if err == nil {
		t.Fatal("delivered to a relay that never greeted")
	}
	if !strings.Contains(err.Error(), "no greeting on port "+port) || !strings.Contains(err.Error(), "port 465") {
		t.Errorf("error = %v", err)
	}
	var re *Retryable
	if !errors.As(err, &re) {
		t.Errorf("not retryable: %v", err)
	}
}

// Every port but 465 still upgrades with STARTTLS when the relay offers it.
// The relay here answers STARTTLS with 250 instead of 220, so a client that
// asked is told so; one that skipped it would deliver.
func TestEmailOnOtherPortsStillAsksForSTARTTLS(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	r := serveRelay(t, ln, "250-relay\r\n250 STARTTLS")
	err = NewEmailSender(nil).Send(context.Background(), map[string]string{
		"host": r.host, "port": r.port, "from": "sg@example.com", "to": "ops@example.com",
	}, Alert{MonitorName: "api", Event: string(state.EventIncidentConfirmed)})
	if err == nil || !strings.Contains(err.Error(), "starttls") {
		t.Fatalf("Send = %v, want a STARTTLS failure", err)
	}
}
