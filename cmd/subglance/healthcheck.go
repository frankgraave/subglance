package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// healthcheckTimeout bounds the whole probe. Docker's own default
// --health-timeout is 30s; staying well under it means the container reports
// unhealthy because the answer was bad, not because the probe was killed.
const healthcheckTimeout = 5 * time.Second

const (
	healthcheckUsage = "usage: subglance healthcheck [--addr ADDRESS]"
	healthcheckAbout = "Asks the server on --addr whether it is ready to serve, and exits 0 when it is and 1 when it is not. " +
		"Meant for a container HEALTHCHECK."
)

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
	if asksForHelp(args) {
		printUsage(out, healthcheckUsage, healthcheckAbout, nil)
		return flag.ErrHelp
	}
	cfg, err := loadConfig("healthcheck", args)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), healthcheckTimeout)
	defer cancel()

	// A dedicated client with no keep-alives: this process makes one request
	// per address and exits, so a pooled connection would only be a socket
	// left half-closed on the server.
	//
	// It does not follow redirects. The readiness endpoint never sends one, so
	// a redirect is itself the answer, and a non-200 one. Following it would
	// also let a dial failure on the redirect target pass for "nothing here"
	// and send the probe on to the next loopback after a server had answered.
	client := &http.Client{
		Timeout:   healthcheckTimeout,
		Transport: &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Only an address that cannot be connected to passes the question on to
	// the next one. Once a server has answered, its answer is the result: a
	// 503 on [::1] is the server saying it is not ready, and asking again on
	// 127.0.0.1 would only reach the same server.
	var unreachable []error
	for _, url := range healthcheckURLs(cfg.Addr) {
		err := probeReady(ctx, client, url)
		if err == nil {
			_, _ = fmt.Fprintf(out, "ok %s\n", url)
			return nil
		}
		if !dialFailed(err) {
			return err
		}
		unreachable = append(unreachable, err)
	}
	return errors.Join(unreachable...)
}

// probeReady asks one URL for readiness and returns nil only for a 200.
func probeReady(ctx context.Context, client *http.Client, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request for %s: %w", url, err)
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
	return nil
}

// dialFailed reports whether err happened before any server was reached:
// nothing accepts on the address, or the address does not exist here.
func dialFailed(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// healthcheckURLs turns a listen address into the URLs this process can dial,
// in the order to try them.
//
// A server listening on ":8080" or "0.0.0.0:8080" is listening on every
// interface, but "http://:8080/" is not a URL and "http://0.0.0.0:8080/" is
// not reliably dialable. Both mean loopback to a probe running beside the
// server, so both are rewritten to it.
func healthcheckURLs(addr string) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// Not host:port at all — hand it to the URL as-is and let the
		// request fail with a message naming what was tried.
		return []string{"http://" + addr + "/api/v1/ready"}
	}

	hosts := loopbackHosts(host)
	urls := make([]string, 0, len(hosts))
	for _, h := range hosts {
		urls = append(urls, "http://"+net.JoinHostPort(h, port)+"/api/v1/ready")
	}
	return urls
}

// loopbackHosts names the hosts a process beside the server can reach a
// listener on host through, in the order to try them. The healthcheck and the
// "is a server still running" check of restore and reset-password both use
// it, so they agree about where the server is.
//
// "[::]" gets two. [::1] comes first because a listener that is IPv6 only
// (net.ipv6.bindv6only=1) answers nowhere else. 127.0.0.1 follows because
// [::1] does not exist where IPv6 is switched off (ipv6.disable=1, or
// disable_ipv6 in a container), while Go still opens "[::]" there and accepts
// on IPv4: probing [::1] alone reported such a server unhealthy forever.
func loopbackHosts(host string) []string {
	if host == "" {
		return []string{"127.0.0.1"}
	}
	ip := net.ParseIP(host)
	switch {
	case ip == nil || !ip.IsUnspecified():
		return []string{host}
	case ip.To4() != nil:
		return []string{"127.0.0.1"}
	default:
		return []string{"::1", "127.0.0.1"}
	}
}
