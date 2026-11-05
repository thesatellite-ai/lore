package setup

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// fakeHost records what setup asked for.
type fakeHost struct {
	plans       []HookPlan
	schemaQuiet []bool
	written     int
	removed     bool
}

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
