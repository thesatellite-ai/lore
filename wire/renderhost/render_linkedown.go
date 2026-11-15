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
	Pinned   int    `json:"pinned"`    // must rules and critical/high hotfixes (never cut)
	Rows     int    `json:"rows"`      // every row shown in the store files
	LeftOut  int    `json:"left_out"`  // "Other notes" rows the budget left out
	Budget   int    `json:"budget"`    // context.budget.internal
	Bytes    int    `json:"bytes"`     // the store files' size
	Core     int    `json:"core"`      // bytes of the pinned part
	OverCore bool   `json:"over_core"` // the pinned part alone is over the budget
}

// text is the --report section ("" for nil).
func (r *privateReport) text() string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nprivate context (outside the checkout: %s)\n", r.Dir)
	fmt.Fprintf(&b, "  internal stores        %4d\n", r.Stores)
	fmt.Fprintf(&b, "  pinned rules/hotfixes  %4d  full (never cut)\n", r.Pinned)
	fmt.Fprintf(&b, "  rows shown             %4d  %d bytes\n", r.Rows, r.Bytes)
	fmt.Fprintf(&b, "  notes left out         %4d  %s %d bytes\n", r.LeftOut, policy.KeyContextBudgetInternal, r.Budget)
	if r.OverCore {
		fmt.Fprintf(&b, "  the pinned part alone is %d bytes, over %s %d: unpin some or raise the key\n", r.Core, policy.KeyContextBudgetInternal, r.Budget)
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
		size += len(f.body)
	}
	return &privateReport{Dir: h.redact(p.dir), Stores: len(p.stores), Pinned: p.pinned, Rows: p.rows, LeftOut: p.leftOut,
		Budget: p.budget, Bytes: size, Core: p.coreBytes, OverCore: p.budget > 0 && p.coreBytes > p.budget}
}

// linkedOwnUntouched is the top of the repo at root when it holds no .lore
// folder, a store links it by its remote and it is not a client repo; ""
// otherwise. Errors read as "" (the caller's own branch then decides).
func (h *Host) linkedOwnUntouched(ctx context.Context, root string) string {
	if h.app == nil {
		return ""
	}
	top := wire.UntouchedRepo(ctx, root)
	if top == "" || h.app.LinkedByRemote(ctx) == "" {
		return ""
	}
	if client, err := h.app.ClientRepo(ctx); err != nil || client {
		return ""
	}
	return top
}

// noLoreMDLine says why a linked own repo gets no LORE.md.
const noLoreMDLine = "no LORE.md: %s has no .lore folder (a repo linked to the org store keeps its notes there; lore init adds a .lore folder if you want one)"

// renderLinkedOwn is render, --dry-run and --report in a linked own repo
// with no .lore folder (top): the private render only.
func (h *Host) renderLinkedOwn(ctx context.Context, f *renderFlags, top string) error {
	if f.report {
		pr := h.privateReportOf(ctx, top)
		if f.asJSON {
			data, err := json.MarshalIndent(map[string]map[string]*privateReport{"data": {"private": pr}}, "", "  ")
			if err != nil {
				return errs.Wrap(errs.Internal, "encode report", err)
			}
			fmt.Fprintln(f.out(), string(data))
			return nil
		}
		fmt.Fprintf(f.out(), noLoreMDLine+"\n", top)
		if pr == nil {
			fmt.Fprintln(f.out(), "no internal store is attached here, so there is no private context either")
			return nil
		}
		fmt.Fprint(f.out(), strings.TrimPrefix(pr.text(), "\n"))
		return nil
	}
	if !f.dryRun {
		if err := wire.RefuseWriteInReadOnlyHome(h.opts.Home); err != nil {
			return err
		}
	}
	if !f.quiet {
		fmt.Fprintf(f.out(), "%s "+noLoreMDLine+"\n", h.decor.muted("·"), top)
	}
	return h.renderPrivate(ctx, f.out(), top, f.dryRun, f.quiet)
}
