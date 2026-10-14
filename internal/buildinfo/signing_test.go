package buildinfo

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	imageWorkflowPath = "../../.github/workflows/docker.yml"
	installDocPath    = "../../docs/installation.md"

	// The identity GitHub puts in a Sigstore certificate is the workflow
	// file's URL followed by the ref it ran on.
	imageWorkflowIdentity = "https://github.com/frankgraave/subglance/.github/workflows/docker.yml@"
	githubOIDCIssuer      = "https://token.actions.githubusercontent.com"
)

// imageWorkflow is the part of docker.yml that decides what is pushed, what
// is signed and which job may ask for a signing token.
type imageWorkflow struct {
	On struct {
		Push struct {
			Branches []string `yaml:"branches"`
			Tags     []string `yaml:"tags"`
		} `yaml:"push"`
	} `yaml:"on"`
	Concurrency struct {
		Group            string `yaml:"group"`
		CancelInProgress any    `yaml:"cancel-in-progress"`
	} `yaml:"concurrency"`
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]struct {
		If          string            `yaml:"if"`
		Needs       any               `yaml:"needs"`
		Permissions map[string]string `yaml:"permissions"`
		Outputs     map[string]string `yaml:"outputs"`
		Steps       []struct {
			ID   string            `yaml:"id"`
			Uses string            `yaml:"uses"`
			With map[string]string `yaml:"with"`
			Env  map[string]string `yaml:"env"`
			Run  string            `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func readImageWorkflow(t *testing.T) imageWorkflow {
	t.Helper()
	raw, err := os.ReadFile(imageWorkflowPath)
	if err != nil {
		t.Fatalf("read %s: %v", imageWorkflowPath, err)
	}
	var wf imageWorkflow
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parse %s: %v", imageWorkflowPath, err)
	}
	return wf
}

// pinnedAction is an action reference pinned to a full commit SHA, as every
// other action in the workflows is.
var pinnedAction = regexp.MustCompile(`^[\w.-]+/[\w.-]+@[0-9a-f]{40}$`)

// ghExpr matches a whole value that is one ${{ expression }}.
var ghExpr = regexp.MustCompile(`^\$\{\{\s*(.+?)\s*\}\}$`)

// signTarget matches `cosign sign` on "${IMAGE}@" followed by either a shell
// variable (group 2, resolved through the step's env) or an expression
// written in place (group 3).
var signTarget = regexp.MustCompile(`cosign sign\b[^\n]*"\$\{IMAGE\}@(\$\{(\w+)\}|\$\{\{\s*([\w.]+)\s*\}\})"`)

// expr returns the expression inside a value that is exactly one ${{ ... }},
// or "" for anything else.
func expr(v string) string {
	if m := ghExpr.FindStringSubmatch(strings.TrimSpace(v)); m != nil {
		return m[1]
	}
	return ""
}

// TestImageIsSignedByDigestOnlyOnPush keeps docker.yml signing the image it
// pushed, by digest, from a job that a pull request never runs.
//
// The digest matters because a tag can be moved between the push and the
// signature, and because the digest names the manifest list for both
// architectures. The job boundary matters because `id-token: write` is what
// lets a run mint a Sigstore certificate in this workflow's name: granted to
// the build job or the whole workflow, it would reach the pull request build
// too.
func TestImageIsSignedByDigestOnlyOnPush(t *testing.T) {
	wf := readImageWorkflow(t)

	if _, ok := wf.Permissions["id-token"]; ok {
		t.Errorf("docker.yml grants id-token at workflow level, so every job, the pull request build included, can mint a signing token")
	}

	// The job that builds with id `build` is the one whose digest must be
	// signed.
	var publishJob string
	for name, job := range wf.Jobs {
		for _, step := range job.Steps {
			if step.ID == "build" && strings.HasPrefix(step.Uses, "docker/build-push-action@") {
				publishJob = name
				if got := expr(step.With["push"]); got != "github.event_name == 'push'" {
					t.Errorf("the build step pushes on %q, want only on a push event", step.With["push"])
				}
			}
		}
	}
	if publishJob == "" {
		t.Fatal("docker.yml has no docker/build-push-action step with id `build`")
	}
	publish := wf.Jobs[publishJob]
	if _, ok := publish.Permissions["id-token"]; ok {
		t.Errorf("job %s builds pull requests too and must not be able to mint a signing token", publishJob)
	}
	if got := expr(publish.Outputs["digest"]); got != "steps.build.outputs.digest" {
		t.Fatalf("job %s exposes digest = %q, want the build step's digest output", publishJob, publish.Outputs["digest"])
	}
	digestRef := "needs." + publishJob + ".outputs.digest"

	signed := false
	for name, job := range wf.Jobs {
		var signs bool
		var installed bool
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, "sigstore/cosign-installer@") {
				installed = true
				if !pinnedAction.MatchString(step.Uses) {
					t.Errorf("job %s installs cosign from %q; pin the action to a commit SHA", name, step.Uses)
				}
			}
			if !strings.Contains(step.Run, "cosign sign") {
				continue
			}
			if strings.Contains(step.Run, "--key") {
				t.Errorf("job %s signs with a key; the image is signed keylessly, against the workflow's identity", name)
			}
			// The reference must be the image at the digest the build
			// pushed: either written in place or through a step env var.
			target := signTarget.FindStringSubmatch(step.Run)
			if target == nil {
				t.Errorf("job %s runs `cosign sign` on something other than \"${IMAGE}@<digest>\": %q", name, step.Run)
				continue
			}
			ref := target[3]
			if target[2] != "" {
				ref = expr(step.Env[target[2]])
			}
			if ref != digestRef {
				t.Errorf("job %s signs ${IMAGE}@%s, want the digest %s pushed (%s)", name, ref, publishJob, digestRef)
				continue
			}
			signs = true
		}
		if !signs {
			continue
		}
		signed = true
		if !installed {
			t.Errorf("job %s runs cosign without installing it", name)
		}
		if job.Permissions["id-token"] != "write" {
			t.Errorf("job %s signs keylessly but does not have id-token: write", name)
		}
		if job.Permissions["packages"] != "write" {
			t.Errorf("job %s cannot push the signature without packages: write", name)
		}
		var needs []string
		switch n := job.Needs.(type) {
		case string:
			needs = append(needs, n)
		case []any:
			for _, v := range n {
				if s, ok := v.(string); ok {
					needs = append(needs, s)
				}
			}
		}
		if !slices.Contains(needs, publishJob) {
			t.Errorf("job %s does not need %s, so it cannot read the digest it pushed", name, publishJob)
		}
		if !strings.Contains(job.If, "github.event_name == 'push'") {
			t.Errorf("job %s runs on %q; it must run only on a push, never for a pull request", name, job.If)
		}
	}
	if !signed {
		t.Errorf("no job in docker.yml signs the pushed digest with `cosign sign`")
	}
}

// TestImagePushRunsAreNotCancelled keeps a newer run from cancelling a push
// run of docker.yml. The image is signed by a second job after `publish`: a
// push run cancelled between the two leaves an image in the registry, under
// its sha tag, that nothing signs later. A pull request or manual run pushes
// nothing, so cancelling it is still safe, but only in a group no push run
// is in: GitHub evaluates cancel-in-progress for the incoming run, so a
// manual run on develop sharing the push run's group would cancel it.
func TestImagePushRunsAreNotCancelled(t *testing.T) {
	wf := readImageWorkflow(t)

	switch v := wf.Concurrency.CancelInProgress.(type) {
	case nil:
		return // GitHub's default: a newer run waits instead of cancelling
	case bool:
		if !v {
			return
		}
	case string:
		if expr(v) == "github.event_name != 'push'" {
			if !strings.Contains(wf.Concurrency.Group, "github.event_name") {
				t.Errorf("docker.yml concurrency group %q does not separate events, so a manual run on develop shares a push run's group and its cancel-in-progress cancels that push run; put github.event_name in the group", wf.Concurrency.Group)
			}
			return
		}
	}
	t.Errorf("docker.yml concurrency has cancel-in-progress: %v; a newer run would cancel a push run between publish and sign and leave its image unsigned, so cancel only runs that are not a push", wf.Concurrency.CancelInProgress)
}

// identityRegexp finds the identity pattern of the `cosign verify` command
// that checks the image, as opposed to the `cosign verify-blob` one for the
// release archives.
var identityRegexp = regexp.MustCompile(`(?s)cosign verify ghcr\.io/frankgraave/subglance\S*.*?--certificate-identity-regexp '([^']+)'.*?--certificate-oidc-issuer (\S+)`)

// TestImageVerificationDocNamesTheSigningWorkflow keeps the verification in
// docs/installation.md pinned to docker.yml, on exactly the refs that
// workflow pushes from. A pattern that names nothing narrower than the
// repository would accept a signature from any workflow in it; one that
// misses a ref docker.yml pushes from would refuse a genuine image.
func TestImageVerificationDocNamesTheSigningWorkflow(t *testing.T) {
	raw, err := os.ReadFile(installDocPath)
	if err != nil {
		t.Fatalf("read %s: %v", installDocPath, err)
	}
	m := identityRegexp.FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatalf("%s has no `cosign verify ghcr.io/frankgraave/subglance` with --certificate-identity-regexp and --certificate-oidc-issuer", installDocPath)
	}
	if m[2] != githubOIDCIssuer {
		t.Errorf("the image verification trusts issuer %q, want %q", m[2], githubOIDCIssuer)
	}
	pattern, err := regexp.Compile(m[1])
	if err != nil {
		t.Fatalf("the documented identity pattern %q does not compile: %v", m[1], err)
	}
	if !strings.HasPrefix(m[1], "^") || !strings.HasSuffix(m[1], "$") {
		t.Errorf("the documented identity pattern %q is not anchored at both ends", m[1])
	}

	// Every ref docker.yml pushes, and so signs, from must verify.
	wf := readImageWorkflow(t)
	var accept []string
	for _, b := range wf.On.Push.Branches {
		accept = append(accept, imageWorkflowIdentity+"refs/heads/"+b)
	}
	for _, tag := range wf.On.Push.Tags {
		// A glob such as v* stands for a version tag.
		accept = append(accept, imageWorkflowIdentity+"refs/tags/"+strings.ReplaceAll(tag, "*", "0.1.0"))
	}
	if len(accept) == 0 {
		t.Fatal("docker.yml pushes from no branch or tag")
	}
	for _, id := range accept {
		if !pattern.MatchString(id) {
			t.Errorf("the documented pattern %q refuses %s, a ref docker.yml signs from", m[1], id)
		}
	}

	// And nothing else: not another workflow, not another branch, not a
	// pull request, not another repository.
	for _, id := range []string{
		"https://github.com/frankgraave/subglance/.github/workflows/release.yml@refs/tags/v0.1.0",
		"https://github.com/frankgraave/subglance/.github/workflows/ci.yml@refs/heads/develop",
		imageWorkflowIdentity + "refs/heads/feature/x",
		imageWorkflowIdentity + "refs/heads/main",
		imageWorkflowIdentity + "refs/pull/1/merge",
		"https://github.com/someone/subglance/.github/workflows/docker.yml@refs/heads/develop",
		"https://github.com/frankgraave/subglance/.github/workflows/docker.yml@refs/heads/develop-x",
	} {
		if pattern.MatchString(id) {
			t.Errorf("the documented pattern %q accepts %s, which docker.yml never signs from", m[1], id)
		}
	}
}
