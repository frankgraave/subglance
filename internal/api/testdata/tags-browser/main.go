// tags-browser runs the production API, SQLite and embedded UI for tag proofs.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"

	"github.com/frankgraave/subglance/internal/api"
	"github.com/frankgraave/subglance/internal/auth"
	"github.com/frankgraave/subglance/internal/events"
	"github.com/frankgraave/subglance/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Path: os.Args[1]})
	if err != nil {
		return err
	}
	defer db.Close()
	password, err := auth.GenerateSessionToken()
	if err != nil {
		return err
	}
	user, err := db.CreateUser(ctx, "tags-browser@example.com", password, store.RoleEditor)
	if err != nil {
		return err
	}
	session, err := db.CreateSession(ctx, user.ID, "tag browser test", "127.0.0.1")
	if err != nil {
		return err
	}
	viewer, err := db.CreateUser(ctx, "tag-viewer@example.com", password, store.RoleViewer)
	if err != nil {
		return err
	}
	readSession, err := db.CreateSession(ctx, viewer.ID, "tag viewer test", "127.0.0.1")
	if err != nil {
		return err
	}
	for i := 0; i < 205; i++ {
		tags := map[string]string{"team": "ops"}
		for _, suffix := range []string{"dark-390", "light-390", "dark-1440", "light-1440"} {
			tags["old-"+suffix] = "prod"
			if i == 0 {
				tags["env-"+suffix] = "staging"
			}
		}
		_, err := db.CreateMonitor(ctx, store.Monitor{Name: fmt.Sprintf("Tag monitor %03d", i+1), Type: "http", Target: "https://example.com", Enabled: true, Tags: tags, Headers: map[string]string{"X-Preserve": "yes"}, Body: "preserve", RepeatAfterS: 900})
		if err != nil {
			return err
		}
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(api.New(log, db).WithBus(events.NewBus(32)).Handler())
	defer server.Close()
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"url": server.URL, "session": session, "viewer_session": readSession}); err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, os.Stdin)
	return err
}
