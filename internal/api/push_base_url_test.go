package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The address the operator declared with --base-url, behind a proxy that
// serves SubGlance under a path prefix.
const proxiedBase = "https://example.com/subglance"

// requestThroughProxy is how a request reaches SubGlance from nginx with its
// defaults: the Host is the upstream the proxy dials, the scheme is plain
// HTTP and nothing says which path prefix the visitor used.
func requestThroughProxy(r *http.Request) *http.Request {
	r.Host = "127.0.0.1:8080"
	return r
}

// TestPushURLIsBuiltOnTheBaseURL: behind a reverse proxy, a push URL built
// from the request names the proxy's upstream address, over HTTP, without
// the prefix. It is shown once, so a job given that URL never reports and
// the monitor alerts on a job that ran. With --base-url set, the URL is the
// one the operator said SubGlance is reached at.
func TestPushURLIsBuiltOnTheBaseURL(t *testing.T) {
	srv, db := testServerWithDB(t)
	srv.WithBaseURL(proxiedBase)

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec,
		requestThroughProxy(jsonRequest(http.MethodPost, "/api/v1/monitors", pushMonitorBody)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	created := decodeMonitor(t, rec.Body.String())

	prefix := proxiedBase + "/api/v1/push/"
	if !strings.HasPrefix(created.PushURL, prefix) {
		t.Fatalf("push_url = %q, want it under %q", created.PushURL, prefix)
	}
	token := strings.TrimPrefix(created.PushURL, prefix)
	if _, err := db.MonitorByPushToken(context.Background(), token); err != nil {
		t.Errorf("the token in %q does not open the monitor: %v", created.PushURL, err)
	}
}

// TestImportedPushURLIsBuiltOnTheBaseURL: a configuration import issues a
// new push URL for each push monitor it creates, in its report, once. The
// same address rule holds there as for a monitor created on its own.
func TestImportedPushURLIsBuiltOnTheBaseURL(t *testing.T) {
	srv, _ := testServerWithDB(t)
	srv.WithBaseURL(proxiedBase)

	const doc = "version: 1\nmonitors:\n  - key: nightly-backup\n    name: Nightly backup\n    type: push\n" +
		"    enabled: true\n    push_interval_s: 86400\n    push_grace_s: 600\n"
	req := requestThroughProxy(httptest.NewRequest(http.MethodPost, "/api/v1/config/import", strings.NewReader(doc)))
	req.Header.Set("Content-Type", "application/yaml")
	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("import: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var rep importReport
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if len(rep.Monitors) != 1 || !strings.HasPrefix(rep.Monitors[0].PushURL, proxiedBase+"/api/v1/push/") {
		t.Errorf("import report monitors = %+v, want one push_url under %s", rep.Monitors, proxiedBase)
	}
}

// TestPushURLWithoutABaseURLStillFollowsTheRequest: no address configured
// means the request is the only evidence, and that is what the URL is built
// from, as before --base-url existed.
func TestPushURLWithoutABaseURLStillFollowsTheRequest(t *testing.T) {
	srv, _ := testServerWithDB(t)

	rec := httptest.NewRecorder()
	authedHandler(srv).ServeHTTP(rec,
		requestThroughProxy(jsonRequest(http.MethodPost, "/api/v1/monitors", pushMonitorBody)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := decodeMonitor(t, rec.Body.String()).PushURL; !strings.HasPrefix(got, "http://127.0.0.1:8080/api/v1/push/") {
		t.Errorf("push_url = %q, want it built from the request", got)
	}
}
