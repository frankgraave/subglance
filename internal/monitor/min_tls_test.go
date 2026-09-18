package monitor

import (
	"crypto/tls"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// The gap SUB-143 describes is exactly here. A monitor can carry a TLS floor
// and both checkers can honour one, and the feature is still unreachable if
// the value is dropped on the way between them — which is the state it was in:
// nothing outside internal/checker ever set the field. A test of the API and a
// test of the checker both pass while this mapping silently loses it, so the
// mapping needs its own.
func TestToCheckerMonitorCarriesTheTLSFloor(t *testing.T) {
	cm := toCheckerMonitor(store.Monitor{
		ID: 7, Name: "appliance", Type: "http",
		Target: "https://appliance.example.com", TimeoutS: 10,
		MinTLSVersion: tls.VersionTLS10,
	})
	if cm.MinTLSVersion != tls.VersionTLS10 {
		t.Errorf("MinTLSVersion = %d, want %d; a stored floor never reaches the dial",
			cm.MinTLSVersion, tls.VersionTLS10)
	}
}

// A monitor with no opinion must arrive as zero rather than as the current
// default, so the checker stays the one place that decides what unset means.
func TestToCheckerMonitorLeavesAnUnsetFloorUnset(t *testing.T) {
	cm := toCheckerMonitor(store.Monitor{
		ID: 8, Name: "plain", Type: "http",
		Target: "https://example.com", TimeoutS: 10,
	})
	if cm.MinTLSVersion != 0 {
		t.Errorf("MinTLSVersion = %d on a monitor that set none; want 0", cm.MinTLSVersion)
	}
}
