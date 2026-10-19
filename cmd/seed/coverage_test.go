package main

import (
	"context"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// schemaMonitorTypes reads the monitor types the live schema accepts, from the
// CHECK constraint on monitors.type of a freshly migrated database.
//
// It is read rather than listed because a list is what let the catalogue fall
// behind: TestCatalogueCoversEveryFeature named five types, the schema grew to
// seven, and nothing failed.
func schemaMonitorTypes(t *testing.T) []string {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "schema.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	var ddl string
	if err := db.Reader.QueryRowContext(ctx,
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'monitors'").Scan(&ddl); err != nil {
		t.Fatalf("read the monitors table: %v", err)
	}
	found := regexp.MustCompile(`CHECK \(type IN \(([^)]*)\)\)`).FindStringSubmatch(ddl)
	if found == nil {
		t.Fatalf("the monitors table has no CHECK (type IN (...)); this test reads the types from it:\n%s", ddl)
	}
	var types []string
	for _, quoted := range strings.Split(found[1], ",") {
		types = append(types, strings.Trim(strings.TrimSpace(quoted), "'"))
	}
	if len(types) < 5 {
		t.Fatalf("read only %v from the schema; the parse is wrong", types)
	}
	return types
}

// TestCatalogueCoversEveryMonitorType holds the demo to every type the schema
// accepts, so a type added to the product is a type the demo shows.
func TestCatalogueCoversEveryMonitorType(t *testing.T) {
	seeded := map[string]bool{}
	for _, spec := range monitors() {
		seeded[spec.monitor.Type] = true
	}
	for _, want := range schemaMonitorTypes(t) {
		if !seeded[want] {
			t.Errorf("the schema accepts %s monitors and the catalogue has none", want)
		}
	}
}

// TestSeedFailureTextsMatchTheChecker runs the real checker against the
// settings the catalogue gives its dns monitor and its JSON assertion, and
// compares what it says with the outage the catalogue scripts for that
// monitor. A seeded failure reads like a real one only for as long as these
// agree, so the expected kind and text are read from the monitor's own
// outage rather than from the constants it was first written with.
func TestSeedFailureTextsMatchTheChecker(t *testing.T) {
	spec := func(name string, kind checker.FailureKind) (store.Monitor, outageSpec) {
		t.Helper()
		for _, s := range monitors() {
			if s.monitor.Name != name {
				continue
			}
			for _, o := range s.profile.outages {
				if o.cause == string(kind) {
					return s.monitor, o
				}
			}
			t.Fatalf("%q scripts no %s outage", name, kind)
		}
		t.Fatalf("no %q in the catalogue", name)
		return store.Monitor{}, outageSpec{}
	}
	guard := checker.NewGuard(true) // loopback stands in for the target

	t.Run("dns mismatch", func(t *testing.T) {
		m, o := spec("Apex A record", checker.FailDNSMismatch)
		resolver := aResolver(t, m.Target, [4]byte{198, 51, 100, 7})
		res := checker.NewDNSChecker(guard).Check(context.Background(), checker.Monitor{
			Type: checker.TypeDNS, Target: m.Target, Timeout: 5 * time.Second,
			DNSRecordType: m.DNS.RecordType, DNSExpected: m.DNS.Expected, DNSResolver: resolver,
		})
		if string(res.Kind) != o.cause || res.Error != o.message {
			t.Errorf("checker says %s %q\nseed files     %s %q", res.Kind, res.Error, o.cause, o.message)
		}
	})

	t.Run("json assertion", func(t *testing.T) {
		m, o := spec("Object storage", checker.FailAssertion)
		if m.JSONAssertion == nil {
			t.Fatal("Object storage carries no JSON assertion")
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"degraded","checks":{"disk":"ok","replication":"lagging"}}`))
		}))
		defer srv.Close()
		res := checker.NewHTTPChecker(checker.HTTPOptions{Guard: guard}).Check(context.Background(), checker.Monitor{
			Type: checker.TypeHTTP, Target: srv.URL, Timeout: 5 * time.Second,
			Method: m.Method, ExpectedStatus: m.ExpectedStatus,
			Keyword: m.Keyword, KeywordMode: checker.KeywordMode(m.KeywordMode),
			JSONAssertion: &checker.JSONAssertion{
				Path: m.JSONAssertion.Path, Operator: checker.JSONOperator(m.JSONAssertion.Operator),
				Expected: []byte(m.JSONAssertion.Expected),
			},
		})
		if string(res.Kind) != o.cause || res.Error != o.message {
			t.Errorf("checker says %s %q\nseed files     %s %q", res.Kind, res.Error, o.cause, o.message)
		}
	})
}

// TestSeededNoticesAreOpenAndDated checks the two expiry notices the demo
// carries, a certificate and a domain registration: open, confirmed, filed as
// notices, and naming a date inside the monitor's warning window as seen from
// the moment the seeder ran.
func TestSeededNoticesAreOpenAndDated(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	pl := testPlan(now)
	date := regexp.MustCompile(`expires in (\d+) days? \(on (\d{4}-\d{2}-\d{2})\)$`)

	kinds := map[string]bool{}
	for _, spec := range monitors() {
		m := spec.monitor
		m.CreatedAt = now.Add(-spec.createdAgo)
		store.ApplyMonitorDefaults(&m)
		window := m.SSLWarnDays
		if m.Type == store.TypeDomain {
			window = m.DomainWarnDays
		}
		for _, inc := range buildHistory(m, spec.profile, 1, pl, rand.New(rand.NewPCG(5, 6))).incidents {
			if !inc.Notice {
				continue
			}
			kinds[inc.Cause] = true
			if inc.Resolved() || !inc.Confirmed() {
				t.Errorf("%s: the notice is not open and confirmed: %+v", m.Name, inc)
			}
			found := date.FindStringSubmatch(inc.LastError)
			if found == nil {
				t.Errorf("%s: the notice names no date: %q", m.Name, inc.LastError)
				continue
			}
			on, err := time.Parse(time.DateOnly, found[2])
			if err != nil {
				t.Fatalf("%s: %v", m.Name, err)
			}
			if !on.After(now) || on.After(now.AddDate(0, 0, window+1)) {
				t.Errorf("%s: %q is not inside the %d-day window from %s", m.Name, inc.LastError, window, now.Format(time.DateOnly))
			}
		}
	}
	for _, want := range []string{string(checker.FailCertExpiry), string(checker.FailDomainExpiry)} {
		if !kinds[want] {
			t.Errorf("no open %s notice in the catalogue", want)
		}
	}
}

// aResolver answers every A query for name with one record, on loopback.
func aResolver(t *testing.T, name string, addr [4]byte) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var q dnsmessage.Message
			if q.Unpack(buf[:n]) != nil || len(q.Questions) != 1 {
				continue
			}
			b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: q.ID, Response: true, Authoritative: true})
			_ = b.StartQuestions()
			_ = b.Question(q.Questions[0])
			_ = b.StartAnswers()
			_ = b.AResource(dnsmessage.ResourceHeader{
				Name: dnsmessage.MustNewName(name + "."), Class: dnsmessage.ClassINET, TTL: 60,
			}, dnsmessage.AResource{A: addr})
			out, err := b.Finish()
			if err != nil {
				continue
			}
			_, _ = pc.WriteTo(out, from)
		}
	}()
	return pc.LocalAddr().String()
}
