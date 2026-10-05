package kumaimport

import (
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/configfile"
)

func httpRow(over row) row {
	r := row{"id": int64(1), "name": "Site", "type": "http", "url": "https://example.com/", "active": int64(1),
		"interval": int64(60), "maxretries": int64(0), "timeout": float64(48), "method": "GET",
		"accepted_statuscodes_json": `["200-299"]`, "maxredirects": int64(10)}
	for k, v := range over {
		r[k] = v
	}
	return r
}

func notesOf(res Result) string {
	var b strings.Builder
	for _, n := range append(res.Skipped, res.Changed...) {
		b.WriteString(n.String() + "\n")
	}
	return b.String()
}

// Values outside SubGlance's bounds are moved inside them, so the dry run
// accepts the file, and the move is reported.
func TestMonitorBoundsAreClampedAndReported(t *testing.T) {
	cases := []struct {
		name  string
		row   row
		check func(configfile.Monitor) bool
		note  string
	}{
		{"interval below 20s", httpRow(row{"interval": int64(5)}),
			func(m configfile.Monitor) bool { return deref(m.IntervalS) == 20 }, "the interval was 5s"},
		{"interval above a day", httpRow(row{"interval": int64(172800)}),
			func(m configfile.Monitor) bool { return deref(m.IntervalS) == 86400 }, "the interval was 172800s"},
		{"retries above 10", httpRow(row{"maxretries": int64(25)}),
			func(m configfile.Monitor) bool { return deref(m.Retries) == 10 }, "retries was 25"},
		{"timeout above 120s", httpRow(row{"timeout": float64(300)}),
			func(m configfile.Monitor) bool { return deref(m.TimeoutS) == 120 }, "the timeout was 300s"},
		{"push interval below a minute", row{"id": int64(1), "name": "Job", "type": "push", "active": int64(1), "interval": int64(20)},
			func(m configfile.Monitor) bool { return deref(m.PushIntervalS) == 60 && m.IntervalS == nil }, "the push interval was 20s"},
		{"resend every 3 checks", httpRow(row{"resend_interval": int64(3)}),
			func(m configfile.Monitor) bool { return deref(m.RepeatAfterS) == 180 }, ""},
		{"no redirects", httpRow(row{"maxredirects": int64(0)}),
			func(m configfile.Monitor) bool { return m.FollowRedirects != nil && !*m.FollowRedirects }, ""},
		{"ignore TLS", httpRow(row{"ignore_tls": int64(1)}),
			func(m configfile.Monitor) bool { return true }, "always verifies the certificate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var res Result
			m, ok := convertMonitor(tc.row, &res)
			if !ok {
				t.Fatalf("skipped: %s", notesOf(res))
			}
			if !tc.check(m) {
				t.Errorf("monitor = %+v", m)
			}
			if got := notesOf(res); (tc.note == "") != (got == "") || !strings.Contains(got, tc.note) {
				t.Errorf("notes = %q, want one containing %q", got, tc.note)
			}
		})
	}
}

// A monitor that cannot be expressed is left out with a reason, never
// converted into something that checks a different thing.
func TestMonitorsThatCannotComeOver(t *testing.T) {
	cases := []struct {
		name   string
		row    row
		reason string
	}{
		{"unknown type from a later Kuma", httpRow(row{"type": "quantum"}), "SubGlance has no quantum check"},
		{"upside down", httpRow(row{"upside_down": int64(1)}), "upside-down mode"},
		{"NTLM auth", httpRow(row{"auth_method": "ntlm"}), "authentication method ntlm"},
		{"mTLS auth", httpRow(row{"auth_method": "mtls"}), "authentication method mtls"},
		{"JSONata expression", httpRow(row{"type": "json-query", "json_path": "$count(items)", "expected_value": "3"}), "JSONata beyond a plain path"},
		{"contains operator", httpRow(row{"type": "json-query", "json_path": "status", "json_path_operator": "contains", "expected_value": "o"}), "compares with contains"},
		{"less than a word", httpRow(row{"type": "json-query", "json_path": "n", "json_path_operator": "<", "expected_value": "many"}), "which is not a number"},
		{"keyword without keyword", httpRow(row{"type": "keyword", "keyword": ""}), "without a keyword"},
		{"not a URL", httpRow(row{"url": "https://"}), "is not an http or https URL"},
		{"port without port", row{"id": int64(1), "name": "Db", "type": "port", "hostname": "db", "port": nil, "interval": int64(60)}, "no usable host and port"},
		{"ping a URL", row{"id": int64(1), "name": "P", "type": "ping", "hostname": "https://x.example/", "interval": int64(60)}, "is not a hostname"},
		{"odd status codes", httpRow(row{"accepted_statuscodes_json": `["abc"]`}), "accepted status codes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var res Result
			if m, ok := convertMonitor(tc.row, &res); ok {
				t.Fatalf("converted: %+v", m)
			}
			if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, tc.reason) {
				t.Errorf("skipped = %v, want a reason containing %q", res.Skipped, tc.reason)
			}
		})
	}
}

// Kuma compares JSON query results as text; SubGlance compares typed values.
// The conversion picks the typed value a person most likely meant, and says
// so where the two can disagree.
func TestJSONQueryValues(t *testing.T) {
	cases := []struct {
		path, op, expected     string
		wantPath, wantExpected string
		note                   bool
	}{
		{"status", "==", "ok", "status", `"ok"`, false},
		{"$.data.items[0].state", "!=", "down", "data.items[0].state", `"down"`, false},
		{"count", "==", "3", "count", "3", true},
		{"healthy", "", "true", "healthy", "true", true},
		{"depth", ">", "10.5", "depth", "10.5", false},
	}
	for _, tc := range cases {
		a, note := jsonAssertion(tc.path, tc.op, tc.expected)
		if a == nil {
			t.Errorf("%s %s %s: not converted: %s", tc.path, tc.op, tc.expected, note)
			continue
		}
		if a.Path != tc.wantPath || a.Expected != tc.wantExpected {
			t.Errorf("%s %s %s = %+v", tc.path, tc.op, tc.expected, a)
		}
		if (note != "") != tc.note {
			t.Errorf("%s %s %s: note = %q, want note %v", tc.path, tc.op, tc.expected, note, tc.note)
		}
	}
}

func TestTags(t *testing.T) {
	var res Result
	got := convertTags("Site", []kumaTag{
		{"Env", "prod"}, {"env", "staging"}, {"Critical", ""}, {"Team Name", "  web   ops "}, {"!!!", "x"},
		{"long", strings.Repeat("v", 80)},
	}, "Edge servers", &res)
	want := map[string]string{"group": "Edge servers", "env": "prod", "critical": "yes", "team-name": "web ops",
		"long": strings.Repeat("v", 64)}
	if len(got) != len(want) {
		t.Errorf("tags = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("tag %s = %q, want %q", k, got[k], v)
		}
	}
	notes := notesOf(res)
	for _, frag := range []string{`kept "prod", left out "staging"`, `tag "!!!" has no letters`, "shortened to 64"} {
		if !strings.Contains(notes, frag) {
			t.Errorf("notes %q lack %q", notes, frag)
		}
	}

	res = Result{}
	var many []kumaTag
	for i := 0; i < 25; i++ {
		many = append(many, kumaTag{name: "t" + strings.Repeat("x", i), value: "v"})
	}
	if got := convertTags("Site", many, "", &res); len(got) != 20 {
		t.Errorf("%d tags kept, want SubGlance's maximum of 20", len(got))
	}
	if !strings.Contains(notesOf(res), "at most 20 tags") {
		t.Errorf("the dropped tags are not reported: %s", notesOf(res))
	}
}

func TestChannels(t *testing.T) {
	cases := []struct {
		name   string
		config string
		check  func(configfile.Channel) bool
		note   string
	}{
		{"smtp with cc and bcc", `{"type":"smtp","smtpTo":"a@example.com","smtpCC":"b@example.com, a@example.com","smtpBCC":"c@example.com","smtpHost":"mail","smtpSecure":true}`,
			func(c configfile.Channel) bool {
				return c.Config["to"] == "a@example.com, b@example.com" && c.Config["password"] == ""
			},
			"Bcc recipients were left out"},
		{"ntfy with a token", `{"type":"ntfy","ntfyserverurl":"https://ntfy.sh/","ntfytopic":"t","ntfyAuthenticationMethod":"accessToken","ntfyaccesstoken":"x"}`,
			func(c configfile.Channel) bool {
				return c.Config["url"] == "https://ntfy.sh" && c.Config["token"] == configfile.Placeholder && c.Config["username"] == ""
			}, ""},
		{"telegram topic", `{"type":"telegram","telegramBotToken":"x","telegramChatID":"5","telegramMessageThreadID":"9"}`,
			func(c configfile.Channel) bool { return c.Config["chat_id"] == "5" }, "topic 9"},
		{"gotify priority out of range", `{"type":"gotify","gotifyserverurl":"https://g","gotifyapplicationToken":"x","gotifyPriority":42}`,
			func(c configfile.Channel) bool { return c.Config["priority_down"] == "" }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var res Result
			c, ok := convertChannel(row{"id": int64(1), "name": "Ch", "active": int64(1), "config": tc.config}, &res)
			if !ok {
				t.Fatalf("skipped: %s", notesOf(res))
			}
			if !tc.check(c) {
				t.Errorf("channel = %+v", c)
			}
			if got := notesOf(res); !strings.Contains(got, tc.note) {
				t.Errorf("notes = %q, want %q", got, tc.note)
			}
		})
	}

	var res Result
	if _, ok := convertChannel(row{"id": int64(1), "name": "Mail", "config": `{"type":"smtp","smtpHost":"mail"}`}, &res); ok {
		t.Error("an email channel without recipients was converted")
	}
}

// A name with a line break must not end the comment it is reported in and
// turn the rest of the line into YAML.
func TestReportStaysInsideComments(t *testing.T) {
	res := Result{Document: configfile.Document{Version: configfile.Version}, Schema: "2.x",
		Skipped: []Note{{Kind: "monitor", Name: "evil\nmonitors: []", Type: "dns", Reason: "x\ny"}}}
	out, err := Render(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "monitors:") {
			t.Fatalf("a name broke out of its comment:\n%s", out)
		}
	}
	if _, err := configfile.Parse(out); err != nil {
		t.Errorf("parse: %v", err)
	}
}
