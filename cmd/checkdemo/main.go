//go:build manual

// Command checkdemo runs the HTTP checker against real targets.
//
// It exists to prove the checker works outside httptest, against real DNS, real
// TLS and real failure modes. Excluded from normal builds by the `manual` tag:
//
//	go run -tags manual ./cmd/checkdemo
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
)

func main() {
	c := checker.NewHTTPChecker(checker.HTTPOptions{Guard: checker.NewGuard(false)})

	monitors := []checker.Monitor{
		{Name: "example.com", Type: checker.TypeHTTP, Target: "https://example.com", Timeout: 10 * time.Second},
		{Name: "cloudflare (expect 200)", Type: checker.TypeHTTP, Target: "https://1.1.1.1", Timeout: 10 * time.Second},
		{Name: "github api", Type: checker.TypeHTTP, Target: "https://api.github.com", Timeout: 10 * time.Second,
			KeywordMode: checker.KeywordMustContain, Keyword: "current_user_url"},
		{Name: "404 page", Type: checker.TypeHTTP, Target: "https://example.com/does-not-exist", Timeout: 10 * time.Second},
		{Name: "expired cert", Type: checker.TypeHTTP, Target: "https://expired.badssl.com", Timeout: 10 * time.Second},
		{Name: "wrong host cert", Type: checker.TypeHTTP, Target: "https://wrong.host.badssl.com", Timeout: 10 * time.Second},
		{Name: "self-signed", Type: checker.TypeHTTP, Target: "https://self-signed.badssl.com", Timeout: 10 * time.Second},
		{Name: "nonexistent domain", Type: checker.TypeHTTP, Target: "https://nope.invalid", Timeout: 5 * time.Second},
		{Name: "SSRF: localhost", Type: checker.TypeHTTP, Target: "http://127.0.0.1:8080", Timeout: 5 * time.Second},
		{Name: "SSRF: cloud metadata", Type: checker.TypeHTTP, Target: "http://169.254.169.254/latest/meta-data/", Timeout: 5 * time.Second},
		{Name: "cert expiry warning", Type: checker.TypeHTTP, Target: "https://example.com", Timeout: 10 * time.Second, SSLWarnDays: 3650},
	}

	fmt.Printf("%-26s %-6s %-9s %-12s %s\n", "MONITOR", "OK", "LATENCY", "KIND", "DETAIL")
	fmt.Println("------------------------------------------------------------------------------------------")

	for _, m := range monitors {
		res := c.Check(context.Background(), m)

		status := "UP"
		if !res.OK {
			status = "DOWN"
		}
		detail := res.Error
		if res.OK {
			detail = fmt.Sprintf("status %d", res.StatusCode)
			if !res.CertExpiry.IsZero() {
				detail += fmt.Sprintf(", cert until %s", res.CertExpiry.Format("2006-01-02"))
			}
		}
		fmt.Printf("%-26s %-6s %-9s %-12s %s\n",
			m.Name, status, res.Latency.Truncate(time.Millisecond), res.Kind, detail)
	}
}
