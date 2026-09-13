package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/frankgraave/subglance/internal/monitor"
	"github.com/frankgraave/subglance/internal/store"
)

// fakePusher records what reached the recorder, so the handler can be tested
// without a scheduler behind it.
type fakePusher struct {
	calls []monitor.PushReport
	ids   []int64
	err   error
}

func (f *fakePusher) RecordPush(_ context.Context, m store.Monitor, rep monitor.PushReport) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, rep)
	f.ids = append(f.ids, m.ID)
	return nil
}

// createPushMonitor makes one through the API, which is the only way a token
// is ever issued, and returns the created response.
func createPushMonitor(t *testing.T, srv *Server, body string) monitorResponse {
	t.Helper()

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodPost, "/api/v1/monitors", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create push monitor: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var got monitorResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

const pushMonitorBody = `{"name":"nightly backup","type":"push","push_interval_s":3600,"push_grace_s":300}`

// TestCreatePushMonitorReturnsItsURLOnce is the credential contract at the API
// boundary: the URL is in the create response and nowhere else, ever.
func TestCreatePushMonitorReturnsItsURLOnce(t *testing.T) {
	srv, _ := testServerWithDB(t)
	created := createPushMonitor(t, srv, pushMonitorBody)

	if created.PushURL == "" {
		t.Fatal("create response carries no push_url")
	}
	if !strings.Contains(created.PushURL, "/api/v1/push/") {
		t.Errorf("push_url = %q, does not look like a push endpoint", created.PushURL)
	}
	if created.PushTokenPrefix == "" {
		t.Error("create response carries no push_token_prefix")
	}
	if created.PushIntervalS != 3600 || created.PushGraceS != 300 {
		t.Errorf("window = %d/%d, want 3600/300", created.PushIntervalS, created.PushGraceS)
	}

	// Every other read must be free of the plaintext.
	for _, path := range []string{
		"/api/v1/monitors",
		"/api/v1/monitors/" + itoa(created.ID),
	} {
		rec := httptest.NewRecorder()
		authedHandler(srv).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		body := rec.Body.String()
		if strings.Contains(body, "push_url") {
			t.Errorf("GET %s leaks push_url", path)
		}
		token := strings.TrimPrefix(created.PushURL, created.PushURL[:strings.LastIndex(created.PushURL, "/")+1])
		if strings.Contains(body, token) {
			t.Errorf("GET %s leaks the push token itself", path)
		}
	}
}

func TestCreatePushMonitorValidation(t *testing.T) {
	srv, _ := testServerWithDB(t)

	cases := map[string]struct {
		body      string
		wantField string
	}{
		"no interval": {
			`{"name":"j","type":"push"}`, "push_interval_s",
		},
		"interval too short": {
			`{"name":"j","type":"push","push_interval_s":10}`, "push_interval_s",
		},
		"interval too long": {
			`{"name":"j","type":"push","push_interval_s":99999999}`, "push_interval_s",
		},
		"negative grace": {
			`{"name":"j","type":"push","push_interval_s":3600,"push_grace_s":-1}`, "push_grace_s",
		},
		"target on a push monitor": {
			`{"name":"j","type":"push","push_interval_s":3600,"target":"https://example.com"}`, "target",
		},
		"push fields on an http monitor": {
			`{"name":"j","type":"http","target":"https://example.com","push_interval_s":3600}`, "push_interval_s",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			authedHandler(srv).ServeHTTP(rec,
				jsonRequest(http.MethodPost, "/api/v1/monitors", tc.body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
			var got errorResponse
			if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Field != tc.wantField {
				t.Errorf("field = %q, want %q (message: %s)", got.Field, tc.wantField, got.Error)
			}
		})
	}
}

// TestPushMonitorGraceDefaults checks the default is 60 and not 0. Zero would
// turn ordinary cron jitter into an outage.
func TestPushMonitorGraceDefaults(t *testing.T) {
	srv, _ := testServerWithDB(t)
	created := createPushMonitor(t, srv,
		`{"name":"j","type":"push","push_interval_s":3600}`)

	if created.PushGraceS != store.DefaultPushGraceS {
		t.Errorf("default grace = %d, want %d", created.PushGraceS, store.DefaultPushGraceS)
	}
}

// TestPushEndpointRecordsAReport walks the whole public path: URL in, report
// through to the recorder.
func TestPushEndpointRecordsAReport(t *testing.T) {
	srv, _ := testServerWithDB(t)
	created := createPushMonitor(t, srv, pushMonitorBody)
	pusher := &fakePusher{}
	srv.WithPushRecorder(pusher)

	path := created.PushURL[strings.Index(created.PushURL, "/api/v1/push/"):]
	rec := httptest.NewRecorder()
	// No credentials at all: this endpoint has to work from a crontab.
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if len(pusher.calls) != 1 {
		t.Fatalf("recorder got %d reports, want 1", len(pusher.calls))
	}
	if !pusher.calls[0].OK {
		t.Error("a bare ping was recorded as a failure")
	}
	if pusher.ids[0] != created.ID {
		t.Errorf("reported for monitor %d, want %d", pusher.ids[0], created.ID)
	}

	var resp pushResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Recorded || resp.MonitorID != created.ID {
		t.Errorf("response = %+v", resp)
	}
}

// TestPushEndpointParsesStatus covers the shapes a shell can produce,
// including `?status=$?` in both its outcomes.
func TestPushEndpointParsesStatus(t *testing.T) {
	cases := map[string]struct {
		query    string
		wantOK   bool
		wantCode int
	}{
		"bare":            {"", true, http.StatusOK},
		"up":              {"?status=up", true, http.StatusOK},
		"exit code zero":  {"?status=0", true, http.StatusOK},
		"down":            {"?status=down", false, http.StatusOK},
		"fail":            {"?status=fail", false, http.StatusOK},
		"exit code one":   {"?status=1", false, http.StatusOK},
		"exit code 127":   {"?status=127", false, http.StatusOK},
		"nonsense status": {"?status=maybe", false, http.StatusBadRequest},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _ := testServerWithDB(t)
			created := createPushMonitor(t, srv, pushMonitorBody)
			pusher := &fakePusher{}
			srv.WithPushRecorder(pusher)

			path := created.PushURL[strings.Index(created.PushURL, "/api/v1/push/"):]
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+tc.query, nil))

			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantCode != http.StatusOK {
				return
			}
			if len(pusher.calls) != 1 {
				t.Fatalf("recorder got %d reports, want 1", len(pusher.calls))
			}
			if pusher.calls[0].OK != tc.wantOK {
				t.Errorf("ok = %v, want %v", pusher.calls[0].OK, tc.wantOK)
			}
		})
	}
}

// TestPushEndpointRejectsUnknownTokens checks both that an unknown token is
// refused and that the refusal says nothing useful to someone guessing.
func TestPushEndpointRejectsUnknownTokens(t *testing.T) {
	srv, _ := testServerWithDB(t)
	createPushMonitor(t, srv, pushMonitorBody)
	pusher := &fakePusher{}
	srv.WithPushRecorder(pusher)

	for _, token := range []string{"sgu_nope", "x", "sgp_apitokenshapedthing"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec,
			httptest.NewRequest(http.MethodGet, "/api/v1/push/"+token, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("token %q: status = %d, want 404", token, rec.Code)
		}
		if body := rec.Body.String(); strings.Contains(body, "monitor") &&
			!strings.Contains(body, "unknown push URL") {
			t.Errorf("token %q: response is too informative: %s", token, body)
		}
	}
	if len(pusher.calls) != 0 {
		t.Errorf("%d reports recorded for unknown tokens", len(pusher.calls))
	}
}

// TestPushEndpointQuotaPrecedesTheTokenLookup guards the route as a whole
// rather than one monitor. The per-monitor cooldown is keyed on an id that only
// exists after the token has been resolved, so without this a flood of made-up
// tokens reaches the reader pool at network speed and crowds out real queries.
//
// An unknown token answered with 429 instead of 404 is the proof: the only way
// to say "too many" without saying "unknown" is to have refused before asking
// the database. The bucket is drained directly rather than by firing a burst of
// requests, so the assertion does not depend on how fast the test host is.
func TestPushEndpointQuotaPrecedesTheTokenLookup(t *testing.T) {
	srv, _ := testServerWithDB(t)
	pusher := &fakePusher{}
	srv.WithPushRecorder(pusher)

	now := time.Now()
	for i := range int(pushFloodBurst) {
		if !srv.pushFlood.allow(now, pushFloodRate, pushFloodBurst) {
			t.Fatalf("the bucket refused claim %d, which is inside its burst", i)
		}
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/api/v1/push/sgu_nosuchtoken", nil))

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429: an unknown token reached the database past an empty quota", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("a refused report does not say when to try again")
	}
	if len(pusher.calls) != 0 {
		t.Errorf("%d reports recorded while the quota was empty", len(pusher.calls))
	}
}

// TestPushFloodBucketRefills keeps the limit from being a one-way gate: a burst
// is refused, and an honest job pinging a second later is not.
func TestPushFloodBucketRefills(t *testing.T) {
	var b tokenBucket
	start := time.Now()

	for i := range int(pushFloodBurst) {
		if !b.allow(start, pushFloodRate, pushFloodBurst) {
			t.Fatalf("refused request %d, which is inside the burst", i)
		}
	}
	if b.allow(start, pushFloodRate, pushFloodBurst) {
		t.Fatal("the bucket let through one more than its burst")
	}

	// One second later there are pushFloodRate tokens back, and no more.
	if !b.allow(start.Add(time.Second), pushFloodRate, pushFloodBurst) {
		t.Error("the bucket never refilled")
	}
}

// TestPushEndpointRateLimits: this is the only unauthenticated write in the
// product, so an unbounded one is a way to fill the disk with one valid token.
func TestPushEndpointRateLimits(t *testing.T) {
	srv, _ := testServerWithDB(t)
	created := createPushMonitor(t, srv, pushMonitorBody)
	srv.WithPushRecorder(&fakePusher{})

	path := created.PushURL[strings.Index(created.PushURL, "/api/v1/push/"):]

	first := httptest.NewRecorder()
	srv.Handler().ServeHTTP(first, httptest.NewRequest(http.MethodGet, path, nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first report: status = %d", first.Code)
	}

	second := httptest.NewRecorder()
	srv.Handler().ServeHTTP(second, httptest.NewRequest(http.MethodGet, path, nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second report: status = %d, want 429", second.Code)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Error("429 carries no Retry-After")
	}
}

// TestPushEndpointAcceptsPausedMonitorsWithoutRecording mirrors the manual
// check: a paused monitor is one the instance promised not to watch.
func TestPushEndpointAcceptsPausedMonitorsWithoutRecording(t *testing.T) {
	srv, _ := testServerWithDB(t)
	created := createPushMonitor(t, srv, pushMonitorBody)
	pusher := &fakePusher{}
	srv.WithPushRecorder(pusher)

	pause := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(pause,
		jsonRequest(http.MethodPost, "/api/v1/monitors/"+itoa(created.ID)+"/pause", ""))
	if pause.Code != http.StatusOK {
		t.Fatalf("pause: status = %d", pause.Code)
	}

	path := created.PushURL[strings.Index(created.PushURL, "/api/v1/push/"):]
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp pushResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Recorded {
		t.Error("recorded = true for a paused monitor")
	}
	if len(pusher.calls) != 0 {
		t.Errorf("%d reports written for a paused monitor", len(pusher.calls))
	}
}

// TestPushEndpointWithoutARecorder: an API built without a checker pipeline
// must say so rather than panic on a nil interface.
func TestPushEndpointWithoutARecorder(t *testing.T) {
	srv, _ := testServerWithDB(t)
	created := createPushMonitor(t, srv, pushMonitorBody)

	path := created.PushURL[strings.Index(created.PushURL, "/api/v1/push/"):]
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// TestPushMessageIsTruncatedOnARuneBoundary: cutting mid-sequence would store
// invalid UTF-8 in a STRICT TEXT column.
func TestPushMessageIsTruncatedOnARuneBoundary(t *testing.T) {
	srv, _ := testServerWithDB(t)
	created := createPushMonitor(t, srv, pushMonitorBody)
	pusher := &fakePusher{}
	srv.WithPushRecorder(pusher)

	// One ASCII byte then two-byte runes, so the 500-byte cut lands squarely
	// inside a rune rather than on a boundary. Without the leading "x" the
	// cut is even and the bug this test exists for cannot occur.
	long := "x" + strings.Repeat("é", maxPushMessageLen)
	path := created.PushURL[strings.Index(created.PushURL, "/api/v1/push/"):]

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, path+"?status=down&msg="+long, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if len(pusher.calls) != 1 {
		t.Fatalf("recorder got %d reports", len(pusher.calls))
	}

	msg := pusher.calls[0].Message
	if len(msg) > maxPushMessageLen {
		t.Errorf("message is %d bytes, want at most %d", len(msg), maxPushMessageLen)
	}
	if !utf8.ValidString(msg) {
		t.Fatal("message was cut mid-rune, storing invalid UTF-8")
	}
}

// TestPatchCannotConvertToOrFromPush. Both directions break something the user
// cannot see, so both are refused.
func TestPatchCannotConvertToOrFromPush(t *testing.T) {
	srv, _ := testServerWithDB(t)
	push := createPushMonitor(t, srv, pushMonitorBody)

	httpRec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(httpRec, jsonRequest(http.MethodPost, "/api/v1/monitors",
		`{"name":"web","type":"http","target":"https://example.com"}`))
	if httpRec.Code != http.StatusCreated {
		t.Fatalf("create http monitor: %d", httpRec.Code)
	}
	var web monitorResponse
	if err := json.NewDecoder(httpRec.Body).Decode(&web); err != nil {
		t.Fatalf("decode: %v", err)
	}

	cases := map[string]struct {
		id   int64
		body string
	}{
		"push to http": {push.ID, `{"type":"http","target":"https://example.com"}`},
		"http to push": {web.ID, `{"type":"push"}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodPatch,
				"/api/v1/monitors/"+itoa(tc.id), tc.body))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestPatchRetunesThePushWindow: a backup that moved from hourly to nightly
// must not have to be recreated, which would invalidate the URL in the crontab.
func TestPatchRetunesThePushWindow(t *testing.T) {
	srv, db := testServerWithDB(t)
	created := createPushMonitor(t, srv, pushMonitorBody)

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodPatch,
		"/api/v1/monitors/"+itoa(created.ID),
		`{"push_interval_s":86400,"push_grace_s":1800}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}

	m, err := db.GetMonitor(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if m.PushIntervalS != 86400 || m.PushGraceS != 1800 {
		t.Errorf("window = %d/%d, want 86400/1800", m.PushIntervalS, m.PushGraceS)
	}

	// The URL must still resolve to the same monitor: a retune is not a
	// rotation, and a script holding the old URL keeps working.
	token := created.PushURL[strings.LastIndex(created.PushURL, "/")+1:]
	same, err := db.MonitorByPushToken(context.Background(), token)
	if err != nil || same.ID != created.ID {
		t.Errorf("push URL stopped working after a retune: %v", err)
	}
}

// TestPatchDoesNotRotateThePushToken pins the other half: an ordinary edit
// must not silently break the URL already sitting in someone's crontab.
func TestPatchDoesNotRotateThePushToken(t *testing.T) {
	srv, db := testServerWithDB(t)
	created := createPushMonitor(t, srv, pushMonitorBody)
	token := created.PushURL[strings.LastIndex(created.PushURL, "/")+1:]

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, jsonRequest(http.MethodPatch,
		"/api/v1/monitors/"+itoa(created.ID), `{"name":"renamed"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}

	m, err := db.MonitorByPushToken(context.Background(), token)
	if err != nil {
		t.Fatalf("push URL stopped working after a rename: %v", err)
	}
	if m.Name != "renamed" {
		t.Errorf("name = %q, want renamed", m.Name)
	}
}

// TestPushURLUsesTheRequestHost. A URL built from a configured base that is
// wrong behind a proxy silently never reports — the exact failure a dead man's
// switch exists to catch.
func TestPushURLUsesTheRequestHost(t *testing.T) {
	srv, _ := testServerWithDB(t)

	req := jsonRequest(http.MethodPost, "/api/v1/monitors", pushMonitorBody)
	req.Host = "status.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}

	var got monitorResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(got.PushURL, "https://status.example.com/api/v1/push/") {
		t.Errorf("push_url = %q, want it built from the request host and forwarded scheme", got.PushURL)
	}
}

// TestPushMonitorStatusReflectsReports is the end-to-end claim in the terms
// the dashboard uses.
func TestPushMonitorStatusReflectsReports(t *testing.T) {
	srv, db := testServerWithDB(t)
	created := createPushMonitor(t, srv, pushMonitorBody)
	ctx := context.Background()

	if err := db.RecordHeartbeat(ctx, store.Heartbeat{
		MonitorID: created.ID, TS: time.Now(), OK: true,
	}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/api/v1/monitors/"+itoa(created.ID), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var got monitorResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "up" {
		t.Errorf("status = %q, want up", got.Status)
	}
	if got.Type != store.TypePush {
		t.Errorf("type = %q, want push", got.Type)
	}
}
