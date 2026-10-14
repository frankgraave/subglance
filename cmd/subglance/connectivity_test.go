package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/config"
	"github.com/frankgraave/subglance/internal/connectivity"
	"github.com/frankgraave/subglance/internal/notifier"
	"github.com/frankgraave/subglance/internal/store"
)

func openConnectivityDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), store.Options{Path: filepath.Join(t.TempDir(), "c.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func buildCanary(t *testing.T, db *store.DB, args ...string) *connectivity.Canary {
	t.Helper()
	cfg, err := config.Load(args)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := newCanary(context.Background(), cfg, db, notifier.New(notifier.Options{DB: db, Log: log}), log)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// What was saved through the settings API is what the check runs with after
// a restart, unless a flag or variable says otherwise.
func TestNewCanaryStartsFromTheSavedSettings(t *testing.T) {
	db := openConnectivityDB(t)
	off := false
	if _, err := db.SaveConnectivity(context.Background(), store.ConnectivityChange{
		Enabled: &off, Targets: []string{"gateway:443"},
	}); err != nil {
		t.Fatal(err)
	}

	c := buildCanary(t, db)
	if c.Enabled() || !reflect.DeepEqual(c.Targets(), []string{"gateway:443"}) {
		t.Fatalf("canary = %+v, want the saved off with gateway:443", c.Settings())
	}

	c = buildCanary(t, db, "--connectivity-check=true", "--connectivity-targets=router:80")
	if !c.Enabled() || !reflect.DeepEqual(c.Targets(), []string{"router:80"}) {
		t.Fatalf("canary = %+v, want the flags to win", c.Settings())
	}
}

// Turned off by a flag, the targets are never judged at startup, so a bad
// list must not stop the canary from being built: it will never dial them.
func TestNewCanaryPinnedOffWithUnusableTargets(t *testing.T) {
	c := buildCanary(t, openConnectivityDB(t), "--connectivity-check=false", "--connectivity-targets=gateway")
	if c.Enabled() {
		t.Fatal("the canary is on with the check turned off by a flag")
	}
}

// The reference names come from the flags and variables, and follow the
// switch: a canary built from the defaults looks up the default names, and
// one told to look up none has none.
func TestNewCanaryTakesTheNames(t *testing.T) {
	db := openConnectivityDB(t)
	if got := buildCanary(t, db).Names(); !reflect.DeepEqual(got, connectivity.DefaultNames) {
		t.Fatalf("names = %v, want %v", got, connectivity.DefaultNames)
	}
	if got := buildCanary(t, db, "--connectivity-names=").Names(); len(got) != 0 {
		t.Fatalf("names = %v, want none", got)
	}
	// Turned off by a flag, an unusable list is never judged and never used.
	if got := buildCanary(t, db, "--connectivity-check=false", "--connectivity-names=1.1.1.1").Names(); len(got) != 0 {
		t.Fatalf("names = %v with the check pinned off and an unusable list", got)
	}
}

// A list saved through the settings API before hosts were held to the
// resolver's rule still starts the server, and the log names the target that
// will never be reached, so the operator learns why the check cannot work.
func TestNewCanaryWarnsAboutASavedTargetThatCannotBeLookedUp(t *testing.T) {
	db := openConnectivityDB(t)
	if _, err := db.Writer.ExecContext(context.Background(),
		`INSERT INTO settings (key, value, updated_at) VALUES ('connectivity.targets', 'exa..mple.com:443,gateway:53', 0)`); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, nil))
	c, err := newCanary(context.Background(), cfg, db, notifier.New(notifier.Options{DB: db, Log: log}), log)
	if err != nil {
		t.Fatalf("a saved list stopped the canary from being built: %v", err)
	}
	if want := []string{"exa..mple.com:443", "gateway:53"}; !reflect.DeepEqual(c.Targets(), want) {
		t.Fatalf("targets = %v, want the saved %v", c.Targets(), want)
	}
	if out := logged.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "exa..mple.com:443") {
		t.Fatalf("log = %q, want a warning naming the target", out)
	}

	// A usable saved list starts without a word about it.
	db = openConnectivityDB(t)
	logged.Reset()
	if _, err := newCanary(context.Background(), cfg, db, notifier.New(notifier.Options{DB: db, Log: log}), log); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logged.String(), "cannot be looked up") {
		t.Fatalf("log = %q, want no warning for the default targets", logged.String())
	}
}
