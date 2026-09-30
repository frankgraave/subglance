package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
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
