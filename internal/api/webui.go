package api

import (
	"net/http"
	"sync"

	"github.com/frankgraave/subglance/internal/webui"
)

// webUI is the dashboard handler, built once.
//
// It is lazy rather than a field on Server because Server is constructed in
// dozens of tests that never touch the UI, and reading the embedded bundle for
// each of them would be waste. The handler itself is stateless and safe for
// concurrent use.
var webUI = sync.OnceValue(func() http.Handler {
	return webui.Handler(http.HandlerFunc(handleAPINotFound))
})

// handleWebUI serves the embedded dashboard.
//
// Because it is registered on "/", it also receives every request that matched
// no other pattern. That is the point: an unknown page path is client-side
// routing and gets the SPA shell, while an unknown /api path stays a JSON 404.
func (s *Server) handleWebUI(w http.ResponseWriter, r *http.Request) {
	webUI().ServeHTTP(w, r)
}

// handleAPINotFound is the 404 for a path under /api that no route matched.
//
// Kept as JSON on purpose. A client that asked for JSON and receives an HTML
// page gets a parse error somewhere far away from the typo that caused it; an
// error object says what went wrong at the point it went wrong.
func handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "no such endpoint")
}
