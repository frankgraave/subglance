package configfile

import (
	"errors"
	"strings"
	"testing"
)

func TestParseRequiresAKnownVersion(t *testing.T) {
	cases := map[string]struct {
		doc, path, want string
	}{
		"missing":  {"monitors: []\n", "version", "is required"},
		"future":   {"version: 2\nsomething_new: true\n", "version", "2 is not supported"},
		"negative": {"version: -1\n", "version", "not supported"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.doc))
			var p *Problem
			if !errors.As(err, &p) {
				t.Fatalf("Parse = %v, want a *Problem", err)
			}
			if p.Path != tc.path || !strings.Contains(p.Msg, tc.want) {
				t.Errorf("problem = %q at %q, want %q at %q", p.Msg, p.Path, tc.want, tc.path)
			}
		})
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	// interval instead of interval_s: accepted silently, this would keep the
	// monitor on its old schedule while the file claims otherwise.
	doc := "version: 1\nmonitors:\n  - key: api\n    name: API\n    type: http\n    target: x\n    interval: 30\n"
	_, err := Parse([]byte(doc))
	if err == nil || !strings.Contains(err.Error(), "interval") {
		t.Fatalf("Parse = %v, want an error naming the unknown field", err)
	}
}

func TestParseRejectsASecondDocument(t *testing.T) {
	_, err := Parse([]byte("version: 1\n---\nversion: 1\n"))
	if err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("Parse = %v, want a refusal of the second document", err)
	}
}

func TestParseChecksKeys(t *testing.T) {
	cases := map[string]struct {
		doc, path string
	}{
		"missing monitor key": {"version: 1\nmonitors:\n  - name: a\n", "monitors[0].key"},
		"bad monitor key":     {"version: 1\nmonitors:\n  - key: Has Space\n", "monitors[0].key"},
		"duplicate monitor": {"version: 1\nmonitors:\n  - key: a\n  - key: b\n  - key: a\n",
			"monitors[2].key"},
		"duplicate channel": {"version: 1\nchannels:\n  - key: ops\n  - key: ops\n", "channels[1].key"},
		"two defaults": {"version: 1\nchannels:\n  - key: a\n    default: true\n  - key: b\n    default: true\n",
			"channels[1].default"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.doc))
			var p *Problem
			if !errors.As(err, &p) || p.Path != tc.path {
				t.Fatalf("Parse = %v, want a problem at %s", err, tc.path)
			}
		})
	}
}

func TestParseAcceptsAMonitorAndAChannelWithTheSameKey(t *testing.T) {
	// Keys are per kind: a monitor and the channel that alerts for it may
	// reasonably share a name.
	doc := "version: 1\nmonitors:\n  - key: shop\nchannels:\n  - key: shop\n"
	if _, err := Parse([]byte(doc)); err != nil {
		t.Fatalf("Parse: %v", err)
	}
}

func TestMarshalRoundTrips(t *testing.T) {
	enabled := true
	in := Document{
		Version: Version,
		Monitors: []Monitor{{Key: "api", Name: "API", Type: "http", Target: "t",
			Enabled: &enabled, Tags: map[string]string{"env": "prod"}, Channels: []string{"ops"}}},
		Channels: []Channel{{Key: "ops", Name: "Ops", Type: "webhook",
			Config: map[string]string{"url": Placeholder}}},
	}
	out, err := Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.HasPrefix(string(out), "# SubGlance configuration") {
		t.Errorf("export does not open with the explanatory header:\n%s", out)
	}
	back, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse(Marshal(doc)): %v\n%s", err, out)
	}
	if back.Monitors[0].Channels[0] != "ops" || back.Channels[0].Config["url"] != Placeholder ||
		*back.Monitors[0].Enabled != true || back.Monitors[0].Tags["env"] != "prod" {
		t.Errorf("round trip lost data: %+v", back)
	}
}

func TestDeriveKey(t *testing.T) {
	none := func(string) bool { return false }
	cases := map[string]string{
		"API (prod)":             "api-prod",
		"  Shop -- checkout ":    "shop-checkout",
		"Ünïcode café":           "n-code-caf",
		"🔥🔥":                     "fallback",
		"":                       "fallback",
		strings.Repeat("a", 100): strings.Repeat("a", MaxKeyLen-6),
	}
	for name, want := range cases {
		got := DeriveKey(name, "fallback", none)
		if got != want {
			t.Errorf("DeriveKey(%q) = %q, want %q", name, got, want)
		}
		if !ValidKey(got) {
			t.Errorf("DeriveKey(%q) = %q, which ValidKey refuses", name, got)
		}
	}

	taken := map[string]bool{"api": true, "api-2": true}
	if got := DeriveKey("API", "x", func(k string) bool { return taken[k] }); got != "api-3" {
		t.Errorf("DeriveKey with api and api-2 taken = %q, want api-3", got)
	}
}
