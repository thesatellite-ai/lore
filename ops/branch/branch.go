// Package branch defines `lore branch …` (layer L5): the branch layers of
// BRANCH_LAYERS_SPEC 13, notes a code branch keeps to itself in a store
// until it lands, as registry operations so the CLI, `lore api` (REST
// /v1/branch/…) and the reference come from one definition.
//
//	lore branch list|show|mode            ─► Env (read)
//	lore branch land|drop|restore|purge|  ─► Env (write; these five push
//	            rename                         to the store's remote at once:
//	                                           they are explicit remote acts,
//	                                           and every help line and output
//	                                           says so, devco D-614)
//	lore branch promote|isolate|share     ─► Env (write; nothing pushed)
//
// The work (refs in the store clone, the policy rows) is wire's: this
// package names what is asked and prints the answer. Named flags only.
package branch

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names (`lore branch <verb>`).
const (
	Noun       = "branch"
	OpList     = "branch.list"
	OpShow     = "branch.show"
	OpMode     = "branch.mode"
	OpLand     = "branch.land"
	OpDrop     = "branch.drop"
	OpRestore  = "branch.restore"
	OpPurge    = "branch.purge"
	OpPromote  = "branch.promote"
	OpIsolate  = "branch.isolate"
	OpShare    = "branch.share"
	OpRename   = "branch.rename"
	OpAdopt    = "branch.adopt"
	OpInstall  = "branch.install_action"
	restNounBr = "branch"
)

// States of a layer in a listing.
const (
	StateOpen     = "open"
	StateMerged   = "merged"
	StateOrphaned = "orphaned"
	StateLanded   = "landed"
	StateDropped  = "dropped"
)

// Env is what the branch operations need; *wire.App implements it.
type Env interface {
	BranchList(ctx context.Context, in ListIn) (ListOut, error)
	BranchShow(ctx context.Context, in ShowIn) (ShowOut, error)
	BranchMode(ctx context.Context, in ModeIn) (ModeOut, error)
	BranchLand(ctx context.Context, in LandIn) (LandOut, error)
	BranchDrop(ctx context.Context, in DropIn) (DoneOut, error)
	BranchRestore(ctx context.Context, in RestoreIn) (DoneOut, error)
	BranchPurge(ctx context.Context, in PurgeIn) (PurgeOut, error)
	BranchPromote(ctx context.Context, in PromoteIn) (DoneOut, error)
	BranchChoose(ctx context.Context, in ChoiceIn, isolate bool) (DoneOut, error)
	BranchRename(ctx context.Context, in RenameIn) (DoneOut, error)
	BranchAdopt(ctx context.Context, in AdoptIn) (DoneOut, error)
	BranchInstallAction(ctx context.Context, in InstallActionIn) (DoneOut, error)
}

// RenameIn is the input of branch.rename.
type RenameIn struct {
	From string `json:"from" flag:"from" desc:"the old code branch name whose notes move (required)"`
	To   string `json:"to_branch" flag:"to" desc:"the new code branch name (required)"`
}

// ListIn is the input of branch.list.
type ListIn struct {
	// --limit / --page: the shared paging flags of every list (op.PageFlags).
	op.PageFlags
	All   bool `json:"all,omitempty" flag:"all" desc:"every code repo linked to the project folder, not only this one"`
	Trash bool `json:"trash,omitempty" flag:"trash" desc:"also list dropped and landed notes kept in the trash"`
}

// Layer is one layer in a listing.
type Layer struct {
	Branch  string  `json:"branch"`
	Repo    string  `json:"repo"`
	Folder  string  `json:"folder"`
	State   string  `json:"state"`
	Rows    []Count `json:"rows"`
	Started string  `json:"started,omitempty"`
	Age     string  `json:"age,omitempty"`
	Tip     string  `json:"tip"`
	Trash   string  `json:"trash,omitempty"`
	// Why explains merged and orphaned ("its last code head is in main").
	Why string `json:"why,omitempty"`
}

// Count is how many rows of one table a layer holds.
type Count struct {
	Table string `json:"table"`
	Count int    `json:"count"`
}

// Counts turns per-table counts into a sorted list.
func Counts(m map[string]int) []Count {
	out := make([]Count, 0, len(m))
	for t, n := range m {
		out = append(out, Count{Table: t, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Table < out[j].Table })
	return out
}

// ListOut is the output of branch.list.
type ListOut struct {
	Store  string  `json:"store"`
	Folder string  `json:"folder"`
	Layers []Layer `json:"layers" page:"rows"`
	Count  int     `json:"count"`
	// Lines are next steps for merged and orphaned layers (text only).
	Lines []string `json:"-"`
}

// Text prints one line per layer, then the next steps.
func (o ListOut) Text() string {
	if len(o.Layers) == 0 {
		if o.Folder == "" && len(o.Lines) > 0 {
			return strings.Join(o.Lines, "\n") + "\n" // no store folder here (devco:D-388-branchlist)
		}
		return "no branch notes in " + o.Folder + " (store " + o.Store + ")\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-28s %-28s %-34s %-5s %s\n", "BRANCH", "REPO", "ROWS", "AGE", "STATE")
	for _, l := range o.Layers {
		fmt.Fprintf(&b, "%-28s %-28s %-34s %-5s %s\n", l.Branch, l.Repo, RowsText(l.Rows), l.Age, l.State)
	}
	for _, line := range o.Lines {
		b.WriteString(line + "\n")
	}
	return b.String()
}

// RowsText is "2 tasks, 1 memory" (sorted by table).
func RowsText(rows []Count) string {
	if len(rows) == 0 {
		return "no rows"
	}
	parts := make([]string, 0, len(rows))
	for _, c := range rows {
		parts = append(parts, fmt.Sprintf("%d %s", c.Count, kindWord(c.Table, c.Count)))
	}
	return strings.Join(parts, ", ")
}

// kindWord is a table name as a word: singular for one row.
func kindWord(table string, n int) string {
	table = strings.ReplaceAll(table, "_", " ")
	if n != 1 {
		return table
	}
	switch {
	case strings.HasSuffix(table, "ies"):
		return strings.TrimSuffix(table, "ies") + "y"
	case strings.HasSuffix(table, "ses"):
		return strings.TrimSuffix(table, "es")
	case strings.HasSuffix(table, "s"):
		return strings.TrimSuffix(table, "s")
	}
	return table
}

// ShowIn is the input of branch.show.
type ShowIn struct {
	Branch string `json:"branch,omitempty" flag:"branch" desc:"the code branch (default: the one checked out)"`
}

// ShowRow is one row of a layer.
type ShowRow struct {
	Table   string `json:"table"`
	ID      string `json:"id"`
	Title   string `json:"title,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
}

// ShowOut is the output of branch.show.
type ShowOut struct {
	Layer Layer     `json:"layer"`
	Base  string    `json:"base"`
	Split string    `json:"split,omitempty"`
	Head  string    `json:"code_head,omitempty"`
	By    string    `json:"started_by,omitempty"`
	Rows  []ShowRow `json:"rows"`
	// OnMain counts the rows of the folder's shared notes that were
	// created on the branch (their `_o.git.branch`): landed or adopted
	// (D-392). It is filled also when the branch has no open notes any
	// more, so a landed branch still answers.
	OnMain []Count `json:"on_main"`
	// Hint names the list and trash commands for the OnMain rows.
	Hint string `json:"hint,omitempty"`
}

// OnMainHint is the hint under the OnMain count: the commands that list
// and trash the rows a branch left on the shared notes (named flags).
func OnMainHint(branch string) string {
	return "list them: lore <kind> list --born-branch " + branch + "; trash them: lore trash add --born-branch " + branch + " --dry-run"
}

// Text prints the layer's facts, then its rows by table, then the rows
// of the shared notes born on the branch.
func (o ShowOut) Text() string {
	var b strings.Builder
	if o.Layer.Tip == "" {
		// no open notes: only what the branch left on the shared notes
		fmt.Fprintf(&b, "branch     %s (folder %s): no open notes\n", o.Layer.Branch, o.Layer.Folder)
		fmt.Fprintf(&b, "on main    %s created on %s (landed or adopted)\n", RowsText(o.OnMain), o.Layer.Branch)
		if o.Hint != "" {
			fmt.Fprintf(&b, "  %s\n", o.Hint)
		}
		return b.String()
	}
	fmt.Fprintf(&b, "branch     %s (repo %s, folder %s)\n", o.Layer.Branch, o.Layer.Repo, o.Layer.Folder)
	fmt.Fprintf(&b, "state      %s\n", o.Layer.State)
	fmt.Fprintf(&b, "rows       %s\n", RowsText(o.Layer.Rows))
	fmt.Fprintf(&b, "started    %s by %s\n", o.Layer.Started, orDash(o.By))
	fmt.Fprintf(&b, "base       %s (store commit)   split %s   code head %s\n", short(o.Base), orDash(short(o.Split)), orDash(short(o.Head)))
	for _, r := range o.Rows {
		mark := ""
		if r.Deleted {
			mark = " (deleted)"
		}
		fmt.Fprintf(&b, "  %-12s %s  %s%s\n", r.Table, r.ID, r.Title, mark)
	}
	if len(o.OnMain) > 0 {
		fmt.Fprintf(&b, "on main    %s created on %s (landed or adopted)\n  %s\n", RowsText(o.OnMain), o.Layer.Branch, o.Hint)
	}
	return b.String()
}

// orDash is s, or "-" when empty.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// short is a commit's first 7 characters.
func short(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// ModeIn is the input of branch.mode.
type ModeIn struct {
	Branch string `json:"branch,omitempty" flag:"branch" desc:"a code branch name (default: the one checked out)"`
}

// ModeOut is the output of branch.mode.
type ModeOut struct {
	Branch string `json:"branch"`
	Repo   string `json:"repo"`
	Folder string `json:"folder"`
	Store  string `json:"store"`
	Mode   string `json:"mode"`
	Step   int    `json:"step"`
	Why    string `json:"why"`
}

// Text prints the three lines of BRANCH_LAYERS 5.
func (o ModeOut) Text() string {
	return fmt.Sprintf("branch     %s (repo %s, project folder %s in store %s)\nmode       %s\ndecided by %s\n", o.Branch, o.Repo, o.Folder, o.Store, o.Mode, o.Why)
}

// LandIn is the input of branch.land. Store, Folder and Repo name the
// layer without a config (the CI step, BRANCH_LAYERS 17).
type LandIn struct {
	Branch string `json:"branch" flag:"branch" desc:"the code branch whose notes land (required)"`
	PR     int    `json:"pr,omitempty" flag:"pr" min:"1" desc:"the pull request number, named in the landing commit"`
	DryRun bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"list what would land; change and push nothing"`
	Store  string `json:"store,omitempty" flag:"store" desc:"the store's url, for CI where no config links the repo (with --folder and --repo)"`
	Folder string `json:"folder,omitempty" flag:"folder" desc:"the project folder in the store (with --store)"`
	Repo   string `json:"repo,omitempty" flag:"repo" desc:"the code repo's address, e.g. github.com/acme/app (with --store)"`
	// Kinds lands only these tables; the other rows stay open.
	Kinds []string `json:"kinds,omitempty" flag:"kinds" desc:"land only rows of these tables (tasks,memories); the rest stay with the branch"`
	// Review asks keep or drop for every row (a terminal only).
	Review bool `json:"review,omitempty" flag:"review" desc:"ask keep or drop for every row first (a terminal only); dropped rows go to the trash with the branch's notes"`
	// Take settles rows both sides changed the same text of.
	Take string   `json:"take,omitempty" flag:"take" desc:"branch or main: the side kept for the rows --ids names, when both changed the same text"`
	IDs  []string `json:"ids,omitempty" flag:"ids" desc:"the rows --take settles"`
	// Into lands a stacked branch's notes into its parent branch's notes.
	Into string `json:"into,omitempty" flag:"into" desc:"land into the notes of this branch (a stacked branch merged into its parent) instead of the shared notes"`
}

// LandOut is the output of branch.land.
type LandOut struct {
	Branch        string  `json:"branch"`
	Folder        string  `json:"folder"`
	Rows          []Count `json:"rows"`
	Commit        string  `json:"commit,omitempty"`
	AlreadyLanded string  `json:"already_landed,omitempty"`
	Trash         string  `json:"trash,omitempty"`
	Pushed        bool    `json:"pushed"`
	DryRun        bool    `json:"dry_run,omitempty"`
	StoreBranch   string  `json:"store_branch"`
	Merged        int     `json:"merged,omitempty"`
	// Nothing is true when the branch has no notes to land (the CI step
	// for a branch that wrote none; exit 0).
	Nothing bool `json:"nothing,omitempty"`
	// Kept are rows --kinds left open (rebased onto the landing); Dropped
	// the rows --review left out (gone to the trash with the notes).
	Kept    []Count `json:"kept,omitempty"`
	Dropped []Count `json:"dropped,omitempty"`
	// Rebased are stacked branches whose notes moved onto the landing.
	Rebased []string `json:"rebased,omitempty"`
	// Into is the parent branch a stacked branch landed into.
	Into string `json:"into,omitempty"`
}

// Text prints the result line.
func (o LandOut) Text() string {
	switch {
	case o.Nothing:
		return fmt.Sprintf("no notes of %s in %s: nothing to land\n", o.Branch, o.Folder)
	case o.AlreadyLanded != "":
		return fmt.Sprintf("already landed: the notes of %s are in %s as commit %s; nothing to do\n", o.Branch, o.Folder, short(o.AlreadyLanded))
	case o.DryRun:
		s := fmt.Sprintf("would land %s of %s into %s on %s", RowsText(o.Rows), o.Branch, o.Folder, o.StoreBranch)
		if len(o.Kept) > 0 {
			s += fmt.Sprintf("; %s would stay with the branch", RowsText(o.Kept))
		}
		return s + " (nothing changed)\n"
	}
	if o.Into != "" {
		return fmt.Sprintf("✓ landed %s of %s into the notes of %s (%s); %s's notes are in the trash\n", RowsText(o.Rows), o.Branch, o.Into, PushedText(o.Pushed), o.Branch)
	}
	s := fmt.Sprintf("✓ landed %s of %s into %s: commit %s on %s (%s)", RowsText(o.Rows), o.Branch, o.Folder, short(o.Commit), o.StoreBranch, PushedText(o.Pushed))
	switch {
	case len(o.Kept) > 0:
		s += fmt.Sprintf("; %s stay with %s (lore branch land --branch %s lands them later)", RowsText(o.Kept), o.Branch, o.Branch)
	default:
		s += fmt.Sprintf("; the notes are in the trash (lore branch restore --branch %s)", o.Branch)
	}
	if len(o.Dropped) > 0 {
		s += fmt.Sprintf("; %s dropped (kept only in the trash copy)", RowsText(o.Dropped))
	}
	if len(o.Rebased) > 0 {
		s += "; the notes of " + strings.Join(o.Rebased, ", ") + " (stacked on it) now sit on the shared notes"
	}
	if o.Merged > 0 {
		s += fmt.Sprintf("; %d row(s) changed on both sides were merged field by field", o.Merged)
	}
	return s + "\n"
}

// PushedText is how a branch verb's output says where its change went:
// these verbs push at once, and an output never leaves the reader to guess
// whether a remote moved (devco D-614).
func PushedText(pushed bool) string {
	if pushed {
		return "pushed to the store's remote"
	}
	return "not pushed; lore sync sends it"
}

// DropIn is the input of branch.drop.
type DropIn struct {
	Branch string `json:"branch" flag:"branch" desc:"the code branch whose notes go to the trash (required)"`
	DryRun bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"show what would be dropped; change nothing"`
}

// RestoreIn is the input of branch.restore.
type RestoreIn struct {
	Branch string `json:"branch" flag:"branch" desc:"the code branch whose notes come back from the trash (required)"`
	At     string `json:"at,omitempty" flag:"at" desc:"the trash stamp to restore (20261120T091200Z; default: the newest)"`
	Revert bool   `json:"revert,omitempty" flag:"revert" desc:"also add a commit that reverts the landing of these notes on the store branch (pushed)"`
}

// AdoptIn is the input of branch.adopt.
type AdoptIn struct {
	Branch string `json:"branch" flag:"branch" desc:"move the shared rows born on this code branch into its notes (required)"`
	DryRun bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"list the rows; change nothing"`
}

// InstallActionIn is the input of branch.install_action.
type InstallActionIn struct {
	DryRun bool `json:"dry_run,omitempty" flag:"dry-run" desc:"print the workflow; write nothing"`
	Force  bool `json:"force,omitempty" flag:"force" desc:"overwrite a workflow file lore did not write"`
}

// PurgeIn is the input of branch.purge.
type PurgeIn struct {
	Branch    string `json:"branch,omitempty" flag:"branch" desc:"purge every trashed copy of this branch's notes"`
	OlderThan string `json:"older_than,omitempty" flag:"older-than" desc:"purge every trashed copy older than this (90d, 720h)"`
	Confirm   bool   `json:"confirm,omitempty" flag:"confirm" desc:"required: purged notes cannot come back"`
}

// PurgeOut is the output of branch.purge.
type PurgeOut struct {
	Purged []string `json:"purged"`
}

// Text prints what went.
func (o PurgeOut) Text() string {
	if len(o.Purged) == 0 {
		return "nothing to purge\n"
	}
	return fmt.Sprintf("✓ purged %d trashed copy(ies) of branch notes for good (%s):\n  %s\n", len(o.Purged), PushedText(true), strings.Join(o.Purged, "\n  "))
}

// PromoteIn is the input of branch.promote.
type PromoteIn struct {
	IDs []string `json:"ids" flag:"ids" desc:"row ids of this branch's notes to put in the shared notes now (comma separated or repeated)"`
}

// ChoiceIn is the input of branch.isolate and branch.share.
type ChoiceIn struct {
	Branch string `json:"branch" flag:"branch" desc:"the code branch (required)"`
	Undo   bool   `json:"undo,omitempty" flag:"undo" desc:"remove this choice again"`
}

// DoneOut is the output of the verbs that print one line.
type DoneOut struct {
	OK    bool     `json:"ok"`
	Lines []string `json:"lines"`
}

// Text prints the lines.
func (o DoneOut) Text() string {
	if len(o.Lines) == 0 {
		return ""
	}
	return strings.Join(o.Lines, "\n") + "\n"
}

// local marks branch.land Local (op.Spec.Local): the CI form lands with
// no repository and no config (--store --folder --repo), so the runner
// opens nothing first; the config form opens the repo itself.
func local(s op.Spec) op.Spec {
	s.Local = true
	return s
}

// mayAsk marks a verb that may ask yes or no first (op.ConfirmRule.MayAsk),
// so every surface offers --yes.
func mayAsk(s op.Spec) op.Spec {
	s.Confirm.MayAsk = true
	return s
}

// Ops returns the branch operations bound to env.
func Ops(env Env) []op.Entry {
	read := []errs.Code{errs.InvalidInput, errs.NotFound, errs.Unsupported, errs.Internal, errs.ConfigInvalid}
	write := append(append([]errs.Code{}, read...), errs.ReadOnly, errs.Stale, errs.Offline, errs.Contended, errs.Refused, errs.StoreLocked, errs.StoreReadOnly)
	r := func(name, summary string, ex ...op.Example) op.Spec {
		return op.Spec{Name: name, Summary: summary, Permission: op.PermRead, Access: op.AccessRead, Errors: read, Examples: ex, RESTNoun: restNounBr}
	}
	w := func(name, summary string, extra []errs.Code, ex ...op.Example) op.Spec {
		return op.Spec{Name: name, Summary: summary, Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(append([]errs.Code{}, write...), extra...), Examples: ex, RESTNoun: restNounBr}
	}
	return []op.Entry{
		op.Define(r(OpList, "List the notes code branches keep to themselves in this project's store folder (branch layers)",
			op.Example{Title: "every branch's own notes", CLI: "lore branch list"}), func(ctx context.Context, in ListIn) (ListOut, error) { return env.BranchList(ctx, in) }),
		op.Define(r(OpShow, "Show one code branch's own notes: its rows, where it started, who started it",
			op.Example{Title: "the notes of a branch", CLI: "lore branch show --branch feat/experiment"}), func(ctx context.Context, in ShowIn) (ShowOut, error) { return env.BranchShow(ctx, in) }),
		op.Define(r(OpMode, "Say whether a code branch keeps its own notes (isolated) or writes to the shared notes, and which rule decided",
			op.Example{Title: "this branch's mode", CLI: "lore branch mode"}), func(ctx context.Context, in ModeIn) (ModeOut, error) { return env.BranchMode(ctx, in) }),
		op.Define(local(w(OpLand, "Squash a code branch's own notes into the shared notes as one commit and push it to the store's remote (after the branch merged)",
			[]errs.Code{errs.LandConflict, errs.NotAStore, errs.NeedsConfirm},
			op.Example{Title: "land a merged branch's notes", CLI: "lore branch land --branch feat/experiment --pr 123"},
			op.Example{Title: "see what would land", CLI: "lore branch land --branch feat/experiment --dry-run"},
			op.Example{Title: "land only the tasks and memories", CLI: "lore branch land --branch feat/experiment --kinds tasks,memories"},
			op.Example{Title: "land a stacked branch into its parent", CLI: "lore branch land --branch feat/b --into feat/a"})), func(ctx context.Context, in LandIn) (LandOut, error) { return env.BranchLand(ctx, in) }),
		op.Define(w(OpDrop, "Move a code branch's own notes to the trash without landing them; pushes to the store's remote at once",
			nil, op.Example{Title: "drop the notes of an abandoned branch", CLI: "lore branch drop --branch fix-login"}), func(ctx context.Context, in DropIn) (DoneOut, error) { return env.BranchDrop(ctx, in) }),
		op.Define(w(OpRestore, "Bring a code branch's dropped or landed notes back from the trash; pushes to the store's remote at once",
			[]errs.Code{errs.NameTaken, errs.LandConflict}, op.Example{Title: "bring dropped notes back", CLI: "lore branch restore --branch fix-login"}), func(ctx context.Context, in RestoreIn) (DoneOut, error) { return env.BranchRestore(ctx, in) }),
		op.Define(w(OpPurge, "Delete trashed branch notes for good, on the store's remote too (pushes at once)",
			[]errs.Code{errs.NeedsConfirm}, op.Example{Title: "purge trashed notes older than 90 days", CLI: "lore branch purge --older-than 90d --confirm"}), func(ctx context.Context, in PurgeIn) (PurgeOut, error) { return env.BranchPurge(ctx, in) }),
		op.Define(w(OpPromote, "Put rows of this branch's own notes into the shared notes now (an urgent rule)",
			[]errs.Code{errs.LandConflict}, op.Example{Title: "share one rule now", CLI: "lore branch promote --ids rul_0193f2a7c4b77e1d8a3f5b6c7d8e9f00"}), func(ctx context.Context, in PromoteIn) (DoneOut, error) { return env.BranchPromote(ctx, in) }),
		op.Define(w(OpIsolate, "Make a code branch keep its own notes, for everyone in this project folder",
			[]errs.Code{errs.PolicyDenied}, op.Example{Title: "isolate a branch a pattern shares", CLI: "lore branch isolate --branch dev-pat"}), func(ctx context.Context, in ChoiceIn) (DoneOut, error) { return env.BranchChoose(ctx, in, true) }),
		op.Define(mayAsk(w(OpAdopt, "Move the shared rows born on a code branch (before branch layers) into that branch's own notes",
			nil, op.Example{Title: "move old feature-branch notes into its layer", CLI: "lore branch adopt --branch feat/x --dry-run"})), func(ctx context.Context, in AdoptIn) (DoneOut, error) { return env.BranchAdopt(ctx, in) }),
		op.Define(mayAsk(w(OpInstall, "Add the GitHub workflow that lands a branch's notes when its pull request merges (asks first)",
			[]errs.Code{errs.NeedsConfirm, errs.LeakBlocked}, op.Example{Title: "preview the workflow", CLI: "lore branch install-action --dry-run"})), func(ctx context.Context, in InstallActionIn) (DoneOut, error) {
			return env.BranchInstallAction(ctx, in)
		}),
		op.Define(w(OpRename, "Move a code branch's notes to its new name after git branch -m; pushes to the store's remote at once",
			[]errs.Code{errs.NameTaken}, op.Example{Title: "follow a renamed branch", CLI: "lore branch rename --from feat/x --to feat/y"}), func(ctx context.Context, in RenameIn) (DoneOut, error) { return env.BranchRename(ctx, in) }),
		op.Define(w(OpShare, "Make a code branch write to the shared notes, for everyone in this project folder",
			[]errs.Code{errs.PolicyDenied}, op.Example{Title: "share a long-lived branch", CLI: "lore branch share --branch integration"}), func(ctx context.Context, in ChoiceIn) (DoneOut, error) { return env.BranchChoose(ctx, in, false) }),
	}
}
