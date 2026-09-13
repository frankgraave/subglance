package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/config"
)

// healthcheckTimeout bounds the whole probe. Docker's own default
// --health-timeout is 30s; staying well under it means the container reports
// unhealthy because the answer was bad, not because the probe was killed.
const healthcheckTimeout = 5 * time.Second

// runHealthcheck probes a running instance and reports whether it can serve
// traffic. It exists because the shipped image is distroless static: there is
// no shell, no curl and no wget, so a HEALTHCHECK can only invoke the binary
// that is already in the image.
//
// It probes /api/v1/ready rather than /health on purpose. The consumers of a
// container healthcheck — compose `depends_on: condition: service_healthy`,
// Swarm, a Kubernetes readiness probe copied from the compose file — are all
// asking "may I send this traffic yet", and that is the readiness question.
// /health answers the narrower "is the process alive", which for an in-process
// check is already implied by the binary running at all.
//
// It reads the same configuration as the server, so a non-default address is
// honoured and the check cannot quietly probe the wrong port. Inside the
// shipped image that means SUBGLANCE_ADDR: Docker runs a HEALTHCHECK as its own
// process, which inherits the container environment but not the flags given to
// the entrypoint.
func runHealthcheck(args []string, out io.Writer) error {
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}

	url := healthcheckURL(cfg.Addr)

	ctx, cancel := context.WithTimeout(context.Background(), healthcheckTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request for %s: %w", url, err)
	}

	// A dedicated client with no keep-alives: this process makes exactly one
	// request and exits, so a pooled connection would only be a socket left
	// half-closed on the server.
	client := &http.Client{
		Timeout:   healthcheckTimeout,
		Transport: &http.Transport{DisableKeepAlives: true},
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("probe %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Drain a bounded amount so the body can be quoted in the failure message.
	// Unbounded would let a misrouted endpoint stream into a health probe.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("probe %s: %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}

	_, _ = fmt.Fprintf(out, "ok %s\n", url)
	return nil
}

// healthcheckURL turns a listen address into one this process can dial.
//
// A server listening on ":8080" or "0.0.0.0:8080" is listening on every
// interface, but "http://:8080/" is not a URL and "http://0.0.0.0:8080/" is
// not reliably dialable. Both mean loopback to a probe running beside the
// server, so both are rewritten to it.
func healthcheckURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// Not host:port at all — hand it to the URL as-is and let the
		// request fail with a message naming what was tried.
		return "http://" + addr + "/api/v1/ready"
	}

	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::", "[::]":
		host = "::1"
	}

	return "http://" + net.JoinHostPort(host, port) + "/api/v1/ready"
}
