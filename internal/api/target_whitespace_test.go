package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// Exercise the authenticated write routes and real persistence, without running
// a checker or resolving any host. Unicode whitespace must not become a saved
// target, nor silently turn into a different hostname.
func TestMonitorTargetWhitespace(t *testing.T) {
	srv, db := testServerWithDB(t)
	handler := authedHandler(srv)
	ctx := context.Background()
	request := func(method, path string, body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(method, path, bytes.NewReader(encoded)))
		return rec
	}
	for _, typ := range []string{"ping", "tcp", "ssl", "http"} {
		t.Run(typ, func(t *testing.T) {
			shape := func(host string) string {
				switch typ {
				case "tcp":
					return host + ":443"
				case "http":
					return "https://" + host + "/health"
				default:
					return host
				}
			}
			seed := request(http.MethodPost, "/api/v1/monitors", map[string]any{"name": "seed", "type": typ, "target": shape("seed.invalid")})
			if seed.Code != http.StatusCreated {
				t.Fatalf("seed: %d %s", seed.Code, seed.Body.String())
			}
			var monitor monitorResponse
			if err := json.Unmarshal(seed.Body.Bytes(), &monitor); err != nil {
				t.Fatal(err)
			}
			path := fmt.Sprintf("/api/v1/monitors/%d", monitor.ID)
			before, err := db.GetMonitor(ctx, monitor.ID)
			if err != nil {
				t.Fatal(err)
			}
			spaces := []rune{' ', '\t', '\r', '\n', '\v', '\f', '\u0085', '\u00a0', '\u1680', '\u2003', '\u2009', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000'}
			type invalidTarget struct{ name, target string }
			var invalid []invalidTarget
			for _, space := range spaces {
				invalid = append(invalid, invalidTarget{fmt.Sprintf("U+%04X", space), shape("exam" + string(space) + "ple.invalid")})
			}
			invalid = append(invalid, invalidTarget{"inside-ipv6-brackets", shape("[2001:db8::\u20031]")})
			// Bare ping/SSL host edges are intentionally trimmed; in an authority
			// bounded by a scheme or port those spaces are internal.
			if typ == "tcp" || typ == "http" {
				invalid = append(invalid,
					invalidTarget{"host-start", "https://\u00a0example.invalid:443"},
					invalidTarget{"host-end", shape("example.invalid\u2003")},
				)
			}
			if typ == "tcp" || typ == "ssl" {
				invalid = append(invalid, invalidTarget{"url-host", "https://user:pass@exam\u2003ple.invalid:443/path"})
			}
			if typ == "http" {
				invalid = append(invalid,
					invalidTarget{"escaped-host", "https://exam" + url.PathEscape("\u00a0") + "ple.invalid/"},
					invalidTarget{"outer-ascii", " https://example.invalid/ "},
					invalidTarget{"outer-unicode", "\u00a0https://example.invalid/\u2003"},
				)
			}
			for _, tc := range invalid {
				t.Run(tc.name, func(t *testing.T) {
					for _, method := range []string{http.MethodPost, http.MethodPatch} {
						t.Run(method, func(t *testing.T) {
							endpoint := "/api/v1/monitors"
							if method == http.MethodPatch {
								endpoint = path
							}
							rows, err := db.ListMonitors(ctx)
							if err != nil {
								t.Fatal(err)
							}
							rec := request(method, endpoint, map[string]any{"name": "changed", "type": typ, "target": tc.target})
							if rec.Code != http.StatusBadRequest {
								t.Fatalf("status = %d, want 400 for internal whitespace; body=%s", rec.Code, rec.Body.String())
							}
							var problem errorResponse
							if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
								t.Fatal(err)
							}
							if problem.Field != "target" || problem.Error == "" {
								t.Fatalf("expected target field error, got %+v", problem)
							}
							afterRows, err := db.ListMonitors(ctx)
							if err != nil {
								t.Fatal(err)
							}
							if len(afterRows) != len(rows) {
								t.Fatal("rejected create persisted a monitor")
							}
							after, err := db.GetMonitor(ctx, monitor.ID)
							if err != nil {
								t.Fatal(err)
							}
							if after.Target != before.Target || after.Name != before.Name || !after.UpdatedAt.Equal(before.UpdatedAt) {
								t.Fatal("rejected update changed the monitor")
							}
						})
					}
				})
			}
			valid := []string{shape("example.invalid"), shape("localhost"), shape("example.invalid."), shape("münchen.invalid"), shape("192.0.2.1"), shape("[2001:db8::1]")}
			if typ != "http" {
				valid = append(valid, " \t\u00a0"+shape("example.invalid")+"\u2003\u3000\n")
			}
			if typ == "ping" || typ == "ssl" {
				valid = append(valid, "2001:db8::1")
			}
			if typ == "tcp" || typ == "ssl" {
				valid = append(valid, "https://user:pass@example.invalid:8443/a b\u2003c?q=x y#z", "https://example.invalid")
			}
			if typ == "http" {
				valid = append(valid, "http://example.invalid/a b\u2003c?q=x\u00a0y", "https://user:pass@example.invalid:8443/health")
			}
			for i, target := range valid {
				t.Run(fmt.Sprintf("valid-%d", i), func(t *testing.T) {
					for _, method := range []string{http.MethodPost, http.MethodPatch} {
						endpoint, status := "/api/v1/monitors", http.StatusCreated
						if method == http.MethodPatch {
							endpoint, status = path, http.StatusOK
						}
						rec := request(method, endpoint, map[string]any{"name": "valid", "type": typ, "target": target})
						if rec.Code != status {
							t.Fatalf("%s target %q: status=%d want=%d body=%s", method, target, rec.Code, status, rec.Body.String())
						}
						var response monitorResponse
						if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
							t.Fatal(err)
						}
						saved, err := db.GetMonitor(ctx, response.ID)
						if err != nil {
							t.Fatal(err)
						}
						if saved.Target != target {
							t.Fatalf("saved target = %q, want unchanged %q", saved.Target, target)
						}
					}
				})
			}
		})
	}
}
