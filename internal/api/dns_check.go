package api

import (
	"strconv"
	"strings"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// dnsCheckWire is the API shape of a dns monitor's settings.
//
// Expected is always a list, also for one value: an A record set or an MX set
// is several records, and the check compares the whole set (see
// checker.DNSChecker). An empty list means any record of the type passes.
type dnsCheckWire struct {
	RecordType string   `json:"record_type"`
	Expected   []string `json:"expected"`
	Resolver   string   `json:"resolver,omitempty"`
}

// maxDNSExpected caps the expected values of one monitor. A record set larger
// than this is not something a person maintains by hand in a form, and the
// cap keeps a failure message readable.
const maxDNSExpected = 20

// maxDNSValueLen is the longest expected value accepted. A TXT record with a
// 4096-bit DKIM key is about 800 bytes; twice that leaves room without letting
// one field carry a document.
const maxDNSValueLen = 2048

// dnsCheckFromWire validates wire settings and converts them for the store. A
// nil input is no settings.
//
// Problems name the sub-field at fault, `dns.expected` and so on, so a form
// with three controls can point at the one that is wrong.
func dnsCheckFromWire(w *dnsCheckWire) (*store.DNSCheck, problem) {
	if w == nil {
		return nil, problem{}
	}
	if !checker.ValidDNSRecordType(w.RecordType) {
		return nil, fieldProblem("dns.record_type",
			"dns.record_type must be one of "+strings.Join(checker.DNSRecordTypes(), ", ")+
				"; got "+strconv.Quote(w.RecordType))
	}
	if len(w.Expected) > maxDNSExpected {
		return nil, fieldProblem("dns.expected",
			"dns.expected holds at most "+strconv.Itoa(maxDNSExpected)+" values")
	}
	out := &store.DNSCheck{RecordType: w.RecordType}
	seen := make(map[string]bool, len(w.Expected))
	for _, raw := range w.Expected {
		v := strings.TrimSpace(raw)
		if len(v) > maxDNSValueLen {
			return nil, fieldProblem("dns.expected",
				"an expected value is longer than "+strconv.Itoa(maxDNSValueLen)+" characters")
		}
		if err := checker.ValidateDNSExpected(w.RecordType, v); err != nil {
			return nil, fieldProblem("dns.expected", err.Error())
		}
		// A value listed twice could never match: the answer holds each
		// record once, and the check wants the sets equal.
		key := v
		if w.RecordType != checker.DNSRecordTXT {
			key = strings.ToLower(strings.TrimSuffix(v, "."))
		}
		if seen[key] {
			return nil, fieldProblem("dns.expected", strconv.Quote(v)+" is listed twice")
		}
		seen[key] = true
		out.Expected = append(out.Expected, v)
	}
	resolver := strings.TrimSpace(w.Resolver)
	if resolver != "" {
		if p := validateDNSResolver(resolver); !p.ok() {
			return nil, p
		}
		out.Resolver = resolver
	}
	return out, problem{}
}

// validateDNSResolver accepts a host or an IP address, with an optional port.
// It is dialled at check time through the SSRF guard, so a private resolver
// is refused there, with the same message every other check gives.
func validateDNSResolver(resolver string) problem {
	bad := func(msg string) problem { return fieldProblem("dns.resolver", msg) }
	if strings.Contains(resolver, "://") || strings.ContainsAny(resolver, "/?#@") {
		return bad("dns.resolver takes a host or an IP address, with an optional port, such as 1.1.1.1 or ns1.example.com:53")
	}
	host, _, err := checker.ParseHostPort(resolver, 53)
	if err != nil {
		return bad("invalid resolver: " + err.Error())
	}
	if host == "" {
		return bad("dns.resolver has no host")
	}
	return problem{}
}

// dnsCheckToWire is the read side of dnsCheckFromWire.
func dnsCheckToWire(d *store.DNSCheck) *dnsCheckWire {
	if d == nil {
		return nil
	}
	expected := d.Expected
	if expected == nil {
		// [] rather than null: the field is a list, and "no values" is a
		// setting (any record passes), not a missing answer.
		expected = []string{}
	}
	return &dnsCheckWire{RecordType: d.RecordType, Expected: expected, Resolver: d.Resolver}
}

// dnsCheckTypeProblem refuses dns settings on a monitor of another type, and
// a dns monitor without them. Either would be refused by the schema; refusing
// it here names the field instead of failing the write.
func dnsCheckTypeProblem(typ string, d *store.DNSCheck) problem {
	switch {
	case typ == store.TypeDNS && d == nil:
		return fieldProblem("dns.record_type", "a dns monitor needs dns.record_type, one of "+
			strings.Join(checker.DNSRecordTypes(), ", "))
	case typ != store.TypeDNS && d != nil:
		return fieldProblem("dns", "dns settings apply only to dns monitors")
	}
	return problem{}
}
