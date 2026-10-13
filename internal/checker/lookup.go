package checker

import (
	"errors"
	"net"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"
)

// systemResolvers names this host's resolvers for a message, when the lookup
// error does not say which one was asked. A variable so a test can name its
// own.
var systemResolvers = resolvConfServers

// classifyLookupError words a failed lookup of the target's name.
//
// A name that does not exist is the resolver's answer, and says so. Anything
// else is a resolver that gave no usable answer, and the message names that
// resolver: the fix is on this host, not at the site, and "timed out after
// 10s" sent whoever was paged to the site. Those failures carry
// ResolverFailed, so the connectivity check can be asked whether the
// resolver is answering anything at all.
func classifyLookupError(start time.Time, e *net.DNSError) Result {
	if e.IsNotFound {
		return fail(start, FailDNS, "DNS lookup failed: host %s not found", e.Name)
	}
	server := resolverName(e.Server)
	var res Result
	if e.IsTimeout {
		res = fail(start, FailDNS, "DNS lookup timed out after %s: no answer from %s",
			time.Since(start).Truncate(time.Millisecond), server)
	} else {
		res = fail(start, FailDNS, "DNS lookup failed: %s (resolver %s)", e.Err, server)
	}
	res.ResolverFailed = true
	return res
}

// resolverName is the resolver a message names: the one the error came from,
// or this host's configured ones when it does not say. Port 53 is left out,
// the way a person writes a resolver's address.
func resolverName(server string) string {
	servers := []string{server}
	if server == "" {
		var err error
		if servers, err = systemResolvers(); err != nil || len(servers) == 0 {
			return "this host's resolver"
		}
	}
	for i, s := range servers {
		if host, port, err := net.SplitHostPort(s); err == nil && port == "53" {
			servers[i] = host
		}
	}
	return strings.Join(servers, ", ")
}

// lookupWatch follows the lookups of one HTTP request, so a check that ran
// out of time while its lookup was still waiting can say so.
//
// The error of a request whose deadline passed does not reliably carry the
// lookup's: the transport returns the context's error as soon as it is done,
// and the dial that would have reported the lookup may still be on its way.
// The trace hooks run inside that lookup, so they know what the error does
// not.
type lookupWatch struct {
	mu      sync.Mutex
	host    string
	pending bool
	err     error
}

func (w *lookupWatch) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart: func(info httptrace.DNSStartInfo) {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.host, w.pending, w.err = info.Host, true, nil
		},
		DNSDone: func(info httptrace.DNSDoneInfo) {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.pending, w.err = false, info.Err
		},
	}
}

// failure returns how the request's latest lookup failed, or nil when it
// did not: a lookup still waiting when the request gave up is a lookup that
// got no answer in time.
func (w *lookupWatch) failure() *net.DNSError {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending {
		return &net.DNSError{Err: "no answer", Name: w.host, IsTimeout: true}
	}
	var dnsErr *net.DNSError
	if errors.As(w.err, &dnsErr) {
		return dnsErr
	}
	return nil
}
