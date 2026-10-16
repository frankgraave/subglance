package api

import (
	"net/http"
	"slices"
	"strings"

	"github.com/frankgraave/subglance/internal/webui"
)

// allowIndex answers, for a request that reached the catch-all, which methods
// the route table serves at its path.
//
// The catch-all is registered for every method (see anyMethod), and that has a
// cost: net/http's mux answers a known path with the wrong method with 405 and
// an Allow header, but only when no other pattern matches the request — and
// the catch-all matches everything. Without this index, DELETE /api/v1/monitors
// was told "no such endpoint", which sends a client hunting for a typo in a
// path that is correct.
//
// It is built from routes() rather than written by hand, so a new route is
// covered the moment it is in the table.
type allowIndex []allowEntry

// allowEntry is one path pattern of the route table and every method it is
// registered for.
type allowEntry struct {
	pattern string
	// literal is the pattern up to its first wildcard. A path that does not
	// start with it cannot match, which settles most requests — a page or a
	// bundle asset of the dashboard — without asking a mux at all.
	literal string
	// mux holds this pattern alone, without a method, so that matching a
	// path uses net/http's own wildcard rules instead of a second parser of
	// patterns that could disagree with the one that routes the request.
	mux     *http.ServeMux
	methods []string
}

// newAllowIndex groups the route table by path pattern. The catch-all is left
// out: it serves every method, so a request that reached it has no method to
// be refused for.
func newAllowIndex(routes []route) allowIndex {
	var ix allowIndex
	byPattern := map[string]int{}
	for _, rt := range routes {
		if rt.Pattern == webUIPattern || rt.Method == anyMethod {
			continue
		}
		i, ok := byPattern[rt.Pattern]
		if !ok {
			mux := http.NewServeMux()
			mux.Handle(rt.Pattern, http.NotFoundHandler())
			literal, _, _ := strings.Cut(rt.Pattern, "{")
			ix = append(ix, allowEntry{pattern: rt.Pattern, literal: literal, mux: mux})
			i = len(ix) - 1
			byPattern[rt.Pattern] = i
		}
		ix[i].methods = append(ix[i].methods, rt.Method)
	}
	return ix
}

// allowed returns the methods the route table serves at r's path, sorted and
// with HEAD wherever GET is (net/http serves a GET pattern for HEAD too), or
// nil when no route has this path.
func (ix allowIndex) allowed(r *http.Request) []string {
	var methods []string
	for _, e := range ix {
		if !strings.HasPrefix(r.URL.Path, e.literal) {
			continue
		}
		// A pattern the request does not match comes back as "". The
		// path is already clean: the outer mux redirects an unclean one
		// before any handler, this one included, sees it.
		if _, p := e.mux.Handler(r); p != e.pattern {
			continue
		}
		for _, m := range e.methods {
			methods = append(methods, m)
			if m == http.MethodGet {
				methods = append(methods, http.MethodHead)
			}
		}
	}
	if methods == nil {
		return nil
	}
	slices.Sort(methods)
	return slices.Compact(methods)
}

// refuseWrongMethod wraps the catch-all: a request whose path the route table
// serves, under a method it does not, is answered 405 with an Allow header
// (RFC 9110 §15.5.6) instead of being passed on to be called unknown.
//
// Under /api the answer is the API's JSON error, like every other answer
// there. Elsewhere — the health and metrics endpoints, a status page, a mute
// link — it is the same plain text the dashboard gives a write to one of its
// pages, now with the methods that path really takes.
func (ix allowIndex) refuseWrongMethod(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods := ix.allowed(r)
		if methods == nil {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Allow", strings.Join(methods, ", "))
		if webui.IsAPIPath(r.URL.Path) {
			writeError(w, http.StatusMethodNotAllowed,
				r.Method+" is not allowed here; this endpoint takes "+strings.Join(methods, ", "))
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})
}
