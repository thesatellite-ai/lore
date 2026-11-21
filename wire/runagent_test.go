package wire

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	opsrule "github.com/thesatellite-ai/lore/ops/rule"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestNoProjectWriteSeesTheAgent (devco D-872, bug hunt H-134): an agent
// writing `--to <store>` from a git repo with no lore project (and from a
// plain folder) runs on the store-only open, which App.run reaches before
// it fills op.Caller.Agent. The caller carries the agent there now:
// agents.person_only refuses the command it lists, and task add asks the
// agent for --commitment (an engine.AgentOf reader); a person passes both.
func TestNoProjectWriteSeesTheAgent(t *testing.T) {
	url := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	for _, k := range []string{"CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_REMOTE_SESSION_ID", "CODEX_THREAD_ID", "LORE_SESSION", "LORE_NO_DETECT"} {
		t.Setenv(k, "")
	}
	open := func(t *testing.T, dir string) *App {
		t.Helper()
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("policies:\n  agents.person_only: [rule.add]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		a, err := New(Options{RepoDir: dir, Home: home, OrgStores: orgAt(url), Wall: time.Now})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = a.Close() })
		return a
	}
	for _, place := range []struct {
		name string
		dir  func(t *testing.T) string
	}{
		{"git-repo-with-no-lore-project", gitRepo},
		{"plain-folder", func(t *testing.T) string { return t.TempDir() }},
	} {
		t.Run(place.name+"/agent", func(t *testing.T) {
			t.Setenv("LORE_HUMAN", "")
			t.Setenv("LORE_ACTOR", "agent:bot")
			a := open(t, place.dir(t))
			_, err := run(t, a, opsrule.OpAdd, `{"body":"Deploys need two approvals","to":"acme/org"}`, true)
			if !errs.Has(err, errs.PolicyDenied) || !strings.Contains(err.Error(), "agents.person_only") {
				t.Fatalf("[devco:D-872] an agent's rule add listed in agents.person_only from a %s: %v, want the agents.person_only refusal", place.name, err)
			}
			_, err = run(t, a, opstask.OpAdd, `{"title":"renew the cert","to":"acme/org"}`, true)
			if err == nil || !strings.Contains(err.Error(), "--commitment is required when an agent adds a task") {
				t.Fatalf("[devco:D-872] an agent's task add with no --commitment from a %s: %v, want the --commitment refusal", place.name, err)
			}
		})
		t.Run(place.name+"/person", func(t *testing.T) {
			t.Setenv("LORE_HUMAN", "1")
			t.Setenv("LORE_ACTOR", "")
			a := open(t, place.dir(t))
			if _, err := run(t, a, opsrule.OpAdd, `{"body":"Deploys need two approvals","to":"acme/org"}`, true); err != nil {
				t.Fatalf("a person's rule add from a %s: %v", place.name, err)
			}
			if _, err := run(t, a, opstask.OpAdd, `{"title":"renew the cert","to":"acme/org"}`, true); err != nil {
				t.Fatalf("a person's task add from a %s: %v", place.name, err)
			}
		})
	}
}

// engineRunSites are the files of wire that call engine.Run themselves,
// each with how the caller there carries the agent. Every other path runs
// the engine through App.runAsCaller (runagent.go).
var engineRunSites = map[string]string{
	"runagent.go":     "runAsCaller: the funnel itself",
	"wire.go":         "App.run's main path fills c.Agent from the opened repo's identity (CallerAgent) first",
	"storefolders.go": "the machine-only open fills c.Agent from its own identity (mach.CallerAgent) first",
}

// TestEngineRunsFillTheAgent (devco D-872): a store-only path that called
// eng.Run with the caller App.run handed it ran with no agent on it, so
// agents.person_only and every engine.AgentOf reader missed a detected
// agent there. A new call of the engine's Run outside the files above
// must go through runAsCaller.
func TestEngineRunsFillTheAgent(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	call := regexp.MustCompile(`\beng\.Run\(`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		if _, ok := engineRunSites[f]; ok {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if call.MatchString(line) && !strings.HasPrefix(strings.TrimSpace(line), "//") {
				t.Errorf("[devco:D-872] %s:%d calls the engine's Run directly: run it through a.runAsCaller (runagent.go) so the caller carries the detected agent, or add the file to engineRunSites with how its caller gets the agent", f, i+1)
			}
		}
	}
}
