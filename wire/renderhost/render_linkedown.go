package renderhost

// render_linkedown.go — `lore render` in a repo linked as one of the org's
// own repos that holds no .lore folder (devco D-429).
//
// Why: `lore org link --own` writes nothing into the repo, so its notes
// live in the store folder. render refused there (E_NOT_A_STORE with a
// hint to link the repo, already linked), --report counted 0 rules, and
// the dry run's LORE.md said "No pinned knowledge yet" while the 29 must
// rules sat in a private file it never named. Now such a repo gets the
// private render (outside the checkout; the tool's local file, which git
// excludes, imports it), no LORE.md and no pointer block, so `git status`
// stays clean; --report counts the private rows. A client repo keeps its
// own branch (render.go: nothing written, never lore files).

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/wire"
)

// privateReport is --report's private render share: what the attached
// internal stores add outside the checkout (render_private.go).
type privateReport struct {
	Dir      string `json:"dir"`
	Stores   int    `json:"stores"`
	Pinned   int    `json:"pinned"`    // must rules and critical/high hotfixes (full, index line or not listed)
	Rows     int    `json:"rows"`      // every row shown in the store files
	LeftOut  int    `json:"left_out"`  // "Other notes" rows the budget left out
	Budget   int    `json:"budget"`    // the budget the files were fitted to (context.budget.internal, clamped)
	Bytes    int    `json:"bytes"`     // the store files' size
	Core     int    `json:"core"`      // bytes of the pinned part in full
	OverCore bool   `json:"over_core"` // the pinned part in full is over the budget
	// D-909: how the hard budget wrote the pinned rows, and the budget as set
	PinnedFull      int `json:"pinned_full"`
	PinnedIndex     int `json:"pinned_index"`
	PinnedNotListed int `json:"pinned_not_listed"`
	BudgetSet       int `json:"budget_set"`
	// fix is the warning's fix for this caller, "" when they can make
	// none (a member of a company folder, devco D-725): the report then
	// names no command, as the warning is not printed to them.
	fix string
}

// text is the --report section ("" for nil).
func (r *privateReport) text() string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nprivate context (outside the checkout: %s)\n", r.Dir)
	fmt.Fprintf(&b, "  internal stores        %4d\n", r.Stores)
	fmt.Fprintf(&b, "  pinned rules/hotfixes  %4d  %d full, %d one-line index, %d not listed\n", r.Pinned, r.PinnedFull, r.PinnedIndex, r.PinnedNotListed)
	fmt.Fprintf(&b, "  rows shown             %4d  %d bytes\n", r.Rows, r.Bytes)
	fmt.Fprintf(&b, "  notes left out         %4d  %s %d bytes\n", r.LeftOut, policy.KeyContextBudgetInternal, r.Budget)
	if r.BudgetSet != r.Budget {
		fmt.Fprintf(&b, "  budget                       %d as set, clamped to %d (everything lore adds to a tool's instruction files stays under %d bytes)\n", r.BudgetSet, r.Budget, policy.ContextCeiling)
	}
	// devco D-725: the fix is the warning's own (commands that run where
	// printed, with the --to of the place the budget is set), and only for
	// whoever may make it; it printed `lore policy set … --yes` with no
	// --to, to members too
	switch {
	case r.OverCore && r.fix != "":
		fmt.Fprintf(&b, "  the pinned part alone is %d bytes in full, over %s %d; fix: %s\n", r.Core, policy.KeyContextBudgetInternal, r.Budget, r.fix)
	case r.OverCore:
		fmt.Fprintf(&b, "  the pinned part alone is %d bytes in full, over %s %d; its rows and budget are changed by an admin of the org\n", r.Core, policy.KeyContextBudgetInternal, r.Budget)
	}
	return b.String()
}

// privateReportOf plans the private render of root for --report; nil
// when no internal store is attached or the plan fails (the report of
// the committed files still prints).
func (h *Host) privateReportOf(ctx context.Context, root string) *privateReport {
	if h.app == nil {
		return nil
	}
	p, err := h.planPrivate(ctx, root)
	if err != nil || len(p.stores) == 0 {
		return nil
	}
	size := 0
	for _, f := range p.files {
		if filepath.Base(f.path) != privateIndexFile { // index.md is never imported
			size += len(f.body)
		}
	}
	r := &privateReport{Dir: h.redact(p.dir), Stores: len(p.stores), Pinned: p.pinned, Rows: p.rows, LeftOut: p.leftOut,
		Budget: p.budget, Bytes: size, Core: p.coreBytes, OverCore: p.budget > 0 && p.coreBytes > p.budget,
		PinnedFull: p.pinForms.full, PinnedIndex: p.pinForms.line, PinnedNotListed: p.pinForms.out, BudgetSet: p.setting}
	if p.overCoreWarns() {
		r.fix = overCoreFix(p.coreBytes, p.pins, p.fix)
	}
	return r
}

// noRepoFile is the one decision render's report, dry run and write take
// in a repo that never gets a repo file (devco D-481): a repo with no
// .lore folder that is a client repo, or that a store links as one of the
// org's own repos. Why one decision: in a client repo --report showed a
// `lore_md .lore/LORE.md` row and --dry-run a LORE.md body while --yes
// (rightly) wrote nothing, because each path asked its own question.
type noRepoFile struct {
	// top is the repo's top folder.
	top string
	// client is set for a client repo (never holds lore files); unset for
	// a linked own repo (holds none until lore init).
	client bool
}

// noRepoFileOf is root's noRepoFile; ok=false when render writes the
// repo's files as usual (it holds a .lore folder, or is neither linked
// nor a client repo: the not-set-up refusal then decides). Errors read as
// ok=false (the caller's own branch then decides).
func (h *Host) noRepoFileOf(ctx context.Context, root string) (noRepoFile, bool) {
	if h.app == nil {
		return noRepoFile{}, false
	}
	top := wire.UntouchedRepo(ctx, root)
	if top == "" {
		return noRepoFile{}, false
	}
	client, err := h.app.ClientRepo(ctx)
	if err != nil {
		return noRepoFile{}, false
	}
	if !client && h.app.LinkedByRemote(ctx) == "" {
		return noRepoFile{}, false
	}
	return noRepoFile{top: top, client: client}, true
}

// line says why no repo file is written (no LORE.md, no pointer block).
func (n noRepoFile) line() string {
	if n.client {
		return fmt.Sprintf(noLoreMDClientLine, n.top)
	}
	return fmt.Sprintf(noLoreMDLine, n.top)
}

// noLoreMDLine says why a linked own repo gets no LORE.md.
const noLoreMDLine = "no LORE.md: %s has no .lore folder, so nothing is written in the repo (a repo linked to the org store keeps its notes there; lore init adds a .lore folder if you want a LORE.md)"

// noLoreMDClientLine says why a client repo gets no LORE.md (F-2: a
// client's repo never holds lore files).
const noLoreMDClientLine = "no LORE.md: %s is a client repo, which never holds lore files (no LORE.md, no pointer block)"

// noRepoFileHow says how a tool gets the notes when no repo file is
// written (devco D-482: the reply named only lore init, and nothing
// pointed a tool at the private file). The per-file and per-prompt
// context (lore context, the agent hooks) picks rows by path and words;
// the always-on rules reach a tool through the private file and, where no
// LORE.md is, the SessionStart agent hook (devco D-494).
const noRepoFileHow = "how a tool gets these notes: (1) the private file below, which the tool's git-excluded local file imports when claude_code (CLAUDE.local.md) or cursor (.cursor/rules/lore-private.mdc) is one of the repo's tools; " +
	"(2) per file or prompt: `lore context --for-file <path>` or `lore context --for-prompt <text>`, run by any agent, or by the agent hooks on their own (`lore setup --agent-hooks`; it needs the tool's folder here, such as .claude/). " +
	"The always-on rules come with (1) and with the agent hooks' session start; per file or prompt only the rows that match"

// noRepoFileClientHow is noRepoFileHow for a client repo: no import file
// goes into its checkout (it holds nothing of lore's), so no tool loads
// the private file on its own.
const noRepoFileClientHow = "how a tool gets these notes: nothing of lore's goes into a client repo, so no tool loads the private file below on its own and `lore setup --agent-hooks` installs nothing here; " +
	"for the always-on rules, add `" + clientSessionStartCmd + "` as a SessionStart hook in your user-level tool settings (outside the repo); " +
	"per file or prompt, an agent runs `lore context --for-file <path>` or `lore context --for-prompt <text>`"

// how is the how-a-tool-gets-these-notes line for n.
func (n noRepoFile) how() string {
	if n.client {
		return noRepoFileClientHow
	}
	return noRepoFileHow
}

// renderNoRepoFile is render, --dry-run and --report (with or without
// --scopes) in a repo that gets no repo file (n): the line saying why, how
// a tool gets the notes, and the private render (outside the checkout;
// in a client repo without any tool's import file), which all three show
// the same way.
func (h *Host) renderNoRepoFile(ctx context.Context, f *renderFlags, n noRepoFile) error {
	if f.report {
		pr := h.privateReportOf(ctx, n.top)
		if f.asJSON {
			data, err := json.MarshalIndent(map[string]noRepoFileJSON{"data": {NoLoreMD: n.line(), Private: pr}}, "", "  ")
			if err != nil {
				return errs.Wrap(errs.Internal, "encode report", err)
			}
			fmt.Fprintln(f.out(), string(data))
			return nil
		}
		fmt.Fprintln(f.out(), n.line())
		if pr == nil {
			fmt.Fprintln(f.out(), "no internal store is attached here, so there is no private context either")
			return nil
		}
		fmt.Fprintln(f.out(), n.how())
		fmt.Fprint(f.out(), pr.text())
		return nil
	}
	if !f.dryRun {
		if err := wire.RefuseWriteInReadOnlyHome(h.opts.Home); err != nil {
			return err
		}
	}
	if !f.quiet {
		fmt.Fprintf(f.out(), "%s %s\n%s %s\n", h.decor.muted("·"), n.line(), h.decor.muted("·"), n.how())
		if !n.client {
			h.skippedPrivateLines(ctx, f.out(), n.top)
		}
	}
	// a client repo gets the files outside the checkout only: no tool's
	// import file goes into it
	return h.renderPrivateIn(ctx, f.out(), n.top, f.dryRun, f.quiet, !n.client)
}

// skippedPrivateLines names each setup tool that would get a private
// import file here but that the linked own repo does not use (D-711: it
// is not written unasked). Such a repo keeps no context.targets row (its
// notes live in the store), so what adds the tool is the tool's own file
// in the repo, which render then finds.
func (h *Host) skippedPrivateLines(ctx context.Context, w *strings.Builder, root string) {
	pick, err := h.resolveTargets(ctx, root, nil, h.coreContextTargets)
	if err != nil {
		return
	}
	for _, p := range pick.sel.Skipped {
		if !p.HasPrivateFile() {
			continue
		}
		own := p.PointerFile
		if len(p.Detect) > 0 {
			own = strings.Join(p.Detect, " or ")
		}
		fmt.Fprintf(w, "%s skipped %s (%s): your setup lists it but this repo does not use it, so it was not written; it is added once the tool's own file is here (%s)\n",
			h.decor.muted("·"), p.Name, p.PrivateFile, own)
	}
}

// noRepoFileJSON is render --report --json in a repo with no repo file.
type noRepoFileJSON struct {
	// NoLoreMD says why no LORE.md is written (noRepoFile.line).
	NoLoreMD string         `json:"no_lore_md"`
	Private  *privateReport `json:"private"`
}
