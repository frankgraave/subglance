package notifier

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/mail"
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

	// roots verifies the relay's certificate. Nil means the system pool,
	// which is what every delivery outside a test uses.
	roots *x509.CertPool
}

// smtpsPort is the port RFC 8314 (section 3.3) assigns to mail submission
// over TLS from the first byte. A relay there sends nothing until it has
// seen a TLS handshake, so speaking plain SMTP to it, as every other port
// is spoken to, waits for a greeting that never comes.
const smtpsPort = 465

// tlsConfig is the TLS configuration for host, whether the connection
// starts in TLS or upgrades to it with STARTTLS.
func (s *EmailSender) tlsConfig(host string) *tls.Config {
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: s.roots}
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
			return onKey(k, &configError{k + " is required"})
		}
	}

	port := cfg["port"]
	if port == "" {
		port = "587"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return onKey("port", &configError{"port must be a number between 1 and 65535"})
	}

	if err := smtpHostProblem(strings.TrimSpace(cfg["host"])); err != nil {
		return onKey("host", err)
	}

	// Held to the parse Send uses, so a channel that saves is one whose
	// envelope can be written. Catching it here beats a rejection from the
	// mail server three hops later that names neither the field nor the
	// value.
	if _, err := senderAddress(cfg["from"]); err != nil {
		return onKey("from", err)
	}
	if _, err := recipientAddresses(cfg["to"]); err != nil {
		return onKey("to", err)
	}
	return nil
}

// Send delivers the message.
func (s *EmailSender) Send(ctx context.Context, cfg map[string]string, a Alert) error {
	// Trimmed as Validate judged it: a channel saved with a padded host
	// would otherwise have its padding looked up as part of the name.
	host := strings.TrimSpace(cfg["host"])
	port := cfg["port"]
	if port == "" {
		port = "587"
	}
	addr := net.JoinHostPort(host, port)

	// Parsed before dialling: a stored value that no longer parses will not
	// parse on the next attempt either, so it fails once, without retries.
	from, err := senderAddress(cfg["from"])
	if err != nil {
		return err
	}
	recipients, err := recipientAddresses(cfg["to"])
	if err != nil {
		return err
	}

	conn, err := s.dial(ctx, addr)
	if err != nil {
		if errors.Is(err, checker.ErrPrivateTarget) {
			// A refused address stays refused; retrying it five
			// more times only delays the operator learning why.
			return blocked(err)
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

	// Port 465 is TLS before anything else, as the RFC 8314 relays that
	// listen there expect. The deadline set above holds the handshake too,
	// since the TLS connection reads through conn. net/smtp sees a
	// *tls.Conn and treats the session as encrypted, so PlainAuth sends the
	// password over it as it does after STARTTLS.
	// Compared as a number, as Validate parsed it, so "0465" is 465 too.
	pn, _ := strconv.Atoi(port)
	implicit := pn == smtpsPort
	if implicit {
		tc := tls.Client(conn, s.tlsConfig(host))
		if err := tc.HandshakeContext(ctx); err != nil {
			return retryable("tls with %s on port %s: %w", host, port, err)
		}
		conn = tc
	}

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		var ne net.Error
		if !implicit && errors.As(err, &ne) && ne.Timeout() {
			// The one way a relay that wants TLS from the start shows
			// itself to plain SMTP: it says nothing. A bare "i/o
			// timeout" reads as a slow or overloaded server.
			return retryable("smtp handshake with %s: no greeting on port %s before the deadline; "+
				"a relay that expects TLS from the start is reached on port %d: %w", host, port, smtpsPort, err)
		}
		return retryable("smtp handshake with %s: %w", host, err)
	}
	defer func() { _ = c.Close() }()

	// STARTTLS when the server offers it. Not demanded, because the common
	// self-hosted case is a relay on localhost or inside a compose network
	// where there is no certificate and no eavesdropper; refusing to send
	// there would mean no alerts at all, which is worse than an unencrypted
	// hop that never leaves the host. A session that started in TLS has
	// nothing to upgrade.
	if ok, _ := c.Extension("STARTTLS"); ok && !implicit {
		if err := c.StartTLS(s.tlsConfig(host)); err != nil {
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

	if err := c.Mail(addrSpec(from)); err != nil {
		return retryable("smtp MAIL FROM: %w", err)
	}

	for _, to := range recipients {
		if err := c.Rcpt(addrSpec(to)); err != nil {
			return retryable("smtp RCPT TO %s: %w", addrSpec(to), err)
		}
	}

	w, err := c.Data()
	if err != nil {
		return retryable("smtp DATA: %w", err)
	}
	if _, err := w.Write([]byte(buildMessage(from, recipients, a))); err != nil {
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

// smtpHostProblem reports why every delivery to host would fail before a
// connection is made, or nil when a connection would at least be attempted.
//
// Send joins host and the port field and hands the result to the dialer,
// which looks the name up as written. Unlike net/http it does not convert an
// internationalised name to its xn-- form, and a host that carries a port of
// its own becomes one name with a colon in it. Every such host used to be
// saved without complaint and then fail each alert as "no such host", which
// reads as an outage of a real relay rather than as a typo in the form, so the
// rule a tcp monitor's host is held to (checker.ResolvableHost) applies here,
// after the three shapes worth naming on their own.
func smtpHostProblem(host string) error {
	if strings.Contains(host, "://") {
		return &configError{"host must be the server's name or address, not a URL"}
	}
	if _, port, err := net.SplitHostPort(host); err == nil {
		if port == "" {
			return &configError{"host must not end with a colon"}
		}
		return &configError{"host must not include a port; set port to " + port + " instead"}
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return &configError{"host takes an IPv6 address without brackets"}
	}
	if err := checker.ResolvableHost(host); err != nil {
		return &configError{"host: " + err.Error()}
	}
	return nil
}

// senderAddress parses the from field as one RFC 5322 mailbox.
//
// A display name is allowed, as in "SubGlance <sg@example.com>": it is the
// form Uptime Kuma asks for, so imported channels carry it. It used to be
// written into MAIL FROM as typed, which put a name and a second pair of
// angle brackets in the envelope; the envelope now gets the bare address and
// the From header keeps the name.
func senderAddress(raw string) (*mail.Address, error) {
	v := strings.TrimSpace(raw)
	if !strings.Contains(v, "@") {
		return nil, &configError{"from is not an e-mail address"}
	}
	a, err := mail.ParseAddress(v)
	if err == nil {
		return a, nil
	}
	if list, lerr := mail.ParseAddressList(v); lerr == nil && len(list) > 1 {
		return nil, &configError{"from takes one address, not a list"}
	}
	return nil, &configError{"from is not an e-mail address"}
}

// recipientAddresses parses the to field as a comma-separated list of RFC
// 5322 mailboxes, names allowed.
//
// Splitting on commas by hand, as this used to, sent "ops@a.com; b@a.com" as
// one recipient and "Ops <ops@a.com>" as an envelope address with a name in
// it, and a line break between two addresses failed every delivery in
// net/smtp. The parser refuses all three; the first two still name the
// people they were meant for once written as the parser reads them.
func recipientAddresses(raw string) ([]*mail.Address, error) {
	v := strings.TrimSpace(raw)
	if !strings.Contains(v, "@") {
		return nil, &configError{"to is not an e-mail address"}
	}
	list, err := mail.ParseAddressList(v)
	if err == nil {
		return list, nil
	}
	if strings.ContainsAny(v, ";\r\n") {
		return nil, &configError{"to separates addresses with commas, not semicolons or line breaks"}
	}
	// Name the entry that does not parse, so a list of five says which one.
	// Parsed one by one only to find it: a quoted name may hold a comma,
	// but then the whole list parses and this is never reached.
	if parts := strings.Split(v, ","); len(parts) > 1 {
		for _, part := range parts {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			if _, perr := mail.ParseAddress(p); perr != nil {
				return nil, &configError{fmt.Sprintf("to: %q is not an e-mail address", p)}
			}
		}
	}
	return nil, &configError{"to is not an e-mail address"}
}

// addrSpec is the address as the SMTP envelope writes it: no display name and
// no angle brackets, with the local part quoted when it needs to be.
func addrSpec(a *mail.Address) string {
	s := (&mail.Address{Address: a.Address}).String()
	return strings.TrimSuffix(strings.TrimPrefix(s, "<"), ">")
}

// headerAddress is a mailbox as a header writes it: the bare address when no
// name was given, so a plain recipient list reads as it was typed.
func headerAddress(a *mail.Address) string {
	if a.Name == "" {
		return addrSpec(a)
	}
	return a.String()
}

// buildMessage renders the RFC 5322 message.
//
// Plain text, no HTML alternative. An alert is four lines that have to be
// readable in a notification preview, on a phone, in a terminal mail client,
// and in whatever the operator actually uses — and plain text is the only
// format that is legible in all of them.
func buildMessage(from *mail.Address, to []*mail.Address, a Alert) string {
	var b strings.Builder

	// SubGlance is the sender's name unless the from field gives one.
	sender := "SubGlance <" + addrSpec(from) + ">"
	if from.Name != "" {
		sender = from.String()
	}
	rcpts := make([]string, len(to))
	for i, r := range to {
		rcpts[i] = headerAddress(r)
	}
	b.WriteString("From: " + sender + "\r\n")
	b.WriteString("To: " + strings.Join(rcpts, ", ") + "\r\n")
	b.WriteString(subjectHeader(sanitiseHeader(a.Title())))
	b.WriteString("Date: " + a.At.UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	body := bodyText(a)
	// A body without this header is 7bit by definition (RFC 2045), and a
	// bullet or an accented monitor name is not. Labelling it lets a relay
	// that has to hand the message to a 7-bit hop convert it instead of
	// guessing.
	if !isASCII(body) {
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	}
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
	b.WriteString(body)
	return b.String()
}

// bodyText is the message body: the title again, the detail and the links.
func bodyText(a Alert) string {
	var b strings.Builder
	b.WriteString(a.Title() + "\r\n\r\n")
	b.WriteString(strings.ReplaceAll(a.Body(), "\n", "\r\n") + "\r\n")
	if links := linkLines(a); links != "" {
		b.WriteString("\r\n" + strings.ReplaceAll(links, "\n", "\r\n") + "\r\n")
	}
	return b.String()
}

// maxEncodedLine is the longest a header line holding an encoded word may be
// (RFC 2047, section 2).
const maxEncodedLine = 76

// subjectHeader renders the Subject line, CRLF included.
//
// A header is ASCII (RFC 5322); text outside it travels as RFC 2047 encoded
// words, which every mail client decodes. Written raw, a monitor named
// "Bücherei Zürich" reached the relay as UTF-8 bytes, which a legacy reader
// shows as mojibake, and which Postfix answers with a bounce when the next
// hop lacks SMTPUTF8, because net/smtp asks for SMTPUTF8 whenever a relay
// offers it. An ASCII title is written exactly as before. So is one that
// merely looks encoded: "=?" in a name is encoded too, or a client would
// decode the name into something else.
func subjectHeader(title string) string {
	if isPlainHeaderText(title) {
		return "Subject: " + title + "\r\n"
	}
	const open, closing = "=?utf-8?q?", "?="
	var b strings.Builder
	b.WriteString("Subject: " + open)
	line := len("Subject: " + open)
	for _, r := range title {
		enc := qEncodeRune(r)
		// The word is closed and the line folded before a character
		// that would push it past the limit; a character is never split
		// across two words (RFC 2047, section 5).
		if line+len(enc)+len(closing) > maxEncodedLine {
			b.WriteString(closing + "\r\n " + open)
			line = len(" " + open)
		}
		b.WriteString(enc)
		line += len(enc)
	}
	b.WriteString(closing + "\r\n")
	return b.String()
}

// isPlainHeaderText reports whether s can stand in a header as typed:
// printable ASCII, and nothing a reader would take for an encoded word.
func isPlainHeaderText(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < ' ' || s[i] > '~' {
			return false
		}
	}
	return !strings.Contains(s, "=?")
}

// qEncodeRune is one character in RFC 2047 "Q" encoding: printable ASCII as
// itself, a space as "_", and every other byte as =XX.
func qEncodeRune(r rune) string {
	if r == ' ' {
		return "_"
	}
	if r > ' ' && r <= '~' && r != '=' && r != '?' && r != '_' {
		return string(r)
	}
	var b strings.Builder
	for _, c := range []byte(string(r)) {
		fmt.Fprintf(&b, "=%02X", c)
	}
	return b.String()
}

// isASCII reports whether s holds only 7-bit bytes.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
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
