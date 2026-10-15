package api

import (
	"net/http"
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
