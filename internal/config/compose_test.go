package config

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	composePath = "../../docker-compose.yml"
	sourcePath  = "config.go"
)

// composeFile is the subset of docker-compose.yml this test reasons about.
type composeFile struct {
	Services map[string]struct {
		Image       string            `yaml:"image"`
		Ports       []string          `yaml:"ports"`
		Volumes     []string          `yaml:"volumes"`
		Environment map[string]string `yaml:"environment"`
	} `yaml:"services"`
	Volumes map[string]any `yaml:"volumes"`
}

func readCompose(t *testing.T) (composeFile, string) {
	t.Helper()
	raw, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("read %s: %v", composePath, err)
	}
	var cf composeFile
	if err := yaml.Unmarshal(raw, &cf); err != nil {
		t.Fatalf("parse %s: %v", composePath, err)
	}
	return cf, string(raw)
}

// envKeysInSource lists every SUBGLANCE_* variable Load actually reads.
func envKeysInSource(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read %s: %v", sourcePath, err)
	}
	re := regexp.MustCompile(`"(SUBGLANCE_[A-Z_]+)"`)
	seen := map[string]bool{}
	var keys []string
	for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			keys = append(keys, m[1])
		}
	}
	if len(keys) == 0 {
		t.Fatalf("found no SUBGLANCE_* keys in %s; the scan is broken", sourcePath)
	}
	sort.Strings(keys)
	return keys
}

// envKeysInCompose lists every SUBGLANCE_* variable the compose file names,
// whether it is set or commented out as an example. Commented examples count:
// they are the documentation an operator reads before changing anything, and a
// stale example is as misleading as a stale table in the README.
func envKeysInCompose(raw string) []string {
	re := regexp.MustCompile(`(?m)^\s*#?\s*(SUBGLANCE_[A-Z_]+)\s*:`)
	seen := map[string]bool{}
	var keys []string
	for _, m := range re.FindAllStringSubmatch(raw, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			keys = append(keys, m[1])
		}
	}
	sort.Strings(keys)
	return keys
}

// TestComposeDocumentsEveryOption is a drift guard in both directions: an
// option added to Load without reaching the compose file is undiscoverable,
// and an option named in the compose file that Load does not read is a silent
// no-op for whoever sets it.
func TestComposeDocumentsEveryOption(t *testing.T) {
	_, raw := readCompose(t)
	inSource := envKeysInSource(t)
	inCompose := envKeysInCompose(raw)

	sourceSet := map[string]bool{}
	for _, k := range inSource {
		sourceSet[k] = true
	}
	composeSet := map[string]bool{}
	for _, k := range inCompose {
		composeSet[k] = true
	}

	for _, k := range inSource {
		if !composeSet[k] {
			t.Errorf("%s is read by Load but never named in %s", k, composePath)
		}
	}
	for _, k := range inCompose {
		if !sourceSet[k] {
			t.Errorf("%s is named in %s but Load never reads it", k, composePath)
		}
	}
}

// TestComposeMatchesImageDefaults pins the compose file to the defaults the
// image itself ships with. The point of the file is that `docker compose up -d`
// works with no edits, which only holds while the published port, the mounted
// path and the entrypoint's own defaults agree.
func TestComposeMatchesImageDefaults(t *testing.T) {
	cf, _ := readCompose(t)

	svc, ok := cf.Services["subglance"]
	if !ok {
		t.Fatalf("no service named subglance in %s", composePath)
	}
	if want := "ghcr.io/frankgraave/subglance"; !strings.HasPrefix(svc.Image, want+":") {
		t.Errorf("image = %q, want a tag of %q", svc.Image, want)
	}

	def := defaults()

	// The container side of the port mapping must match the default --addr,
	// otherwise the published port reaches nothing.
	_, wantPort, ok := strings.Cut(def.Addr, ":")
	if !ok {
		t.Fatalf("default Addr %q has no port", def.Addr)
	}
	foundPort := false
	for _, p := range svc.Ports {
		if _, container, ok := strings.Cut(p, ":"); ok && container == wantPort {
			foundPort = true
		}
	}
	if !foundPort {
		t.Errorf("ports %v publish nothing on container port %s (the default --addr)", svc.Ports, wantPort)
	}

	// The volume must cover the default --data-dir, or the database is lost
	// on the first `docker compose down`.
	foundVol := false
	var volName string
	for _, v := range svc.Volumes {
		name, target, ok := strings.Cut(v, ":")
		if ok && target == def.DataDir {
			foundVol = true
			volName = name
		}
	}
	if !foundVol {
		t.Fatalf("volumes %v do not mount the default data-dir %s", svc.Volumes, def.DataDir)
	}
	if _, ok := cf.Volumes[volName]; !ok {
		t.Errorf("service mounts %q but the top-level volumes block does not declare it", volName)
	}
}

// TestComposeEnvironmentValuesAreValid feeds the values the compose file sets
// through Load, so a typo there fails here instead of at someone's first start.
func TestComposeEnvironmentValuesAreValid(t *testing.T) {
	cf, _ := readCompose(t)
	svc := cf.Services["subglance"]
	for k, v := range svc.Environment {
		t.Setenv(k, v)
	}
	if _, err := Load(nil); err != nil {
		t.Fatalf("Load with the compose environment: %v", err)
	}
}
