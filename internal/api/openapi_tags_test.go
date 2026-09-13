package api

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/frankgraave/subglance/internal/store"
)

// tagMapSchema is the subset of components.schemas.TagMap these tests check.
type tagMapSchema struct {
	PropertyNames struct {
		MinLength int    `yaml:"minLength"`
		MaxLength int    `yaml:"maxLength"`
		Pattern   string `yaml:"pattern"`
	} `yaml:"propertyNames"`
	AdditionalProperties struct {
		MinLength int    `yaml:"minLength"`
		MaxLength int    `yaml:"maxLength"`
		Pattern   string `yaml:"pattern"`
	} `yaml:"additionalProperties"`
	MaxProperties int `yaml:"maxProperties"`
}

func loadTagMapSchema(t *testing.T) tagMapSchema {
	t.Helper()

	spec := loadSpec(t)
	node, ok := spec.Components.Schemas["TagMap"]
	if !ok {
		t.Fatalf("%s has no components.schemas.TagMap", filepath.Clean(specPath))
	}
	var schema tagMapSchema
	if err := node.Decode(&schema); err != nil {
		t.Fatalf("decode TagMap: %v", err)
	}
	return schema
}

// TestOpenAPITagMapMatchesValidation is why the tag constraints are written
// into the schema rather than only into prose: a request that satisfies the
// documented contract must not come back as a 400. Each bound is checked from
// both sides, so widening the validator without touching the document, or the
// other way round, fails here.
func TestOpenAPITagMapMatchesValidation(t *testing.T) {
	schema := loadTagMapSchema(t)

	if schema.MaxProperties != store.MaxTagsPerMonitor {
		t.Errorf("TagMap maxProperties = %d, want %d", schema.MaxProperties, store.MaxTagsPerMonitor)
	}

	atMax := map[string]string{
		strings.Repeat("k", schema.PropertyNames.MaxLength): strings.Repeat("v", schema.AdditionalProperties.MaxLength),
	}
	if _, err := store.NormaliseTags(atMax); err != nil {
		t.Errorf("a tag at the documented maximum lengths was rejected: %v", err)
	}

	overLong := map[string]struct{ key, value string }{
		"key one over the documented maximum": {
			key:   strings.Repeat("k", schema.PropertyNames.MaxLength+1),
			value: "prod",
		},
		"value one over the documented maximum": {
			key:   "env",
			value: strings.Repeat("v", schema.AdditionalProperties.MaxLength+1),
		},
	}
	for name, tc := range overLong {
		if _, err := store.NormaliseTags(map[string]string{tc.key: tc.value}); err == nil {
			t.Errorf("%s was accepted, so the documented bound is not the real one", name)
		}
	}

	// The documented minimums exist so that a blank key or value is a
	// contract violation rather than a surprise 400.
	if schema.PropertyNames.MinLength != 1 {
		t.Errorf("TagMap propertyNames.minLength = %d, want 1", schema.PropertyNames.MinLength)
	}
	if schema.AdditionalProperties.MinLength != 1 {
		t.Errorf("TagMap additionalProperties.minLength = %d, want 1", schema.AdditionalProperties.MinLength)
	}
	if _, err := store.NormaliseTags(map[string]string{"env": "   "}); err == nil {
		t.Error("a blank value was accepted, but the schema documents a minimum length of 1")
	}
}

// TestOpenAPITagKeyPatternMatchesValidation checks the documented key pattern
// against the validator with keys that sit on either side of it. A pattern
// copied into the document and then left behind would let a client build a key
// the server refuses.
func TestOpenAPITagKeyPatternMatchesValidation(t *testing.T) {
	schema := loadTagMapSchema(t)
	pattern := mustCompile(t, schema.PropertyNames.Pattern)

	keys := []string{
		"env", "customer", "a", "a1", "team.core", "team-core", "team_core",
		"-env", "env-", ".env", "env.", "my env", "Env", "en v", "envü", "e$nv",
	}
	for _, key := range keys {
		_, err := store.NormaliseTags(map[string]string{key: "prod"})
		// Normalisation happens before the key is checked, so the pattern
		// describes the normalised key, which is what the document says.
		normalised := strings.ToLower(strings.TrimSpace(key))
		if got, want := pattern.MatchString(normalised), err == nil; got != want {
			t.Errorf("key %q: schema pattern accepts=%v, NormaliseTags accepts=%v", key, got, want)
		}
	}

	valuePattern := mustCompile(t, schema.AdditionalProperties.Pattern)
	for _, value := range []string{"prod", "Acme", "https://example.com:8443", "two\nlines", "a\tb"} {
		_, err := store.NormaliseTags(map[string]string{"env": value})
		if got, want := valuePattern.MatchString(strings.TrimSpace(value)), err == nil; got != want {
			t.Errorf("value %q: schema pattern accepts=%v, NormaliseTags accepts=%v", value, got, want)
		}
	}
}

// mustCompile fails the test rather than panicking, so a malformed pattern in
// the document reads as a spec problem instead of a crashed test binary.
func mustCompile(t *testing.T, pattern string) *regexp.Regexp {
	t.Helper()

	if pattern == "" {
		t.Fatal("TagMap is missing a pattern; the document would not constrain anything")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("compile pattern %q from the spec: %v", pattern, err)
	}
	return re
}
