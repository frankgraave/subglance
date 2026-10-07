package checker

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// DNS record types a dns monitor can compare. The list is short on purpose:
// these are the records whose change takes a site or its mail offline, or
// shows that someone else now controls the zone.
const (
	DNSRecordA     = "A"
	DNSRecordAAAA  = "AAAA"
	DNSRecordCNAME = "CNAME"
	DNSRecordMX    = "MX"
	DNSRecordTXT   = "TXT"
)

// dnsQueryTypes maps each supported record type to its wire type.
var dnsQueryTypes = map[string]dnsmessage.Type{
	DNSRecordA:     dnsmessage.TypeA,
	DNSRecordAAAA:  dnsmessage.TypeAAAA,
	DNSRecordCNAME: dnsmessage.TypeCNAME,
	DNSRecordMX:    dnsmessage.TypeMX,
	DNSRecordTXT:   dnsmessage.TypeTXT,
}

// DNSRecordTypes lists the record types a dns monitor accepts, in the order a
// form offers them.
func DNSRecordTypes() []string {
	return []string{DNSRecordA, DNSRecordAAAA, DNSRecordCNAME, DNSRecordMX, DNSRecordTXT}
}

// ValidDNSRecordType reports whether a record type is one a dns monitor can
// compare. It is case-sensitive: the API stores the canonical upper-case
// spelling, so "a" is refused rather than silently rewritten.
func ValidDNSRecordType(t string) bool {
	_, ok := dnsQueryTypes[t]
	return ok
}

// ValidateDNSExpected checks one expected value against its record type, so a
// value that can never match is refused when the monitor is saved instead of
// failing every check after it.
func ValidateDNSExpected(recordType, value string) error {
	v := strings.TrimSpace(value)
	if v == "" {
		return errors.New("an expected value cannot be empty")
	}
	switch recordType {
	case DNSRecordA:
		a, err := netip.ParseAddr(v)
		if err != nil || !a.Unmap().Is4() {
			return fmt.Errorf("%q is not an IPv4 address", value)
		}
	case DNSRecordAAAA:
		a, err := netip.ParseAddr(v)
		if err != nil || !a.Is6() || a.Is4In6() {
			return fmt.Errorf("%q is not an IPv6 address", value)
		}
	case DNSRecordCNAME:
		if !validDNSName(v) {
			return fmt.Errorf("%q is not a host name", value)
		}
	case DNSRecordMX:
		if _, _, _, ok := parseMX(v); !ok {
			return fmt.Errorf("%q is not a mail host; write mail.example.com, or 10 mail.example.com to check the preference too", value)
		}
	case DNSRecordTXT:
		// Any text is a possible TXT record.
	default:
		return fmt.Errorf("unknown record type %q", recordType)
	}
	return nil
}

// validDNSName reports whether s can be sent as a query name: labels of 1 to
// 63 characters, 253 in all, without spaces. Underscores are allowed because
// _dmarc and _domainkey names are exactly what a TXT check is pointed at.
func validDNSName(s string) bool {
	s = strings.TrimSuffix(s, ".")
	if s == "" || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			ok := r == '-' || r == '_' ||
				(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
			if !ok {
				return false
			}
		}
	}
	return true
}

// ValidDNSName reports whether a dns monitor can query a name. A URL or a
// name with a port is not one; the API names the part to drop.
func ValidDNSName(s string) bool { return validDNSName(s) }

// DNSChecker asks a resolver for one record type of one name and compares the
// answer with what the monitor expects.
//
// # Why it builds its own queries
//
// net.Resolver answers questions about addresses, not about records. Its
// LookupCNAME returns the name a chain of aliases finally ends at, so a check
// that www points at an alias of a CDN would compare against the CDN's
// internal name and fail on a zone that is configured correctly. A query of
// its own asks for exactly the record type the monitor names and reads exactly
// what the resolver answered, through a resolver the monitor may choose.
//
// # Which resolver
//
// Without a resolver set, the check asks the host's own, read from
// /etc/resolv.conf: the answer the rest of the host would get. A resolver set
// on the monitor (an authoritative server, or 1.1.1.1 to see past a local
// cache) is dialled through the SSRF guard like any other target. The host's
// own resolver is not, because it is the operator's configuration rather than
// something a user typed, and inside a container it is a loopback address the
// guard would refuse.
type DNSChecker struct {
	guarded *net.Dialer
	plain   *net.Dialer

	// systemServers returns the host's resolvers as host:port. Tests replace
	// it with a fake server.
	systemServers func() ([]string, error)
}

// NewDNSChecker returns a checker that sends queries to a monitor's own
// resolver only through the SSRF guard.
func NewDNSChecker(guard *Guard) *DNSChecker {
	if guard == nil {
		guard = NewGuard(false) // fail closed
	}
	return &DNSChecker{
		guarded:       &net.Dialer{Control: guard.ControlFunc()},
		plain:         &net.Dialer{},
		systemServers: resolvConfServers,
	}
}

// resolvConfPath is a variable so a test can point it at a fixture.
var resolvConfPath = "/etc/resolv.conf"

// resolvConfServers reads the nameserver lines of /etc/resolv.conf.
func resolvConfServers() ([]string, error) {
	data, err := os.ReadFile(resolvConfPath)
	if err != nil {
		return nil, fmt.Errorf("read the system resolver from %s: %w; set a resolver on this monitor", resolvConfPath, err)
	}
	servers := parseResolvConf(string(data))
	if len(servers) == 0 {
		return nil, fmt.Errorf("%s names no nameserver; set a resolver on this monitor", resolvConfPath)
	}
	return servers, nil
}

// parseResolvConf returns every nameserver address in a resolv.conf, as
// host:port with port 53.
func parseResolvConf(conf string) []string {
	var out []string
	for _, line := range strings.Split(conf, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		addr, err := netip.ParseAddr(fields[1])
		if err != nil {
			continue
		}
		out = append(out, net.JoinHostPort(addr.String(), "53"))
	}
	return out
}

// Check sends one query and compares the answer.
func (c *DNSChecker) Check(ctx context.Context, m Monitor) Result {
	start := time.Now()

	name := strings.TrimSuffix(strings.TrimSpace(m.Target), ".")
	if !validDNSName(name) {
		return fail(start, FailInternal, "%q is not a name a DNS query can ask for", m.Target)
	}
	qtype, known := dnsQueryTypes[m.DNSRecordType]
	if !known {
		return fail(start, FailInternal, "unknown DNS record type %q", m.DNSRecordType)
	}

	timeout := m.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	servers, dialer, err := c.servers(m.DNSResolver)
	if err != nil {
		return fail(start, FailInternal, "%v", err)
	}

	var (
		answer dnsmessage.Message
		server string
	)
	for i, s := range servers {
		// Each resolver but the last gets an even share of what is left of
		// the budget, the way the libc resolver splits its timeout, so a
		// silent first nameserver cannot spend all of it and leave a working
		// second one unasked. The check's own deadline stays the bound.
		attempt, cancelAttempt := ctx, context.CancelFunc(func() {})
		if deadline, ok := ctx.Deadline(); ok && i < len(servers)-1 {
			share := time.Until(deadline) / time.Duration(len(servers)-i)
			attempt, cancelAttempt = context.WithTimeout(ctx, share)
		}
		answer, err = exchange(attempt, dialer, s, name, qtype)
		cancelAttempt()
		server = s
		if err == nil || ctx.Err() != nil || i == len(servers)-1 {
			break
		}
	}
	if err != nil {
		return classifyResolverError(start, ctx, server, err)
	}

	switch answer.RCode {
	case dnsmessage.RCodeSuccess:
	case dnsmessage.RCodeNameError:
		return fail(start, FailDNS, "DNS lookup failed: %s does not exist (NXDOMAIN from %s)", name, server)
	default:
		return fail(start, FailDNS, "DNS lookup failed: %s answered %s for %s", server, rcodeWord(answer.RCode), name)
	}

	got := recordsOf(answer, qtype, name)
	if dnsMatches(m.DNSRecordType, m.DNSExpected, got) {
		return ok(start, 0)
	}
	return fail(start, FailDNSMismatch, "%s", describeDNSMismatch(m.DNSRecordType, name, m.DNSExpected, got))
}

// servers picks where to send the query and which dialer may reach it.
func (c *DNSChecker) servers(resolver string) ([]string, *net.Dialer, error) {
	resolver = strings.TrimSpace(resolver)
	if resolver == "" {
		servers, err := c.systemServers()
		return servers, c.plain, err
	}
	host, port, err := ParseHostPort(resolver, 53)
	if err != nil {
		return nil, nil, fmt.Errorf("resolver %q: %w", resolver, err)
	}
	return []string{net.JoinHostPort(host, strconv.Itoa(port))}, c.guarded, nil
}

// classifyResolverError words a query that got no answer at all. The guard's
// refusal and a timeout read the same as on every other check; anything else
// is a resolver that could not be asked, which is a DNS failure.
func classifyResolverError(start time.Time, ctx context.Context, server string, err error) Result {
	// The socket carries the context's deadline, so its read can give up a
	// moment before the context's own timer has fired and ctx.Err() says so.
	// That is the same timeout, and must read as one.
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return fail(start, FailTimeout, "timed out after %s", time.Since(start).Truncate(time.Millisecond))
	}
	if errors.Is(err, ErrPrivateTarget) || errors.Is(err, context.DeadlineExceeded) ||
		ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return classifyRequestError(start, ctx, err)
	}
	return fail(start, FailDNS, "DNS lookup failed: no answer from %s: %v", server, err)
}

// rcodeWord names a response code the way a DNS tool prints it.
func rcodeWord(rc dnsmessage.RCode) string {
	s := strings.TrimPrefix(rc.String(), "RCode")
	return strings.ToUpper(s)
}

// dnsUDPSize is the EDNS0 buffer size offered: the size DNS Flag Day 2020
// settled on, large enough for the TXT sets a domain usually has and small
// enough not to fragment.
const dnsUDPSize = 1232

// exchange sends one query over UDP, and again over TCP when the answer did
// not fit.
func exchange(ctx context.Context, d *net.Dialer, server, name string, qtype dnsmessage.Type) (dnsmessage.Message, error) {
	id, query, err := buildQuery(name, qtype)
	if err != nil {
		return dnsmessage.Message{}, err
	}
	msg, err := exchangeOver(ctx, d, "udp", server, id, query, name, qtype)
	if err != nil {
		return msg, err
	}
	if msg.Truncated {
		return exchangeOver(ctx, d, "tcp", server, id, query, name, qtype)
	}
	return msg, nil
}

func buildQuery(name string, qtype dnsmessage.Type) (uint16, []byte, error) {
	var idBytes [2]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return 0, nil, fmt.Errorf("query id: %w", err)
	}
	id := binary.BigEndian.Uint16(idBytes[:])

	qname, err := dnsmessage.NewName(name + ".")
	if err != nil {
		return 0, nil, fmt.Errorf("query name %q: %w", name, err)
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, RecursionDesired: true})
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return 0, nil, err
	}
	if err := b.Question(dnsmessage.Question{Name: qname, Type: qtype, Class: dnsmessage.ClassINET}); err != nil {
		return 0, nil, err
	}
	if err := b.StartAdditionals(); err != nil {
		return 0, nil, err
	}
	var opt dnsmessage.ResourceHeader
	if err := opt.SetEDNS0(dnsUDPSize, dnsmessage.RCodeSuccess, false); err != nil {
		return 0, nil, err
	}
	if err := b.OPTResource(opt, dnsmessage.OPTResource{}); err != nil {
		return 0, nil, err
	}
	out, err := b.Finish()
	return id, out, err
}

func exchangeOver(ctx context.Context, d *net.Dialer, network, server string, id uint16, query []byte, name string, qtype dnsmessage.Type) (dnsmessage.Message, error) {
	conn, err := d.DialContext(ctx, network, server)
	if err != nil {
		return dnsmessage.Message{}, err
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	var raw []byte
	if network == "tcp" {
		framed := make([]byte, 2+len(query))
		binary.BigEndian.PutUint16(framed, uint16(len(query))) // #nosec G115 -- a built query is far below 64 KiB
		copy(framed[2:], query)
		if _, err := conn.Write(framed); err != nil {
			return dnsmessage.Message{}, err
		}
		var size [2]byte
		if _, err := io.ReadFull(conn, size[:]); err != nil {
			return dnsmessage.Message{}, err
		}
		raw = make([]byte, binary.BigEndian.Uint16(size[:]))
		if _, err := io.ReadFull(conn, raw); err != nil {
			return dnsmessage.Message{}, err
		}
		return parseAnswer(raw, id, name, qtype)
	}

	if _, err := conn.Write(query); err != nil {
		return dnsmessage.Message{}, err
	}
	buf := make([]byte, 65535)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return dnsmessage.Message{}, err
		}
		// A datagram that is not the answer to this query (a late answer
		// to an earlier one, or a spoofing attempt) is ignored rather than
		// believed; the deadline still bounds the wait.
		msg, perr := parseAnswer(buf[:n], id, name, qtype)
		if perr == nil {
			return msg, nil
		}
	}
}

// parseAnswer decodes a response and refuses one that does not answer the
// question that was asked.
func parseAnswer(raw []byte, id uint16, name string, qtype dnsmessage.Type) (dnsmessage.Message, error) {
	var msg dnsmessage.Message
	if err := msg.Unpack(raw); err != nil {
		return msg, fmt.Errorf("unreadable answer: %w", err)
	}
	if !msg.Response || msg.ID != id {
		return msg, errors.New("answer does not match the query")
	}
	if len(msg.Questions) != 1 || msg.Questions[0].Type != qtype ||
		!sameDNSName(msg.Questions[0].Name.String(), name) {
		return msg, errors.New("answer is for a different question")
	}
	return msg, nil
}

// recordsOf collects the answer's records of the asked type, written the way
// expected values are.
//
// A, AAAA, MX and TXT are taken from the whole answer section, because a
// resolver asked about an alias answers with the chain and then the records
// at its end, and those are the records a client would use. A CNAME is taken
// only where it belongs to the asked name: that is the record the zone holds.
func recordsOf(msg dnsmessage.Message, qtype dnsmessage.Type, name string) []string {
	var out []string
	for _, rr := range msg.Answers {
		if rr.Header.Type != qtype {
			continue
		}
		switch body := rr.Body.(type) {
		case *dnsmessage.AResource:
			out = append(out, netip.AddrFrom4(body.A).String())
		case *dnsmessage.AAAAResource:
			out = append(out, netip.AddrFrom16(body.AAAA).String())
		case *dnsmessage.CNAMEResource:
			if sameDNSName(rr.Header.Name.String(), name) {
				out = append(out, canonicalDNSName(body.CNAME.String()))
			}
		case *dnsmessage.MXResource:
			host := canonicalDNSName(body.MX.String())
			if host == "" {
				host = "." // a null MX
			}
			out = append(out, strconv.Itoa(int(body.Pref))+" "+host)
		case *dnsmessage.TXTResource:
			// A long TXT record arrives as several strings of at most 255
			// bytes; SPF and DKIM read them joined, and so does this.
			out = append(out, strings.Join(body.TXT, ""))
		}
	}
	return out
}

func canonicalDNSName(s string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
}

func sameDNSName(a, b string) bool { return canonicalDNSName(a) == canonicalDNSName(b) }

// parseMX reads an MX value: a host, or a preference and a host. hasPref is
// false when the value names only the host, and ok false when it is neither.
//
// The host "." is accepted: it is the null MX of RFC 7505, the record a domain
// publishes to say it receives no mail, and checking that it stays published
// is a reasonable thing to monitor. A lone number is refused rather than read
// as a host, because it is a preference whose host was left out.
func parseMX(v string) (pref int, host string, hasPref, ok bool) {
	mxHost := func(s string) (string, bool) {
		if s == "." {
			return ".", true
		}
		if _, err := strconv.Atoi(s); err == nil {
			return "", false
		}
		return canonicalDNSName(s), validDNSName(s)
	}
	fields := strings.Fields(v)
	switch len(fields) {
	case 1:
		if h, valid := mxHost(fields[0]); valid {
			return 0, h, false, true
		}
	case 2:
		n, err := strconv.Atoi(fields[0])
		h, valid := mxHost(fields[1])
		if err == nil && n >= 0 && n <= 65535 && valid {
			return n, h, true, true
		}
	}
	return 0, "", false, false
}

// dnsMatches decides whether the answer is what the monitor expects.
//
// No expected values means only that a record of the type must exist.
//
// For A, AAAA, CNAME and MX the answer must be exactly the expected set, in
// any order: an extra address or mail host is how a hijacked zone looks, and
// a check that only asked whether the right one was still there would pass
// it. TXT is the exception. A name's TXT records are shared between unrelated
// uses (an SPF policy, a site-verification token for every service that ever
// asked), so each expected value must be present and the others are allowed;
// an exact match would fail the day someone verified a new service.
func dnsMatches(recordType string, expected, got []string) bool {
	if len(expected) == 0 {
		return len(got) > 0
	}
	exact := recordType != DNSRecordTXT
	if exact && len(got) != len(expected) {
		return false
	}
	used := make([]bool, len(got))
	for _, want := range expected {
		found := false
		for i, have := range got {
			if !used[i] && dnsValueMatches(recordType, want, have) {
				used[i], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// dnsValueMatches compares one expected value with one record as it is
// written by recordsOf.
func dnsValueMatches(recordType, want, have string) bool {
	want = strings.TrimSpace(want)
	switch recordType {
	case DNSRecordA, DNSRecordAAAA:
		w, err1 := netip.ParseAddr(want)
		h, err2 := netip.ParseAddr(have)
		return err1 == nil && err2 == nil && w.Unmap() == h.Unmap()
	case DNSRecordCNAME:
		return sameDNSName(want, have)
	case DNSRecordMX:
		wp, wh, wantPref, wok := parseMX(want)
		hp, hh, _, hok := parseMX(have)
		if !wok || !hok || wh != hh {
			return false
		}
		return !wantPref || wp == hp
	default:
		return want == have
	}
}

// maxDNSValueShown caps one value in a failure message. A DKIM key is a
// kilobyte of base64, and the message is read on a phone.
const maxDNSValueShown = 80

// describeDNSMismatch writes the expected and the received records side by
// side, which is the whole answer to "what changed".
func describeDNSMismatch(recordType, name string, expected, got []string) string {
	list := func(values []string) string {
		shown := make([]string, 0, len(values))
		for _, v := range values {
			if len(v) > maxDNSValueShown {
				v = v[:maxDNSValueShown] + "…"
			}
			if recordType == DNSRecordTXT {
				v = strconv.Quote(v)
			}
			shown = append(shown, v)
		}
		slices.Sort(shown)
		return strings.Join(shown, ", ")
	}
	if len(got) == 0 {
		if len(expected) == 0 {
			return fmt.Sprintf("no %s record for %s", recordType, name)
		}
		return fmt.Sprintf("no %s record for %s; expected %s", recordType, name, list(expected))
	}
	if recordType == DNSRecordTXT {
		return fmt.Sprintf("%s records for %s do not include %s; got %s", recordType, name, list(expected), list(got))
	}
	return fmt.Sprintf("%s records for %s: expected %s, got %s", recordType, name, list(expected), list(got))
}
