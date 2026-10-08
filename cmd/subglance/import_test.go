package main

import (
	"bytes"
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
		!strings.Contains(out.String(), "key: shop-prod") {
		t.Errorf("output = %.300s", out.String())
	}
	if !strings.Contains(report.String(), "Converted 13 of 16 monitors and 8 of 8 channels") ||
		strings.Contains(out.String(), "Converted 13 of") {
		t.Errorf("report = %q", report.String())
	}

	// -o after the path, the way people type it.
	dest := filepath.Join(t.TempDir(), "kuma.yaml")
	out.Reset()
	if err := importTo([]string{"uptime-kuma", fixture, "-o", dest}, &out, &report); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("with -o, standard output got %d bytes", out.Len())
	}
	written, err := os.ReadFile(dest)
	if err != nil || !bytes.Contains(written, []byte("key: shop-prod")) {
		t.Errorf("-o file: %v %.200s", err, written)
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
