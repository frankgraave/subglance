package api

import (
	"maps"
	"strings"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// Only an http check sends a request and reads a response, so only it reads
// the method, the expected status, the keyword, the headers and the body; only
// an http or ssl check makes a TLS handshake, so only those read the TLS floor
// and the certificate warning. The tcp, ping, dns and domain checks read the
// target and the timeout, and a push monitor is not probed at all.
//
// A setting saved on a type that never reads it is shown in the monitor's
// settings and never evaluated: a keyword on a tcp monitor is a watch that
// cannot fire, because the check passes whatever the service says. That is
// the mistake a JSON assertion on a tcp monitor (json_assertion.go) and a body
// check on a HEAD monitor (headBodyCheckProblem) are already refused for.
//
// What is refused is a value that changes the check: the value the monitor
// would get with the field left out (store.ApplyMonitorDefaults) is accepted
// on every type, so a client that sends every field with its default, as a
// generic API client or an older form does, is not refused for it.

// readsHTTPSettings reports whether a check of this type sends a request and
// reads the response.
func readsHTTPSettings(typ string) bool { return typ == "http" }

// readsTLSSettings reports whether a check of this type makes a TLS handshake.
func readsTLSSettings(typ string) bool { return typ == "http" || typ == "ssl" }

// unreadFields names the request field each judged setting arrives in, in the
// order a refusal reports them.
var unreadFields = []string{
	"method", "expected_status", "keyword", "headers", "body",
	"ssl_warn_days", "min_tls_version",
}

// settingDefaults is what a monitor holds for a field it was saved without.
func settingDefaults() store.Monitor {
	var d store.Monitor
	store.ApplyMonitorDefaults(&d)
	return d
}

// changesUnreadSetting reports whether m holds, in field, a value other than
// the one it would have with the field left out.
func changesUnreadSetting(m store.Monitor, field string) bool {
	d := settingDefaults()
	switch field {
	case "method":
		return m.Method != "" && !strings.EqualFold(strings.TrimSpace(m.Method), d.Method)
	case "expected_status":
		return m.ExpectedStatus != "" && m.ExpectedStatus != d.ExpectedStatus
	case "keyword":
		// The checker's own rule (http.go): a keyword is read only with a
		// rule other than absent_ok, so a word left on "ignore" changes
		// nothing, as on a HEAD monitor.
		return m.Keyword != "" && m.KeywordMode != "" && m.KeywordMode != d.KeywordMode
	case "headers":
		return len(m.Headers) > 0
	case "body":
		return m.Body != ""
	case "ssl_warn_days":
		return m.SSLWarnDays != 0 && m.SSLWarnDays != d.SSLWarnDays
	case "min_tls_version":
		return m.MinTLSVersion != 0
	}
	return false
}

// readBy is the sentence a refusal uses for the checks that read field.
func readBy(field string) string {
	if field == "ssl_warn_days" || field == "min_tls_version" {
		return "an http or ssl check"
	}
	return "an http check"
}

// readsSetting reports whether a check of type typ reads field.
func readsSetting(typ, field string) bool {
	if field == "ssl_warn_days" || field == "min_tls_version" {
		return readsTLSSettings(typ)
	}
	return readsHTTPSettings(typ)
}

// unreadSettingsProblem refuses the first setting judge selects that m's type
// never reads and that holds a value other than its default. judge is nil to
// judge every field, as a create and a preview do.
func unreadSettingsProblem(m store.Monitor, judge func(field string) bool) problem {
	for _, field := range unreadFields {
		if readsSetting(m.Type, field) || (judge != nil && !judge(field)) {
			continue
		}
		if changesUnreadSetting(m, field) {
			return fieldProblem(field, field+" is read only by "+readBy(field)+
				"; this "+m.Type+" monitor never uses it, so remove it or change the type")
		}
	}
	return problem{}
}

// applyUnreadSettings judges a PATCH's check settings on the monitor it
// leaves, and drops the ones a changed type does not read.
//
// A setting is judged only when the request sends it with a value the monitor
// did not already hold, or sends it together with a new type. A monitor saved
// before the rule keeps working: it can be renamed, paused, and imported from
// its own export, which sends no setting its type does not read.
//
// A new type that does not read a setting resets it to its default, unless the
// same request sends it, as a change away from dns drops the dns settings and
// a change away from domain drops domain_warn_days: an http monitor's headers,
// often a credential, mean nothing to a tcp check and are not kept for it.
func applyUnreadSettings(m *store.Monitor, before store.Monitor, req patchMonitorRequest) problem {
	retyped := m.Type != before.Type
	if retyped {
		dropUnreadSettings(m, req)
	}
	sent := map[string]bool{
		"method":          req.Method != nil && (retyped || !strings.EqualFold(m.Method, before.Method)),
		"expected_status": req.ExpectedStatus != nil && (retyped || m.ExpectedStatus != before.ExpectedStatus),
		"keyword": (req.Keyword != nil || req.KeywordMode != nil) &&
			(retyped || m.Keyword != before.Keyword || m.KeywordMode != before.KeywordMode),
		"headers":         req.Headers != nil && (retyped || !maps.Equal(m.Headers, before.Headers)),
		"body":            req.Body != nil && (retyped || m.Body != before.Body),
		"ssl_warn_days":   req.SSLWarnDays != nil && (retyped || m.SSLWarnDays != before.SSLWarnDays),
		"min_tls_version": req.MinTLSVersion != nil && (retyped || m.MinTLSVersion != before.MinTLSVersion),
	}
	return unreadSettingsProblem(*m, func(field string) bool { return sent[field] })
}

// dropUnreadSettings resets the settings m's type does not read and the
// request does not send to the values a new monitor gets.
func dropUnreadSettings(m *store.Monitor, req patchMonitorRequest) {
	d := settingDefaults()
	if !readsHTTPSettings(m.Type) {
		if req.Method == nil {
			m.Method = d.Method
		}
		if req.ExpectedStatus == nil {
			m.ExpectedStatus = d.ExpectedStatus
		}
		if req.Keyword == nil && req.KeywordMode == nil {
			m.Keyword, m.KeywordMode = "", d.KeywordMode
		}
		if req.Headers == nil {
			m.Headers = nil
		}
		if req.Body == nil {
			m.Body = ""
		}
	}
	if !readsTLSSettings(m.Type) {
		if req.SSLWarnDays == nil {
			m.SSLWarnDays = d.SSLWarnDays
		}
		if req.MinTLSVersion == nil {
			m.MinTLSVersion = 0
		}
	}
}

// requestedSettings is the judged settings of a create or preview request as
// the monitor they describe. The TLS floor was validated before this runs; a
// label that does not parse counts as none.
func requestedSettings(typ, method, expectedStatus, keyword, keywordMode string,
	headers map[string]string, body string, sslWarnDays *int, minTLS *string) store.Monitor {
	m := store.Monitor{
		Type: typ, Method: method, ExpectedStatus: expectedStatus,
		Keyword: keyword, KeywordMode: keywordMode, Headers: headers, Body: body,
	}
	if sslWarnDays != nil {
		m.SSLWarnDays = *sslWarnDays
	}
	if minTLS != nil {
		m.MinTLSVersion, _ = checker.ParseTLSVersion(*minTLS)
	}
	return m
}
