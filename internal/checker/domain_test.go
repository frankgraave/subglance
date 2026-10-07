package checker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRDAP is an IANA bootstrap file and one registry's RDAP server in one
// httptest server. The bootstrap maps the TLDs in tlds to the server itself.
type fakeRDAP struct {
	srv *httptest.Server

	// domains maps a domain to the RDAP document the server returns for it;
	// a domain not in it is a 404.
	domains map[string]string
	// status, when set, is returned for every domain lookup instead.
	status int

	bootstrapStatus atomic.Int32
	bootstrapHits   atomic.Int32
	lookups         atomic.Int32
	lastAccept      atomic.Value
}

func newFakeRDAP(t *testing.T, tlds ...string) *fakeRDAP {
	t.Helper()
	f := &fakeRDAP{domains: map[string]string{}}
	f.bootstrapStatus.Store(http.StatusOK)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/bootstrap.json":
			f.bootstrapHits.Add(1)
			if s := int(f.bootstrapStatus.Load()); s != http.StatusOK {
				w.WriteHeader(s)
				return
			}
			quoted := make([]string, len(tlds))
			for i, tld := range tlds {
				quoted[i] = fmt.Sprintf("%q", tld)
			}
			_, _ = fmt.Fprintf(w, `{"version":"1.0","services":[[[%s],["%s/rdap/"]],[["other"],["https://rdap.invalid/"]]]}`,
				strings.Join(quoted, ","), f.srv.URL)
		case strings.HasPrefix(r.URL.Path, "/rdap/domain/"):
			f.lookups.Add(1)
			f.lastAccept.Store(r.Header.Get("Accept"))
			if f.status != 0 {
				w.WriteHeader(f.status)
				return
			}
			doc, ok := f.domains[strings.TrimPrefix(r.URL.Path, "/rdap/domain/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/rdap+json")
			_, _ = w.Write([]byte(doc))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// rdapDomain is an RDAP domain document with the events a registry sends.
func rdapDomain(expiry string) string {
	return `{"objectClassName":"domain","ldhName":"EXAMPLE.COM","events":[` +
		`{"eventAction":"registration","eventDate":"1995-08-14T04:00:00Z"},` +
		`{"eventAction":"expiration","eventDate":"` + expiry + `"},` +
		`{"eventAction":"last update of RDAP database","eventDate":"2026-10-07T09:00:00Z"}]}`
}

// domainChecker points a checker at the fake, with the clock at now. The
// guard is open: the fake listens on loopback.
func (f *fakeRDAP) checker(now time.Time) *DomainChecker {
	c := NewDomainChecker(NewGuard(true))
	c.bootstrapURL = f.srv.URL + "/bootstrap.json"
	c.now = func() time.Time { return now }
	return c
}

var domainNow = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func domainMonitor(target string, warnDays int) Monitor {
	return Monitor{Type: TypeDomain, Target: target, Timeout: 5 * time.Second, DomainWarnDays: warnDays}
}

func TestDomainCheckerPassesOutsideTheWarningWindow(t *testing.T) {
	f := newFakeRDAP(t, "com")
	f.domains["example.com"] = rdapDomain("2027-08-13T04:00:00Z")

	res := f.checker(domainNow).Check(context.Background(), domainMonitor("www.example.com", 30))
	if !res.OK || res.Expiring || res.Kind != FailNone || res.Error != "" {
		t.Fatalf("result = %+v, want a plain pass", res)
	}
	if want := time.Date(2027, 8, 13, 4, 0, 0, 0, time.UTC); !res.DomainExpiry.Equal(want) {
		t.Errorf("DomainExpiry = %v, want %v", res.DomainExpiry, want)
	}
	if res.CheckedAt != domainNow {
		t.Errorf("CheckedAt = %v, want the start of the check", res.CheckedAt)
	}
	if got, _ := f.lastAccept.Load().(string); got != "application/rdap+json" {
		t.Errorf("Accept = %q, want application/rdap+json", got)
	}
}

func TestDomainCheckerWarnsInsideTheWindow(t *testing.T) {
	f := newFakeRDAP(t, "com")
	f.domains["example.com"] = rdapDomain("2026-10-19T12:00:00Z")

	res := f.checker(domainNow).Check(context.Background(), domainMonitor("example.com", 30))
	if !res.OK || !res.Expiring || res.Kind != FailDomainExpiry {
		t.Fatalf("result = %+v, want an expiring pass", res)
	}
	if want := "domain registration of example.com expires in 12 days (on 2026-10-19)"; res.Error != want {
		t.Errorf("Error = %q, want %q", res.Error, want)
	}
}

func TestDomainCheckerWithoutAThresholdNeverWarns(t *testing.T) {
	f := newFakeRDAP(t, "com")
	f.domains["example.com"] = rdapDomain("2026-10-08T12:00:00Z")

	res := f.checker(domainNow).Check(context.Background(), domainMonitor("example.com", 0))
	if !res.OK || res.Expiring {
		t.Fatalf("result = %+v, want a pass: 0 days never warns", res)
	}
}

func TestDomainCheckerFailsOnceExpired(t *testing.T) {
	f := newFakeRDAP(t, "com")
	f.domains["example.com"] = rdapDomain("2026-10-01T00:00:00Z")

	res := f.checker(domainNow).Check(context.Background(), domainMonitor("example.com", 30))
	if res.OK || res.Expiring || res.Kind != FailDomainExpiry {
		t.Fatalf("result = %+v, want a failure", res)
	}
	if want := "domain registration of example.com expired on 2026-10-01"; res.Error != want {
		t.Errorf("Error = %q, want %q", res.Error, want)
	}
}

func TestDomainCheckerAsksForTheRegisteredDomain(t *testing.T) {
	f := newFakeRDAP(t, "uk", "co.uk")
	f.domains["example.co.uk"] = rdapDomain("2027-01-01T00:00:00Z")

	res := f.checker(domainNow).Check(context.Background(), domainMonitor("Shop.Example.CO.UK.", 30))
	if !res.OK {
		t.Fatalf("result = %+v, want a pass for example.co.uk", res)
	}
}

func TestDomainCheckerUnknownWhenTheTLDHasNoRDAP(t *testing.T) {
	f := newFakeRDAP(t, "com")

	res := f.checker(domainNow).Check(context.Background(), domainMonitor("example.nl", 30))
	if res.OK || res.Kind != FailUnknown {
		t.Fatalf("result = %+v, want unknown", res)
	}
	if want := "the .nl registry publishes no RDAP service, so the expiry date of example.nl cannot be read"; res.Error != want {
		t.Errorf("Error = %q, want %q", res.Error, want)
	}
	if f.lookups.Load() != 0 {
		t.Errorf("asked an RDAP server %d times for a TLD it does not serve", f.lookups.Load())
	}
}

func TestDomainCheckerUnknownWhenTheRegistryDoesNotAnswer(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   string
	}{
		{"not found", 0, "has no record of example.com"},
		{"rate limited", http.StatusTooManyRequests, "is limiting requests (HTTP 429)"},
		{"server error", http.StatusBadGateway, "answered HTTP 502"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeRDAP(t, "com")
			f.status = tc.status
			res := f.checker(domainNow).Check(context.Background(), domainMonitor("example.com", 30))
			if res.OK || res.Kind != FailUnknown || !strings.Contains(res.Error, tc.want) {
				t.Fatalf("result = %+v, want unknown naming %q", res, tc.want)
			}
		})
	}
}

func TestDomainCheckerUnknownOnAnAnswerWithoutAnExpiryDate(t *testing.T) {
	for _, tc := range []struct{ name, doc, want string }{
		{"no expiration event", `{"events":[{"eventAction":"registration","eventDate":"1995-08-14T04:00:00Z"}]}`, "does not publish an expiry date"},
		{"not json", `<html>maintenance</html>`, "not RDAP JSON"},
		{"not a date", rdapDomain("next year"), "not a date"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeRDAP(t, "com")
			f.domains["example.com"] = tc.doc
			res := f.checker(domainNow).Check(context.Background(), domainMonitor("example.com", 30))
			if res.OK || res.Kind != FailUnknown || !strings.Contains(res.Error, tc.want) {
				t.Fatalf("result = %+v, want unknown naming %q", res, tc.want)
			}
		})
	}
}

func TestDomainCheckerUnknownWhenTheBootstrapCannotBeRead(t *testing.T) {
	f := newFakeRDAP(t, "com")
	f.bootstrapStatus.Store(http.StatusServiceUnavailable)

	res := f.checker(domainNow).Check(context.Background(), domainMonitor("example.com", 30))
	if res.OK || res.Kind != FailUnknown || !strings.Contains(res.Error, "list of RDAP servers") {
		t.Fatalf("result = %+v, want unknown naming the bootstrap", res)
	}
}

// The bootstrap file is fetched once a day, not once a check, and a copy that
// is out of date is still used when a fresh one cannot be had.
func TestDomainCheckerCachesTheBootstrap(t *testing.T) {
	f := newFakeRDAP(t, "com")
	f.domains["example.com"] = rdapDomain("2027-08-13T04:00:00Z")
	now := domainNow
	c := f.checker(now)
	c.now = func() time.Time { return now }

	for range 3 {
		if res := c.Check(context.Background(), domainMonitor("example.com", 30)); !res.OK {
			t.Fatalf("result = %+v", res)
		}
	}
	if got := f.bootstrapHits.Load(); got != 1 {
		t.Errorf("bootstrap fetched %d times for three checks, want 1", got)
	}

	now = now.Add(25 * time.Hour)
	f.bootstrapStatus.Store(http.StatusServiceUnavailable)
	if res := c.Check(context.Background(), domainMonitor("example.com", 30)); !res.OK {
		t.Fatalf("result with a stale bootstrap = %+v, want the old copy used", res)
	}
	if got := f.bootstrapHits.Load(); got != 2 {
		t.Errorf("bootstrap fetched %d times, want a refresh attempt once it was a day old", got)
	}
}

// A registry whose address is private is refused like any other target: the
// bootstrap file is fetched from the internet, and an entry pointing at an
// internal address is not a registry.
func TestDomainCheckerDialsThroughTheGuard(t *testing.T) {
	f := newFakeRDAP(t, "com")
	f.domains["example.com"] = rdapDomain("2027-08-13T04:00:00Z")
	c := NewDomainChecker(NewGuard(false))
	c.bootstrapURL = f.srv.URL + "/bootstrap.json"

	res := c.Check(context.Background(), domainMonitor("example.com", 30))
	if res.OK || res.Kind != FailUnknown || !strings.Contains(res.Error, "private or reserved") {
		t.Fatalf("result = %+v, want unknown naming the guard", res)
	}
}

func TestDomainCheckerRefusesANameThatIsNotADomain(t *testing.T) {
	c := NewDomainChecker(NewGuard(true))
	for _, target := range []string{"co.uk", "localhost", "https://example.com", ""} {
		res := c.Check(context.Background(), domainMonitor(target, 30))
		if res.OK || res.Kind != FailInternal {
			t.Errorf("%q: result = %+v, want an internal failure", target, res)
		}
	}
}

func TestMatchBootstrapPrefersTheLongestEntryAndHTTPS(t *testing.T) {
	services := []rdapService{
		{tlds: []string{"uk"}, urls: []string{"https://rdap.uk.example/"}},
		{tlds: []string{"co.uk"}, urls: []string{"http://plain.example", "https://secure.example"}},
	}
	got := matchBootstrap(services, "example.co.uk")
	want := []string{"https://secure.example/", "http://plain.example/"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("servers = %v, want %v", got, want)
	}
	if got := matchBootstrap(services, "example.uk"); len(got) != 1 || got[0] != "https://rdap.uk.example/" {
		t.Errorf("servers for example.uk = %v", got)
	}
	if got := matchBootstrap(services, "examplecouk"); got != nil {
		t.Errorf("servers for a name with no matching TLD = %v, want none", got)
	}
}

func TestRegisteredDomain(t *testing.T) {
	for in, want := range map[string]string{
		"example.com":           "example.com",
		"WWW.Example.com.":      "example.com",
		"a.b.example.co.uk":     "example.co.uk",
		"xn--bcher-kva.example": "xn--bcher-kva.example",
	} {
		got, err := RegisteredDomain(in)
		if err != nil || got != want {
			t.Errorf("RegisteredDomain(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"com", "co.uk", "example", "exa mple.com", "example.com:443"} {
		if got, err := RegisteredDomain(in); err == nil {
			t.Errorf("RegisteredDomain(%q) = %q, want an error", in, got)
		}
	}
}

func TestMinIntervalIsSixHoursForDomainOnly(t *testing.T) {
	if got := MinInterval(TypeDomain); got != 6*time.Hour {
		t.Errorf("MinInterval(domain) = %v", got)
	}
	if got := MinInterval(TypeHTTP); got != 0 {
		t.Errorf("MinInterval(http) = %v, want none", got)
	}
}
