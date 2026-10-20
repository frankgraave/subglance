package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/configfile"
	"github.com/frankgraave/subglance/internal/store"
	"gopkg.in/yaml.v3"
)

// seedEmptyHeaders stores two HTTP monitors that send a header with an empty
// value, which the API and the edit form accept and net/http sends as it
// is: one beside a credential, one with nothing else to fill in.
func seedEmptyHeaders(t *testing.T, db *store.DB) {
	t.Helper()
	ctx := context.Background()
	for _, m := range []store.Monitor{
		{Name: "Empty only", Type: "http", Target: "https://empty.example.com/", Enabled: true,
			Headers: map[string]string{"X-Empty": ""}},
		{Name: "Empty and token", Type: "http", Target: "https://token.example.com/", Enabled: true,
			Headers: map[string]string{"X-Empty": "", "Authorization": secretAuthValue}},
	} {
		_, err := db.CreateMonitor(ctx, m)
		must(t, err)
	}
}

// monitorNamed returns the stored monitor with that name.
func monitorNamed(t *testing.T, db *store.DB, name string) store.Monitor {
	t.Helper()
	mons, err := db.ListMonitors(context.Background())
	must(t, err)
	for _, m := range mons {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("no monitor named %q", name)
	return store.Monitor{}
}

func TestExportWritesAnEmptyHeaderValueAsItIs(t *testing.T) {
	srv, db := testServerWithDB(t)
	seedEmptyHeaders(t, db)

	var doc configfile.Document
	must(t, yaml.Unmarshal([]byte(exportYAML(t, srv)), &doc))
	seen := 0
	for _, m := range doc.Monitors {
		switch m.Name {
		case "Empty only":
			seen++
			if v, ok := m.Headers["X-Empty"]; !ok || v != "" {
				t.Errorf("%s: X-Empty written as %q (present %v), want \"\"", m.Name, v, ok)
			}
		case "Empty and token":
			seen++
			if v := m.Headers["X-Empty"]; v != "" {
				t.Errorf("%s: X-Empty written as %q, want \"\"", m.Name, v)
			}
			// A value that is not empty is still a credential.
			if v := m.Headers["Authorization"]; v != configfile.Placeholder {
				t.Errorf("%s: Authorization written as %q, want it withheld", m.Name, v)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("export holds %d of the 2 seeded monitors", seen)
	}
}

func TestOwnExportWithAnEmptyHeaderImportsUnchanged(t *testing.T) {
	// A file from a release that withheld empty values too must import
	// into the instance that wrote it just the same. Each case has an
	// instance of its own, so one that goes wrong cannot fail the other.
	for _, tc := range []struct {
		name   string
		legacy bool
	}{{"current export", false}, {"older export", true}} {
		t.Run(tc.name, func(t *testing.T) {
			srv, db := testServerWithDB(t)
			seedEmptyHeaders(t, db)
			file := exportYAML(t, srv)
			if tc.legacy {
				older := strings.ReplaceAll(file, `X-Empty: ""`, "X-Empty: "+configfile.Placeholder)
				if older == file {
					t.Fatal("the export holds no empty X-Empty to turn into a placeholder")
				}
				file = older
			}
			code, rep, body := importYAML(t, srv, file, false)
			if code != http.StatusOK {
				t.Fatalf("import = %d: %s", code, body)
			}
			if rep.Summary.Update != 0 || rep.Summary.Create != 0 || rep.Summary.NeedsSecrets != 0 {
				t.Errorf("summary = %+v, want everything unchanged\n%s", rep.Summary, body)
			}
			for _, name := range []string{"Empty only", "Empty and token"} {
				m := monitorNamed(t, db, name)
				if !m.Enabled {
					t.Errorf("%s was switched off", name)
				}
				if v, ok := m.Headers["X-Empty"]; !ok || v != "" {
					t.Errorf("%s: stored X-Empty = %q (present %v), want it kept empty", name, v, ok)
				}
			}
		})
	}
}

func TestExportWithAnEmptyHeaderImportsElsewhereWithoutAsking(t *testing.T) {
	src, srcDB := testServerWithDB(t)
	seedEmptyHeaders(t, srcDB)
	exported := exportYAML(t, src)

	dst, dstDB := testServerWithDB(t)
	code, rep, body := importYAML(t, dst, exported, false)
	if code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	needs := map[string]string{}
	for _, it := range rep.Monitors {
		needs[it.Name] = strings.Join(it.NeedsSecrets, ",")
	}
	if got := needs["Empty only"]; got != "" {
		t.Errorf("Empty only needs_secrets = %q, want none", got)
	}
	if got := needs["Empty and token"]; got != "headers.Authorization" {
		t.Errorf("Empty and token needs_secrets = %q, want only headers.Authorization", got)
	}

	if m := monitorNamed(t, dstDB, "Empty only"); !m.Enabled || len(m.Headers) != 1 || m.Headers["X-Empty"] != "" {
		t.Errorf("Empty only imported as enabled=%v headers=%v, want enabled with X-Empty empty", m.Enabled, m.Headers)
	} else if _, ok := m.Headers["X-Empty"]; !ok {
		t.Errorf("Empty only imported without its X-Empty header")
	}
	// Still waiting for its token, but the empty header is not part of
	// what it waits for.
	m := monitorNamed(t, dstDB, "Empty and token")
	if m.Enabled {
		t.Error("Empty and token is enabled without its Authorization value")
	}
	if v, ok := m.Headers["X-Empty"]; !ok || v != "" {
		t.Errorf("Empty and token: stored X-Empty = %q (present %v), want it empty", v, ok)
	}
}
