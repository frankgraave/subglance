package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/mutelink"
	"github.com/frankgraave/subglance/internal/store"
)

// muteFixture is a server with mute links on, one channel, and a monitor
// with an open incident.
type muteFixture struct {
	srv      *Server
	db       *store.DB
	signer   *mutelink.Signer
	channel  store.Channel
	incident store.Incident
}

func newMuteFixture(t *testing.T) muteFixture {
	t.Helper()
	ctx := context.Background()
	srv, db := testServerWithDB(t)
	signer, err := mutelink.New(bytes.Repeat([]byte{3}, 32))
	must(t, err)
	srv.WithMuteLinks(signer)

	ch := seedChannel(t, db, store.ChannelWebhook, map[string]string{"url": "https://example.com/hook"})
	m, err := db.CreateMonitor(ctx, store.Monitor{
		Name: `Checkout <script>alert(1)</script>`, Type: "http", Target: "https://example.com", Enabled: true,
	})
	must(t, err)
	inc, err := db.OpenIncident(ctx, m.ID, time.Now().Add(-5*time.Minute), "status", "500")
	must(t, err)
	return muteFixture{srv: srv, db: db, signer: signer, channel: ch, incident: inc}
}

func (f muteFixture) link(expires time.Time) string {
	return mutelink.PathPrefix + f.signer.Token(f.incident.ID, f.channel.ID, expires)
}

// follow requests path with no credentials, as a phone or a link
// previewer would.
func (f muteFixture) follow(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func (f muteFixture) acked(t *testing.T) bool {
	t.Helper()
	inc, err := f.db.GetIncident(context.Background(), f.incident.ID)
	must(t, err)
	return inc.Acked()
}

// TestMuteLinkGetNeverMutes is the reason the page exists: a chat app or a
// mail scanner opens every link in a message, and that must not mute.
func TestMuteLinkGetNeverMutes(t *testing.T) {
	f := newMuteFixture(t)
	link := f.link(time.Now().Add(time.Hour))

	for range 3 {
		rec := f.follow(t, http.MethodGet, link)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		if f.acked(t) {
			t.Fatal("opening the link muted the incident")
		}
		body := rec.Body.String()
		if !strings.Contains(body, `<form method="post">`) || !strings.Contains(body, ">Mute repeat alerts</button>") {
			t.Errorf("the page has no button that posts back:\n%s", body)
		}
		if strings.Contains(body, "<script>alert(1)") || !strings.Contains(body, "Checkout &lt;script&gt;") {
			t.Errorf("the monitor name is not escaped:\n%s", body)
		}
	}
	for header, want := range map[string]string{
		"Cache-Control":   "no-store",
		"Referrer-Policy": "no-referrer",
		"X-Robots-Tag":    "noindex",
		"Content-Type":    "text/html; charset=utf-8",
	} {
		if got := f.follow(t, http.MethodGet, link).Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	csp := f.follow(t, http.MethodGet, link).Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "style-src 'sha256-", "form-action 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("Content-Security-Policy %q lacks %q", csp, want)
		}
	}
}

func TestMuteLinkPostMutesOnceAndSaysSoAfter(t *testing.T) {
	f := newMuteFixture(t)
	link := f.link(time.Now().Add(time.Hour))

	rec := f.follow(t, http.MethodPost, link)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !f.acked(t) {
		t.Fatal("the button did not mute the incident")
	}
	if !strings.Contains(rec.Body.String(), "Repeat alerts are muted") {
		t.Errorf("the answer does not say it is muted:\n%s", rec.Body.String())
	}
	first, err := f.db.GetIncident(context.Background(), f.incident.ID)
	must(t, err)
	if first.Resolved() {
		t.Error("muting resolved the incident")
	}

	// Following it again, by GET or POST, changes nothing and says so.
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := f.follow(t, method, link)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, "already muted") || strings.Contains(body, "<button") {
			t.Errorf("%s after muting: %d\n%s", method, rec.Code, body)
		}
	}
	again, err := f.db.GetIncident(context.Background(), f.incident.ID)
	must(t, err)
	if !again.AckedAt.Equal(first.AckedAt) {
		t.Errorf("a second press moved the mute time from %v to %v", first.AckedAt, again.AckedAt)
	}
}

func TestMuteLinkAfterTheIncidentIsOver(t *testing.T) {
	f := newMuteFixture(t)
	link := f.link(time.Now().Add(time.Hour))
	_, err := f.db.ResolveIncident(context.Background(), f.incident.MonitorID, time.Now())
	must(t, err)

	rec := f.follow(t, http.MethodPost, link)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "This incident is over") {
		t.Errorf("POST on a resolved incident: %d\n%s", rec.Code, rec.Body.String())
	}
	if f.acked(t) {
		t.Error("a resolved incident was marked muted")
	}
}

func TestMuteLinkThatDoesNotWork(t *testing.T) {
	f := newMuteFixture(t)
	good := f.signer.Token(f.incident.ID, f.channel.ID, time.Now().Add(time.Hour))
	parts := strings.Split(good, ".")

	other, err := mutelink.New(bytes.Repeat([]byte{4}, 32))
	must(t, err)

	cases := map[string]string{
		"changed incident": mutelink.PathPrefix + "999." + strings.Join(parts[1:], "."),
		"cut short":        mutelink.PathPrefix + good[:len(good)-4],
		"another key":      mutelink.PathPrefix + other.Token(f.incident.ID, f.channel.ID, time.Now().Add(time.Hour)),
		"unknown incident": mutelink.PathPrefix + f.signer.Token(f.incident.ID+100, f.channel.ID, time.Now().Add(time.Hour)),
		"unknown channel":  mutelink.PathPrefix + f.signer.Token(f.incident.ID, f.channel.ID+100, time.Now().Add(time.Hour)),
	}
	var bodies []string
	for name, path := range cases {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			rec := f.follow(t, method, path)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s %s: status %d, want 404", name, method, rec.Code)
			}
			bodies = append(bodies, rec.Body.String())
		}
	}
	if f.acked(t) {
		t.Fatal("a link that does not work muted the incident")
	}
	// One answer for all of them, naming nothing.
	for _, b := range bodies[1:] {
		if b != bodies[0] {
			t.Errorf("links that do not work get different pages:\n%s\n---\n%s", bodies[0], b)
			break
		}
	}
	if strings.Contains(bodies[0], "Checkout") {
		t.Error("the page for a link that does not work names the monitor")
	}

	// Removing the channel takes back the links sent to it.
	link := f.link(time.Now().Add(time.Hour))
	if rec := f.follow(t, http.MethodGet, link); rec.Code != http.StatusOK {
		t.Fatalf("before the channel is removed: %d", rec.Code)
	}
	must(t, f.db.DeleteChannel(context.Background(), f.channel.ID))
	if rec := f.follow(t, http.MethodPost, link); rec.Code != http.StatusNotFound || f.acked(t) {
		t.Errorf("after the channel is removed: status %d, muted %v; want 404 and not muted", rec.Code, f.acked(t))
	}
}

// TestMuteLinkWhenTheMonitorCannotBeRead covers a store that answers for the
// incident but not for its monitor: the page must not mute on a read that
// failed, and says it cannot answer rather than drawing a page without the
// monitor.
func TestMuteLinkWhenTheMonitorCannotBeRead(t *testing.T) {
	f := newMuteFixture(t)
	link := f.link(time.Now().Add(time.Hour))

	// Reading a monitor needs this column; reading the incident, the
	// channel and muting do not.
	if _, err := f.db.Writer.ExecContext(t.Context(), "ALTER TABLE monitors RENAME COLUMN dns_resolver TO dns_resolver_gone"); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := f.follow(t, method, link)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s with an unreadable monitor: status %d, want 503: %s", method, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "Nothing was changed") {
			t.Errorf("%s with an unreadable monitor does not say nothing changed:\n%s", method, rec.Body.String())
		}
	}
	if f.acked(t) {
		t.Fatal("the button muted the incident although its monitor could not be read")
	}
}

// TestMuteLinkWhenTheMonitorIsGone covers a monitor deleted between the
// incident read and the monitor read. Deleting a monitor takes its incidents
// with it, so the link is one that no longer works, not a store fault.
func TestMuteLinkWhenTheMonitorIsGone(t *testing.T) {
	f := newMuteFixture(t)
	link := f.link(time.Now().Add(time.Hour))

	// Leave the incident behind as if the read had raced the deletion.
	ctx := t.Context()
	for _, q := range []string{"PRAGMA foreign_keys = OFF", "DELETE FROM monitors WHERE id = ?", "PRAGMA foreign_keys = ON"} {
		var args []any
		if strings.Contains(q, "?") {
			args = append(args, f.incident.MonitorID)
		}
		if _, err := f.db.Writer.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if rec := f.follow(t, method, link); rec.Code != http.StatusNotFound {
			t.Errorf("%s with the monitor gone: status %d, want 404: %s", method, rec.Code, rec.Body.String())
		}
	}
	if f.acked(t) {
		t.Fatal("the button muted an incident whose monitor is gone")
	}
}

func TestMuteLinkExpired(t *testing.T) {
	f := newMuteFixture(t)
	link := f.link(time.Now().Add(-time.Second))
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := f.follow(t, method, link)
		if rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), "expired") {
			t.Errorf("%s on an expired link: %d\n%s", method, rec.Code, rec.Body.String())
		}
	}
	if f.acked(t) {
		t.Error("an expired link muted the incident")
	}
}

// TestMuteLinkWithoutASigner: an instance started without --base-url signs
// no links and honours none.
func TestMuteLinkWithoutASigner(t *testing.T) {
	f := newMuteFixture(t)
	link := f.link(time.Now().Add(time.Hour))
	f.srv.WithMuteLinks(nil)
	if rec := f.follow(t, http.MethodPost, link); rec.Code != http.StatusNotFound || f.acked(t) {
		t.Errorf("without a signer: status %d, muted %v", rec.Code, f.acked(t))
	}
}

func TestRedactPathHidesTheMuteToken(t *testing.T) {
	if got := redactPath("/mute/312.4.1791090000.abc"); got != "/mute/{token}" {
		t.Errorf("redactPath = %q", got)
	}
	if got := redactPath("/mute/"); got != "/mute/" {
		t.Errorf("redactPath(/mute/) = %q", got)
	}
}
