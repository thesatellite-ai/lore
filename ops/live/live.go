// Package live defines `lore live show|diff|log|restore|compact|purge`
// (layer L5): inspect and repair the live ref, refs/lore/live, where live
// rows are shared with everyone at once, one folder per lore project
// (LORE_TEAM_SPEC §1 "Commands", §6.6, §9.4, F8).
//
// Flow of every operation:
//
//	op ─▶ Env.LiveRef (the live ref of the cwd project; E_NOT_PROJECT_ROOT
//	      outside a project, E_UNSUPPORTED when live rows do not apply:
//	      not a git repo, LORE_LIVE=0, or a binary with no live ref wired)
//	   ─▶ one Ref method ─▶ typed Out ─▶ Text() prints v0.1.17's lines
//
// Why a port: the live ref machinery (git plumbing, merge, push, landing,
// the per-command session that fetches first and pushes last) is a host
// concern, not a row write through the engine. The ops only name what is
// asked and print the answer, so the CLI, `lore api` and the docs come
// from one definition (PASS3 ONE-PATH) while the host that owns the live
// session implements Ref.
package live

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names (`lore live <verb>`, REST /v1/live/…).
const (
	Noun      = "live"
	OpShow    = "live.show"
	OpDiff    = "live.diff"
	OpLog     = "live.log"
	OpRestore = "live.restore"
	OpCompact = "live.compact"
	OpPurge   = "live.purge"
)

// restNoun keeps the REST collection "live" (the default plural would be
// "lives").
const restNoun = "live"

// DefaultLogLimit is how many commits `lore live log` prints when
// --number is unset (v0.1.17 liveLogLimit).
const DefaultLogLimit = 20

// Row states of `lore live show` (v0.1.17 spelling).
const (
	StateLive      = "live"
	StateTombstone = "tombstone"
	StateConflict  = "CONFLICT"
)

// sinceLayout is the "since" column of `lore live show`.
const sinceLayout = "2006-01-02 15:04"

// sinceJSONLayout is "since" in --json and REST: UTC RFC 3339 with
// milliseconds, like every row time (the text keeps sinceLayout).
const sinceJSONLayout = "2006-01-02T15:04:05.000Z07:00"

// noRef is what `lore live diff` prints for a ref that does not exist.
const noRef = "(none)"

// Row is one live row of the project's folder.
type Row struct {
	Table string
	ID    string
	// State is StateLive, StateTombstone or StateConflict.
	State string
	// Since is when the row went live.
	Since time.Time
}

// Listing is the project's live folder as the local live ref holds it.
type Listing struct {
	Folder string
	Epoch  int
	// Rows are sorted by "table/id".
	Rows []Row
}

// Refs is `lore live diff`: the three live ref tips ("" = none) and the
// paths of the writes the remote has not confirmed yet.
type Refs struct {
	Local, Tracking, Seen string
	Pending               []string
}

// History is `lore live log`: the epoch and git's log lines (one commit per
// line, "%h %ad %an  %s", newest first, ending with a newline).
type History struct {
	Epoch int
	Lines string
}

// Restored is the result of a restore: rows pushed back and rows left out
// (already landed on the default branch, or purged).
type Restored struct {
	Rows, LeftOut int
}

// CompactPlan is what a compaction keeps and changes.
type CompactPlan struct {
	// Ref is the live ref's name (refs/lore/live).
	Ref string
	// Commits is the ref's commit count now; Threshold live.compact_after.
	Commits, Threshold int
	// Files is how many files the one new commit keeps.
	Files              int
	FromEpoch, ToEpoch int
	// Purged counts purged rows the compaction drops.
	Purged int
}

// Compacted is the result of a compaction. Nothing is true when there was
// no live ref to compact; Backup is the local backup ref of the old history.
type Compacted struct {
	Plan    CompactPlan
	Nothing bool
	Backup  string
}

// Ref is the live ref of the project the command runs in. The host that
// owns the live session implements it (the release binary's live wiring).
//
// Invariants: reads never write the ref; Restore, Compact and Purge refuse
// with E_READ_ONLY while the remote live ref was deleted or rewritten
// (Restore is the repair and runs anyway), answer E_OFFLINE when the
// remote did not confirm (nothing is lost: the local copy is kept), and
// leave the end-of-command push to the host's session.
type Ref interface {
	// List is the project's live folder.
	List(ctx context.Context) (Listing, error)
	// File is one live row file as stored (E_NOT_FOUND when id is not live
	// in the project's folder).
	File(ctx context.Context, id string) ([]byte, error)
	// Refs is the local, tracking and seen tips and the pending writes.
	Refs(ctx context.Context) (Refs, error)
	// Log is the newest n commits of the local live ref (E_NOT_FOUND when
	// there is no live ref yet).
	Log(ctx context.Context, n int) (History, error)
	// Restore pushes the local copy (or the newest backup) back to the
	// remote, leaving out landed and purged rows (§6.6).
	Restore(ctx context.Context) (Restored, error)
	// Compact rewrites the ref to one parentless commit with a higher epoch
	// (F8); dryRun only plans.
	Compact(ctx context.Context, dryRun bool) (Compacted, error)
	// Purge removes ids from live for good and lists them in
	// _live/purged.json (§6.6).
	Purge(ctx context.Context, ids []string) error
}

// Env is what the live operations need; *wire.App implements it.
type Env interface {
	// LiveRef is the live ref of the cwd project, or E_NOT_PROJECT_ROOT /
	// E_UNSUPPORTED (see the package comment).
	LiveRef(ctx context.Context) (Ref, error)
}

// IsOp reports whether name is one of the live operations: they need no
// store open (wire answers them before opening the engine, so a folder
// outside a project gets v0.1.17's E_NOT_PROJECT_ROOT).
func IsOp(name string) bool {
	switch name {
	case OpShow, OpDiff, OpLog, OpRestore, OpCompact, OpPurge:
		return true
	}
	return false
}

// ShowIn is the input of live.show.
type ShowIn struct {
	ID string `json:"id,omitempty" arg:"0" desc:"a live row id: print its file (default: list every live row)"`
}

// ShowOut is the output of live.show: the listing, or one file's content.
type ShowOut struct {
	Folder string    `json:"folder"`
	Epoch  int       `json:"epoch"`
	Rows   []ShowRow `json:"rows"`
	Count  int       `json:"count"`
	ID     string    `json:"id,omitempty"`
	File   string    `json:"file,omitempty"`
}

// ShowRow is one listed live row.
type ShowRow struct {
	Table string `json:"table"`
	ID    string `json:"id"`
	State string `json:"state"`
	Since string `json:"since"`
	// shown is Since as the text prints it (v0.1.17's column); "" for a
	// row decoded from JSON, which prints Since as given.
	shown string
}

// Text prints v0.1.17's listing, or the file as stored.
func (o ShowOut) Text() string {
	if o.ID != "" {
		return o.File
	}
	var b strings.Builder
	for _, r := range o.Rows {
		since := r.shown
		if since == "" {
			since = r.Since
		}
		fmt.Fprintf(&b, "%-10s %-40s %-9s since %s\n", r.Table, r.ID, r.State, since)
	}
	fmt.Fprintf(&b, "%d live rows in %s (epoch %d)\n", o.Count, o.Folder, o.Epoch)
	return b.String()
}

// DiffIn is the input of live.diff (none).
type DiffIn struct{}

// DiffOut is the output of live.diff.
type DiffOut struct {
	Local    string   `json:"local"`
	Tracking string   `json:"tracking"`
	Seen     string   `json:"seen"`
	Pending  []string `json:"pending"`
}

// Text prints v0.1.17's four lines and the pending paths.
func (o DiffOut) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "local     %s\ntracking  %s\nseen      %s\n", orNone(o.Local), orNone(o.Tracking), orNone(o.Seen))
	fmt.Fprintf(&b, "pending   %d write(s) not confirmed by the remote\n", len(o.Pending))
	for _, p := range o.Pending {
		b.WriteString("  " + p + "\n")
	}
	return b.String()
}

func orNone(s string) string {
	if s == "" {
		return noRef
	}
	return s
}

// LogIn is the input of live.log.
type LogIn struct {
	Number int `json:"number,omitempty" flag:"number" short:"n" desc:"commits to show (default 20)"`
}

// LogOut is the output of live.log.
type LogOut struct {
	Epoch int    `json:"epoch"`
	Log   string `json:"log"`
}

// Text prints the epoch line and git's log lines.
func (o LogOut) Text() string { return fmt.Sprintf("epoch %d\n%s", o.Epoch, o.Log) }

// RestoreIn is the input of live.restore (none).
type RestoreIn struct{}

// RestoreOut is the output of live.restore.
type RestoreOut struct {
	Rows    int `json:"rows"`
	LeftOut int `json:"left_out"`
}

// Text prints v0.1.17's confirmation line.
func (o RestoreOut) Text() string {
	return fmt.Sprintf("✓ restored refs/lore/live: %d rows (left out %d already landed or purged)\n", o.Rows, o.LeftOut)
}

// CompactIn is the input of live.compact.
type CompactIn struct {
	DryRun bool `json:"dry_run,omitempty" flag:"dry-run" desc:"print what would happen, change nothing"`
}

// CompactOut is the output of live.compact.
type CompactOut struct {
	Ref       string `json:"ref"`
	DryRun    bool   `json:"dry_run"`
	Nothing   bool   `json:"nothing"`
	Commits   int    `json:"commits"`
	Threshold int    `json:"threshold"`
	Files     int    `json:"files"`
	FromEpoch int    `json:"from_epoch"`
	ToEpoch   int    `json:"to_epoch"`
	Purged    int    `json:"purged"`
	Backup    string `json:"backup,omitempty"`
}

// Text prints v0.1.17's plan line (--dry-run) or result line.
func (o CompactOut) Text() string {
	switch {
	case o.Nothing:
		return "nothing to compact: no live ref yet\n"
	case o.DryRun:
		s := fmt.Sprintf("%s has %d commits (threshold %d); compacting would keep %d files, epoch %d → %d",
			o.Ref, o.Commits, o.Threshold, o.Files, o.FromEpoch, o.ToEpoch)
		if o.Purged > 0 {
			s += fmt.Sprintf(", dropping %d purged rows", o.Purged)
		}
		return s + "\n"
	}
	return fmt.Sprintf("✓ compacted %s to 1 commit (epoch %d); backup kept locally as %s\n", o.Ref, o.ToEpoch, o.Backup)
}

// PurgeIn is the input of live.purge.
type PurgeIn struct {
	IDs []string `json:"ids" arg:"0" rest:"true" desc:"the live row ids to remove for good"`
}

// PurgeOut is the output of live.purge.
type PurgeOut struct {
	Purged []string `json:"purged"`
}

// Text prints v0.1.17's confirmation line.
func (o PurgeOut) Text() string {
	return fmt.Sprintf("✓ purged %s from live; run `lore live compact` to drop them from the ref's history too\n", strings.Join(o.Purged, ", "))
}

// readErrors are the codes every live operation can return.
var readErrors = []errs.Code{errs.NotProjectRoot, errs.Unsupported, errs.InvalidInput, errs.Internal}

// writeErrors adds the refusals of the operations that change the ref.
var writeErrors = append(append([]errs.Code{}, readErrors...), errs.ReadOnly, errs.Offline, errs.NotFound)

// Ops returns the live operations bound to env.
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpShow, Summary: "List this project's live rows, or print one live file",
			Permission: op.PermRead, Access: op.AccessRead, RESTNoun: restNoun,
			Errors: append(append([]errs.Code{}, readErrors...), errs.NotFound),
		}, func(ctx context.Context, in ShowIn) (ShowOut, error) { return show(ctx, env, in) }),
		op.Define(op.Spec{
			Name: OpDiff, Summary: "Local, tracking and remote live ref, and the writes not pushed yet",
			Permission: op.PermRead, Access: op.AccessRead, RESTNoun: restNoun,
			Errors: readErrors,
		}, func(ctx context.Context, _ DiffIn) (DiffOut, error) { return diff(ctx, env) }),
		op.Define(op.Spec{
			Name: OpLog, Summary: "History of the live ref with its epoch",
			Permission: op.PermRead, Access: op.AccessRead, RESTNoun: restNoun,
			Errors: append(append([]errs.Code{}, readErrors...), errs.NotFound),
		}, func(ctx context.Context, in LogIn) (LogOut, error) { return logOf(ctx, env, in) }),
		op.Define(op.Spec{
			Name:       OpRestore,
			Summary:    "Put the live rows back on the remote from the local copy after it was deleted or rewritten",
			Permission: op.PermWrite, Access: op.AccessWrite, RESTNoun: restNoun,
			Confirm: op.ConfirmRule{PersonOnlyEligible: true},
			Errors:  writeErrors,
		}, func(ctx context.Context, _ RestoreIn) (RestoreOut, error) { return restore(ctx, env) }),
		op.Define(op.Spec{
			Name:       OpCompact,
			Summary:    "Rewrite the live ref to one commit with a higher epoch (keeps every row; a local backup is kept)",
			Permission: op.PermWrite, Access: op.AccessWrite, RESTNoun: restNoun,
			Confirm: op.ConfirmRule{PersonOnlyEligible: true},
			Errors:  writeErrors,
		}, func(ctx context.Context, in CompactIn) (CompactOut, error) { return compact(ctx, env, in) }),
		op.Define(op.Spec{
			Name:       OpPurge,
			Summary:    "Remove rows from live for good (leak cleanup): no clone can push them back",
			Permission: op.PermWrite, Access: op.AccessWrite, RESTNoun: restNoun,
			Confirm: op.ConfirmRule{PersonOnlyEligible: true},
			Errors:  writeErrors,
		}, func(ctx context.Context, in PurgeIn) (PurgeOut, error) { return purge(ctx, env, in) }),
	}
}

// show lists the folder, or prints one file when an id is given.
func show(ctx context.Context, env Env, in ShowIn) (ShowOut, error) {
	r, err := env.LiveRef(ctx)
	if err != nil {
		return ShowOut{}, err
	}
	if id := strings.TrimSpace(in.ID); id != "" {
		b, ferr := r.File(ctx, id)
		if ferr != nil {
			return ShowOut{}, ferr
		}
		return ShowOut{ID: id, File: string(b), Rows: []ShowRow{}}, nil
	}
	l, err := r.List(ctx)
	if err != nil {
		return ShowOut{}, err
	}
	out := ShowOut{Folder: l.Folder, Epoch: l.Epoch, Rows: make([]ShowRow, 0, len(l.Rows)), Count: len(l.Rows)}
	for _, x := range l.Rows {
		out.Rows = append(out.Rows, ShowRow{Table: x.Table, ID: x.ID, State: x.State, Since: x.Since.UTC().Format(sinceJSONLayout), shown: x.Since.Format(sinceLayout)})
	}
	return out, nil
}

// diff reports the three tips and the pending writes.
func diff(ctx context.Context, env Env) (DiffOut, error) {
	r, err := env.LiveRef(ctx)
	if err != nil {
		return DiffOut{}, err
	}
	rs, err := r.Refs(ctx)
	if err != nil {
		return DiffOut{}, err
	}
	pending := rs.Pending
	if pending == nil {
		pending = []string{}
	}
	return DiffOut{Local: rs.Local, Tracking: rs.Tracking, Seen: rs.Seen, Pending: pending}, nil
}

// logOf prints the newest --number commits (default DefaultLogLimit).
func logOf(ctx context.Context, env Env, in LogIn) (LogOut, error) {
	n := in.Number
	if n < 0 {
		return LogOut{}, errs.New(errs.InvalidInput, "live log: --number must be positive")
	}
	if n == 0 {
		n = DefaultLogLimit
	}
	r, err := env.LiveRef(ctx)
	if err != nil {
		return LogOut{}, err
	}
	h, err := r.Log(ctx, n)
	if err != nil {
		return LogOut{}, err
	}
	return LogOut{Epoch: h.Epoch, Log: h.Lines}, nil
}

// restore pushes the local copy back (§6.6).
func restore(ctx context.Context, env Env) (RestoreOut, error) {
	r, err := env.LiveRef(ctx)
	if err != nil {
		return RestoreOut{}, err
	}
	res, err := r.Restore(ctx)
	if err != nil {
		return RestoreOut{}, err
	}
	return RestoreOut(res), nil
}

// compact plans (--dry-run) or runs the compaction (F8).
func compact(ctx context.Context, env Env, in CompactIn) (CompactOut, error) {
	r, err := env.LiveRef(ctx)
	if err != nil {
		return CompactOut{}, err
	}
	c, err := r.Compact(ctx, in.DryRun)
	if err != nil {
		return CompactOut{}, err
	}
	p := c.Plan
	return CompactOut{
		Ref: p.Ref, DryRun: in.DryRun, Nothing: c.Nothing && !in.DryRun, Commits: p.Commits, Threshold: p.Threshold,
		Files: p.Files, FromEpoch: p.FromEpoch, ToEpoch: p.ToEpoch, Purged: p.Purged, Backup: c.Backup,
	}, nil
}

// purge removes the ids from live for good (§6.6).
func purge(ctx context.Context, env Env, in PurgeIn) (PurgeOut, error) {
	ids := make([]string, 0, len(in.IDs))
	for _, id := range in.IDs {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return PurgeOut{}, errs.New(errs.InvalidInput, "live purge: at least one id is required").
			WithHint("lore live purge <id>...")
	}
	r, err := env.LiveRef(ctx)
	if err != nil {
		return PurgeOut{}, err
	}
	if err := r.Purge(ctx, ids); err != nil {
		return PurgeOut{}, err
	}
	return PurgeOut{Purged: ids}, nil
}
