// Package sync defines the repo-sync nouns (layer L5): `lore sync …`,
// `lore land` and `lore explain` as registry operations, so the CLI, the
// local REST API and the reference docs come from one definition (PASS3
// ONE-PATH).
//
//	lore sync [run]                 ─► Env.SyncRun        (stores + repo pass)
//	lore sync status|conflicts|peek|trash [list]          (read the sync state)
//	lore sync resolve|purge|fix-projects|merge-rows|promote|trash restore
//	lore sync install-action|ci-merge                     (pull-request merges)
//	lore land [id] [--all --kind --mission --dry-run] ─► Env.Land
//	lore explain <id>                                  ─► Env.Explain
//
// Only the commands the 0.1.18 design lists are here (PASS3 DESIGN-ONLY):
// CLI_REFERENCE 2.9 (sync, status), 2.15 (sync trash pointer), 3.9 /
// FA-DEL-4 (sync purge pointer), the PROV section (fix-projects), F10 and
// DELETION 599 (merge-rows), LORE_TEAM_SPEC's command table (land, explain,
// conflicts, resolve, peek, promote, install-action, ci-merge). sync dupes,
// export, install-git and uninstall-action are dropped (p3/dropped.md).
//
// The work itself is the repo's git wiring (merge driver, .gitattributes,
// hooks, the live ref): a binary supplies it as the Env (wire.Options.Sync;
// the old binary's v0.1.17 code until it moves into the root module). A
// binary without one answers E_UNSUPPORTED (P3-SYNC-1).
//
// Output: every output keeps v0.1.17's --json shape (the schema_version /
// kind / count / data envelope where the old command had one), and the
// human report is the host's lines (Lines, never in JSON), so scripts and
// people see exactly what v0.1.17 printed.
package sync

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names. CLI words follow op.CLIPath (`_` becomes `-`).
const (
	Noun            = "sync"
	NounLand        = "land"
	NounExplain     = "explain"
	OpRun           = "sync.run"
	OpStatus        = "sync.status"
	OpConflicts     = "sync.conflicts"
	OpResolve       = "sync.resolve"
	OpTrashList     = "sync.trash.list"
	OpTrashRestore  = "sync.trash.restore"
	OpPurge         = "sync.purge"
	OpFixProjects   = "sync.fix_projects"
	OpPeek          = "sync.peek"
	OpPromote       = "sync.promote"
	OpMergeRows     = "sync.merge_rows"
	OpCIMerge       = "sync.ci_merge"
	OpInstallAction = "sync.install_action"
	OpLand          = "land.run"
	OpExplain       = "explain.run"

	// Verbs the bare nouns run (`lore sync`, `lore land`, `lore explain`,
	// `lore sync trash`): the binaries' shortcuts use them.
	VerbRun   = "run"
	VerbList  = "list"
	NounTrash = "trash"
)

// SchemaVersion is the v0.1.17 JSON envelope version every enveloped
// output carries.
const SchemaVersion = 1

// JSON envelope kinds (v0.1.17 json_helper.go contract, kept as is).
const (
	KindReport    = "sync.report"
	KindStatus    = "sync.status"
	KindConflicts = "sync.conflicts"
	KindTrash     = "sync.trash"
	KindPeek      = "sync.peek"
	KindCIMerge   = "sync.ci-merge"
	KindAction    = "sync.action"
)

// Env is what the sync operations need: the binary's repo-sync host.
type Env interface {
	SyncRun(ctx context.Context, in RunIn) (RunOut, error)
	SyncStatus(ctx context.Context, in StatusIn) (StatusOut, error)
	SyncConflicts(ctx context.Context, in ConflictsIn) (ConflictsOut, error)
	SyncResolve(ctx context.Context, in ResolveIn) (DoneOut, error)
	SyncTrashList(ctx context.Context, in TrashListIn) (TrashListOut, error)
	SyncTrashRestore(ctx context.Context, in TrashRestoreIn) (DoneOut, error)
	SyncPurge(ctx context.Context, in PurgeIn) (DoneOut, error)
	SyncFixProjects(ctx context.Context, in FixProjectsIn) (DoneOut, error)
	SyncPeek(ctx context.Context, in PeekIn) (PeekOut, error)
	SyncPromote(ctx context.Context, in PromoteIn) (DoneOut, error)
	SyncMergeRows(ctx context.Context, in MergeRowsIn) (DoneOut, error)
	SyncCIMerge(ctx context.Context, in CIMergeIn) (CIMergeOut, error)
	SyncInstallAction(ctx context.Context, in InstallActionIn) (ActionOut, error)
	Land(ctx context.Context, in LandIn) (DoneOut, error)
	Explain(ctx context.Context, in ExplainIn) (ExplainOut, error)
}

// CoreStores is an optional upgrade of Env: a host that answers true
// leaves the attached stores' exchange and the queued run time (TIME 11.9)
// of `lore sync` to the core, which runs them after the host's own pass
// (cmd/lore's live-only host; the old binary's host does both itself and
// does not implement it).
type CoreStores interface {
	CoreStores() bool
}

// Ops returns the sync, land and explain operations bound to env. A nil
// env (a binary without a repo-sync host) registers the same operations,
// so the reference and REST routes stay whole, and each answers
// E_UNSUPPORTED (P3-SYNC-1).
func Ops(env Env) []op.Entry {
	o := ops{env: env}
	read := []errs.Code{errs.InvalidInput, errs.NotFound, errs.Unsupported, errs.Internal}
	write := append(append([]errs.Code{}, read...), errs.ReadOnly, errs.Stale, errs.NotAStore)
	r := func(name, summary string, ex ...op.Example) op.Spec {
		return op.Spec{Name: name, Summary: summary, Permission: op.PermRead, Access: op.AccessRead, Errors: read, Examples: ex}
	}
	w := func(name, summary string, ex ...op.Example) op.Spec {
		return op.Spec{Name: name, Summary: summary, Permission: op.PermWrite, Access: op.AccessWrite, Errors: write, Examples: ex}
	}
	// sync.run is read access: v0.1.17 ran `lore sync` under LORE_READ_ONLY
	// too (the pass then refreshes the cache and writes no files).
	return []op.Entry{
		op.Define(mayAsk(liveRef(r(OpRun, "Sync now: exchange every store, reconcile .lore/data, and print what changed", op.Example{Title: "sync now", CLI: "lore sync run"}))), run(o, Env.SyncRun)),
		op.Define(r(OpStatus, "Show sync state: baseline, pending exports, conflicts, broken files, live rows", op.Example{Title: "where sync stands", CLI: "lore sync status"}), run(o, Env.SyncStatus)),
		op.Define(r(OpConflicts, "List versions kept aside when both sides edited the same row", op.Example{Title: "the versions kept aside", CLI: "lore sync conflicts"}), run(o, Env.SyncConflicts)),
		op.Define(w(OpResolve, "Close a conflict, keeping the current version or the saved other one", op.Example{Title: "keep the other side of conflict 1", CLI: "lore sync resolve --id 1 --take other"}), run(o, Env.SyncResolve)),
		op.Define(r(OpTrashList, "Moved: lore trash --local (this spelling goes away in the next release)", op.Example{Title: "moved: list the local trash", CLI: "lore trash list --local"}), run(o, Env.SyncTrashList)),
		op.Define(w(OpTrashRestore, "Moved: lore trash restore --local --target <n>", op.Example{Title: "moved: restore local trash entry 1", CLI: "lore trash restore --local --target 1"}), run(o, Env.SyncTrashRestore)),
		op.Define(confirmed(w(OpPurge, "Durably remove rows archived before a cutoff (moved: lore trash purge --archived-before)", op.Example{Title: "moved: purge rows archived before a date", CLI: "lore trash purge --archived-before 2026-01-01 --confirm"})), run(o, Env.SyncPurge)),
		op.Define(w(OpFixProjects, "Collapse several project rows into one (two parallel bootstraps met in a merge)", op.Example{Title: "see which project rows would be collapsed", CLI: "lore sync fix-projects --dry-run"}), run(o, Env.SyncFixProjects)),
		op.Define(r(OpPeek, "List the lore rows another branch carries, without checking it out", op.Example{Title: "the tasks another branch carries", CLI: "lore sync peek --ref origin/feature-invite --table tasks"}), run(o, Env.SyncPeek)),
		op.Define(w(OpPromote, "Commit rows onto another local branch (e.g. an urgent rule onto main)", op.Example{Title: "put an urgent rule onto main", CLI: "lore sync promote --ids rul_1f3a --to main"}), run(o, Env.SyncPromote)),
		op.Define(w(OpMergeRows, "Fold a duplicate row into another (references move, the dropped row is trashed)", op.Example{Title: "fold a duplicate memory into another", CLI: "lore sync merge-rows --table memories --keep mem_0c3a --drop mem_0c3b"}), run(o, Env.SyncMergeRows)),
		op.Define(w(OpCIMerge, "Merge a base branch into this branch when only lore data conflicts (for CI)", op.Example{Title: "in CI: merge main when only lore data conflicts", CLI: "lore sync ci-merge --base main"}), run(o, Env.SyncCIMerge)),
		op.Define(w(OpInstallAction, "Add a GitHub workflow that merges lore data in pull requests (opt-in)", op.Example{Title: "preview the workflow file", CLI: "lore sync install-action --dry-run"}), run(o, Env.SyncInstallAction)),
		op.Define(liveRef(w(OpLand, "Write live rows to main now (finished rows land; open rows are copied and stay live)", op.Example{Title: "see what would land", CLI: "lore land run --dry-run"}, op.Example{Title: "land one finished task", CLI: "lore land run --id tsk_31"})), run(o, Env.Land)),
		op.Define(explainSpec(r(OpExplain, "Show every copy of a row (live, this branch, the default branch), which one wins and why, and its land decision")), run(o, Env.Explain)),
	}
}

// confirmed adds E_NEEDS_CONFIRM to a durable verb's errors: sync purge
// answers through `lore trash purge`, which refuses without --confirm.
func confirmed(s op.Spec) op.Spec {
	s.Errors = append(append([]errs.Code{}, s.Errors...), errs.NeedsConfirm)
	return s
}

// mayAsk marks a verb whose run may ask a yes/no question of its own
// (op.ConfirmRule.MayAsk), so every surface offers the yes answer
// (--yes, REST "yes"): `lore sync` asks before writing the org store
// links of a moved repo (issue #23, wire followMovedLinks), and --yes
// answers it on a run without a terminal or a person to type y.
func mayAsk(s op.Spec) op.Spec {
	s.Confirm.MayAsk = true
	return s
}

// liveRef adds E_READ_ONLY to a verb that pushes the live ref: it
// refuses while the remote live ref was deleted or rewritten (G-SYNC-3,
// `lore live restore` first).
func liveRef(s op.Spec) op.Spec {
	if slices.Contains(s.Errors, errs.ReadOnly) {
		return s
	}
	s.Errors = append(append([]errs.Code{}, s.Errors...), errs.ReadOnly)
	return s
}

// ops binds the run functions to one Env (nil: no repo-sync host).
type ops struct{ env Env }

// input is every input type of this package (a closed set, so the run
// adapter below needs no open type parameter).
type input interface {
	RunIn | StatusIn | ConflictsIn | ResolveIn | TrashListIn | TrashRestoreIn | PurgeIn | FixProjectsIn |
		PeekIn | PromoteIn | MergeRowsIn | CIMergeIn | InstallActionIn | LandIn | ExplainIn
}

// run adapts one Env method to an op run function: E_UNSUPPORTED when the
// binary supplied no host, else the method's own result.
func run[In input, Out op.Texter](o ops, m func(Env, context.Context, In) (Out, error)) func(context.Context, In) (Out, error) {
	return func(ctx context.Context, in In) (Out, error) {
		if o.env == nil {
			var zero Out
			return zero, errs.New(errs.Unsupported, "repo sync is not available in this binary").
				WithHint("run the release lore binary in a git repository")
		}
		return m(o.env, ctx, in)
	}
}

// text joins human lines, one per line (the op.Texter of every output).
func text(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
