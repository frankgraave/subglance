package checker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"
)

// MinDomainInterval is the shortest interval a domain monitor may be checked
// at. A registration's expiry date moves once a year, and the RDAP servers
// that publish it are run by registries that rate-limit generously only for
// clients that ask rarely; asking every minute would get the instance blocked
// and learn nothing a daily check does not.
const MinDomainInterval = 6 * time.Hour

// DefaultDomainInterval is a domain monitor's interval when none is given:
// once a day, because the warning threshold is counted in days.
const DefaultDomainInterval = 24 * time.Hour

// DefaultDomainWarnDays is a domain monitor's warning threshold when none is
// given. Longer than a certificate's, because renewing a domain can need a
// person with the registrar login and a payment method, not a cron job.
const DefaultDomainWarnDays = 30

// MinInterval is the shortest interval a check type may run at, including
// while it is down. Zero means no floor beyond the scheduler's own.
func MinInterval(t Type) time.Duration {
	if t == TypeDomain {
		return MinDomainInterval
	}
	return 0
}

// ianaRDAPBootstrap is IANA's registry of which RDAP server answers for which
// top-level domain (RFC 9224).
const ianaRDAPBootstrap = "https://data.iana.org/rdap/dns.json"

// bootstrapMaxAge is how long a fetched bootstrap file is used before it is
// fetched again. IANA changes it a few times a month; a day old is current.
const bootstrapMaxAge = 24 * time.Hour

// maxRDAPBody caps what is read from an RDAP or bootstrap response. A domain
// answer is a few kilobytes and the bootstrap file under 100 KiB; anything
// near this is not an answer worth parsing.
const maxRDAPBody = 1 << 20

// RegisteredDomain returns the name a registry holds for target: the label
// under its public suffix. "www.example.co.uk" is registered as
// "example.co.uk"; a public suffix on its own ("co.uk") is registered by
// nobody and returns an error.
//
// Names are ASCII, as a dns monitor's are: an internationalised domain is
// written in its xn-- form, which is the name the registry holds.
func RegisteredDomain(target string) (string, error) {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(target), "."))
	if !validDNSName(name) || !strings.Contains(name, ".") {
		return "", fmt.Errorf("%q is not a domain name", target)
	}
	reg, err := publicsuffix.EffectiveTLDPlusOne(name)
	if err != nil {
		return "", fmt.Errorf("%q is a public suffix, not a registered domain", target)
	}
	return reg, nil
}

// DomainChecker reads a domain's registration expiry date over RDAP.
//
// # Why RDAP and not WHOIS
//
// WHOIS is free text in a different layout per registry, and reading a date
// out of it means a parser per TLD that breaks without notice. RDAP is the
// JSON protocol ICANN requires of every generic TLD registry, and IANA
// publishes which server answers for which TLD, so one parser covers every
// registry that offers it.
//
// # What it cannot know
//
// Not every country-code registry runs RDAP, and a registry's server can be
// down or rate-limiting. None of that says anything about the domain, so the
// check reports FailUnknown with the reason rather than failing: an outage of
// a registry's API is not the monitored domain's outage, and must not open an
// incident.
type DomainChecker struct {
	client *http.Client
	ua     string

	// bootstrapURL is IANA's bootstrap file; tests point it at a fake.
	bootstrapURL string
	now          func() time.Time

	mu        sync.Mutex
	services  []rdapService
	fetchedAt time.Time
}

// rdapService is one entry of the bootstrap file: the TLDs it covers and the
// base URLs of the servers that answer for them.
type rdapService struct {
	tlds []string
	urls []string
}

// NewDomainChecker returns a checker that dials RDAP servers through the SSRF
// guard. An RDAP server is a public registry; one that resolves to a private
// address is not one.
func NewDomainChecker(guard *Guard) *DomainChecker {
	if guard == nil {
		guard = NewGuard(false) // fail closed
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: guard.ControlFunc()}
	return &DomainChecker{
		client: &http.Client{
			Transport: &http.Transport{
				DialContext:         dialer.DialContext,
				TLSHandshakeTimeout: 10 * time.Second,
				ForceAttemptHTTP2:   true,
				DisableKeepAlives:   true,
			},
			// Registries redirect between their own hosts (a referral to
			// the registrar's server, or http to https). A handful of hops
			// is every legitimate chain; more is a loop.
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("stopped after 5 redirects")
				}
				return nil
			},
		},
		ua:           "SubGlance/1.0 (+https://subglance.com)",
		bootstrapURL: ianaRDAPBootstrap,
		now:          time.Now,
	}
}

// Check reads the domain's expiry date and compares it with the monitor's
// warning threshold.
func (c *DomainChecker) Check(ctx context.Context, m Monitor) Result {
	start := c.now()
	res := c.check(ctx, m, start)
	res.CheckedAt = start
	res.Latency = c.now().Sub(start)
	return res
}

func (c *DomainChecker) check(ctx context.Context, m Monitor, start time.Time) Result {
	domain, err := RegisteredDomain(m.Target)
	if err != nil {
		return Result{Kind: FailInternal, Error: err.Error()}
	}
	timeout := m.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	bases, err := c.serversFor(ctx, domain)
	if err != nil {
		return unknown(err.Error())
	}
	if len(bases) == 0 {
		tld := domain[strings.LastIndex(domain, ".")+1:]
		return unknown(fmt.Sprintf("the .%s registry publishes no RDAP service, so the expiry date of %s cannot be read", tld, domain))
	}

	// The bootstrap can list more than one server for a TLD. One that cannot
	// be reached, or answers with a server error, says nothing about the
	// domain, so the next one is asked; an answer about the domain itself (a
	// record, no such domain, slow down) ends the search.
	var expiry time.Time
	for _, base := range bases {
		var tryNext bool
		expiry, tryNext, err = c.expiry(ctx, base, domain)
		if err == nil || !tryNext {
			break
		}
	}
	if err != nil {
		return unknown(err.Error())
	}

	res := Result{OK: true, DomainExpiry: expiry}
	remaining := expiry.Sub(start)
	switch {
	case remaining <= 0:
		res.OK = false
		res.Kind = FailDomainExpiry
		res.Error = fmt.Sprintf("domain registration of %s expired on %s", domain, expiry.Format(time.DateOnly))
	case remaining < time.Duration(m.DomainWarnDays)*24*time.Hour:
		// Like a certificate about to expire: the domain still resolves,
		// so this is a notice that alerts without counting as downtime.
		// A threshold of 0 never gets here: remaining is positive.
		res.Expiring = true
		res.Kind = FailDomainExpiry
		// Rounded up: with hours left the registration still holds, and
		// "expires in 0 days" would read as already gone.
		days := int(math.Ceil(remaining.Hours() / 24))
		unit := "days"
		if days == 1 {
			unit = "day"
		}
		res.Error = fmt.Sprintf("domain registration of %s expires in %d %s (on %s)",
			domain, days, unit, expiry.Format(time.DateOnly))
	}
	return res
}

// unknown is a check that could not learn the answer.
func unknown(reason string) Result {
	return Result{Kind: FailUnknown, Error: reason}
}

// serversFor returns the RDAP base URLs for a domain's TLD, https first,
// fetching the bootstrap file when there is none or it is old. An old copy is
// used when a fresh one cannot be fetched: which registry answers for a TLD
// rarely changes, and IANA being unreachable is no reason to stop checking.
func (c *DomainChecker) serversFor(ctx context.Context, domain string) ([]string, error) {
	c.mu.Lock()
	services, fetched := c.services, c.fetchedAt
	c.mu.Unlock()

	if services == nil || c.now().Sub(fetched) > bootstrapMaxAge {
		fresh, err := c.fetchBootstrap(ctx)
		switch {
		case err == nil:
			c.mu.Lock()
			c.services, c.fetchedAt = fresh, c.now()
			c.mu.Unlock()
			services = fresh
		case services == nil:
			return nil, fmt.Errorf("could not read IANA's list of RDAP servers: %w", err)
		}
	}
	return matchBootstrap(services, domain), nil
}

// matchBootstrap picks the service whose entry is the longest suffix of the
// domain, as RFC 9224 section 4 asks, and orders its URLs https first.
func matchBootstrap(services []rdapService, domain string) []string {
	best, bestLen := -1, 0
	for i, s := range services {
		for _, entry := range s.tlds {
			e := strings.ToLower(strings.Trim(entry, "."))
			if e == "" || (domain != e && !strings.HasSuffix(domain, "."+e)) {
				continue
			}
			if n := strings.Count(e, ".") + 1; n > bestLen {
				best, bestLen = i, n
			}
		}
	}
	if best < 0 {
		return nil
	}
	var https, plain []string
	for _, u := range services[best].urls {
		if !strings.HasSuffix(u, "/") {
			u += "/"
		}
		if strings.HasPrefix(u, "https://") {
			https = append(https, u)
		} else if strings.HasPrefix(u, "http://") {
			plain = append(plain, u)
		}
	}
	return append(https, plain...)
}

// fetchBootstrap reads IANA's bootstrap file.
func (c *DomainChecker) fetchBootstrap(ctx context.Context) ([]rdapService, error) {
	body, status, err := c.get(ctx, c.bootstrapURL, "application/json")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", status)
	}
	var doc struct {
		Services [][][]string `json:"services"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("not a bootstrap file: %w", err)
	}
	out := make([]rdapService, 0, len(doc.Services))
	for _, s := range doc.Services {
		if len(s) == 2 {
			out = append(out, rdapService{tlds: s[0], urls: s[1]})
		}
	}
	if len(out) == 0 {
		return nil, errors.New("the file lists no services")
	}
	return out, nil
}

// expiry asks one RDAP server for the domain and reads its expiration event.
// tryNext is true when the failure is the server's rather than an answer
// about the domain: it could not be reached, or it answered with an error
// status other than 404 and 429. Another server for the TLD may then answer.
func (c *DomainChecker) expiry(ctx context.Context, base, domain string) (expiry time.Time, tryNext bool, err error) {
	host := base
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	host = strings.TrimSuffix(host, "/")
	body, status, err := c.get(ctx, base+"domain/"+domain, "application/rdap+json")
	if err != nil {
		// Not when the check's own deadline is spent: there is no time left
		// to ask anyone else.
		return time.Time{}, ctx.Err() == nil, fmt.Errorf("no answer from the RDAP server %s: %w", host, err)
	}
	switch {
	case status == http.StatusNotFound:
		return time.Time{}, false, fmt.Errorf("the RDAP server %s has no record of %s; it may not be registered", host, domain)
	case status == http.StatusTooManyRequests:
		return time.Time{}, false, fmt.Errorf("the RDAP server %s is limiting requests (HTTP 429); the next check will try again", host)
	case status != http.StatusOK:
		return time.Time{}, true, fmt.Errorf("the RDAP server %s answered HTTP %d", host, status)
	}
	var doc struct {
		Events []struct {
			Action string `json:"eventAction"`
			Date   string `json:"eventDate"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return time.Time{}, false, fmt.Errorf("the RDAP server %s sent an answer that is not RDAP JSON", host)
	}
	for _, e := range doc.Events {
		if e.Action != "expiration" {
			continue
		}
		t, err := time.Parse(time.RFC3339, e.Date)
		if err != nil {
			return time.Time{}, false, fmt.Errorf("the RDAP server %s gave an expiry date that is not a date: %q", host, e.Date)
		}
		return t.UTC(), false, nil
	}
	return time.Time{}, false, fmt.Errorf("the RDAP server %s does not publish an expiry date for %s", host, domain)
}

// get fetches a URL and returns at most maxRDAPBody bytes of its body.
func (c *DomainChecker) get(ctx context.Context, url, accept string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", c.ua)
	resp, err := c.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, 0, errors.New("timed out")
		}
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRDAPBody+1))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if len(body) > maxRDAPBody {
		return nil, resp.StatusCode, fmt.Errorf("answer larger than %d KiB", maxRDAPBody>>10)
	}
	return body, resp.StatusCode, nil
}
