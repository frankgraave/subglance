package checker

import (
	"os"
	"testing"
)

// Tests in this package fall into two groups, and the split matters for CI.
//
// Most tests are hermetic: they parse strings, or they talk to a loopback
// server the test itself started. Those always run.
//
// A few need something the machine cannot fabricate — a DNS resolver, an
// outbound socket, an ICMP-capable kernel. A CI runner's network is not the
// internet: DNS may be filtered, ICMP is usually blocked outright, and a
// third-party host having a bad day must never turn this repository red.
//
// So network-dependent tests call requireNetwork(t), which skips them under
// `go test -short`. CI runs the short suite on every push; the full suite runs
// on a schedule, where a failure means "go look at it", not "your PR is
// broken".
//
// Set SUBGLANCE_TEST_NETWORK=1 to force them on even in short mode.
func requireNetwork(t *testing.T) {
	t.Helper()

	if os.Getenv("SUBGLANCE_TEST_NETWORK") == "1" {
		return
	}
	if testing.Short() {
		t.Skip("needs outbound network; skipped under -short")
	}
}
