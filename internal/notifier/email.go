package notifier

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
)

// EmailSender delivers an alert over SMTP.
//
// E-mail is the channel that works without signing up for anything, on a
// server that already exists, reaching a person who is not sitting in a chat
// application at 03:00. It ships in the free build like every other channel:
// an uptime monitor that cannot send mail unless you pay is not a monitor,
// it is a demonstration.
type EmailSender struct {
	// dial is swappable so tests can exercise the delivery path against a
	// local listener instead of a real mail server.
	dial func(ctx context.Context, addr string) (net.Conn, error)
}

// NewEmailSender builds an e-mail channel. The guard may be nil, which means
// the SMTP host is not restricted.
func NewEmailSender(guard *checker.Guard) *EmailSender {
	return &EmailSender{dial: guardedDialer(guard)}
}

// guardedDialer returns the dial function the SMTP path uses.
//
// The SMTP host is operator-supplied just like a webhook URL, so it gets the
// same treatment: a channel pointed at 127.0.0.1:2375 would otherwise have
// SubGlance open a TCP connection to the Docker socket's port and speak
// whatever the far end reads as SMTP. The check sits in the dialer's Control
// hook so it sees the resolved address rather than the name.
func guardedDialer(guard *checker.Guard) func(ctx context.Context, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: defaultTimeout}
	if guard != nil {
		d.Control = guard.ControlFunc()
	}
	return func(ctx context.Context, addr string) (net.Conn, error) {
		return d.DialContext(ctx, "tcp", addr)
	}
}

// Validate checks the SMTP settings.
func (s *EmailSender) Validate(cfg map[string]string) error {
	for _, k := range []string{"host", "from", "to"} {
		if strings.TrimSpace(cfg[k]) == "" {
			return &configError{k + " is required"}
		}
	}

	port := cfg["port"]
	if port == "" {
		port = "587"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return &configError{"port must be a number between 1 and 65535"}
	}

	// A missing @ is the error people actually make, usually by putting a
	// display name in the field. Catching it here beats a rejection from
	// the mail server three hops later that names neither the field nor
	// the value.
	for _, k := range []string{"from", "to"} {
		if !strings.Contains(cfg[k], "@") {
			return &configError{k + " is not an e-mail address"}
		}
	}
	return nil
}

// Send delivers the message.
func (s *EmailSender) Send(ctx context.Context, cfg map[string]string, a Alert) error {
	host := cfg["host"]
	port := cfg["port"]
	if port == "" {
		port = "587"
	}
	addr := net.JoinHostPort(host, port)

	conn, err := s.dial(ctx, addr)
	if err != nil {
		if errors.Is(err, checker.ErrPrivateTarget) {
			// A refused address stays refused; retrying it five
			// more times only delays the operator learning why.
			return fmt.Errorf("delivery blocked: %w", err)
		}
		return retryable("connect to %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	// A deadline on the connection as well as the context: net/smtp does
	// not take a context, so without this a server that accepts the TCP
	// connection and then says nothing would hold the worker until the
	// process restarts.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(defaultTimeout))
	}

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return retryable("smtp handshake with %s: %w", host, err)
	}
	defer func() { _ = c.Close() }()

	// STARTTLS when the server offers it. Not demanded, because the common
	// self-hosted case is a relay on localhost or inside a compose network
	// where there is no certificate and no eavesdropper; refusing to send
	// there would mean no alerts at all, which is worse than an unencrypted
	// hop that never leaves the host.
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return retryable("starttls with %s: %w", host, err)
		}
	}

	if user := cfg["username"]; user != "" {
		auth := smtp.PlainAuth("", user, cfg["password"], host)
		if err := c.Auth(auth); err != nil {
			// Bad credentials are not a transient condition, and
			// retrying them twelve times is how an account gets
			// locked out.
			return fmt.Errorf("smtp authentication failed: %w", scrubSMTPPassword(err, cfg["password"]))
		}
	}

	if err := c.Mail(cfg["from"]); err != nil {
		return retryable("smtp MAIL FROM: %w", err)
	}

	recipients := splitRecipients(cfg["to"])
	for _, to := range recipients {
		if err := c.Rcpt(to); err != nil {
			return retryable("smtp RCPT TO %s: %w", to, err)
		}
	}

	w, err := c.Data()
	if err != nil {
		return retryable("smtp DATA: %w", err)
	}
	if _, err := w.Write([]byte(buildMessage(cfg["from"], recipients, a))); err != nil {
		return retryable("write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return retryable("close message: %w", err)
	}

	// Quit failing after the body was accepted does not mean the mail was
	// lost, and retrying would send it twice. A duplicate alert is worse
	// than a missing goodbye.
	_ = c.Quit()
	return nil
}

// splitRecipients parses the comma-separated recipient list.
func splitRecipients(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// buildMessage renders the RFC 5322 message.
//
// Plain text, no HTML alternative. An alert is four lines that have to be
// readable in a notification preview, on a phone, in a terminal mail client,
// and in whatever the operator actually uses — and plain text is the only
// format that is legible in all of them.
func buildMessage(from string, to []string, a Alert) string {
	var b strings.Builder

	b.WriteString("From: SubGlance <" + from + ">\r\n")
	b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	b.WriteString("Subject: " + sanitiseHeader(a.Title()) + "\r\n")
	b.WriteString("Date: " + a.At.UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	// Threading: every alert about one incident shares a reference, so a
	// mail client groups the reminder with the original and the recovery
	// with both, instead of showing three unrelated messages.
	if a.IncidentID != 0 {
		ref := fmt.Sprintf("<incident-%d@subglance.local>", a.IncidentID)
		b.WriteString("References: " + ref + "\r\n")
		b.WriteString("In-Reply-To: " + ref + "\r\n")
	}
	b.WriteString("\r\n")

	b.WriteString(a.Title() + "\r\n\r\n")
	b.WriteString(strings.ReplaceAll(a.Body(), "\n", "\r\n") + "\r\n")
	return b.String()
}

// sanitiseHeader strips CR and LF from a header value.
//
// A monitor name is operator-supplied text that lands in a Subject line. A
// newline in it would end the header and let the rest be read as more headers
// — the classic header-injection route to sending mail somewhere nobody asked
// for.
func sanitiseHeader(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

// scrubSMTPPassword keeps a password out of an error message.
func scrubSMTPPassword(err error, password string) error {
	if password == "" {
		return err
	}
	return &configError{strings.ReplaceAll(err.Error(), password, "password")}
}
