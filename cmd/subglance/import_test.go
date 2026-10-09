package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportUptimeKuma(t *testing.T) {
	fixture := filepath.Join("..", "..", "internal", "kumaimport", "testdata", "kuma-2.5.5.db")

	// To standard output, with the summary on the report stream so that
	// `> file.yaml` captures only the file.
	var out, report bytes.Buffer
	if err := importTo([]string{"uptime-kuma", fixture}, &out, &report); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "# Converted from an Uptime Kuma 2.x database") ||
		!strings.Contains(out.String(), "key: shop-prod") ||
		!strings.Contains(out.String(), "status_pages:") ||
		!strings.Contains(out.String(), "slug: public") {
		t.Errorf("output = %.300s", out.String())
	}
	if !strings.Contains(report.String(), "Converted 13 of 16 monitors, 8 of 8 channels, 6 of 14 maintenance windows and 1 of 2 status pages from Uptime Kuma 2.x.\n") ||
		strings.Contains(out.String(), "Converted 13 of") {
		t.Errorf("report = %q", report.String())
	}

	// -o after the path, the way people type it.
	dest := filepath.Join(t.TempDir(), "kuma.yaml")
	out.Reset()
	report.Reset()
	if err := importTo([]string{"uptime-kuma", fixture, "-o", dest}, &out, &report); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("with -o, standard output got %d bytes", out.Len())
	}
	written, err := os.ReadFile(dest)
	if err != nil || !bytes.Contains(written, []byte("key: shop-prod")) ||
		!bytes.Contains(written, []byte("slug: public")) {
		t.Errorf("-o file: %v %.200s", err, written)
	}
	if !strings.Contains(report.String(), "1 of 2 status pages from Uptime Kuma 2.x.") {
		t.Errorf("-o report = %q", report.String())
	}
	// Placeholders only, but the file still says where messages go.
	if st, _ := os.Stat(dest); st.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", st.Mode().Perm())
	}
}

func TestImportRefusesBadArguments(t *testing.T) {
	db := filepath.Join(t.TempDir(), "kuma.db")
	orig, err := os.ReadFile(filepath.Join("..", "..", "internal", "kumaimport", "testdata", "kuma-1.23.16.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"needs a source":                 nil,
		`cannot import from "kuma"`:      {"kuma", db},
		"needs exactly one path":         {"uptime-kuma"},
		"names the Kuma database itself": {"uptime-kuma", filepath.Dir(db), "-o", db},
	}
	for want, args := range cases {
		var out, report bytes.Buffer
		err := importTo(args, &out, &report)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%v: err = %v, want %q", args, err, want)
		}
	}
	if now, _ := os.ReadFile(db); !bytes.Equal(now, orig) {
		t.Error("the Kuma database was overwritten")
	}
}

// Monitors added for Kuma's domain expiry warning are not Kuma monitors that
// came over, so the summary counts them on a line of their own.
func TestImportCountsAddedDomainMonitorsApart(t *testing.T) {
	orig, err := os.ReadFile(filepath.Join("..", "..", "internal", "kumaimport", "testdata", "kuma-2.5.5.db"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "kuma.db")
	if err := os.WriteFile(path, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec("UPDATE monitor SET domain_expiry_notification = 1 WHERE name IN ('Shop (prod)', 'Router')"); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	var out, report bytes.Buffer
	if err := importTo([]string{"uptime-kuma", path}, &out, &report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.String(), "Converted 13 of 16 monitors,") ||
		!strings.Contains(report.String(), "\nAdded 2 domain monitors for Kuma's domain expiry warnings.\n") {
		t.Errorf("report = %q", report.String())
	}
	if !strings.Contains(out.String(), "key: example-com-registration") {
		t.Errorf("output has no domain monitor for example.com: %.400s", out.String())
	}
}
