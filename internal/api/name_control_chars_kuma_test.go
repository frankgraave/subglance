package api

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankgraave/subglance/internal/kumaimport"
	"github.com/frankgraave/subglance/internal/store"
)

// A Kuma notification named across two lines still lets the converted file
// import: the converter puts the name on one line, and the import, which
// refuses a line break in a name, takes the result.
func TestKumaNameWithALineBreakImports(t *testing.T) {
	ctx := t.Context()
	config := `{"type":"discord","discordWebhookUrl":"https://discord.example/x"}`
	path := kumaWithNotification(t, "kuma-2.5.5.db", "Ops\nalerts", config)
	res, err := kumaimport.Convert(ctx, path)
	must(t, err)
	file, err := kumaimport.Render(res)
	must(t, err)

	srv, db := testServerWithDB(t)
	if code, _, body := importYAML(t, srv, string(file), false); code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	chans, err := db.ListChannels(ctx)
	must(t, err)
	var found bool
	for _, c := range chans {
		found = found || c.Name == "Ops alerts"
		if store.HasControlCharacter(c.Name) {
			t.Errorf("stored channel name %q", c.Name)
		}
	}
	if !found {
		t.Errorf("no channel named \"Ops alerts\" among %d", len(chans))
	}
}

// A Kuma tag whose value holds control characters still lets the converted
// file import: the converter puts the value on one line, and the import,
// which refuses a control character in a tag value, takes the result.
func TestKumaTagValueWithControlCharactersImports(t *testing.T) {
	ctx := t.Context()
	original, err := os.ReadFile(filepath.Join("..", "kumaimport", "testdata", "kuma-2.5.5.db"))
	must(t, err)
	path := filepath.Join(t.TempDir(), "kuma.db")
	must(t, os.WriteFile(path, original, 0o600))
	source, err := sql.Open("sqlite", path)
	must(t, err)
	t.Cleanup(func() { _ = source.Close() })
	added, err := source.ExecContext(ctx, "INSERT INTO tag (name, color) VALUES ('Region', '#000000')")
	must(t, err)
	tagID, err := added.LastInsertId()
	must(t, err)
	_, err = source.ExecContext(ctx,
		"INSERT INTO monitor_tag (monitor_id, tag_id, value) SELECT id, ?, ? FROM monitor WHERE name = 'Shop (prod)'",
		tagID, "eu\x00west\x1b1")
	must(t, err)
	must(t, source.Close())

	res, err := kumaimport.Convert(ctx, path)
	must(t, err)
	file, err := kumaimport.Render(res)
	must(t, err)

	srv, db := testServerWithDB(t)
	if code, _, body := importYAML(t, srv, string(file), false); code != http.StatusOK {
		t.Fatalf("import = %d: %s", code, body)
	}
	mons, err := db.ListMonitors(ctx)
	must(t, err)
	var found bool
	for _, m := range mons {
		found = found || m.Tags["region"] == "eu west 1"
		for key, value := range m.Tags {
			if store.HasControlCharacter(value) {
				t.Errorf("monitor %q stored tag %s=%q", m.Name, key, value)
			}
		}
	}
	if !found {
		t.Errorf("no monitor carries region=\"eu west 1\" among %d", len(mons))
	}
}
