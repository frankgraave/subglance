package configfile

import (
	"errors"
	"strings"
	"testing"
)

func parseChannel(t *testing.T, quiet string) Channel {
	t.Helper()
	doc := "version: 1\nchannels:\n  - key: ops\n    name: Ops\n    type: webhook\n" + quiet
	d, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return d.Channels[0]
}

func TestQuietTellsAbsentFromNull(t *testing.T) {
	// Omitted keeps the channel's quiet hours; null removes them. A pointer
	// field could not tell the two apart, and a file exported from a channel
	// without quiet hours would then leave the target's in place.
	q, set, err := parseChannel(t, "").Quiet("channels[0].quiet_hours")
	if err != nil || set || q != nil {
		t.Errorf("omitted: q=%v set=%v err=%v, want not set", q, set, err)
	}
	q, set, err = parseChannel(t, "    quiet_hours: null\n").Quiet("channels[0].quiet_hours")
	if err != nil || !set || q != nil {
		t.Errorf("null: q=%v set=%v err=%v, want set and empty", q, set, err)
	}
	q, set, err = parseChannel(t, "    quiet_hours:\n      start: \"23:00\"\n      end: \"07:00\"\n"+
		"      timezone: Europe/Amsterdam\n      during: hold\n").Quiet("channels[0].quiet_hours")
	if err != nil || !set || q == nil || *q != (QuietHours{"23:00", "07:00", "Europe/Amsterdam", "hold"}) {
		t.Errorf("window: q=%+v set=%v err=%v", q, set, err)
	}
}

func TestQuietRefusesWhatTheDocumentDecoderWould(t *testing.T) {
	// The node escapes the document's KnownFields check, so a misspelt
	// member must still be refused rather than read as an empty value.
	cases := map[string]string{
		"unknown field": "    quiet_hours:\n      start: \"23:00\"\n      ends: \"07:00\"\n",
		"twice":         "    quiet_hours:\n      start: \"23:00\"\n      start: \"22:00\"\n",
		"not a mapping": "    quiet_hours: [\"23:00\", \"07:00\"]\n",
		"scalar":        "    quiet_hours: overnight\n",
	}
	for name, quiet := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := parseChannel(t, quiet).Quiet("channels[0].quiet_hours")
			var p *Problem
			if !errors.As(err, &p) || !strings.HasPrefix(p.Path, "channels[0].quiet_hours") {
				t.Fatalf("Quiet = %v, want a problem at channels[0].quiet_hours", err)
			}
		})
	}
}

func TestExportShapesSayNoneInAFormImportActsOn(t *testing.T) {
	none, err := QuietHoursNode(nil)
	if err != nil {
		t.Fatal(err)
	}
	empty := ""
	in := Document{Version: Version,
		Channels: []Channel{{Key: "ops", Name: "Ops", Type: "webhook", QuietHours: none}},
		Monitors: []Monitor{{Key: "api", Name: "API", Type: "http", Target: "t", Headers: Headers{}, Body: &empty}},
	}
	out, err := Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"quiet_hours: null", "headers: {}", `body: ""`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("export lacks %q:\n%s", want, out)
		}
	}
	back, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse(Marshal(doc)): %v\n%s", err, out)
	}
	if _, set, _ := back.Channels[0].Quiet("q"); !set {
		t.Error("quiet_hours: null read back as omitted")
	}
	if m := back.Monitors[0]; m.Headers == nil || len(m.Headers) != 0 || m.Body == nil || *m.Body != "" {
		t.Errorf("headers/body read back as %v / %v, want empty and present", m.Headers, m.Body)
	}

	// Absent stays absent: a hand-written file is not given keys it lacks.
	out, err = Marshal(Document{Version: Version, Monitors: []Monitor{{Key: "db", Name: "DB", Type: "tcp", Target: "t"}},
		Channels: []Channel{{Key: "ops", Name: "Ops", Type: "webhook"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"quiet_hours", "headers", "body"} {
		if strings.Contains(string(out), absent) {
			t.Errorf("a document without %s writes it:\n%s", absent, out)
		}
	}
}
