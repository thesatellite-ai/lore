//go:build teame2e

package teame2e

// team_e2e_synccimerge_test.go — B11 ports of sync_e2e_test.go's ci-merge
// and install-action e2e tests, run against LORE_TEAM_BIN (cmd/lore): two
// clones edit one rule on main and on a feature branch, and
// `lore sync ci-merge --base origin/main` settles what a host's merge
// button cannot (LORE_TEAM_SPEC command table; adapters/reposync).

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
	"gopkg.in/yaml.v3"
)

// stubScriptMode is the mode of the CI stub scripts: the owner must be able
// to execute them.
const stubScriptMode = 0o700

// bugRepoSyncHost: cmd/lore's sync host answered E_UNSUPPORTED for
// install-action, ci-merge and peek until wire/host/live_sync.go delegated
// them to wire.RepoSync (2fe8c14). Closed: the symptom now fails the test.
var bugRepoSyncHost = th.Bug{ID: "BUG-REPOSYNC-HOST", Owner: "p3-modeb (wire/host/live_sync.go)", Open: false}

// ciMergeBaseRef is the --base every test merges from.
const ciMergeBaseRef = "origin/main"

// ciOutcome is `lore sync ci-merge --json`'s data.
type ciOutcome struct {
	Outcome  string   `json:"outcome"`
	Base     string   `json:"base"`
	Commit   string   `json:"commit"`
	Merged   []string `json:"merged"`
	Blocking []string `json:"blocking"`
	Reason   string   `json:"reason"`
}

// ciWorld is alice on main with one rule pushed, and bob's clone on a new
// feature branch.
type ciWorld struct {
	w          *th.World
	app        *th.Remote
	alice, bob *th.Clone
	rule       string
}

func newCIWorld(t *testing.T) *ciWorld {
	t.Helper()
	s := newSyncSolo(t)
	cw := &ciWorld{w: s.w, app: s.app, alice: s.c}
	cw.rule = addSyncRule(t, s.c, "v1")
	s.c.CommitAll("rule")
	s.c.AdminPush("origin", "main")
	cw.bob = s.w.Human("bob", "bob@acme.example").Clone(s.app, "app")
	cw.bob.MustLore("rule", "list")
	cw.bob.Git("checkout", "-q", "-b", "feature")
	return cw
}

func ciMergeOf(t *testing.T, c *th.Clone) ciOutcome {
	t.Helper()
	r := c.Lore("sync", "ci-merge", "--base", ciMergeBaseRef, "--json")
	bugRepoSyncHost.PendingOn(t, r, "E_UNSUPPORTED", "ci-merge")
	r.OK(t)
	var env struct {
		Data ciOutcome `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &env); err != nil {
		t.Fatalf("ci-merge output: %v\n%s", err, r)
	}
	return env.Data
}

// ruleFields is the part of a rule row file the tests read.
type ruleFields struct {
	Body     string `json:"body"`
	Severity string `json:"severity"`
}

// ruleDoc reads a rule's row file in a clone.
func ruleDoc(t *testing.T, c *th.Clone, id string) ruleFields {
	t.Helper()
	b, ok := c.ReadFile(ruleFile(id))
	if !ok {
		t.Fatalf("no row file for %s", id)
	}
	var m ruleFields
	if err := json.Unmarshal([]byte(b), &m); err != nil {
		t.Fatalf("row file of %s: %v\n%s", id, err, b)
	}
	return m
}

// TestB11Sync_CIMergeSettlesLoreConflict (TestE2E_CIMergeSettlesLoreConflict):
// different fields of one rule on two branches conflict for the host;
// ci-merge settles it with lore's driver and commits a merge on top of the
// branch, leaving nothing behind; a second run is up to date.
func TestB11Sync_CIMergeSettlesLoreConflict(t *testing.T) {
	cw := newCIWorld(t)
	cw.bob.MustLore("rule", "edit", cw.rule, "--severity", "should")
	cw.bob.CommitAll("bob: severity")
	cw.bob.Git("push", "-q", "-u", "origin", "feature")
	cw.alice.MustLore("rule", "edit", cw.rule, "--body", "v2 from main")
	cw.alice.CommitAll("alice: body")
	cw.alice.AdminPush("origin", "main")

	cw.bob.Git("fetch", "-q")
	head := cw.bob.Head()
	res := ciMergeOf(t, cw.bob)
	if res.Outcome != "merged" || res.Commit == "" || len(res.Merged) == 0 {
		t.Fatalf("result = %+v", res)
	}
	doc := ruleDoc(t, cw.bob, cw.rule)
	if doc.Body != "v2 from main" || doc.Severity != "should" {
		t.Fatalf("both edits must survive: body=%q severity=%q", doc.Body, doc.Severity)
	}
	if parents := strings.Fields(cw.bob.Git("log", "-1", "--format=%P")); len(parents) != 2 || parents[0] != head {
		t.Fatalf("not a merge on top of the branch: %v", parents)
	}
	if st := cw.bob.Status(); st != "" {
		t.Fatalf("left changes behind: %s", st)
	}
	if again := ciMergeOf(t, cw.bob); again.Outcome != "up-to-date" {
		t.Fatalf("second run = %+v", again)
	}
}

// TestB11Sync_CIMergeLeavesHumanConflictsAlone (TestE2E_CIMergeLeavesHumanConflictsAlone):
// the same text on both sides, or a code conflict, changes nothing and
// names the blocking paths.
func TestB11Sync_CIMergeLeavesHumanConflictsAlone(t *testing.T) {
	t.Run("same text", func(t *testing.T) {
		cw := newCIWorld(t)
		cw.bob.MustLore("rule", "edit", cw.rule, "--body", "bob's text")
		cw.bob.CommitAll("bob")
		cw.alice.MustLore("rule", "edit", cw.rule, "--body", "alice's text")
		cw.alice.CommitAll("alice")
		cw.alice.AdminPush("origin", "main")
		cw.bob.Git("fetch", "-q")
		head := cw.bob.Head()
		res := ciMergeOf(t, cw.bob)
		if res.Outcome != "needs-human" || len(res.Blocking) != 1 || !strings.Contains(res.Blocking[0], cw.rule) || res.Reason == "" {
			t.Fatalf("result = %+v", res)
		}
		if cw.bob.Head() != head || cw.bob.Status() != "" {
			t.Fatal("needs-human must leave the branch untouched")
		}
	})
	t.Run("code conflict", func(t *testing.T) {
		cw := newCIWorld(t)
		cw.bob.WriteFile("README.md", "# bob\n")
		cw.bob.CommitAll("bob readme")
		cw.alice.WriteFile("README.md", "# alice\n")
		cw.alice.CommitAll("alice readme")
		cw.alice.AdminPush("origin", "main")
		cw.bob.Git("fetch", "-q")
		res := ciMergeOf(t, cw.bob)
		if res.Outcome != "needs-human" || strings.Join(res.Blocking, ",") != "README.md" {
			t.Fatalf("result = %+v", res)
		}
		if _, err := os.Stat(filepath.Join(cw.bob.Dir, ".git", "MERGE_HEAD")); err == nil {
			t.Fatal("a merge was left in progress")
		}
	})
}

// TestB11Sync_CIMergeCleanAndGuards (TestE2E_CIMergeCleanAndGuards): new
// rows on both sides are a clean merge; a dirty checkout and a missing
// --base are refused.
func TestB11Sync_CIMergeCleanAndGuards(t *testing.T) {
	cw := newCIWorld(t)
	addSyncRule(t, cw.bob, "only on feature")
	cw.bob.CommitAll("bob")
	addSyncRule(t, cw.alice, "only on main")
	cw.alice.CommitAll("alice")
	cw.alice.AdminPush("origin", "main")
	cw.bob.Git("fetch", "-q")
	if res := ciMergeOf(t, cw.bob); res.Outcome != "clean" {
		t.Fatalf("new rows on both sides must be a clean merge: %+v", res)
	}
	cw.bob.WriteFile("scratch.txt", "x")
	if r := cw.bob.Lore("sync", "ci-merge", "--base", ciMergeBaseRef); r.Code == 0 || !strings.Contains(r.Out(), "clean checkout") {
		t.Fatalf("a dirty checkout must be refused: %s", r)
	}
	if r := cw.bob.Lore("sync", "ci-merge"); r.Code == 0 || !strings.Contains(r.Out(), "base") {
		t.Fatalf("a missing --base must be refused: %s", r)
	}
}

// actionWorkflow is the slice of the generated workflow the test reads.
type actionWorkflow struct {
	Jobs map[string]struct {
		Steps []struct {
			Run string `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// TestB11Sync_InstalledWorkflowScriptMergesPullRequest
// (TestE2E_InstalledWorkflowScriptMergesPullRequest): the merge step of
// the workflow install-action writes, run in a fresh CI clone with gh (and
// jq, which GitHub runners have) stubbed, merges the pull request branch,
// pushes it and writes the run summary; afterwards the branch is up to
// date with main and both edits are in it.
func TestB11Sync_InstalledWorkflowScriptMergesPullRequest(t *testing.T) {
	cw := newCIWorld(t)
	cw.bob.MustLore("rule", "edit", cw.rule, "--severity", "should")
	cw.bob.CommitAll("bob")
	cw.bob.Git("push", "-q", "-u", "origin", "feature")
	cw.alice.MustLore("rule", "edit", cw.rule, "--body", "v2 from main")
	cw.alice.CommitAll("alice")
	cw.alice.AdminPush("origin", "main")

	gen := cw.alice.Lore("sync", "install-action", "--dry-run", "--lore-version", "v0.1.10")
	bugRepoSyncHost.PendingOn(t, gen, "E_UNSUPPORTED", "install-action")
	gen.OK(t)
	var doc actionWorkflow
	if err := yaml.Unmarshal([]byte(gen.Stdout), &doc); err != nil {
		t.Fatalf("install-action --dry-run is not YAML: %v\n%s", err, gen.Stdout)
	}
	script := ""
	for _, s := range doc.Jobs["merge"].Steps {
		if strings.Contains(s.Run, "lore sync ci-merge") {
			script = s.Run
		}
	}
	if script == "" {
		t.Fatalf("no merge step in:\n%s", gen.Stdout)
	}

	ci := cw.w.Human("ci", "ci@acme.example").Clone(cw.app, "ci")
	stub := t.TempDir()
	writeStub := func(name, body string) {
		if err := os.WriteFile(filepath.Join(stub, name), []byte(body), stubScriptMode); err != nil {
			t.Fatal(err)
		}
	}
	writeStub("gh", "#!/bin/sh\ncase \"$*\" in\n  *headRefName*) echo feature ;;\n  *baseRefName*) echo main ;;\n  *'pr list'*) echo 7 ;;\n  *) echo \"unexpected gh $*\" >&2; exit 1 ;;\nesac\n")
	// jq -r '.data.<key>' over stdin: all the script uses.
	writeStub("jq", "#!/bin/sh\nexec python3 -c 'import json,sys\nd=json.load(sys.stdin)\nfor k in sys.argv[1].strip(\".\").split(\".\"):\n    d=d[k]\nprint(d)' \"$2\"\n")
	summary := filepath.Join(t.TempDir(), "summary.md")
	cmd := ci.Command()
	path := os.Getenv("PATH")
	for _, kv := range cmd.Env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("bash is required: %v", err)
	}
	cmd.Path, cmd.Args = bash, []string{bash, "-e", "-o", "pipefail", "-c", script}
	cmd.Env = append(cmd.Env, "PATH="+stub+string(os.PathListSeparator)+path, "EVENT=pull_request", "PR=7", "GITHUB_STEP_SUMMARY="+summary)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("workflow script failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "#7 (feature <- main): merged") {
		t.Fatalf("unexpected run: %s", out)
	}
	if got, _ := os.ReadFile(filepath.Clean(summary)); !strings.Contains(string(got), "#7: merged") {
		t.Fatalf("summary = %q", got)
	}
	cw.bob.Git("fetch", "-q")
	cw.bob.Git("reset", "-q", "--hard", "origin/feature")
	if res := ciMergeOf(t, cw.bob); res.Outcome != "up-to-date" {
		t.Fatalf("after the pushed merge = %+v", res)
	}
	if d := ruleDoc(t, cw.bob, cw.rule); d.Body != "v2 from main" || d.Severity != "should" {
		t.Fatalf("both edits must be on the branch: %v", d)
	}
}

// TestB11Sync_PeekAndPromote (TestE2E_PeekOtherBranch,
// TestE2E_PromoteToMain): a rule written on a feature branch is not on
// main; `sync peek feature/x rules` lists it without a checkout (and
// --json is the sync.peek envelope); a bad ref is refused. `sync promote
// <id> --to main` (after a --dry-run) commits it onto main without leaving
// the branch; promoting onto the checked-out branch is refused.
func TestB11Sync_PeekAndPromote(t *testing.T) {
	s := newSyncSolo(t)
	c := s.c
	c.Git("checkout", "-q", "-b", "feature/x")
	rule := addSyncRule(t, c, "urgent: never log tokens")
	c.CommitAll("feature")
	c.Git("checkout", "-q", "main")
	c.MustLore("rule", "list").Never(t, "never log tokens")

	r := c.Lore("sync", "peek", "feature/x", "rules")
	bugRepoSyncHost.PendingOn(t, r, "E_UNSUPPORTED", "peek")
	r.OK(t).Says(t, rule, "urgent: never log tokens")
	type peekJSON struct {
		Kind string `json:"kind"`
		Data []struct {
			Table string `json:"table"`
			ID    string `json:"id"`
		} `json:"data"`
	}
	pj := th.DecodeJSON[peekJSON](t, c.MustLore("sync", "peek", "feature/x", "--json"))
	tables := map[string]bool{}
	for _, d := range pj.Data {
		tables[d.Table] = true
	}
	if pj.Kind != "sync.peek" || !tables["projects"] || !tables["rules"] {
		t.Fatalf("peek --json: %+v", pj)
	}
	if bad := c.Lore("sync", "peek", "no-such-ref"); bad.Code == 0 {
		t.Fatalf("a bad ref must be refused: %s", bad)
	}
	c.MustLore("rule", "list").Never(t, "never log tokens")

	c.Git("checkout", "-q", "feature/x")
	c.MustLore("sync", "promote", rule, "--to", "main", "--dry-run").Says(t, "would commit")
	if bad := c.Lore("sync", "promote", rule, "--to", "feature/x"); bad.Code == 0 {
		t.Fatalf("promote onto the checked-out branch must be refused: %s", bad)
	}
	c.MustLore("sync", "promote", rule, "--to", "main").Says(t, "not pushed")
	if st := c.Status(); st != "" {
		t.Fatalf("promote dirtied the checkout: %s", st)
	}
	c.Git("checkout", "-q", "main")
	c.MustLore("rule", "list").Says(t, "never log tokens")
}
