package monitor

import (
	"testing"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// The stored assertion reaches the checker with its expected value still JSON,
// so the type of "up" versus up survives the hop.
func TestToCheckerMonitorCarriesJSONAssertion(t *testing.T) {
	cm := toCheckerMonitor(store.Monitor{
		Type: "http", Target: "https://example.com",
		JSONAssertion: &store.JSONAssertion{Path: "items[0].ok", Operator: "equals", Expected: "true"},
	})
	a := cm.JSONAssertion
	if a == nil {
		t.Fatal("assertion dropped on the way to the checker")
	}
	if a.Path != "items[0].ok" || a.Operator != checker.JSONEquals || string(a.Expected) != "true" {
		t.Errorf("assertion = %+v", *a)
	}

	cm = toCheckerMonitor(store.Monitor{
		Type: "http", Target: "https://example.com",
		JSONAssertion: &store.JSONAssertion{Path: "id", Operator: "exists"},
	})
	if cm.JSONAssertion == nil || cm.JSONAssertion.Expected != nil {
		t.Errorf("exists assertion = %+v, want no expected value", cm.JSONAssertion)
	}

	if cm := toCheckerMonitor(store.Monitor{Type: "http"}); cm.JSONAssertion != nil {
		t.Errorf("a monitor without an assertion got %+v", cm.JSONAssertion)
	}
}
