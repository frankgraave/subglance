package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// A monitor, a channel and an API token each take a name of at most 100
// characters, counted as characters: 100 "é" is 200 bytes and fits. A
// monitor's name had no ceiling at all, and every alert about it carries
// it as the title, which Discord refuses past 256 characters.

var (
	longName  = strings.Repeat("é", maxNameLen+1)
	fullName  = strings.Repeat("é", maxNameLen)
	nameLimit = "name must be 100 characters or fewer"
)

func TestCreateMonitorRefusesALongName(t *testing.T) {
	srv, db := testServerWithDB(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
		`{"name":"`+longName+`","type":"http","target":"https://example.com"}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "name", nameLimit)
	if mons, _ := db.ListMonitors(context.Background()); len(mons) != 0 {
		t.Errorf("a refused create stored %d monitors", len(mons))
	}
}

func TestMonitorNameLimitCountsCharacters(t *testing.T) {
	srv, db := testServerWithDB(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/monitors",
		`{"name":"`+fullName+`","type":"http","target":"https://example.com"}`)
	mustStatus(t, rec, http.StatusCreated, "create with 100 characters")
	mons, err := db.ListMonitors(context.Background())
	must(t, err)
	if len(mons) != 1 || mons[0].Name != fullName {
		t.Fatalf("stored %+v", mons)
	}
	rec = patch(t, srv, "/api/v1/monitors/"+itoa(mons[0].ID), `{"name":"`+strings.Repeat("ü", maxNameLen)+`"}`)
	mustStatus(t, rec, http.StatusOK, "rename to 100 characters")
}

func TestPatchMonitorRefusesALongName(t *testing.T) {
	srv, db := testServerWithDB(t)
	m := seedMonitor(t, db, store.Monitor{Name: "API", Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true})
	rec := patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), `{"name":"`+longName+`"}`)
	wantStatusProblem(t, rec.Code, rec.Body.String(), "name", nameLimit)
	got, err := db.GetMonitor(context.Background(), m.ID)
	must(t, err)
	if got.Name != "API" {
		t.Errorf("a refused rename stored %q", got.Name)
	}
}

// A monitor saved before the limit keeps its name through any other edit,
// and through a PATCH that sends the same name back, as a form or an
// import of the monitor's own export does.
func TestPatchMonitorKeepsALongNameSavedBeforeTheLimit(t *testing.T) {
	srv, db := testServerWithDB(t)
	old := strings.Repeat("n", 150)
	m := seedMonitor(t, db, store.Monitor{Name: old, Type: "http", Target: "https://example.com",
		IntervalS: 60, TimeoutS: 10, Enabled: true})
	mustStatus(t, patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), `{"interval_s":120}`), http.StatusOK, "edit the interval")
	mustStatus(t, patch(t, srv, "/api/v1/monitors/"+itoa(m.ID), `{"name":"`+old+`"}`), http.StatusOK, "send the same name")
	got, err := db.GetMonitor(context.Background(), m.ID)
	must(t, err)
	if got.Name != old || got.IntervalS != 120 {
		t.Errorf("stored name %d chars, interval %d", len(got.Name), got.IntervalS)
	}
}

func TestChannelNameLimitCountsCharactersAndNamesTheField(t *testing.T) {
	channel := func(name string) string {
		return `{"name":"` + name + `","type":"webhook","config":{"url":"https://hook.example.com/x"}}`
	}
	srv, db := testServerWithDB(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels", channel(fullName))
	mustStatus(t, rec, http.StatusCreated, "create with 100 characters")
	chans, err := db.ListChannels(context.Background())
	must(t, err)
	if len(chans) != 1 || chans[0].Name != fullName {
		t.Fatalf("stored %+v", chans)
	}

	for name, tc := range map[string]struct{ name, want string }{
		"too long": {longName, nameLimit},
		"blank":    {"   ", "name is required"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels", channel(tc.name))
			wantStatusProblem(t, rec.Code, rec.Body.String(), "name", tc.want)
			rec = doJSON(t, srv, http.MethodPut, "/api/v1/channels/"+itoa(chans[0].ID), channel(tc.name))
			wantStatusProblem(t, rec.Code, rec.Body.String(), "name", tc.want)
		})
	}
}

func TestTokenNameIsTrimmedMeasuredInCharactersAndNamesTheField(t *testing.T) {
	srv, db := testServerWithDB(t)
	for name, tc := range map[string]struct{ name, want string }{
		"too long": {longName, nameLimit},
		"blank":    {"   ", "name is required"},
		"empty":    {"", "name is required"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := doJSON(t, srv, http.MethodPost, "/api/v1/tokens", `{"name":"`+tc.name+`"}`)
			wantStatusProblem(t, rec.Code, rec.Body.String(), "name", tc.want)
		})
	}

	ctx := context.Background()
	admin, err := db.GetUserByEmail(ctx, templateAdmin)
	must(t, err)
	before, err := db.ListAPITokens(ctx, admin.ID)
	must(t, err)
	mustStatus(t, doJSON(t, srv, http.MethodPost, "/api/v1/tokens", `{"name":"`+fullName+`"}`),
		http.StatusCreated, "create with 100 characters")
	mustStatus(t, doJSON(t, srv, http.MethodPost, "/api/v1/tokens", `{"name":"  grafana  "}`),
		http.StatusCreated, "create with surrounding spaces")
	after, err := db.ListAPITokens(ctx, admin.ID)
	must(t, err)
	names := map[string]bool{}
	for _, tok := range after {
		names[tok.Name] = true
	}
	if len(after) != len(before)+2 || !names[fullName] || !names["grafana"] {
		t.Errorf("tokens after = %d (before %d), names %v", len(after), len(before), names)
	}
}

// An import is held to the same limits, and names the entry it refuses.
func TestImportRefusesALongNameAtItsEntry(t *testing.T) {
	cases := map[string]struct{ doc, field string }{
		"monitor": {"version: 1\nmonitors:\n  - key: a\n    name: " + longName +
			"\n    type: tcp\n    target: a.example.com:1\n", "monitors[0].name"},
		"channel": {"version: 1\nchannels:\n  - key: c\n    name: " + longName +
			"\n    type: webhook\n    config:\n      url: https://hook.example.com/x\n", "channels[0].name"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			code, _, body := importYAML(t, srv, tc.doc, false)
			wantStatusProblem(t, code, body, tc.field, nameLimit)
			ctx := context.Background()
			mons, _ := db.ListMonitors(ctx)
			chans, _ := db.ListChannels(ctx)
			if len(mons)+len(chans) != 0 {
				t.Errorf("a refused file wrote %d monitors and %d channels", len(mons), len(chans))
			}
		})
	}
}

func TestImportAcceptsNamesOfMultibyteLettersUpToTheLimit(t *testing.T) {
	srv, db := testServerWithDB(t)
	doc := "version: 1\nchannels:\n  - key: c\n    name: " + fullName +
		"\n    type: webhook\n    config:\n      url: https://hook.example.com/x\n" +
		"monitors:\n  - key: a\n    name: " + fullName + "\n    type: tcp\n    target: a.example.com:1\n"
	code, _, body := importYAML(t, srv, doc, false)
	if code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	ctx := context.Background()
	mons, err := db.ListMonitors(ctx)
	must(t, err)
	chans, err := db.ListChannels(ctx)
	must(t, err)
	if len(mons) != 1 || mons[0].Name != fullName || len(chans) != 1 || chans[0].Name != fullName {
		t.Errorf("stored %d monitors, %d channels", len(mons), len(chans))
	}
}

// TestOpenAPITrimmedNameRequestsSetNoRawLimit keeps the document from
// refusing what the server takes. A channel's and a token's name are trimmed
// before they are measured, so "  " + 100 characters + "  " is accepted; a
// maxLength on the request schema counts the raw value and would make that
// request a contract violation. The bound lives in the description instead.
func TestOpenAPITrimmedNameRequestsSetNoRawLimit(t *testing.T) {
	spec := loadSpec(t)
	for _, schemaName := range []string{"ChannelRequest", "CreateTokenRequest"} {
		node, ok := spec.Components.Schemas[schemaName]
		if !ok {
			t.Fatalf("spec has no components.schemas.%s", schemaName)
		}
		var schema struct {
			Properties struct {
				Name struct {
					MaxLength *int `yaml:"maxLength"`
				} `yaml:"name"`
			} `yaml:"properties"`
		}
		if err := node.Decode(&schema); err != nil {
			t.Fatalf("decode %s: %v", schemaName, err)
		}
		if schema.Properties.Name.MaxLength != nil {
			t.Errorf("%s.name sets maxLength %d on the raw value, but the server trims before it counts",
				schemaName, *schema.Properties.Name.MaxLength)
		}
	}

	srv, _ := testServerWithDB(t)
	mustStatus(t, doJSON(t, srv, http.MethodPost, "/api/v1/tokens", `{"name":"  `+fullName+`  "}`),
		http.StatusCreated, "create with a full-length name and surrounding spaces")
}
