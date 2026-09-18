package store

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for encryption at rest of notification channel configuration.
//
// The property under test throughout is the one an operator cares about: with
// a key, the secrets are not in the file; without one, nothing changed; with
// the wrong key, the process refuses to start rather than running with five
// channels that will fail during an incident.

// testKey derives a deterministic key from a seed so that a test can reopen
// with "the same key" and with "a different key" without depending on order.
// Real keys come from a CSPRNG; these are fixtures.
func testKey(t *testing.T, seed byte) []byte {
	t.Helper()
	key := make([]byte, SecretKeyLength)
	for i := range key {
		key[i] = seed ^ byte(i)
	}
	return key
}

func openKeyedDB(t *testing.T, path string, key, previous []byte) (*DB, error) {
	t.Helper()
	return Open(context.Background(), Options{Path: path, SecretKey: key, PreviousSecretKey: previous})
}

// readStoredConfig returns config_json exactly as it sits in the file, which is
// the only vantage point from which "encrypted at rest" means anything.
func readStoredConfig(t *testing.T, db *DB, id int64) string {
	t.Helper()
	var raw string
	if err := db.Reader.QueryRow("SELECT config_json FROM notif_channels WHERE id = ?", id).Scan(&raw); err != nil {
		t.Fatalf("read stored config: %v", err)
	}
	return raw
}

// With a key, the secret must not be findable in the file, and the round trip
// must still hand callers the same map.
func TestChannelConfigIsUnreadableInTheFileWhenAKeyIsSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "encrypted.db")
	const token = "123456:AAH-super-secret-bot-token"

	db, err := openKeyedDB(t, path, testKey(t, 0x11), nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	created, err := db.CreateChannel(t.Context(), Channel{
		Name: "ops", Type: ChannelTelegram, Enabled: true,
		Config: map[string]string{"bot_token": token, "chat_id": "-100"},
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	stored := readStoredConfig(t, db, created.ID)
	if !isEncryptedConfig(stored) {
		t.Errorf("stored config is not marked as encrypted: %q", stored)
	}
	if strings.Contains(stored, token) {
		t.Errorf("stored config still contains the bot token: %q", stored)
	}
	if strings.Contains(stored, "chat_id") {
		t.Errorf("stored config leaks a key name: %q", stored)
	}

	got, err := db.GetChannel(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if got.Config["bot_token"] != token || got.Config["chat_id"] != "-100" {
		t.Errorf("config = %v, want it to read back intact", got.Config)
	}

	// And it must survive the process ending, which is the whole point.
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := openKeyedDB(t, path, testKey(t, 0x11), nil)
	if err != nil {
		t.Fatalf("reopen with the same key: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	again, err := reopened.GetChannel(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetChannel after reopen: %v", err)
	}
	if again.Config["bot_token"] != token {
		t.Errorf("bot_token = %q after reopen, want the original", again.Config["bot_token"])
	}
}

// Without a key nothing changes: the stored value stays the same JSON it has
// always been, so an existing installation that does not opt in is untouched.
func TestWithoutAKeyConfigIsStoredAsPlainJSON(t *testing.T) {
	db := openTestDB(t)

	created, err := db.CreateChannel(t.Context(), Channel{
		Name: "ops", Type: ChannelSlack, Enabled: true,
		Config: map[string]string{"url": "https://hooks.slack.test/abc"},
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if got, want := readStoredConfig(t, db, created.ID),
		`{"url":"https://hooks.slack.test/abc"}`; got != want {
		t.Errorf("stored config = %q, want the unchanged plaintext %q", got, want)
	}
}

// The requirement this covers is the one that decides whether the feature is
// safe to ship: a wrong key must be a startup failure, naming what is wrong,
// and must not present an empty configuration to anything.
func TestWrongKeyRefusesToStartInsteadOfReadingEmptyConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wrongkey.db")

	db, err := openKeyedDB(t, path, testKey(t, 0x22), nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := db.CreateChannel(t.Context(), Channel{
		Name: "ops", Type: ChannelSlack, Enabled: true,
		Config: map[string]string{"url": "https://hooks.slack.test/abc"},
	}); err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := openKeyedDB(t, path, testKey(t, 0x33), nil)
	if err == nil {
		channels, listErr := reopened.ListChannels(context.Background())
		_ = reopened.Close()
		t.Fatalf("Open succeeded with the wrong key; it must refuse. "+
			"ListChannels then returned %d channels (err=%v), which is the silent "+
			"failure this refusal exists to prevent", len(channels), listErr)
	}

	msg := err.Error()
	for _, want := range []string{"cannot be decrypted", "--secret-key"} {
		if !strings.Contains(msg, want) {
			t.Errorf("startup error does not mention %q, so it is not actionable: %v", want, err)
		}
	}
	if !strings.Contains(msg, "No row has been changed") {
		t.Errorf("startup error does not say the database is untouched, "+
			"which is the first thing the operator needs to know: %v", err)
	}
}

// Removing the key while encrypted rows exist is the other half of the same
// requirement: it must fail loudly, and the message must name the deliberate
// way to turn encryption off.
func TestRemovingTheKeyWithEncryptedRowsRefusesToStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keygone.db")
	key := testKey(t, 0x44)

	db, err := openKeyedDB(t, path, key, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := db.CreateChannel(t.Context(), Channel{
		Name: "ops", Type: ChannelSlack, Enabled: true,
		Config: map[string]string{"url": "https://hooks.slack.test/abc"},
	}); err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := openKeyedDB(t, path, nil, nil)
	if err == nil {
		_ = reopened.Close()
		t.Fatal("Open succeeded with no key against encrypted rows; the channels would " +
			"have failed silently at delivery time")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--secret-key-previous") {
		t.Errorf("error does not name the deliberate way to turn encryption off: %v", err)
	}
	if !strings.Contains(msg, "1 notification channel has encrypted configuration") {
		t.Errorf("error does not say how many channels are affected: %v", err)
	}
}

// Rotation: the old key as --secret-key-previous, the new one as --secret-key,
// for one start. Afterwards the new key alone works and the old one does not.
func TestRotatingTheKeyRewrapsEveryRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rotate.db")
	oldKey, newKey := testKey(t, 0x55), testKey(t, 0x66)
	const url = "https://hooks.slack.test/rotate-me"

	db, err := openKeyedDB(t, path, oldKey, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	created, err := db.CreateChannel(t.Context(), Channel{
		Name: "ops", Type: ChannelSlack, Enabled: true,
		Config: map[string]string{"url": url},
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	before := readStoredConfig(t, db, created.ID)
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	rotated, err := openKeyedDB(t, path, newKey, oldKey)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if got := rotated.ChannelEncryption(); got.Rewrapped != 1 || !got.Enabled {
		t.Errorf("report = %+v, want one rewrapped row with encryption enabled", got)
	}
	if after := readStoredConfig(t, rotated, created.ID); after == before {
		t.Error("stored ciphertext is unchanged after rotation, so the row was not rewrapped")
	}
	if err := rotated.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The new key alone is now sufficient...
	onlyNew, err := openKeyedDB(t, path, newKey, nil)
	if err != nil {
		t.Fatalf("open with the new key alone: %v", err)
	}
	got, err := onlyNew.GetChannel(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if got.Config["url"] != url {
		t.Errorf("url = %q, want it to survive rotation", got.Config["url"])
	}
	if err := onlyNew.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// ...and the old key alone is not, or the rotation achieved nothing.
	stale, err := openKeyedDB(t, path, oldKey, nil)
	if err == nil {
		_ = stale.Close()
		t.Fatal("the old key still opens the database after rotation")
	}
}

// Turning encryption off has to be possible on purpose, or refusing to start
// without a key would be a trap rather than a safeguard.
func TestDisablingEncryptionWritesPlaintextBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disable.db")
	key := testKey(t, 0x77)
	const url = "https://hooks.slack.test/going-plain"

	db, err := openKeyedDB(t, path, key, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	created, err := db.CreateChannel(t.Context(), Channel{
		Name: "ops", Type: ChannelSlack, Enabled: true,
		Config: map[string]string{"url": url},
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The old key as --secret-key-previous, no --secret-key.
	plain, err := openKeyedDB(t, path, nil, key)
	if err != nil {
		t.Fatalf("disable encryption: %v", err)
	}
	if got := plain.ChannelEncryption(); got.Decrypted != 1 || got.Enabled {
		t.Errorf("report = %+v, want one decrypted row with encryption off", got)
	}
	if stored := readStoredConfig(t, plain, created.ID); stored != `{"url":"`+url+`"}` {
		t.Errorf("stored config = %q, want plain JSON", stored)
	}
	if err := plain.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// And from here on, no key at all is a normal start again.
	after, err := openKeyedDB(t, path, nil, nil)
	if err != nil {
		t.Fatalf("open with no key after disabling: %v", err)
	}
	defer func() { _ = after.Close() }()
	got, err := after.GetChannel(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if got.Config["url"] != url {
		t.Errorf("url = %q, want it intact after disabling encryption", got.Config["url"])
	}
}

// A ciphertext someone edited must be refused, not decrypted into whatever it
// happens to become. This is why the construction is authenticated: an
// unauthenticated mode would hand a notifier an attacker-influenced URL.
func TestTamperedCiphertextIsRefusedRatherThanDelivered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tampered.db")
	key := testKey(t, 0x88)

	db, err := openKeyedDB(t, path, key, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	created, err := db.CreateChannel(t.Context(), Channel{
		Name: "ops", Type: ChannelSlack, Enabled: true,
		Config: map[string]string{"url": "https://hooks.slack.test/abc"},
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	// Flip one character of the ciphertext body, leaving the marker intact,
	// which is exactly what an attacker with write access would manage.
	stored := readStoredConfig(t, db, created.ID)
	body := []byte(stored[len(configCipherPrefix):])
	if body[len(body)-1] == 'A' {
		body[len(body)-1] = 'B'
	} else {
		body[len(body)-1] = 'A'
	}
	if _, err := db.Writer.Exec("UPDATE notif_channels SET config_json = ? WHERE id = ?",
		configCipherPrefix+string(body), created.ID); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	got, err := db.GetChannel(context.Background(), created.ID)
	if err == nil {
		t.Fatalf("GetChannel accepted a tampered ciphertext and returned config %v; "+
			"a modified config must never reach a notifier", got.Config)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// And the next start refuses too, rather than leaving it to be discovered
	// during an outage.
	reopened, openErr := openKeyedDB(t, path, key, nil)
	if openErr == nil {
		_ = reopened.Close()
		t.Fatal("Open accepted a database holding a tampered channel config")
	}
}

// TestOlderSchemaPlaintextChannelsAreEncryptedOnFirstStartWithAKey runs against
// testdata/channels_schema_0008.sqlite.
//
// That file is not a hand-built fixture. It was produced by building
// cmd/genfixture against commit 00f62ac — the last commit before this feature
// existed, with migrations up to 0008_notification_outbox.sql and no crypto
// anywhere in internal/ — and letting that binary's own CreateChannel write
// four channels. So the plaintext in it is plaintext the shipped code wrote,
// in the shape the shipped code wrote it, including a channel with no config
// at all and one with an SMTP password. Regenerate it the same way if the old
// write path ever needs revisiting; do not edit it by hand, because a fixture
// that is maintained by hand stops being evidence about what older versions
// actually stored.
func TestOlderSchemaPlaintextChannelsAreEncryptedOnFirstStartWithAKey(t *testing.T) {
	path := copyFixture(t, "testdata/channels_schema_0008.sqlite")
	key := testKey(t, 0x99)

	// What the old version left behind, read without any of this package's
	// help, so the test knows the starting state rather than assuming it.
	before := rawChannelConfigs(t, path)
	if len(before) != 4 {
		t.Fatalf("fixture holds %d channels, want the four the old binary wrote", len(before))
	}
	for id, raw := range before {
		if isEncryptedConfig(raw) {
			t.Fatalf("fixture channel %d is already encrypted; it is not older-version data", id)
		}
	}
	if !strings.Contains(before[3], "plaintext-smtp-password") {
		t.Fatalf("fixture channel 3 does not hold the plaintext SMTP password: %q", before[3])
	}

	db, err := openKeyedDB(t, path, key, nil)
	if err != nil {
		t.Fatalf("first start with a key against an older database: %v", err)
	}

	if got := db.ChannelEncryption(); !got.Enabled || got.Encrypted != 4 || got.Rewrapped != 0 {
		t.Errorf("report = %+v, want all four plaintext rows encrypted on first start", got)
	}

	// Every row is now ciphertext and none of the secrets is in the file.
	after := rawChannelConfigs(t, path)
	for id, raw := range after {
		if !isEncryptedConfig(raw) {
			t.Errorf("channel %d is still stored as %q after the first start with a key", id, raw)
		}
	}
	for _, secret := range []string{
		"xoxb-plaintext-secret",
		"123456:AAH-plaintext-bot-token",
		"plaintext-smtp-password",
	} {
		for id, raw := range after {
			if strings.Contains(raw, secret) {
				t.Errorf("channel %d still exposes %q in the file", id, secret)
			}
		}
	}

	// And the values the old version stored are the values this one reads.
	channels, err := db.ListChannels(context.Background())
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	byName := map[string]Channel{}
	for _, c := range channels {
		byName[c.Name] = c
	}
	if got := byName["ops-slack"].Config["url"]; got != "https://hooks.slack.test/services/T000/B000/xoxb-plaintext-secret" {
		t.Errorf("ops-slack url = %q, want the value the older version stored", got)
	}
	if got := byName["oncall-telegram"].Config["bot_token"]; got != "123456:AAH-plaintext-bot-token" {
		t.Errorf("oncall-telegram bot_token = %q, want the value the older version stored", got)
	}
	if got := byName["smtp-relay"].Config["password"]; got != "plaintext-smtp-password" {
		t.Errorf("smtp-relay password = %q, want the value the older version stored", got)
	}
	// The channel the old binary wrote with no config must survive as an empty
	// map rather than becoming nil or a decryption failure.
	if cfg := byName["bare-webhook"].Config; cfg == nil || len(cfg) != 0 {
		t.Errorf("bare-webhook config = %v, want an empty non-nil map", cfg)
	}

	// Assignments made by the old binary must still point at the same rows:
	// the rewrite changes config_json and nothing else.
	assigned, err := db.ListMonitorChannels(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListMonitorChannels: %v", err)
	}
	if len(assigned) != 3 {
		t.Errorf("monitor 1 has %d channels, want the 3 the older version assigned", len(assigned))
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A second start with the same key must be a no-op, or every restart would
	// churn the table.
	again, err := openKeyedDB(t, path, key, nil)
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	defer func() { _ = again.Close() }()
	if got := again.ChannelEncryption(); got.Encrypted != 0 || got.Rewrapped != 0 {
		t.Errorf("report = %+v on the second start, want nothing to do", got)
	}
}

// The migration must not be a one-way door either: an operator who enabled
// encryption against an older database has to be able to get back out.
func TestOlderSchemaCanBeReturnedToPlaintext(t *testing.T) {
	path := copyFixture(t, "testdata/channels_schema_0008.sqlite")
	key := testKey(t, 0xA1)

	db, err := openKeyedDB(t, path, key, nil)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	back, err := openKeyedDB(t, path, nil, key)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	defer func() { _ = back.Close() }()
	if got := back.ChannelEncryption(); got.Decrypted != 4 {
		t.Errorf("report = %+v, want all four rows decrypted", got)
	}
	if got := rawChannelConfigs(t, path)[3]; !strings.Contains(got, "plaintext-smtp-password") {
		t.Errorf("channel 3 = %q, want the original plaintext back", got)
	}
}

// copyFixture puts the read-only testdata database somewhere writable, since a
// start with a key rewrites it.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	path := filepath.Join(t.TempDir(), filepath.Base(name))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write fixture copy: %v", err)
	}
	return path
}

// rawChannelConfigs reads config_json through its own connection, deliberately
// not through DB: a test that asks this package what it stored is a test that
// believes this package.
func rawChannelConfigs(t *testing.T, path string) map[int64]string {
	t.Helper()
	conn, err := openPool(path, 1)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = conn.Close() }()

	rows, err := conn.Query("SELECT id, config_json FROM notif_channels ORDER BY id")
	if err != nil {
		t.Fatalf("read configs: %v", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int64]string{}
	for rows.Next() {
		var (
			id  int64
			cfg string
		)
		if err := rows.Scan(&id, &cfg); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[id] = cfg
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func TestParseSecretKeyAcceptsKeyMaterialAndFiles(t *testing.T) {
	key := testKey(t, 0xB2)
	hexKey := hex.EncodeToString(key)

	t.Run("hex", func(t *testing.T) {
		got, err := ParseSecretKey(hexKey)
		if err != nil {
			t.Fatalf("ParseSecretKey: %v", err)
		}
		if string(got) != string(key) {
			t.Error("hex key did not decode to the same bytes")
		}
	})

	t.Run("file with trailing newline", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "key")
		if err := os.WriteFile(path, []byte(hexKey+"\n"), 0o600); err != nil {
			t.Fatalf("write key file: %v", err)
		}
		got, err := ParseSecretKey(path)
		if err != nil {
			t.Fatalf("ParseSecretKey: %v", err)
		}
		if string(got) != string(key) {
			t.Error("key file did not decode to the same bytes")
		}
	})

	t.Run("empty means no encryption", func(t *testing.T) {
		got, err := ParseSecretKey("   ")
		if err != nil || got != nil {
			t.Errorf("ParseSecretKey(blank) = %v, %v; want nil, nil", got, err)
		}
	})

	// A passphrase is refused rather than stretched, and the message has to
	// say what is expected or the operator has nothing to act on.
	t.Run("passphrase is refused with a usable message", func(t *testing.T) {
		_, err := ParseSecretKey("correct horse battery staple")
		if err == nil {
			t.Fatal("ParseSecretKey accepted a passphrase")
		}
		if !strings.Contains(err.Error(), "32 bytes") {
			t.Errorf("error does not state the required length: %v", err)
		}
	})

	t.Run("short key file names the file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "short")
		if err := os.WriteFile(path, []byte("abcd"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		_, err := ParseSecretKey(path)
		if err == nil {
			t.Fatal("ParseSecretKey accepted a four-byte key file")
		}
		if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "openssl rand") {
			t.Errorf("error is not actionable: %v", err)
		}
	})
}

// Two writes of the same value must not produce the same stored bytes. A
// repeated nonce would break GCM outright, and it is the kind of mistake that
// leaves everything apparently working.
func TestEveryWriteGetsAFreshNonce(t *testing.T) {
	c, err := newConfigCipher(testKey(t, 0xC3))
	if err != nil {
		t.Fatalf("newConfigCipher: %v", err)
	}
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		got, err := c.seal(`{"url":"https://example.test/"}`)
		if err != nil {
			t.Fatalf("seal: %v", err)
		}
		if seen[got] {
			t.Fatalf("seal produced the same ciphertext twice at iteration %d, so a nonce was reused", i)
		}
		seen[got] = true
	}
}
