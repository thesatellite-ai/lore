package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// fakeHost records what setup asked for.
type fakeHost struct {
	plans       []HookPlan
	schemaQuiet []bool
	notLore     bool
	written     int
	removed     bool
}

// LoreRepoReady implements RepoReady: the fake host is a lore repo
// unless notLore says otherwise.
func (h *fakeHost) LoreRepoReady(context.Context) bool { return !h.notLore }

func (h *fakeHost) UpdateSchema(_ context.Context, quiet bool) ([]string, error) {
	h.schemaQuiet = append(h.schemaQuiet, quiet)
	if quiet {
		return nil, nil
	}
	return []string{"→ running ent schema migration...", "✓ setup complete"}, nil
}

func (h *fakeHost) PlanAgentHooks(_ context.Context, remove, _ bool) ([]HookPlan, error) {
	h.removed = remove
	return h.plans, nil
}

func (h *fakeHost) WriteAgentHooks(_ context.Context, plans []HookPlan, _ bool) error {
	for _, p := range plans {
		if p.Change {
			h.written++
		}
	}
	return nil
}

func twoPlans() []HookPlan {
	return []HookPlan{
		{Tool: "claude_code", File: ".claude/settings.json", Note: "add SessionStart", Change: true},
		{Tool: "codex", File: "no confirmed session hook", Note: "nothing to install"},
	}
}

// TestSchemaModes: plain setup prints the host's lines; --schema-only
// runs quietly; hook-only flags without --agent-hooks are refused.
func TestSchemaModes(t *testing.T) {
	ctx := context.Background()
	h := &fakeHost{}
	out, err := Run(ctx, h, RunIn{}, false)
	if err != nil || !strings.Contains(out.Text(), "setup complete") {
		t.Fatalf("setup: %q %v", out.Text(), err)
	}
	quiet, err := Run(ctx, h, RunIn{SchemaOnly: true}, false)
	if err != nil || quiet.Text() != "" || len(h.schemaQuiet) != 2 || !h.schemaQuiet[1] {
		t.Fatalf("--schema-only: %q %v %v", quiet.Text(), err, h.schemaQuiet)
	}
	for _, in := range []RunIn{{DryRun: true}, {Remove: true}, {PromptSearch: true}, {AgentHooks: true, SchemaOnly: true}} {
		if _, err := Run(ctx, h, in, true); !errs.Has(err, errs.InvalidInput) {
			t.Fatalf("%+v: %v, want E_INVALID_INPUT", in, err)
		}
	}
}

// TestAgentHooksAsksThenWrites: --dry-run lists and writes nothing; no
// confirmation is E_NEEDS_CONFIRM with the list and the --yes command;
// confirmed writes the changed files; nothing to change never asks.
func TestAgentHooksAsksThenWrites(t *testing.T) {
	ctx := context.Background()
	h := &fakeHost{plans: twoPlans()}
	dry, err := Run(ctx, h, RunIn{AgentHooks: true, DryRun: true}, false)
	if err != nil || !strings.HasSuffix(dry.Text(), lineDryRun+"\n") || h.written != 0 {
		t.Fatalf("dry run: %q %v", dry.Text(), err)
	}
	if !strings.HasPrefix(dry.Text(), "claude_code   .claude/settings.json") {
		t.Fatalf("plan line: %q", dry.Text())
	}
	_, err = Run(ctx, h, RunIn{AgentHooks: true, Remove: true}, false)
	if !errs.Has(err, errs.NeedsConfirm) || h.written != 0 || !h.removed {
		t.Fatalf("unconfirmed: %v written %d", err, h.written)
	}
	body := errs.EnvelopeOf(err).Error
	if !strings.Contains(body.Message, "Write 1 settings file(s)?") || body.Next == nil || strings.Join(body.Next.Args, " ") != "setup --agent-hooks --yes --remove" {
		t.Fatalf("refusal = %+v", body)
	}
	done, err := Run(ctx, h, RunIn{AgentHooks: true}, true)
	if err != nil || h.written != 1 || done.Written != 1 || !strings.HasSuffix(done.Text(), "✓ 1 settings file(s) written\n") {
		t.Fatalf("confirmed: %q %v", done.Text(), err)
	}
	none := &fakeHost{plans: twoPlans()[1:]}
	n, err := Run(ctx, none, RunIn{AgentHooks: true}, false)
	if err != nil || !strings.HasSuffix(n.Text(), lineNothing+"\n") {
		t.Fatalf("nothing to change: %q %v", n.Text(), err)
	}
}

// TestOpsSpec: Local and MayAsk (asks only from its own run).
func TestOpsSpec(t *testing.T) {
	s := Ops(nil)[0].Spec()
	if s.Name != OpRun || !s.Local || !s.Confirm.MayAsk || s.Confirm.Always || s.Confirm.When != "" {
		t.Fatalf("spec = %+v", s)
	}
}

// notRepoHost answers like the schema step outside a git work tree.
type notRepoHost struct{ fakeHost }

func (*notRepoHost) UpdateSchema(context.Context, bool) ([]string, error) {
	return nil, errs.New(errs.NotAStore, "git: the folder is not inside a git work tree")
}

// TestBareSetupOutsideRepo: bare `lore setup` in ~ (no repo) still asks
// for the page; --schema-only keeps the error (ONB Q1).
func TestBareSetupOutsideRepo(t *testing.T) {
	out, err := Run(t.Context(), &notRepoHost{}, RunIn{}, false)
	if err != nil || !out.Web.Open || len(out.Lines) != 0 {
		t.Fatalf("bare outside a repo: %+v %v", out, err)
	}
	if _, err = Run(t.Context(), &notRepoHost{}, RunIn{SchemaOnly: true}, false); !errs.Has(err, errs.NotAStore) {
		t.Fatalf("--schema-only outside a repo: %v", err)
	}
}

// TestPageRunWritesNothingOutsideLore: bare `lore setup` / `lore setup
// web` in a folder lore is not set up in skips the schema update (it
// would open the repo store and write in the repo, A13 bug 3) and still
// asks for the page; --schema-only still runs it.
func TestPageRunWritesNothingOutsideLore(t *testing.T) {
	h := &fakeHost{notLore: true}
	for _, in := range []RunIn{{}, {Web: true, NoOpen: true}} {
		out, err := Run(t.Context(), h, in, false)
		if err != nil || !out.Web.Open || len(out.Lines) != 0 || len(h.schemaQuiet) != 0 {
			t.Fatalf("%+v: %+v %v schema %v", in, out, err, h.schemaQuiet)
		}
	}
	if _, err := Run(t.Context(), h, RunIn{SchemaOnly: true}, false); err != nil || len(h.schemaQuiet) != 1 {
		t.Fatalf("--schema-only: %v %v", err, h.schemaQuiet)
	}
}

// TestLoreRepoAt: a repo with .lore/data/_meta.json is a lore repo from
// any folder inside it; a git repo without it (even with a half-made
// .lore/ holding only a cache) is not; the walk stops at .git.
func TestLoreRepoAt(t *testing.T) {
	outer := t.TempDir()
	mk := func(p string) {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	repo := filepath.Join(outer, "notes")
	mk(filepath.Join(repo, ".git"))
	mk(filepath.Join(repo, ".lore"))
	if err := os.WriteFile(filepath.Join(repo, ".lore", "lore-cache.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "src", "a")
	mk(sub)
	// a lore repo above the git top must not count
	mk(filepath.Join(outer, ".lore", "data"))
	if err := os.WriteFile(filepath.Join(outer, ".lore", "data", "_meta.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if LoreRepoAt(sub) || LoreRepoAt(repo) {
		t.Fatal("a git repo without .lore/data/_meta.json counted as a lore repo")
	}
	mk(filepath.Join(repo, ".lore", "data"))
	if err := os.WriteFile(filepath.Join(repo, ".lore", "data", "_meta.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !LoreRepoAt(sub) || !LoreRepoAt(repo) {
		t.Fatal("an initialised repo did not count")
	}
}
