package buildinfo

import (
	"os"
	"path"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

const dependabotPath = "../../.github/dependabot.yml"

// dependabotConfig is the part of .github/dependabot.yml that decides which
// Dockerfile bumps are proposed.
type dependabotConfig struct {
	Updates []struct {
		Ecosystem string `yaml:"package-ecosystem"`
		Directory string `yaml:"directory"`
		Ignore    []struct {
			Name        string   `yaml:"dependency-name"`
			UpdateTypes []string `yaml:"update-types"`
		} `yaml:"ignore"`
	} `yaml:"updates"`
}

const (
	semverMajor = "version-update:semver-major"
	semverMinor = "version-update:semver-minor"
	semverPatch = "version-update:semver-patch"
)

// TestDependabotProposesOnlyToolchainPatches keeps Dependabot's docker
// ecosystem from moving the golang or node image to a new minor or major on
// its own, while still letting it propose a patch. The Dockerfile tags are
// the versions TestEveryBuildUsesTheSameToolchain compares with the
// workflows, which this ecosystem cannot see: a minor proposed here is a
// toolchain upgrade decided by a bot, and a patch that is never proposed is
// a security fix nobody hears about.
func TestDependabotProposesOnlyToolchainPatches(t *testing.T) {
	raw, err := os.ReadFile(dependabotPath)
	if err != nil {
		t.Fatalf("read %s: %v", dependabotPath, err)
	}
	var cfg dependabotConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse %s: %v", dependabotPath, err)
	}

	found := false
	for _, u := range cfg.Updates {
		if u.Ecosystem != "docker" || u.Directory != "/" {
			continue
		}
		found = true
		for _, image := range []string{"golang", "node"} {
			var ignored []string
			for _, rule := range u.Ignore {
				if match, _ := path.Match(rule.Name, image); !match {
					continue
				}
				if len(rule.UpdateTypes) == 0 {
					// A rule with no update-types ignores every version.
					ignored = append(ignored, semverMajor, semverMinor, semverPatch)
					continue
				}
				ignored = append(ignored, rule.UpdateTypes...)
			}
			for _, want := range []string{semverMajor, semverMinor} {
				if !slices.Contains(ignored, want) {
					t.Errorf("%s: the docker ecosystem does not ignore %s for %s, so it proposes a new "+
						"toolchain for the image that no workflow tests", dependabotPath, want, image)
				}
			}
			if slices.Contains(ignored, semverPatch) {
				t.Errorf("%s: the docker ecosystem ignores %s for %s, so a security patch to the "+
					"image's toolchain is never proposed", dependabotPath, semverPatch, image)
			}
		}
	}
	if !found {
		t.Errorf("%s has no docker ecosystem for directory \"/\": nothing proposes a patch "+
			"to the Dockerfile's golang and node images", dependabotPath)
	}
}
