package repoguard

// Releases are cut from the branch `dev`, by hand: the old workflow tagged
// the next patch on every push to main whose subject lacked
// `[skip release]`, so the later squash-merge of dev into main would have
// cut a release from main on its own. These tests read
// .github/workflows/release.yml and fail if a push trigger comes back or
// the run stops refusing a ref other than the release branch.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// releaseBranch is the only branch a release may be cut from.
const releaseBranch = "dev"

// releaseWorkflow is the part of release.yml the guards read.
type releaseWorkflow struct {
	On   map[string]yaml.Node `yaml:"on"`
	Env  map[string]string    `yaml:"env"`
	Jobs map[string]struct {
		Steps []struct {
			Name string `yaml:"name"`
			Run  string `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// readReleaseWorkflow parses .github/workflows/release.yml.
func readReleaseWorkflow(t *testing.T) releaseWorkflow {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	var wf releaseWorkflow
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parse release.yml: %v", err)
	}
	return wf
}

func TestReleaseWorkflowDispatchOnly(t *testing.T) {
	wf := readReleaseWorkflow(t)
	if len(wf.On) != 1 {
		t.Fatalf("release.yml must fire on workflow_dispatch alone, has triggers %v", keys(wf.On))
	}
	if _, ok := wf.On["workflow_dispatch"]; !ok {
		t.Fatalf("release.yml has no workflow_dispatch trigger: %v", keys(wf.On))
	}
}

func TestReleaseWorkflowRefusesOtherRefs(t *testing.T) {
	wf := readReleaseWorkflow(t)
	if got := wf.Env["RELEASE_BRANCH"]; got != releaseBranch {
		t.Fatalf("release.yml env RELEASE_BRANCH = %q, want %q", got, releaseBranch)
	}
	for _, job := range wf.Jobs {
		for _, st := range job.Steps {
			if strings.Contains(st.Run, `"refs/heads/$RELEASE_BRANCH"`) && strings.Contains(st.Run, "exit 1") {
				return
			}
		}
	}
	t.Fatal("no step in release.yml refuses a run whose ref is not refs/heads/$RELEASE_BRANCH")
}

// keys lists a trigger map's names for a failure message.
func keys(m map[string]yaml.Node) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
