package api

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// Encryption at rest sits underneath this package, and the point of putting it
// there is that nothing here has to change. These tests state that as a
// property rather than trusting it: the read path must still mask, and the
// secret must not surface in a log at any level.

// encryptionTestKey is the key material the keyed server below uses. It is a
// repeated byte pattern so the test can search a log for the key itself.
const encryptionTestKeyHex = "abababababababababababababababababababababababababababababababab"

func keyedServer(t *testing.T) (*Server, *store.DB, *bytes.Buffer) {
	t.Helper()

	key, err := hex.DecodeString(encryptionTestKeyHex)
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	db, err := store.Open(context.Background(), store.Options{
		Path:      filepath.Join(t.TempDir(), "api-encrypted.db"),
		SecretKey: key,
	})
	if err != nil {
		t.Fatalf("open encrypted store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

	srv := New(log, db)
	seedUser(t, srv, db, "admin@example.com", store.RoleAdmin)
	return srv, db, &logged
}

// The mask is what stops a viewer reading a webhook URL out of the API. It has
// to keep working over an encrypted store, and it has to keep masking rather
// than accidentally returning ciphertext, which would look masked while being
// a different bug.
func TestMaskingIsUnchangedOverAnEncryptedStore(t *testing.T) {
	srv, db, _ := keyedServer(t)

	const hook = "https://hooks.slack.com/services/T000/B000/verysecrettail"
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/channels",
		`{"name":"ops","type":"slack","config":{"url":"`+hook+`"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create channel: status = %d: %s", rec.Code, rec.Body.String())
	}

	var got channelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Config["url"] == hook {
		t.Error("the API returned the webhook URL in full")
	}
	if want := "****tail"; got.Config["url"] != want {
		t.Errorf("masked url = %q, want %q — the same mask as without encryption",
			got.Config["url"], want)
	}

	// And the store still hands this package the real value, so a
	// read-modify-write can put the unmasked secret back and a sender can use
	// it. That is the requirement that encryption be invisible above the store.
	stored, err := db.GetChannel(context.Background(), got.ID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if stored.Config["url"] != hook {
		t.Errorf("stored url = %q, want the original", stored.Config["url"])
	}
}

// --log-level debug is a documented setting, so "it only leaks at debug" is not
// a defence. Neither a configuration value nor the key may appear.
func TestChannelSecretsAndKeyNeverReachTheLogAtDebugLevel(t *testing.T) {
	srv, db, logged := keyedServer(t)

	const hook = "https://hooks.slack.com/services/T000/B000/DEBUGLEAKCANARY"
	created := createSlackChannel(t, srv, "ops", hook)
	id := strconv.FormatInt(created.ID, 10)

	// Both directions, plus an update, which is the save path.
	doJSON(t, srv, http.MethodGet, "/api/v1/channels", "")
	doJSON(t, srv, http.MethodGet, "/api/v1/channels/"+id, "")
	doJSON(t, srv, http.MethodPatch, "/api/v1/channels/"+id, `{"name":"ops-renamed"}`)
	if _, err := db.ListChannels(context.Background()); err != nil {
		t.Fatalf("ListChannels: %v", err)
	}

	out := logged.String()
	for _, canary := range []string{hook, "DEBUGLEAKCANARY", encryptionTestKeyHex} {
		if strings.Contains(out, canary) {
			t.Errorf("the log contains %q:\n%s", canary, out)
		}
	}
}
