package api

import (
	"slices"
	"testing"
)

// Pin the JSON Schema expression: NOT(required both) allows either diagnostic
// alone and legacy rows with neither, but rejects contradictory evidence.
func TestOpenAPIRawHeartbeatDiagnosticsAreExclusive(t *testing.T) {
	node, ok := loadSpec(t).Components.Schemas["RawHeartbeat"]
	if !ok {
		t.Fatal("RawHeartbeat schema is missing")
	}
	var schema struct {
		Required []string `yaml:"required"`
		Not      struct {
			Required []string `yaml:"required"`
		} `yaml:"not"`
	}
	if err := node.Decode(&schema); err != nil {
		t.Fatal(err)
	}
	want := []string{"response", "response_capture_reason"}
	slices.Sort(schema.Not.Required)
	if !slices.Equal(schema.Not.Required, want) {
		t.Fatalf("RawHeartbeat must forbid both diagnostics with not.required: got %v, want %v", schema.Not.Required, want)
	}
	for _, field := range want {
		if slices.Contains(schema.Required, field) {
			t.Errorf("%s must remain optional for legacy rows", field)
		}
	}
}
