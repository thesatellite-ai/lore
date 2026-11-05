// Package setup defines `lore setup` (layer L5, CLI_REFERENCE 2.14 and
// 3.18, CONTEXT_BUDGET 6.9, FMT 8.11): the database schema update after an
// install or upgrade (--schema-only: only that, quietly, as `lore upgrade`
// runs it after the swap), and --agent-hooks: the coding tools' session
// and context hooks in their local settings files.
//
//	setup.run ─▶ --agent-hooks ─▶ Host.PlanAgentHooks ─▶ one line per hook
//	          │                   ─▶ nothing to change | --dry-run: stop
//	          │                   ─▶ not confirmed: E_NEEDS_CONFIRM (the surface asks)
//	          │                   ─▶ Host.WriteAgentHooks ─▶ "✓ N settings file(s) written"
//	          └▶ otherwise ─▶ Host.UpdateSchema(quiet = --schema-only) ─▶ its lines
//
// The schema and the settings files are this machine's and the
// checkout's own files, never rows, so setup is a Local operation; the
// binary that owns the database supplies Host (PASS3 ONE-PATH, P3-MISC-3,
// P3-MISC-4).
package setup

import (
	"context"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names (`lore setup`, a bare-noun shortcut of run).
const (
	Noun  = "setup"
	OpRun = "setup.run"
	// RunVerb is the generated verb the bare `lore setup` runs.
	RunVerb = "run"
)

// restNoun keeps the REST collection "setup" (the default plural would be
// "setups").
const restNoun = "setup"

// Lines of the --agent-hooks mode (v0.1.17 wording).
const (
	lineNothing = "Nothing to change."
	lineDryRun  = "Nothing written (dry run)."
	hooksHint   = "rerun with --yes to write them, or --dry-run to only show them"
)

// HookPlan is what --agent-hooks does to one tool's hook.
type HookPlan struct {
	Tool string `json:"tool"`
	// File is the settings file (slash path in the repo), or why there is
	// none ("no confirmed session hook").
	File string `json:"file"`
	Note string `json:"note"`
	// Change is true when the file is rewritten.
	Change bool `json:"change"`
	// Hook is the hook itself, for Host.WriteAgentHooks; zero for a tool
	// with no confirmed hook.
	Hook toolprofile.SessionHook `json:"-"`
}

// Host is the machinery behind setup, supplied by the binary that owns
// the database and the tool settings writers.
//
// Invariants: PlanAgentHooks writes nothing; WriteAgentHooks rewrites
// only the plans with Change set, in place, keeping everything in each
// file that is not lore's hook.
type Host interface {
	// UpdateSchema runs the database schema update (and, unless quiet,
	// the search index rebuild); it returns the lines it reports.
	UpdateSchema(ctx context.Context, quiet bool) ([]string, error)
	// PlanAgentHooks lists, per tool, what --agent-hooks would change in
	// the checkout (E_INVALID_INPUT outside a git work tree).
	PlanAgentHooks(ctx context.Context, remove, promptSearch bool) ([]HookPlan, error)
	// WriteAgentHooks applies plans (remove takes lore's hooks out).
	WriteAgentHooks(ctx context.Context, plans []HookPlan, remove bool) error
}

// Env is what setup needs; *wire.App implements it.
type Env interface {
	// SetupHost is the binary's Host (E_UNSUPPORTED when it has none).
	SetupHost(ctx context.Context) (Host, error)
}

// RunIn is the input of setup.run.
type RunIn struct {
	SchemaOnly   bool `json:"schema_only,omitempty" flag:"schema-only" desc:"update only the local database schema (used by lore upgrade after the swap)"`
	AgentHooks   bool `json:"agent_hooks,omitempty" flag:"agent-hooks" desc:"install the session and context hook snippets for the tools set up here (asks first)"`
	Remove       bool `json:"remove,omitempty" flag:"remove" desc:"with --agent-hooks: take the snippets out again"`
	DryRun       bool `json:"dry_run,omitempty" flag:"dry-run" desc:"with --agent-hooks: only show the changes"`
	PromptSearch bool `json:"prompt_search,omitempty" flag:"prompt-search" desc:"with --agent-hooks: also install the per-prompt search hook on this machine"`
}

// RunOut is the output of setup.run.
type RunOut struct {
	Lines []string `json:"lines"`
	// Hooks is the --agent-hooks plan (empty otherwise).
	Hooks []HookPlan `json:"hooks,omitempty"`
	// Written is how many settings files --agent-hooks rewrote.
	Written int `json:"written,omitempty"`
}

// Text implements op.Texter: the lines, as v0.1.17 printed them.
func (o RunOut) Text() string {
	if len(o.Lines) == 0 {
		return ""
	}
	return strings.Join(o.Lines, "\n") + "\n"
}

// Ops returns the setup operation bound to env, for op.NewRegistry.
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpRun, Summary: "Update the local database schema after install or upgrade; --agent-hooks: install the tools' session and context hooks",
			Permission: op.PermWrite, Access: op.AccessRead, Local: true, RESTNoun: restNoun,
			Errors: []errs.Code{errs.InvalidInput, errs.NeedsConfirm, errs.ReadOnly, errs.Unsupported, errs.Internal},
			// MayAsk: --agent-hooks lists the changes first and asks
			// (CLI_REFERENCE 2.14 "after a confirmation").
			Confirm:  op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "show what --agent-hooks would change", CLI: "lore setup --agent-hooks --dry-run", Want: ""}},
		}, func(ctx context.Context, in RunIn) (RunOut, error) {
			h, err := env.SetupHost(ctx)
			if err != nil {
				return RunOut{}, err
			}
			return Run(ctx, h, in, op.Confirmed(ctx))
		}),
	}
}

// Run is setup.run over h; yes is the caller's confirmation.
func Run(ctx context.Context, h Host, in RunIn, yes bool) (RunOut, error) {
	if !in.AgentHooks {
		if in.Remove || in.DryRun || in.PromptSearch {
			return RunOut{}, errs.New(errs.InvalidInput, "--remove, --dry-run and --prompt-search go with --agent-hooks").
				WithHint("run `lore setup --agent-hooks --dry-run`")
		}
		lines, err := h.UpdateSchema(ctx, in.SchemaOnly)
		if lines == nil {
			lines = []string{}
		}
		return RunOut{Lines: lines}, err
	}
	if in.SchemaOnly {
		return RunOut{}, errs.New(errs.InvalidInput, "--schema-only and --agent-hooks are two different runs").WithHint("run them one after the other")
	}
	return agentHooks(ctx, h, in, yes)
}

// agentHooks is the --agent-hooks mode: list, stop or ask, then write.
func agentHooks(ctx context.Context, h Host, in RunIn, yes bool) (RunOut, error) {
	promptSearch := in.PromptSearch || in.Remove // --remove takes out every lore hook
	plans, err := h.PlanAgentHooks(ctx, in.Remove, promptSearch)
	if err != nil {
		return RunOut{}, err
	}
	out := RunOut{Hooks: plans, Lines: make([]string, 0, len(plans)+1)}
	changes := 0
	for _, p := range plans {
		out.Lines = append(out.Lines, fmt.Sprintf("%-13s %-28s %s", p.Tool, p.File, p.Note))
		if p.Change {
			changes++
		}
	}
	switch {
	case changes == 0:
		out.Lines = append(out.Lines, lineNothing)
		return out, nil
	case in.DryRun:
		out.Lines = append(out.Lines, lineDryRun)
		return out, nil
	}
	if !yes {
		q := fmt.Sprintf("Write %d settings file(s)?", changes)
		next := []string{"setup", "--agent-hooks", "--yes"}
		if in.Remove {
			next = append(next, "--remove")
		}
		return RunOut{}, errs.New(errs.NeedsConfirm, strings.Join(out.Lines, "\n")+"\n"+q).
			WithQuestion(q).WithHint(hooksHint).WithNext("lore", next...)
	}
	if err := h.WriteAgentHooks(ctx, plans, in.Remove); err != nil {
		return RunOut{}, err
	}
	out.Written = changes
	out.Lines = append(out.Lines, fmt.Sprintf("✓ %d settings file(s) written", changes))
	return out, nil
}
