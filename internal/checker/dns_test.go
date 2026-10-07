package checker

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// fakeDNS is a resolver on loopback that answers from a table, over UDP and
// TCP on the same port, so the checker can be tested without a network.
type fakeDNS struct {
	t    *testing.T
	addr string

	mu      sync.Mutex
	records map[string][]dnsmessage.Resource // key: name + "/" + type
	rcode   map[string]dnsmessage.RCode
	// truncateUDP answers every UDP query with TC set and no records, so
	// only the TCP retry can see them.
	truncateUDP bool
	// decoyFirst sends a datagram with the wrong query ID before the real
	// answer, the shape of a late or spoofed reply.
	decoyFirst bool
	udpQueries int
	tcpQueries int
}

func newFakeDNS(t *testing.T) *fakeDNS {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	port := pc.LocalAddr().(*net.UDPAddr).Port
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		_ = pc.Close()
		t.Skipf("tcp port %d taken: %v", port, err)
	}
	f := &fakeDNS{
		t: t, addr: pc.LocalAddr().String(),
		records: map[string][]dnsmessage.Resource{},
		rcode:   map[string]dnsmessage.RCode{},
	}
	t.Cleanup(func() { _ = pc.Close(); _ = ln.Close() })
	go f.serveUDP(pc)
	go f.serveTCP(ln)
	return f
}

func mustName(t *testing.T, s string) dnsmessage.Name {
	t.Helper()
	n, err := dnsmessage.NewName(s)
	if err != nil {
		t.Fatalf("name %q: %v", s, err)
	}
	return n
}

func (f *fakeDNS) add(name string, body dnsmessage.ResourceBody) {
	f.addOwned(name, name, body)
}

// addOwned files a record under the queried name `asked` but owned by
// `owner`, the way an answer that follows a CNAME chain looks.
func (f *fakeDNS) addOwned(asked, owner string, body dnsmessage.ResourceBody) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.ToLower(asked) + "/" + typeOf(body).String()
	f.records[key] = append(f.records[key], dnsmessage.Resource{
		Header: dnsmessage.ResourceHeader{Name: mustName(f.t, owner+"."), Type: typeOf(body), Class: dnsmessage.ClassINET, TTL: 60},
		Body:   body,
	})
}

func typeOf(body dnsmessage.ResourceBody) dnsmessage.Type {
	switch body.(type) {
	case *dnsmessage.AResource:
		return dnsmessage.TypeA
	case *dnsmessage.AAAAResource:
		return dnsmessage.TypeAAAA
	case *dnsmessage.CNAMEResource:
		return dnsmessage.TypeCNAME
	case *dnsmessage.MXResource:
		return dnsmessage.TypeMX
	case *dnsmessage.TXTResource:
		return dnsmessage.TypeTXT
	}
	panic("unsupported body")
}

func (f *fakeDNS) answer(raw []byte, overUDP bool) ([]byte, uint16, bool) {
	var q dnsmessage.Message
	if err := q.Unpack(raw); err != nil || len(q.Questions) != 1 {
		return nil, 0, false
	}
	question := q.Questions[0]
	name := strings.TrimSuffix(strings.ToLower(question.Name.String()), ".")
	key := name + "/" + question.Type.String()

	f.mu.Lock()
	rcode, hasRcode := f.rcode[name]
	answers := append([]dnsmessage.Resource(nil), f.records[key]...)
	truncate := overUDP && f.truncateUDP
	if overUDP {
		f.udpQueries++
	} else {
		f.tcpQueries++
	}
	f.mu.Unlock()

	resp := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: q.ID, Response: true, RecursionDesired: true, RecursionAvailable: true},
		Questions: q.Questions,
	}
	switch {
	case hasRcode:
		resp.RCode = rcode
	case truncate:
		resp.Truncated = true
	default:
		resp.Answers = answers
	}
	out, err := resp.Pack()
	if err != nil {
		f.t.Errorf("pack answer: %v", err)
		return nil, 0, false
	}
	return out, q.ID, true
}

func (f *fakeDNS) serveUDP(pc net.PacketConn) {
	buf := make([]byte, 65535)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		out, id, ok := f.answer(buf[:n], true)
		if !ok {
			continue
		}
		f.mu.Lock()
		decoy := f.decoyFirst
		f.mu.Unlock()
		if decoy {
			wrong := append([]byte(nil), out...)
			binary.BigEndian.PutUint16(wrong, id+1)
			_, _ = pc.WriteTo(wrong, from)
		}
		_, _ = pc.WriteTo(out, from)
	}
}

func (f *fakeDNS) serveTCP(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = conn.Close() }()
			var size [2]byte
			if _, err := io.ReadFull(conn, size[:]); err != nil {
				return
			}
			raw := make([]byte, binary.BigEndian.Uint16(size[:]))
			if _, err := io.ReadFull(conn, raw); err != nil {
				return
			}
			out, _, ok := f.answer(raw, false)
			if !ok {
				return
			}
			framed := make([]byte, 2+len(out))
			binary.BigEndian.PutUint16(framed, uint16(len(out)))
			copy(framed[2:], out)
			_, _ = conn.Write(framed)
		}()
	}
}

func a4(s string) *dnsmessage.AResource {
	return &dnsmessage.AResource{A: netip.MustParseAddr(s).As4()}
}

// dnsMonitor is a dns monitor asking the fake resolver directly.
func (f *fakeDNS) monitor(name, recordType string, expected ...string) Monitor {
	return Monitor{Type: TypeDNS, Target: name, Timeout: 2 * time.Second,
		DNSRecordType: recordType, DNSExpected: expected, DNSResolver: f.addr}
}

// openChecker allows the loopback resolver; the guard's own test is below.
func openChecker() *DNSChecker { return NewDNSChecker(NewGuard(true)) }

func TestDNSCheckerARecords(t *testing.T) {
	f := newFakeDNS(t)
	f.add("example.test", a4("192.0.2.1"))
	f.add("example.test", a4("192.0.2.2"))
	c := openChecker()

	tests := []struct {
		name     string
		expected []string
		wantOK   bool
		wantErr  string
	}{
		{name: "same set in another order", expected: []string{"192.0.2.2", "192.0.2.1"}, wantOK: true},
		{name: "no expected values: any record passes", wantOK: true},
		{name: "one address missing from the answer", expected: []string{"192.0.2.1", "192.0.2.3"},
			wantErr: "A records for example.test: expected 192.0.2.1, 192.0.2.3, got 192.0.2.1, 192.0.2.2"},
		// An extra address in the answer is how a hijacked zone looks, so
		// the subset of what is expected is not enough.
		{name: "an extra address in the answer", expected: []string{"192.0.2.1"},
			wantErr: "expected 192.0.2.1, got 192.0.2.1, 192.0.2.2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := c.Check(context.Background(), f.monitor("example.test", DNSRecordA, tt.expected...))
			if res.OK != tt.wantOK {
				t.Fatalf("OK = %v, want %v (error %q)", res.OK, tt.wantOK, res.Error)
			}
			if tt.wantOK {
				return
			}
			if res.Kind != FailDNSMismatch {
				t.Errorf("Kind = %q, want %q", res.Kind, FailDNSMismatch)
			}
			if !strings.Contains(res.Error, tt.wantErr) {
				t.Errorf("Error = %q, want it to contain %q", res.Error, tt.wantErr)
			}
		})
	}
}

func TestDNSCheckerNoRecordOfTheType(t *testing.T) {
	f := newFakeDNS(t)
	f.add("example.test", a4("192.0.2.1"))
	c := openChecker()

	res := c.Check(context.Background(), f.monitor("example.test", DNSRecordAAAA))
	if res.OK || res.Kind != FailDNSMismatch || res.Error != "no AAAA record for example.test" {
		t.Fatalf("got OK=%v kind=%q error=%q", res.OK, res.Kind, res.Error)
	}
	res = c.Check(context.Background(), f.monitor("example.test", DNSRecordAAAA, "2001:db8::1"))
	if res.OK || res.Error != "no AAAA record for example.test; expected 2001:db8::1" {
		t.Fatalf("got OK=%v error=%q", res.OK, res.Error)
	}
}

func TestDNSCheckerAAAAComparesAddressesNotSpelling(t *testing.T) {
	f := newFakeDNS(t)
	f.add("v6.test", &dnsmessage.AAAAResource{AAAA: netip.MustParseAddr("2001:db8::1").As16()})
	res := openChecker().Check(context.Background(), f.monitor("v6.test", DNSRecordAAAA, "2001:0db8:0:0:0:0:0:1"))
	if !res.OK {
		t.Fatalf("a longhand spelling of the same address failed: %q", res.Error)
	}
}

func TestDNSCheckerNXDOMAIN(t *testing.T) {
	f := newFakeDNS(t)
	f.mu.Lock()
	f.rcode["gone.test"] = dnsmessage.RCodeNameError
	f.rcode["broken.test"] = dnsmessage.RCodeServerFailure
	f.mu.Unlock()
	c := openChecker()

	res := c.Check(context.Background(), f.monitor("gone.test", DNSRecordA, "192.0.2.1"))
	if res.OK || res.Kind != FailDNS || !strings.Contains(res.Error, "gone.test does not exist (NXDOMAIN") {
		t.Fatalf("NXDOMAIN: OK=%v kind=%q error=%q", res.OK, res.Kind, res.Error)
	}
	res = c.Check(context.Background(), f.monitor("broken.test", DNSRecordA))
	if res.OK || res.Kind != FailDNS || !strings.Contains(res.Error, "answered SERVERFAILURE for broken.test") {
		t.Fatalf("SERVFAIL: OK=%v kind=%q error=%q", res.OK, res.Kind, res.Error)
	}
}

// A CNAME check compares the record the zone holds for the asked name, not
// the end of the chain: www pointing at an alias of a CDN is configured
// correctly even though the CDN resolves it further.
func TestDNSCheckerCNAMEComparesTheAskedName(t *testing.T) {
	f := newFakeDNS(t)
	f.add("www.example.test", &dnsmessage.CNAMEResource{CNAME: mustName(t, "Shop.Example-CDN.test.")})
	f.addOwned("www.example.test", "shop.example-cdn.test", &dnsmessage.CNAMEResource{CNAME: mustName(t, "edge7.example-cdn.test.")})
	c := openChecker()

	if res := c.Check(context.Background(), f.monitor("www.example.test", DNSRecordCNAME, "shop.example-cdn.test.")); !res.OK {
		t.Fatalf("the asked name's own CNAME failed: %q", res.Error)
	}
	res := c.Check(context.Background(), f.monitor("www.example.test", DNSRecordCNAME, "edge7.example-cdn.test"))
	if res.OK || !strings.Contains(res.Error, "got shop.example-cdn.test") {
		t.Fatalf("the end of the chain matched: OK=%v error=%q", res.OK, res.Error)
	}
}

func TestDNSCheckerNullMX(t *testing.T) {
	f := newFakeDNS(t)
	f.add("nomail.test", &dnsmessage.MXResource{Pref: 0, MX: mustName(t, ".")})
	res := openChecker().Check(context.Background(), f.monitor("nomail.test", DNSRecordMX, "0 ."))
	if !res.OK {
		t.Fatalf("a null MX did not match \"0 .\": %q", res.Error)
	}
}

func TestDNSCheckerMX(t *testing.T) {
	f := newFakeDNS(t)
	f.add("example.test", &dnsmessage.MXResource{Pref: 10, MX: mustName(t, "mx1.example.test.")})
	f.add("example.test", &dnsmessage.MXResource{Pref: 20, MX: mustName(t, "mx2.example.test.")})
	c := openChecker()

	for _, tt := range []struct {
		expected []string
		wantOK   bool
	}{
		{[]string{"mx1.example.test", "mx2.example.test"}, true},
		{[]string{"10 mx1.example.test", "20 MX2.example.test."}, true},
		{[]string{"20 mx1.example.test", "10 mx2.example.test"}, false},
		{[]string{"mx1.example.test"}, false},
	} {
		res := c.Check(context.Background(), f.monitor("example.test", DNSRecordMX, tt.expected...))
		if res.OK != tt.wantOK {
			t.Errorf("expected %v: OK = %v, want %v (error %q)", tt.expected, res.OK, tt.wantOK, res.Error)
		}
	}
}

// TXT records are shared between unrelated uses, so the expected values must
// be present and the rest are allowed. Long records arrive split in 255-byte
// strings and are compared joined.
func TestDNSCheckerTXT(t *testing.T) {
	f := newFakeDNS(t)
	long := strings.Repeat("k", 300)
	f.add("example.test", &dnsmessage.TXTResource{TXT: []string{"v=spf1 include:_spf.example.test -all"}})
	f.add("example.test", &dnsmessage.TXTResource{TXT: []string{"site-verification=abc"}})
	f.add("example.test", &dnsmessage.TXTResource{TXT: []string{"v=DKIM1; p=" + long[:200], long[200:]}})
	c := openChecker()

	if res := c.Check(context.Background(), f.monitor("example.test", DNSRecordTXT, "v=spf1 include:_spf.example.test -all")); !res.OK {
		t.Fatalf("SPF among other TXT records failed: %q", res.Error)
	}
	if res := c.Check(context.Background(), f.monitor("example.test", DNSRecordTXT, "v=DKIM1; p="+long)); !res.OK {
		t.Fatalf("a TXT record split over two strings failed: %q", res.Error)
	}
	res := c.Check(context.Background(), f.monitor("example.test", DNSRecordTXT, "v=spf1 -all"))
	if res.OK || res.Kind != FailDNSMismatch || !strings.Contains(res.Error, `do not include "v=spf1 -all"`) {
		t.Fatalf("a missing TXT value passed: OK=%v error=%q", res.OK, res.Error)
	}
	// The DKIM value is cut in the message rather than printed whole.
	if strings.Contains(res.Error, long) || !strings.Contains(res.Error, "…") {
		t.Errorf("a long value was not shortened in the message: %q", res.Error)
	}
}

func TestDNSCheckerFallsBackToTCPWhenTruncated(t *testing.T) {
	f := newFakeDNS(t)
	f.add("big.test", a4("192.0.2.9"))
	f.mu.Lock()
	f.truncateUDP = true
	f.mu.Unlock()

	res := openChecker().Check(context.Background(), f.monitor("big.test", DNSRecordA, "192.0.2.9"))
	if !res.OK {
		t.Fatalf("truncated answer was not retried over TCP: %q", res.Error)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.udpQueries != 1 || f.tcpQueries != 1 {
		t.Errorf("queries: udp %d, tcp %d; want 1 and 1", f.udpQueries, f.tcpQueries)
	}
}

// A datagram that does not answer this query is ignored, not believed.
func TestDNSCheckerIgnoresAnAnswerWithAnotherID(t *testing.T) {
	f := newFakeDNS(t)
	f.add("example.test", a4("192.0.2.1"))
	f.mu.Lock()
	f.decoyFirst = true
	f.mu.Unlock()

	res := openChecker().Check(context.Background(), f.monitor("example.test", DNSRecordA, "192.0.2.1"))
	if !res.OK {
		t.Fatalf("check failed after a decoy datagram: %q", res.Error)
	}
}

// A resolver set on the monitor is something a user typed, so it is dialled
// through the SSRF guard like any other target.
func TestDNSCheckerGuardsTheMonitorsResolver(t *testing.T) {
	f := newFakeDNS(t)
	f.add("example.test", a4("192.0.2.1"))
	c := NewDNSChecker(NewGuard(false))

	res := c.Check(context.Background(), f.monitor("example.test", DNSRecordA))
	if res.OK || res.Kind != FailInternal || !strings.Contains(res.Error, "refused to connect") {
		t.Fatalf("a loopback resolver was dialled through a closed guard: OK=%v kind=%q error=%q", res.OK, res.Kind, res.Error)
	}
}

// The host's own resolver is the operator's configuration, and inside a
// container it is a loopback address; the guard must not refuse it.
func TestDNSCheckerUsesTheSystemResolverUnguarded(t *testing.T) {
	f := newFakeDNS(t)
	f.add("example.test", a4("192.0.2.1"))
	c := NewDNSChecker(NewGuard(false))
	c.systemServers = func() ([]string, error) { return []string{f.addr}, nil }

	m := f.monitor("example.test", DNSRecordA, "192.0.2.1")
	m.DNSResolver = ""
	if res := c.Check(context.Background(), m); !res.OK {
		t.Fatalf("system resolver check failed: kind=%q error=%q", res.Kind, res.Error)
	}
}

func TestDNSCheckerTriesTheNextSystemResolver(t *testing.T) {
	f := newFakeDNS(t)
	f.add("example.test", a4("192.0.2.1"))
	// A closed port: the first resolver refuses at once.
	dead, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := dead.LocalAddr().String()
	_ = dead.Close()

	c := openChecker()
	c.systemServers = func() ([]string, error) { return []string{deadAddr, f.addr}, nil }
	m := f.monitor("example.test", DNSRecordA, "192.0.2.1")
	m.DNSResolver = ""
	if res := c.Check(context.Background(), m); !res.OK {
		t.Fatalf("second system resolver was not tried: %q", res.Error)
	}
}

func TestDNSCheckerTimesOut(t *testing.T) {
	// Bound but never answered.
	silent, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = silent.Close() })
	m := Monitor{Type: TypeDNS, Target: "example.test", Timeout: 200 * time.Millisecond,
		DNSRecordType: DNSRecordA, DNSResolver: silent.LocalAddr().String()}
	res := openChecker().Check(context.Background(), m)
	if res.OK || res.Kind != FailTimeout {
		t.Fatalf("got OK=%v kind=%q error=%q, want a timeout", res.OK, res.Kind, res.Error)
	}
}

func TestDNSCheckerRefusesWhatItCannotAsk(t *testing.T) {
	c := openChecker()
	for _, m := range []Monitor{
		{Type: TypeDNS, Target: "https://example.test/", DNSRecordType: DNSRecordA},
		{Type: TypeDNS, Target: "example.test", DNSRecordType: "SOA"},
	} {
		if res := c.Check(context.Background(), m); res.OK || res.Kind != FailInternal {
			t.Errorf("%+v: OK=%v kind=%q", m, res.OK, res.Kind)
		}
	}
}

func TestParseResolvConf(t *testing.T) {
	conf := "# comment\nnameserver 127.0.0.53\noptions edns0\nnameserver 2001:db8::53\nnameserver not-an-ip\nsearch .\n"
	got := parseResolvConf(conf)
	want := []string{"127.0.0.53:53", "[2001:db8::53]:53"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestResolvConfServersSaysWhatToDo(t *testing.T) {
	old := resolvConfPath
	t.Cleanup(func() { resolvConfPath = old })

	resolvConfPath = filepath.Join(t.TempDir(), "missing")
	if _, err := resolvConfServers(); err == nil || !strings.Contains(err.Error(), "set a resolver on this monitor") {
		t.Errorf("missing file: %v", err)
	}
	empty := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.WriteFile(empty, []byte("search .\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolvConfPath = empty
	if _, err := resolvConfServers(); err == nil || !strings.Contains(err.Error(), "names no nameserver") {
		t.Errorf("no nameserver: %v", err)
	}
}

func TestValidateDNSExpected(t *testing.T) {
	good := map[string][]string{
		DNSRecordA:     {"192.0.2.1"},
		DNSRecordAAAA:  {"2001:db8::1"},
		DNSRecordCNAME: {"shop.example.test", "shop.example.test."},
		DNSRecordMX:    {"mail.example.test", "10 mail.example.test", "0 ."},
		DNSRecordTXT:   {"v=spf1 -all", "anything at all"},
	}
	bad := map[string][]string{
		DNSRecordA:     {"2001:db8::1", "example.test", "192.0.2.300", ""},
		DNSRecordAAAA:  {"192.0.2.1", "::ffff:192.0.2.1"},
		DNSRecordCNAME: {"https://example.test", "has space.test"},
		DNSRecordMX:    {"ten mail.example.test", "10", "70000 mail.example.test"},
		DNSRecordTXT:   {"   "},
	}
	for typ, values := range good {
		for _, v := range values {
			if err := ValidateDNSExpected(typ, v); err != nil {
				t.Errorf("%s %q refused: %v", typ, v, err)
			}
		}
	}
	for typ, values := range bad {
		for _, v := range values {
			if err := ValidateDNSExpected(typ, v); err == nil {
				t.Errorf("%s %q accepted", typ, v)
			}
		}
	}
	if err := ValidateDNSExpected("SOA", "x"); err == nil {
		t.Error("unknown record type accepted")
	}
}

func TestClassifyResolverErrorKeepsTheGuardMessage(t *testing.T) {
	res := classifyResolverError(time.Now(), context.Background(), "192.0.2.53:53",
		errors.New("connection refused"))
	if res.Kind != FailDNS || !strings.Contains(res.Error, "no answer from 192.0.2.53:53") {
		t.Errorf("got kind=%q error=%q", res.Kind, res.Error)
	}
}
