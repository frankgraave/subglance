package kumaimport

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/configfile"
)

// The bounds SubGlance's API enforces, restated so a converted value lands
// inside them and the dry run accepts the file. A value moved to fit is
// reported in Result.Changed.
const (
	minIntervalS     = 20
	maxIntervalS     = 86400
	minTimeoutS      = 1
	maxTimeoutS      = 120
	maxRetries       = 10
	minPushIntervalS = 60
	maxPushIntervalS = 30 * 24 * 60 * 60
	minRepeatAfterS  = 60
	maxRepeatAfterS  = 86400

	maxTags        = 20
	maxTagKeyLen   = 32
	maxTagValueLen = 64
)

// unsupportedType explains, per Kuma monitor type, why it does not come over.
// A type missing from this map and from convertMonitor gets the generic
// reason, so a type added by a later Kuma release is still listed.
var unsupportedType = map[string]string{
	"group":             "a group is not a check; the monitors in it carry the tag group",
	"docker":            "SubGlance has no Docker container check",
	"real-browser":      "SubGlance has no browser check; an http monitor on the same URL is the closest",
	"grpc-keyword":      "SubGlance has no gRPC check",
	"mqtt":              "SubGlance has no MQTT check",
	"kafka-producer":    "SubGlance has no Kafka check",
	"rabbitmq":          "SubGlance has no RabbitMQ check",
	"sqlserver":         "SubGlance has no database query check; a tcp monitor on the server's port is the closest",
	"postgres":          "SubGlance has no database query check; a tcp monitor on the server's port is the closest",
	"mysql":             "SubGlance has no database query check; a tcp monitor on the server's port is the closest",
	"mongodb":           "SubGlance has no database query check; a tcp monitor on the server's port is the closest",
	"redis":             "SubGlance has no Redis check; a tcp monitor on the server's port is the closest",
	"radius":            "SubGlance has no RADIUS check",
	"steam":             "SubGlance has no game server check",
	"gamedig":           "SubGlance has no game server check",
	"tailscale-ping":    "SubGlance has no Tailscale check",
	"snmp":              "SubGlance has no SNMP check",
	"smtp":              "SubGlance has no SMTP check; a tcp monitor on the server's port is the closest",
	"websocket-upgrade": "SubGlance has no WebSocket check",
	"globalping":        "SubGlance checks from its own host only",
	"manual":            "a manual status is not a check",
}

func skipMonitor(res *Result, name, typ, reason string) {
	res.Skipped = append(res.Skipped, Note{Kind: "monitor", Name: name, Type: typ, Reason: reason})
}

func changed(res *Result, kind, name, typ, reason string) {
	res.Changed = append(res.Changed, Note{Kind: kind, Name: name, Type: typ, Reason: reason})
}

// convertMonitor converts one Kuma monitor row, or reports why it cannot.
func convertMonitor(m row, res *Result) (configfile.Monitor, bool) {
	typ := m.str("type")
	name := strings.TrimSpace(m.str("name"))
	if name == "" {
		name = "Kuma monitor " + m.str("id")
	}
	note := func(reason string) { changed(res, "monitor", name, typ, reason) }

	switch typ {
	case "http", "keyword", "json-query", "port", "ping", "push", "dns":
	default:
		reason, ok := unsupportedType[typ]
		if !ok {
			reason = "SubGlance has no " + typ + " check"
		}
		skipMonitor(res, name, typ, reason)
		return configfile.Monitor{}, false
	}
	// Upside-down mode reports a reachable service as down. Imported as an
	// ordinary monitor it would raise the opposite alarm, which is worse
	// than not importing it.
	if m.bool("upside_down") {
		skipMonitor(res, name, typ, "upside-down mode has no SubGlance equivalent; imported as is, it would report the opposite state")
		return configfile.Monitor{}, false
	}

	out := configfile.Monitor{Name: name, Enabled: ptr(m.bool("active"))}
	interval := m.int("interval")

	switch typ {
	case "http", "keyword", "json-query":
		if ok := convertHTTP(m, &out, res, name, typ); !ok {
			return configfile.Monitor{}, false
		}
	case "port":
		host, port := strings.TrimSpace(m.str("hostname")), m.int("port")
		if host == "" || port < 1 || port > 65535 {
			skipMonitor(res, name, typ, "it has no usable host and port")
			return configfile.Monitor{}, false
		}
		out.Type, out.Target = "tcp", net.JoinHostPort(host, strconv.Itoa(port))
	case "ping":
		host := strings.TrimSpace(m.str("hostname"))
		if host == "" || (strings.ContainsAny(host, "/?#@:") && net.ParseIP(host) == nil) ||
			strings.ContainsFunc(host, unicode.IsSpace) {
			skipMonitor(res, name, typ, "its host "+strconv.Quote(host)+" is not a hostname or IP address")
			return configfile.Monitor{}, false
		}
		out.Type, out.Target = "ping", host
	case "dns":
		if ok := convertDNS(m, &out, res, name, typ); !ok {
			return configfile.Monitor{}, false
		}
	case "push":
		// Kuma's interval on a push monitor is how often the job reports,
		// which is what push_interval_s means. The grace period keeps
		// SubGlance's default: Kuma has none.
		out.Type = "push"
		p := interval
		switch {
		case p < minPushIntervalS:
			note(fmt.Sprintf("the push interval was %ds; SubGlance's minimum is %ds", p, minPushIntervalS))
			p = minPushIntervalS
		case p > maxPushIntervalS:
			note(fmt.Sprintf("the push interval was %ds; SubGlance's maximum is %ds", p, maxPushIntervalS))
			p = maxPushIntervalS
		}
		out.PushIntervalS = ptr(p)
		note("it gets a new push URL, shown once in the import report; point the job that calls Kuma's push URL at it")
	}

	if typ != "push" {
		switch {
		case interval < minIntervalS:
			note(fmt.Sprintf("the interval was %ds; SubGlance checks at most every %ds", interval, minIntervalS))
			interval = minIntervalS
		case interval > maxIntervalS:
			note(fmt.Sprintf("the interval was %ds; SubGlance checks at least every %ds", interval, maxIntervalS))
			interval = maxIntervalS
		}
		out.IntervalS = ptr(interval)
	}

	retries := m.int("maxretries")
	if retries > maxRetries {
		note(fmt.Sprintf("retries was %d; SubGlance allows at most %d", retries, maxRetries))
		retries = maxRetries
	}
	if retries < 0 {
		retries = 0
	}
	out.Retries = ptr(retries)

	// Kuma repeats an alert every resend_interval checks while a monitor is
	// down; SubGlance repeats after a time. Zero is Kuma's default and means
	// no repeats; it is left out so the monitor gets SubGlance's default,
	// which the documentation for this command states.
	if n := m.int("resend_interval"); n > 0 && interval > 0 {
		s := n * interval
		if typ == "push" {
			s = n * deref(out.PushIntervalS)
		}
		if s < minRepeatAfterS {
			note(fmt.Sprintf("alerts were repeated every %ds; SubGlance repeats at most once a minute", s))
			s = minRepeatAfterS
		}
		if s > maxRepeatAfterS {
			note(fmt.Sprintf("alerts were repeated every %d checks; SubGlance repeats at most once a day", n))
			s = maxRepeatAfterS
		}
		out.RepeatAfterS = ptr(s)
	}
	return out, true
}

// convertHTTP fills in the fields of the three HTTP-based Kuma types.
func convertHTTP(m row, out *configfile.Monitor, res *Result, name, typ string) bool {
	note := func(reason string) { changed(res, "monitor", name, typ, reason) }
	skip := func(reason string) bool { skipMonitor(res, name, typ, reason); return false }

	target := strings.TrimSpace(m.str("url"))
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		strings.ContainsFunc(u.Hostname(), unicode.IsSpace) {
		return skip("its URL " + strconv.Quote(target) + " is not an http or https URL with a host")
	}
	out.Type, out.Target = "http", target

	switch auth := m.str("auth_method"); auth {
	case "", "null":
	case "basic":
		// SubGlance has no separate basic-auth fields; the header is what
		// Kuma sends, and its value is a credential like any other header.
		addHeader(out, "Authorization")
		note("basic authentication became an Authorization header; fill in \"Basic \" followed by base64 of user:password")
	case "bearer":
		addHeader(out, "Authorization")
		note("bearer authentication became an Authorization header; fill in \"Bearer \" followed by the token")
	default:
		return skip("authentication method " + auth + " is not supported by SubGlance")
	}

	method := strings.ToUpper(strings.TrimSpace(m.str("method")))
	switch method {
	case "":
		method = "GET"
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
	default:
		return skip("HTTP method " + method + " is not supported by SubGlance")
	}
	out.Method = ptr(method)

	status, ok := statusSpec(m.str("accepted_statuscodes_json"))
	if !ok {
		return skip("its accepted status codes " + m.str("accepted_statuscodes_json") + " cannot be read")
	}
	out.ExpectedStatus = ptr(status)

	// Kuma follows up to maxredirects redirects, and none at zero.
	out.FollowRedirects = ptr(m.int("maxredirects") > 0)

	if raw := strings.TrimSpace(m.str("headers")); raw != "" && raw != "null" {
		var headers map[string]any
		if err := json.Unmarshal([]byte(raw), &headers); err != nil {
			return skip("its request headers are not a JSON object")
		}
		for k := range headers {
			if k = strings.TrimSpace(k); k != "" {
				addHeader(out, k)
			}
		}
	}
	if strings.TrimSpace(m.str("body")) != "" {
		out.Body = ptr(configfile.Placeholder)
		bodyContentType(m.str("http_body_encoding"), out, note)
	}

	if m.bool("ignore_tls") {
		note("Kuma ignored certificate errors here; SubGlance always verifies the certificate, so a self-signed or expired one fails the check")
	}

	if t := m.int("timeout"); t > 0 {
		if t > maxTimeoutS {
			note(fmt.Sprintf("the timeout was %ds; SubGlance waits at most %ds", t, maxTimeoutS))
			t = maxTimeoutS
		}
		out.TimeoutS = ptr(max(t, minTimeoutS))
	}

	switch typ {
	case "keyword":
		kw := m.str("keyword")
		if kw == "" {
			return skip("it is a keyword monitor without a keyword")
		}
		mode := "must_contain"
		if m.bool("invert_keyword") {
			mode = "must_not_contain"
		}
		out.Keyword, out.KeywordMode = ptr(kw), ptr(mode)
	case "json-query":
		a, reason := jsonAssertion(m.str("json_path"), m.str("json_path_operator"), m.str("expected_value"))
		if a == nil {
			return skip(reason)
		}
		if reason != "" {
			note(reason)
		}
		node, err := configfile.AssertionNode(a)
		if err != nil {
			return skip("its JSON query cannot be written: " + err.Error())
		}
		out.JSONAssertion = node
	}
	return true
}

// bodyEncodingTypes is the Content-Type Kuma sends with a request body, per
// Body Encoding. Kuma sets it itself (server/model/monitor.js in 1.23 and
// 2.5), so it is not in the monitor's headers; SubGlance sends a body with
// no type unless a header gives one, and a JSON API behind a parser that
// keys on the type answers 400 or 415 to it. "form" exists from 2.x on.
var bodyEncodingTypes = map[string]string{
	"":     "application/json",
	"json": "application/json",
	"form": "application/x-www-form-urlencoded",
	"xml":  "text/xml; charset=utf-8",
}

// bodyContentType gives a monitor with a request body the Content-Type Kuma
// sent with it. The value is written out rather than withheld: Kuma derived
// it from the encoding, so it is not a secret of the user's. A Content-Type
// in the monitor's own headers wins, as it did in Kuma, where those headers
// are applied after its own; that header is already in out, withheld.
func bodyContentType(encoding string, out *configfile.Monitor, note func(string)) {
	for k := range out.Headers {
		if strings.EqualFold(k, "Content-Type") {
			return
		}
	}
	typ, ok := bodyEncodingTypes[encoding]
	if !ok {
		note("its body encoding " + strconv.Quote(encoding) + " is not one the importer knows; " +
			"add a Content-Type header with the type the server expects")
		return
	}
	if out.Headers == nil {
		out.Headers = map[string]string{}
	}
	out.Headers["Content-Type"] = typ
}

// addHeader adds a header whose value is withheld, as an export withholds
// every header value. The monitor is imported paused until it is filled in.
func addHeader(out *configfile.Monitor, name string) {
	if out.Headers == nil {
		out.Headers = map[string]string{}
	}
	for k := range out.Headers {
		if strings.EqualFold(k, name) {
			return
		}
	}
	out.Headers[name] = configfile.Placeholder
}

// statusSpec turns Kuma's list of accepted codes into SubGlance's spelling.
func statusSpec(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return "200-299", true
	}
	var codes []string
	if err := json.Unmarshal([]byte(raw), &codes); err != nil {
		return "", false
	}
	if len(codes) == 0 {
		return "200-299", true
	}
	spec := strings.Join(codes, ",")
	if _, err := checker.ParseStatusMatcher(spec); err != nil {
		return "", false
	}
	return spec, true
}

// simplePath is the part of JSONata a SubGlance path can express: keys
// joined by dots, with array indexes.
var simplePath = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*(\[[0-9]+\])*(\.[A-Za-z_][A-Za-z0-9_-]*(\[[0-9]+\])*)*$`)

// jsonOperators maps Kuma's comparison operators onto SubGlance's.
var jsonOperators = map[string]string{
	"":   "equals", // Kuma 1.x had no operator column: it always compared for equality
	"==": "equals",
	"!=": "not_equals",
	"<":  "less_than",
	">":  "greater_than",
}

// jsonAssertion converts a Kuma JSON query. A nil assertion comes with the
// reason it could not be converted; a non-nil one may come with a note.
//
// Kuma evaluates a JSONata expression and compares the result as text, so
// "42" matches both the number 42 and the string "42". SubGlance compares
// typed values. An expected value that reads as a number, true, false or
// null is written as that JSON value, and the note says so, because it is
// the one place the two can disagree.
func jsonAssertion(path, op, expected string) (*configfile.JSONAssertion, string) {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(strings.TrimPrefix(path, "$."), "$")
	if !simplePath.MatchString(path) {
		return nil, "its JSON query " + strconv.Quote(path) + " uses JSONata beyond a plain path, which SubGlance cannot evaluate"
	}
	operator, ok := jsonOperators[strings.TrimSpace(op)]
	if !ok {
		return nil, "its JSON query compares with " + op + ", which SubGlance has no operator for"
	}

	value := strconv.Quote(expected)
	note := ""
	var probe any
	if err := json.Unmarshal([]byte(strings.TrimSpace(expected)), &probe); err == nil {
		switch probe.(type) {
		case float64, bool, nil:
			value = strings.TrimSpace(expected)
			note = "the JSON query now expects the JSON value " + value + "; Kuma compared text, so change it to the string " +
				strconv.Quote(expected) + " if the field holds a string"
		}
	}
	if operator == "less_than" || operator == "greater_than" {
		if _, isNum := probe.(float64); !isNum {
			return nil, "its JSON query compares " + op + " against " + strconv.Quote(expected) + ", which is not a number"
		}
		note = ""
	}
	a := checker.JSONAssertion{Path: path, Operator: checker.JSONOperator(operator), Expected: json.RawMessage(value)}
	if _, err := checker.ValidateJSONAssertion(a); err != nil {
		return nil, "its JSON query cannot be expressed in SubGlance: " + err.Error()
	}
	return &configfile.JSONAssertion{Path: path, Operator: operator, Expected: value}, note
}

type kumaTag struct{ name, value string }

// convertTags turns Kuma's tags, and the group a monitor sits in, into
// SubGlance tags, which are one value per lowercase key.
func convertTags(monitor string, tags []kumaTag, group string, res *Result) map[string]string {
	out := map[string]string{}
	note := func(reason string) { changed(res, "monitor", monitor, "", reason) }
	add := func(name, value string) {
		key := tagKey(name)
		if key == "" {
			note("tag " + strconv.Quote(name) + " has no letters or digits to make a key from and was left out")
			return
		}
		if utf8.RuneCountInString(strings.Join(strings.Fields(value), " ")) > maxTagValueLen {
			note("tag " + key + " was shortened to " + strconv.Itoa(maxTagValueLen) + " characters")
		}
		value = tagValue(value)
		if cur, dup := out[key]; dup {
			if cur != value {
				note("tag " + key + " had more than one value; kept " + strconv.Quote(cur) + ", left out " + strconv.Quote(value))
			}
			return
		}
		if len(out) == maxTags {
			note("tag " + key + " was left out: SubGlance keeps at most " + strconv.Itoa(maxTags) + " tags per monitor")
			return
		}
		out[key] = value
	}
	if group != "" {
		add("group", group)
	}
	for _, t := range tags {
		add(t.name, t.value)
	}
	return out
}

// tagValue makes a SubGlance tag value from a Kuma tag value or group name,
// as the server stores it: spaces collapsed, at most maxTagValueLen
// characters, and no space at either end, which the server would trim and a
// maintenance window on the tag would then no longer match. A SubGlance tag
// always has a value; a bare Kuma tag is a flag, and "yes" says that.
func tagValue(s string) string {
	v := strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(v) > maxTagValueLen {
		v = strings.TrimSpace(string([]rune(v)[:maxTagValueLen]))
	}
	if v == "" {
		v = "yes"
	}
	return v
}

// tagKey makes a SubGlance tag key from a Kuma tag name: lowercase letters
// and digits, with dashes between words.
func tagKey(name string) string {
	key := configfile.DeriveKey(name, "", func(string) bool { return false })
	if len(key) > maxTagKeyLen {
		key = strings.TrimRight(key[:maxTagKeyLen], "-")
	}
	return key
}

func ptr[T any](v T) *T { return &v }

func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
