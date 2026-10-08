package kumaimport

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"unicode"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/configfile"
)

// kumaDNSPort is the resolver port Kuma fills in for a new DNS monitor, and
// the one SubGlance assumes when a resolver is written without a port.
const kumaDNSPort = 53

// kumaCondition is one entry of a Kuma 2.x monitor's `conditions` column: an
// expression on a variable, or a group of further entries.
type kumaCondition struct {
	Type     string          `json:"type"`
	Variable string          `json:"variable"`
	Operator string          `json:"operator"`
	Value    any             `json:"value"`
	Children []kumaCondition `json:"children"`
}

// convertDNS fills in a dns monitor from a Kuma DNS monitor.
//
// Kuma asks for one record type of one name and passes when the answer is
// not empty, or, with conditions (2.x only), when any record satisfies them.
// SubGlance compares the answer with a list of expected values. What maps
// without changing the meaning comes over; the rest is skipped with the
// reason, because a monitor that passes where Kuma failed is worse than one
// that has to be made by hand.
func convertDNS(m row, out *configfile.Monitor, res *Result, name, typ string) bool {
	note := func(reason string) { changed(res, "monitor", name, typ, reason) }
	skip := func(reason string) bool { skipMonitor(res, name, typ, reason); return false }

	recordType := strings.ToUpper(strings.TrimSpace(m.str("dns_resolve_type")))
	if recordType == "" {
		recordType = checker.DNSRecordA // Kuma's default for a new DNS monitor
	}
	if !checker.ValidDNSRecordType(recordType) {
		return skip("SubGlance does not check " + recordType + " records; a dns monitor checks " +
			strings.Join(checker.DNSRecordTypes(), ", "))
	}

	host := strings.TrimSuffix(strings.TrimSpace(m.str("hostname")), ".")
	if _, err := netip.ParseAddr(host); err == nil || !checker.ValidDNSName(host) {
		return skip("its name " + strconv.Quote(host) + " is not a domain name")
	}

	expected, expectNote, ok := dnsExpected(m.str("conditions"), recordType)
	if !ok {
		return skip(expectNote)
	}

	resolver, notes, reason := dnsResolver(m.str("dns_resolve_server"), m)
	if reason != "" {
		return skip(reason)
	}
	// Notes are written only once the monitor is certain to come over, so a
	// skipped monitor is not also listed as imported with a change.
	if expectNote != "" {
		note(expectNote)
	}
	for _, n := range notes {
		note(n)
	}

	out.Type, out.Target = "dns", host
	out.DNS = &configfile.DNSCheck{RecordType: recordType, Expected: expected, Resolver: resolver}
	return true
}

// dnsExpected turns Kuma's conditions into expected values. Without
// conditions Kuma passes on any answer, which is an empty list. One "record
// equals value" is one expected value. Anything else has no equivalent: the
// reason says so and ok is false.
//
// A non-empty reason with ok true is a change to check: for A, AAAA and MX
// SubGlance wants exactly the listed records, where Kuma wanted any record to
// match.
func dnsExpected(raw, recordType string) (expected []string, reason string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return []string{}, "", true
	}
	var conds []kumaCondition
	if err := json.Unmarshal([]byte(raw), &conds); err != nil {
		return nil, "its conditions cannot be read", false
	}
	if len(conds) == 0 {
		return []string{}, "", true
	}
	c := conds[0]
	if len(conds) > 1 || c.Type != "expression" {
		return nil, "its conditions go beyond one \"record equals\" test, which is what a dns monitor's expected values express", false
	}
	if c.Variable != "record" {
		return nil, "its condition tests " + strconv.Quote(c.Variable) + "; a dns monitor compares the records only", false
	}
	if c.Operator != "equals" {
		return nil, "its condition compares the record with " + strconv.Quote(c.Operator) +
			"; a dns monitor compares records for equality only", false
	}
	value := conditionValue(c.Value)
	// Kuma compares a TXT record with the value exactly, spaces included; a
	// dns monitor's expected TXT value is trimmed. A value with spaces at
	// either end would match a different record after the import.
	if s, isText := c.Value.(string); isText && recordType == checker.DNSRecordTXT && s != value {
		return nil, "its condition value " + strconv.Quote(s) +
			" starts or ends with spaces, which Kuma compared and a dns monitor's expected TXT value cannot hold", false
	}
	if err := checker.ValidateDNSExpected(recordType, value); err != nil {
		return nil, "its condition value cannot be an expected " + recordType + " record: " + err.Error(), false
	}
	// CNAME has one record and TXT allows others beside the expected ones,
	// so for those two one expected value means what Kuma's condition meant.
	if recordType != checker.DNSRecordTXT && recordType != checker.DNSRecordCNAME {
		reason = "Kuma passed when any " + recordType + " record equalled " + strconv.Quote(value) +
			"; a dns monitor expects exactly the records listed, so add any other record the name should have to dns.expected"
	}
	return []string{value}, reason, true
}

// conditionValue is a condition's value as text. Kuma's form writes a
// string; a number written by hand is read as the digits.
func conditionValue(v any) string {
	switch v := v.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

// dnsResolver picks the resolver a dns monitor asks. Kuma 1.23 stores one
// address; 2.x stores a comma-separated list that it tries in order, of
// addresses or host names. A dns monitor asks one resolver, so the first is
// kept and the rest are listed. Kuma keeps the port in its own column.
//
// A non-empty skip means the resolver cannot be written as a SubGlance
// resolver at all, or that Kuma had none.
func dnsResolver(raw string, m row) (resolver string, notes []string, skip string) {
	var servers []string
	for _, s := range strings.Split(raw, ",") {
		s = strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) || r == '[' || r == ']' {
				return -1
			}
			return r
		}, s)
		if s != "" {
			servers = append(servers, s)
		}
	}
	if len(servers) == 0 {
		// Kuma refuses to check without a resolver, so the monitor was down
		// there. A dns monitor without one asks the host's resolver and
		// could pass where Kuma failed.
		return "", nil, "Kuma had no resolver set, so its check never ran; a dns monitor without one asks the resolver of the host SubGlance runs on"
	}

	first := servers[0]
	addr, err := netip.ParseAddr(first)
	isAddr := err == nil
	if !isAddr && !checker.ValidDNSName(first) {
		return "", nil, "its resolver " + strconv.Quote(first) + " is not an IP address or a host name"
	}

	port := m.int("port")
	if port == 0 {
		port = kumaDNSPort // an empty port column: Kuma itself would ask port 53
	}
	if port < 1 || port > 65535 {
		return "", nil, "its resolver port " + strconv.Itoa(port) + " is not a port"
	}
	resolver = first
	if port != kumaDNSPort {
		resolver = net.JoinHostPort(first, strconv.Itoa(port))
	}

	if len(servers) > 1 {
		notes = append(notes, fmt.Sprintf("Kuma tried %d resolvers in turn; a dns monitor asks one, so %s is kept and %s left out",
			len(servers), first, strings.Join(servers[1:], ", ")))
	}
	switch {
	case isAddr && checker.NewGuard(false).CheckAddr(addr) != nil:
		notes = append(notes, "the resolver "+first+" is a private or reserved address; SubGlance asks it only when started with --allow-private-targets")
	case !isAddr:
		// Kuma looks the name up and asks whatever address it gets; a name
		// such as "adguard" usually stands for a machine on the local
		// network, which the guard refuses at connect time.
		notes = append(notes, "the resolver "+first+" is a host name; if it resolves to a private address, SubGlance asks it only when started with --allow-private-targets")
	}
	return resolver, notes, ""
}
