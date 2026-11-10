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
	"os"
	"path/filepath"
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
	// Web, NoOpen and Port are the guided page (ONBOARDING_DESIGN 5.1,
	// Q1): bare `lore setup` asks for the page too; Web asks for it
	// explicitly (`lore setup web`, scripts). The setup name comes from the
	// global --setup (op.SetupOf).
	Web    bool `json:"web,omitempty" flag:"web" desc:"open the guided setup page (the default of bare lore setup on a terminal)"`
	NoOpen bool `json:"no_open,omitempty" flag:"no-open" desc:"print the page address instead of opening a browser"`
	Port   int  `json:"port,omitempty" flag:"port" desc:"the page's local port (default: a free one)"`
}

// RunOut is the output of setup.run.
type RunOut struct {
	Lines []string `json:"lines"`
	// Hooks is the --agent-hooks plan (empty otherwise).
	Hooks []HookPlan `json:"hooks,omitempty"`
	// Written is how many settings files --agent-hooks rewrote.
	Written int `json:"written,omitempty"`
	// Web is set when the caller should open the guided page (bare
	// `lore setup`, --web; Q1). The op never opens it: cmd/lore starts
	// surface/setupweb when Web is set and stdout is a terminal or --web
	// was given; REST and --json only report it.
	Web WebStart `json:"web,omitzero"`
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
			Name: OpRun, Summary: "Open the guided setup page (setups, identity, GitHub, stores, tools, folders); --schema-only: update the local database schema after an upgrade; --agent-hooks: install the tools' session and context hooks",
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
// schemaSkippedLine starts the warning a page run prints when the schema
// update could not run (an offline or broken attached store); the page
// still opens.
const schemaSkippedLine = "schema update skipped (run lore setup --schema-only once the store is reachable): "

func Run(ctx context.Context, h Host, in RunIn, yes bool) (RunOut, error) {
	if !in.AgentHooks {
		if in.Remove || in.DryRun || in.PromptSearch {
			return RunOut{}, errs.New(errs.InvalidInput, "--remove, --dry-run and --prompt-search go with --agent-hooks").
				WithHint("run `lore setup --agent-hooks --dry-run`")
		}
		if in.SchemaOnly && (in.Web || in.NoOpen || in.Port != 0) {
			return RunOut{}, errs.New(errs.InvalidInput, "--schema-only is the quiet upgrade step; --web, --no-open and --port go with the setup page").
				WithHint("run `lore setup` for the page")
		}
		// Q1: the page run updates the schema quietly first; a plain
		// non-page caller (REST, --json) still gets the lines. A folder
		// lore is not set up in has no schema to update, and the update
		// would open the repo store, which writes the cache, the git
		// merge driver, .gitattributes and hooks before the person chose
		// anything (A13 bug 3; ONB 1.4 step 8: `lore init` runs only from
		// the this_folder step). --schema-only (lore upgrade) is unchanged.
		var lines []string
		var err error
		if in.SchemaOnly || loreRepoHere(ctx, h) {
			lines, err = h.UpdateSchema(ctx, in.SchemaOnly)
		}
		if !in.SchemaOnly && errs.Has(err, errs.NotAStore) {
			// Bare `lore setup` in ~ on a fresh machine: there is no repo
			// and so no search index to rebuild. The page is what the
			// person came for, so the schema step is skipped quietly
			// (--schema-only, lore upgrade's step, still reports it).
			lines, err = nil, nil
		}
		if !in.SchemaOnly && err != nil {
			// A repo whose attached store is offline or broken
			// (E_STORE_UNREACHABLE: no local copy yet and no network) must
			// not keep the person out of the page that repairs it: the
			// schema step is reported as one warning line and the page
			// opens. --schema-only (lore upgrade) still fails on it.
			msg, _, _ := strings.Cut(errs.EnvelopeOf(err).Error.Message, "\n")
			lines = append(lines, schemaSkippedLine+msg)
			err = nil
		}
		if lines == nil {
			lines = []string{}
		}
		out := RunOut{Lines: lines}
		if !in.SchemaOnly {
			out.Web = WebStart{Open: true, Setup: op.SetupOf(ctx), NoOpen: in.NoOpen, Port: in.Port}
		}
		return out, err
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

// RepoReady is an optional upgrade of Host: whether the folder lore runs
// in is an initialised lore repo. A Host without it is answered from the
// file system (LoreRepoAt of the working folder).
type RepoReady interface {
	LoreRepoReady(ctx context.Context) bool
}

// loreMeta is the file `lore init` writes; a .lore/ without it (only a
// cache) is not a lore repo yet.
var loreMeta = filepath.Join(".lore", "data", "_meta.json")

// loreRepoHere reports whether the working folder is in a lore repo.
func loreRepoHere(ctx context.Context, h Host) bool {
	if r, ok := h.(RepoReady); ok {
		return r.LoreRepoReady(ctx)
	}
	wd, err := os.Getwd()
	if err != nil {
		return false
	}
	return LoreRepoAt(wd)
}

// LoreRepoAt reports whether dir lies in an initialised lore repo: walking
// up, a folder with .lore/data/_meta.json comes before (or is) the first
// folder with .git. Reads only.
func LoreRepoAt(dir string) bool {
	for d := filepath.Clean(dir); ; {
		if _, err := os.Stat(filepath.Join(d, loreMeta)); err == nil {
			return true
		}
		if _, err := os.Stat(filepath.Join(d, gitDirName)); err == nil {
			return false
		}
		up := filepath.Dir(d)
		if up == d {
			return false
		}
		d = up
	}
}

// gitDirName marks a repo's top folder (a folder, or a file in a worktree).
const gitDirName = ".git"
