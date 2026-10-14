package buildinfo

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The files that choose a Go or a Node for a build. ci.yml tests with them,
// release.yml builds the archives, network.yml runs the full suite nightly,
// and the Dockerfile builds the image, which is the install path the README
// leads with.
const (
	dockerfilePath = "../../Dockerfile"
	workflowDir    = "../../.github/workflows"
)

// pin is one place that names a toolchain version.
type pin struct {
	where string // file and step, for the failure message
	value string
}

// exactVersion is a patch version and nothing looser. A range such as "24"
// or a tag such as "1.27-alpine" resolves to whatever is newest on the day
// it is read, which is how the image came to be built with a Go and a Node
// that no CI job had run.
var exactVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// dockerFrom matches a build stage on one of the two toolchain images.
var dockerFrom = regexp.MustCompile(`(?m)^FROM\s+(?:--platform=\S+\s+)?(golang|node):(\S+?)(?:-alpine\S*)?(?:\s+AS\s+\S+)?\s*$`)

func dockerfilePins(t *testing.T) (goPins, nodePins []pin) {
	t.Helper()
	raw, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("read %s: %v", dockerfilePath, err)
	}
	for _, m := range dockerFrom.FindAllStringSubmatch(string(raw), -1) {
		p := pin{where: "Dockerfile FROM " + m[1] + ":" + m[2], value: m[2]}
		if m[1] == "golang" {
			goPins = append(goPins, p)
		} else {
			nodePins = append(nodePins, p)
		}
	}
	return goPins, nodePins
}

// workflow is the part of a GitHub Actions file that chooses a toolchain.
type workflow struct {
	Env  map[string]string `yaml:"env"`
	Jobs map[string]struct {
		Env   map[string]string `yaml:"env"`
		Steps []struct {
			Uses string            `yaml:"uses"`
			With map[string]string `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

var envRef = regexp.MustCompile(`^\$\{\{\s*env\.(\w+)\s*\}\}$`)

// workflowPins reads every setup-go and setup-node step of one workflow and
// resolves the version it asks for. A step without a version is an error: it
// would fall back to go.mod's minimum or the runner's default, neither of
// which is a version anything else here is built with.
func workflowPins(t *testing.T, path string) (goPins, nodePins []pin) {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // a fixed directory of this repository's own workflow files
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var wf workflow
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	name := filepath.Base(path)
	// Sorted by job name, so a failure lists its places the same way
	// on every run.
	for _, jobName := range slices.Sorted(maps.Keys(wf.Jobs)) {
		job := wf.Jobs[jobName]
		for i, step := range job.Steps {
			action, _, _ := strings.Cut(step.Uses, "@")
			var key string
			switch action {
			case "actions/setup-go":
				key = "go-version"
			case "actions/setup-node":
				key = "node-version"
			default:
				continue
			}
			where := fmt.Sprintf("%s job %s step %d (%s)", name, jobName, i+1, action)
			value, ok := step.With[key]
			if !ok {
				t.Errorf("%s sets no %s, so it uses whatever the runner or go.mod picks", where, key)
				continue
			}
			if m := envRef.FindStringSubmatch(value); m != nil {
				resolved, ok := job.Env[m[1]]
				if !ok {
					resolved, ok = wf.Env[m[1]]
				}
				if !ok {
					t.Errorf("%s reads env.%s, which %s does not define", where, m[1], name)
					continue
				}
				where += " via env." + m[1]
				value = resolved
			}
			p := pin{where: where, value: value}
			if key == "go-version" {
				goPins = append(goPins, p)
			} else {
				nodePins = append(nodePins, p)
			}
		}
	}
	return goPins, nodePins
}

// samePin fails unless every pin is an exact version and all are the same one.
func samePin(t *testing.T, tool string, pins []pin) {
	t.Helper()
	for _, p := range pins {
		if !exactVersion.MatchString(p.value) {
			t.Errorf("%s: %s %q is not an exact patch version", p.where, tool, p.value)
		}
	}
	for _, p := range pins[1:] {
		if p.value != pins[0].value {
			t.Errorf("%s builds with %s %s, but %s uses %s; what CI proves must be what ships",
				p.where, tool, p.value, pins[0].where, pins[0].value)
		}
	}
}

// TestEveryBuildUsesTheSameToolchain keeps the image, the release archives
// and the CI runs on one Go and one Node. Without it the image drifted to Go
// 1.27 and Node 26 while every test ran on Go 1.26 and Node 24: two floating
// Dockerfile tags, moved by Dependabot, which cannot see the workflows.
func TestEveryBuildUsesTheSameToolchain(t *testing.T) {
	goPins, nodePins := dockerfilePins(t)
	if len(goPins) == 0 || len(nodePins) == 0 {
		t.Fatalf("found %d golang and %d node stages in the Dockerfile, want both: "+
			"a renamed image would otherwise leave nothing to compare", len(goPins), len(nodePins))
	}

	// Each of these files must set the toolchain up at least once, so a
	// workflow restructured beyond this reader fails here rather than
	// silently dropping out of the comparison.
	wantGo := map[string]bool{"ci.yml": true, "release.yml": true, "network.yml": true}
	wantNode := map[string]bool{"ci.yml": true, "release.yml": true}

	entries, err := os.ReadDir(workflowDir)
	if err != nil {
		t.Fatalf("read %s: %v", workflowDir, err)
	}
	for _, e := range entries {
		if ext := filepath.Ext(e.Name()); e.IsDir() || ext != ".yml" && ext != ".yaml" {
			continue
		}
		g, n := workflowPins(t, filepath.Join(workflowDir, e.Name()))
		if len(g) > 0 {
			delete(wantGo, e.Name())
		}
		if len(n) > 0 {
			delete(wantNode, e.Name())
		}
		goPins = append(goPins, g...)
		nodePins = append(nodePins, n...)
	}
	for name := range wantGo {
		t.Errorf("%s sets up no Go that this test could read", name)
	}
	for name := range wantNode {
		t.Errorf("%s sets up no Node that this test could read", name)
	}

	samePin(t, "Go", goPins)
	samePin(t, "Node", nodePins)
}
