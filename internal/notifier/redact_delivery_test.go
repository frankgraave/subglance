package notifier

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/state"
	"github.com/frankgraave/subglance/internal/store"
)

// A delivery that fails on the way out must not write the channel's
// credentials to the log or to the database, with or without --secret-key.
// Go's HTTP client puts the whole request URL in every transport error, and
// for Slack, Discord and a plain webhook that URL is the credential.

// unreachable is an address nothing listens on: every connection is refused
// at once, which is the transport error that carries the request URL.
const unreachable = "http://127.0.0.1:1"

// leakCase is one channel whose secrets must stay out of what a failed
// delivery leaves behind.
type leakCase struct {
	name    string
	typ     string
	cfg     map[string]string
	secrets []string
}

// leakCases covers every channel type, each pointed at unreachable with its
// credentials where that type keeps them: in the URL path, in a header, in a
// password, in a topic.
func leakCases() []leakCase {
	return []leakCase{
		{name: "slack", typ: store.ChannelSlack,
			cfg:     map[string]string{"url": unreachable + "/services/T000/B000/SlackSecret0123"},
			secrets: []string{"SlackSecret0123"}},
		{name: "discord", typ: store.ChannelDiscord,
			cfg:     map[string]string{"url": unreachable + "/api/webhooks/42/DiscordSecret0123"},
			secrets: []string{"DiscordSecret0123"}},
		{name: "webhook", typ: store.ChannelWebhook,
			cfg: map[string]string{
				"url":     unreachable + "/hook?key=HookSecret0123",
				"headers": "Authorization: Bearer HeaderSecret0123",
			},
			secrets: []string{"HookSecret0123", "HeaderSecret0123"}},
		{name: "telegram", typ: store.ChannelTelegram,
			cfg:     map[string]string{"bot_token": "123456:TelegramSecret0123", "chat_id": "42"},
			secrets: []string{"TelegramSecret0123"}},
		{name: "ntfy", typ: store.ChannelNtfy,
			cfg:     map[string]string{"url": unreachable, "topic": "NtfyTopicSecret0123", "token": "tk_NtfySecret0123"},
			secrets: []string{"NtfyTopicSecret0123", "NtfySecret0123"}},
		{name: "gotify", typ: store.ChannelGotify,
			cfg:     map[string]string{"url": unreachable + "/GotifyPathSecret0123", "token": "GotifySecret0123"},
			secrets: []string{"GotifyPathSecret0123", "GotifySecret0123"}},
		{name: "sms-gateway", typ: store.ChannelSMS,
			cfg: map[string]string{
				"provider": SMSProviderAndroidGateway, "url": unreachable + "/GatewayPathSecret0123",
				"username": "sms", "password": "GatewaySecret0123", "numbers": "+31612345678",
			},
			secrets: []string{"GatewayPathSecret0123", "GatewaySecret0123"}},
		{name: "sms-twilio", typ: store.ChannelSMS,
			cfg: map[string]string{
				"provider": SMSProviderTwilio, "account_sid": twilioTestSID,
				"auth_token": "TwilioSecret0123", "from": "SubGlance", "numbers": "+31612345678",
			},
			secrets: []string{"TwilioSecret0123"}},
		{name: "email", typ: store.ChannelEmail,
			cfg: map[string]string{
				"host": "127.0.0.1", "port": "1", "from": "alerts@example.com", "to": "ops@example.com",
				"username": "alerts", "password": "SmtpSecret0123",
			},
			secrets: []string{"SmtpSecret0123"}},
	}
}

// realSenders is the production sender set, built with guard, with the two
// whose API host is fixed pointed at unreachable instead.
func realSenders(guard *checker.Guard) map[string]Sender {
	tg := NewTelegramSender(guard)
	tg.apiBase = unreachable
	sms := NewSMSSender(guard)
	sms.twilioBase = unreachable
	return map[string]Sender{
		store.ChannelWebhook:  NewWebhookSender(guard),
		store.ChannelDiscord:  NewDiscordSender(guard),
		store.ChannelSlack:    NewSlackSender(guard),
		store.ChannelTelegram: tg,
		store.ChannelEmail:    NewEmailSender(guard),
		store.ChannelNtfy:     NewNtfySender(guard),
		store.ChannelGotify:   NewGotifySender(guard),
		store.ChannelSMS:      sms,
	}
}

// encryptedDB opens a database with a secret key, so that channel config is
// sealed and anything readable in the file came from somewhere else.
func encryptedDB(t *testing.T) (*store.DB, string) {
	t.Helper()
	key := bytes.Repeat([]byte{7}, store.SecretKeyLength)
	path := filepath.Join(t.TempDir(), "secret.db")
	db, err := store.Open(context.Background(), store.Options{Path: path, SecretKey: key})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, path
}

// fileBytes checkpoints the write-ahead log into the database file and
// returns both files' contents, which is everything a backup or a stolen
// volume would hold.
func fileBytes(t *testing.T, db *store.DB, path string) []byte {
	t.Helper()
	var busy, frames, done int64
	if err := db.Writer.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &done); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	var out []byte
	for _, p := range []string{path, path + "-wal"} {
		b, err := os.ReadFile(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read %s: %v", p, err)
		}
		out = append(out, b...)
	}
	return out
}

func TestAFailedDeliveryKeepsChannelSecretsOutOfLogAndDatabase(t *testing.T) {
	for _, mode := range []struct {
		name  string
		guard *checker.Guard
	}{
		// Connection refused is retried, then given up: the retry log
		// line, MarkRetry, the give-up line, MarkFailed and the channel's
		// failure record.
		{name: "refused", guard: nil},
		// The guard refusing a private address is permanent: the
		// rejection line, MarkFailed and the failure record.
		{name: "blocked", guard: checker.NewGuard(false)},
	} {
		for _, c := range leakCases() {
			t.Run(mode.name+"/"+c.name, func(t *testing.T) {
				ctx := context.Background()
				db, path := encryptedDB(t)

				m, err := db.CreateMonitor(ctx, store.Monitor{Name: "api", Type: "http",
					Target: "https://example.com/health", IntervalS: 60, TimeoutS: 10, Enabled: true})
				if err != nil {
					t.Fatal(err)
				}
				ch, err := db.CreateChannel(ctx, store.Channel{Name: "broken", Type: c.typ, Config: c.cfg, Enabled: true})
				if err != nil {
					t.Fatal(err)
				}
				if err := db.SetMonitorChannels(ctx, m.ID, []int64{ch.ID}); err != nil {
					t.Fatal(err)
				}
				// A second channel of the same kind, unreachable too, so
				// the notice about the first is tried through it and
				// fails with an error of its own.
				viaCfg := map[string]string{}
				var viaSecrets []string
				for k, v := range c.cfg {
					viaCfg[k] = strings.ReplaceAll(v, "Secret0123", "ViaSecret0123")
				}
				for _, s := range c.secrets {
					viaSecrets = append(viaSecrets, strings.ReplaceAll(s, "Secret0123", "ViaSecret0123"))
				}
				via, err := db.CreateChannel(ctx, store.Channel{Name: "via", Type: c.typ, Config: viaCfg, Enabled: true})
				if err != nil {
					t.Fatal(err)
				}
				if err := db.SetDefaultChannel(ctx, via.ID); err != nil {
					t.Fatal(err)
				}

				var logs bytes.Buffer
				clock := time.Date(2026, 10, 13, 4, 0, 0, 0, time.UTC)
				n := New(Options{
					DB:          db,
					Log:         slog.New(slog.NewTextHandler(&logs, nil)),
					Senders:     realSenders(mode.guard),
					Now:         func() time.Time { return clock },
					GroupWindow: -1,
				})

				inc := openIncident(t, db, m.ID, clock, "connection refused")
				if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, clock); err != nil {
					t.Fatalf("enqueue: %v", err)
				}
				for i := 0; i < maxAttempts+1; i++ {
					if _, err := n.sweep(ctx); err != nil {
						t.Fatalf("sweep %d: %v", i, err)
					}
					n.reportFailures(ctx)
					clock = clock.Add(time.Hour)
				}

				failures, err := db.ChannelFailures(ctx)
				if err != nil {
					t.Fatal(err)
				}
				f, ok := failures[ch.ID]
				if !ok || f.LastError == "" {
					t.Fatalf("the delivery did not give up on the channel; the test is not testing a failure (log:\n%s)", logs.String())
				}
				if !strings.Contains(logs.String(), "could not send a failure notice") {
					t.Fatalf("the notice was not tried through the second channel (log:\n%s)", logs.String())
				}
				// What the operator needs stays: which host failed.
				if !strings.Contains(f.LastError, "127.0.0.1") {
					t.Errorf("the stored error lost the host: %q", f.LastError)
				}

				file := fileBytes(t, db, path)
				for _, s := range append(append([]string{}, c.secrets...), viaSecrets...) {
					if strings.Contains(logs.String(), s) {
						t.Errorf("secret %q is in the log:\n%s", s, logs.String())
					}
					if bytes.Contains(file, []byte(s)) {
						t.Errorf("secret %q is in the database file", s)
					}
				}
			})
		}
	}
}

// leakingSender fails the way an HTTP channel does, with the channel's URL in
// the message, and answers retryable when told to.
func leakingSender(retry bool) Sender {
	return senderFunc(func(_ context.Context, cfg map[string]string, _ Alert) error {
		err := fmt.Errorf("Post %q: dial tcp 127.0.0.1:1: connect: connection refused", cfg["url"])
		if retry {
			return &Retryable{Err: err}
		}
		return err
	})
}

func TestTestAndNoticeErrorsLeaveTheURLOut(t *testing.T) {
	db, _, ch := testDB(t)
	ctx := context.Background()
	const secret = "TestPathSecret0123"
	ch.Config = map[string]string{"url": "https://hooks.example.com/services/" + secret}
	ch, err := db.UpdateChannel(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetDefaultChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	n := newTestNotifier(t, db, leakingSender(false), time.Now)

	// Send test: the API writes this error into its response and its log.
	err = n.Test(ctx, ch)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Errorf("Test error = %v, want a failure without the URL's path", err)
	}
	// The stored URL is masked whole, as the API masks it; the cause
	// after it is what the operator reads.
	if err != nil && !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("Test error = %v, want the cause kept", err)
	}

	// A notice to the default channel: the backup and connectivity
	// notices log this one.
	err = n.SendNotice(ctx, Alert{MonitorName: "SubGlance", Event: EventBackupFailed})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Errorf("SendNotice error = %v, want a failure without the URL's path", err)
	}
}

func TestRedactSendErrorKeepsWhatKindOfFailureItWas(t *testing.T) {
	cfg := map[string]string{"url": "https://hooks.example.com/services/KindSecret0123"}
	leak := fmt.Errorf("Post %q: connection refused", cfg["url"])

	if redactSendError(nil, cfg) != nil {
		t.Error("a nil error came back non-nil")
	}

	retry := redactSendError(&Retryable{Err: leak}, cfg)
	var r *Retryable
	if !errors.As(retry, &r) {
		t.Error("a retryable error stopped being retryable, so it would be given up at once")
	}
	if strings.Contains(retry.Error(), "KindSecret0123") {
		t.Errorf("retryable error kept the secret: %v", retry)
	}

	refused := redactSendError(blocked(fmt.Errorf("dial %s: %w", cfg["url"], checker.ErrPrivateTarget)), cfg)
	if !errors.Is(refused, checker.ErrPrivateTarget) {
		t.Error("a refused private address is no longer recognised as one")
	}
	if strings.Contains(refused.Error(), "KindSecret0123") {
		t.Errorf("blocked error kept the secret: %v", refused)
	}

	plain := errors.New("endpoint returned 503")
	var wrapped *redactedError
	if got := redactSendError(plain, cfg); errors.As(got, &wrapped) || got.Error() != plain.Error() {
		t.Errorf("an error with nothing to take out was wrapped or changed: %#v", got)
	}
}

// TestStoredErrorsFromBeforeTheFixAreRewritten is the startup rewrite with
// the notifier's own rules, against a database that holds what a failed
// delivery used to write: the URL in full, in the outbox and in the channel's
// failure record.
func TestStoredErrorsFromBeforeTheFixAreRewritten(t *testing.T) {
	ctx := context.Background()
	db, path := encryptedDB(t)
	const secret = "OldRowSecret0123"
	url := "https://hooks.slack.com/services/T000/B000/" + secret

	m, err := db.CreateMonitor(ctx, store.Monitor{Name: "api", Type: "http",
		Target: "https://example.com/health", IntervalS: 60, TimeoutS: 10, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := db.CreateChannel(ctx, store.Channel{Name: "ops", Type: store.ChannelSlack,
		Config: map[string]string{"url": url}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	inc := openIncident(t, db, m.ID, time.Now(), "connection refused")
	old := fmt.Sprintf("gave up after 6 attempts: Post %q: dial tcp: lookup hooks.slack.com: no such host", url)

	failed, err := db.EnqueueDelivery(ctx, store.Delivery{ChannelID: ch.ID, MonitorID: m.ID,
		IncidentID: inc.ID, Event: string(state.EventIncidentConfirmed), Payload: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.MarkFailed(ctx, failed.ID, old); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordChannelFailure(ctx, ch.ID, time.Now(), old); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(fileBytes(t, db, path), []byte(secret)) {
		t.Fatal("the seeded rows do not hold the secret; the test is not testing a rewrite")
	}

	changed, err := db.RedactDeliveryErrors(ctx, RedactError)
	if err != nil {
		t.Fatalf("RedactDeliveryErrors: %v", err)
	}
	if changed != 2 {
		t.Errorf("rows changed = %d, want 2", changed)
	}
	if bytes.Contains(fileBytes(t, db, path), []byte(secret)) {
		t.Error("the secret is still in the database file after the rewrite")
	}
	got, err := db.GetDelivery(ctx, failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.LastError, "hooks.slack.com") || !strings.HasPrefix(got.LastError, "gave up after 6 attempts") {
		t.Errorf("rewritten error = %q, want the same message with the host kept", got.LastError)
	}
}

// TestANoticeErrorIsRedactedWithTheChannelItWasSentThrough covers the one
// error that belongs to a different channel than the one being reported: the
// notice about a failing channel goes out through another, and when that one
// fails too, its error carries its own credentials, not the failing one's. A
// server that echoes a token back in its refusal puts it in the text as a bare
// word, where only that channel's config can recognise it.
func TestANoticeErrorIsRedactedWithTheChannelItWasSentThrough(t *testing.T) {
	db, m, ch := testDB(t)
	ctx := context.Background()
	const viaToken = "ViaToken0123"
	ch.Config = map[string]string{"url": "https://hooks.example.com/broken"}
	ch, err := db.UpdateChannel(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	via, err := db.CreateChannel(ctx, store.Channel{Name: "via", Type: store.ChannelWebhook,
		Config:  map[string]string{"url": "https://hooks.example.com/via", "headers": "X-Token: " + viaToken},
		Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetDefaultChannel(ctx, via.ID); err != nil {
		t.Fatal(err)
	}

	sender := senderFunc(func(_ context.Context, cfg map[string]string, _ Alert) error {
		if cfg["url"] == ch.Config["url"] {
			return errors.New("endpoint rejected the alert (404)")
		}
		return errors.New("endpoint rejected the alert (401): unknown token X-Token: " + viaToken)
	})
	var logs bytes.Buffer
	clock := time.Date(2026, 10, 13, 4, 0, 0, 0, time.UTC)
	n := New(Options{DB: db, Log: slog.New(slog.NewTextHandler(&logs, nil)),
		Senders: map[string]Sender{store.ChannelWebhook: sender},
		Now:     func() time.Time { return clock }, GroupWindow: -1})

	inc := openIncident(t, db, m.ID, clock, "connection refused")
	if err := n.Enqueue(ctx, m, inc, state.EventIncidentConfirmed, clock); err != nil {
		t.Fatal(err)
	}
	if _, err := n.sweep(ctx); err != nil {
		t.Fatal(err)
	}
	n.reportFailures(ctx)

	if !strings.Contains(logs.String(), "could not send a failure notice") {
		t.Fatalf("the notice was not tried through the other channel (log:\n%s)", logs.String())
	}
	if strings.Contains(logs.String(), viaToken) {
		t.Errorf("the other channel's token is in the log:\n%s", logs.String())
	}
}
