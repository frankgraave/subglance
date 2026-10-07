// Command preview renders the public status page for a fixed set of
// scenarios, with the stylesheet embedded in this build, and writes each one
// to a file together with the Content-Security-Policy the server would send
// for it (<name>.csp.txt; a page with an accent has a policy of its own), and
// the branded scenario's logo under logos/.
//
// It exists for the browser test (web/src/statuspage/statuspage.browser.test.ts),
// which measures the real rendered page rather than a copy of it, and for
// looking at the page without seeding a database. Build the frontend first:
// without it the pages render unstyled, and the command refuses.
//
//	go run ./internal/statuspage/preview -out /tmp/status-pages
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/frankgraave/subglance/internal/statuspage"
	"github.com/frankgraave/subglance/internal/webui"
)

func main() {
	out := flag.String("out", "", "directory to write the pages into")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "preview:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	if out == "" {
		return fmt.Errorf("-out is required")
	}
	css := webui.StatusPageCSS()
	if css == nil {
		return fmt.Errorf("no status-page.css in this build: run `npm run build` in web/ first")
	}
	r, err := statuspage.NewRenderer(css)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o750); err != nil {
		return err
	}
	for name, page := range statuspage.PreviewScenarios(time.Now()) {
		html, err := r.Render(page)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(out, name+".html"), html, 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(out, name+".csp.txt"), []byte(r.ContentSecurityPolicyFor(page)), 0o600); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(out, "logos"), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "logos", statuspage.PreviewLogoFile), statuspage.PreviewLogo(), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "csp.txt"), []byte(r.ContentSecurityPolicy()), 0o600)
}
