// Package migrate defines `lore migrate run` (FORMAT_EVOLUTION, layer L5):
// it rewrites row files still in the v0.1.17 format (`_v: 1`) as `_v: 2`.
//
// Reading never needs it: a `_v: 1` file is upcast in memory on every read
// (model/row). Rewriting is a separate, explicit step because a file
// rewritten as `_v: 2` can no longer be read by a teammate still on
// v0.1.17; running this command is the team's "we have all upgraded".
//
// Scope: every table of a migrated noun (Rewriter.MigratedTables) is
// rewritten in the repo store and every attached store; other tables keep `_v: 1`
// until their noun migrates. It also converts the v0.1.17 settings the core
// owns (STORE_SERVER_SPEC 13): ~/.lore/identity.yaml into `identity:` of
// ~/.lore/config.yaml (CFG9) and v1 lore-store.yaml markers into v2 (a
// permanent sto_ id). The run holds the migration lock (E_MIGRATE_LOCKED).
//
// `lore migrate enable <feature>` (FORMAT_EVOLUTION rule 2) declares a
// format feature in .lore/data/_meta.json before its first use:
//
//	enable ─► Rewriter.EnableFeature ─► (O6 open and no --yes: E_NEEDS_CONFIRM)
//	       ─► rowfs.DeclareRepo ─► features ∪ {name}, min_lore_version = max(floor, Since)
//
// run with a Journal (journal.go): lock ─► Begin (backup ref, journal or
// resume) ─► steps ─► Finish (push unless --no-push/--offline); plus
// status, rollback (FQ5) and unlock.
package migrate

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
)

// OpRun is the operation name: `lore migrate run`, POST /v1/migrate/run.
const OpRun = "migrate.run"

// restNoun keeps the REST collection singular (/v1/migrate/run): the noun
// is a verb-like command, its default plural reads wrong.
const restNoun = "migrate"

// AllTablesRewriter is a Rewriter that rewrites every table in one pass
// per store: one read of each store, one write batch (one commit for a
// store clone) and one cache refresh, where calling RewriteV1 per table
// re-read every store once per table (owner item 27: minutes for a repo
// of 5,000 rows). run uses it when the Rewriter has it.
type AllTablesRewriter interface {
	// RewriteAllV1 is RewriteV1 for each of tables (RewriteRepoV1 when
	// repoOnly), keyed by table; a table with no v1 row may be absent.
	RewriteAllV1(ctx context.Context, tables []row.Table, repoOnly, dryRun bool) (map[row.Table][]ids.AnyID, error)
}

// LeftReporter is a Rewriter that names the files a run leaves at
// `_v: 1` (T-2: a run left actors, projects, repos, entity_tags and the
// data root's _meta.json at v1 and said nothing, so the repo was mixed
// silently). run asks it after the rewrites, dry run included, and prints
// every group with why it stays and what converts it.
type LeftReporter interface {
	// LeftV1 lists what the run leaves at `_v: 1` in the places it
	// rewrote (the repo store only when repoOnly), given the tables it
	// rewrites. A dry run names what the real run would leave.
	LeftV1(ctx context.Context, tables []row.Table, repoOnly, dryRun bool) ([]Left, error)
}

// RepoSettingsMigrator is a Rewriter with settings steps that touch only
// the repo's own branch files, so `--offline` runs them too (M-1: an
// offline run left every 0.1.17 entity_tags link unfolded, and once the
// link files are rewritten as `_v: 2` no later run would fold them).
type RepoSettingsMigrator interface {
	// MigrateRepoSettings runs the repo-only settings steps (the entity_tags
	// links folded into tag_ids); MigrateSettings runs them as well, so an
	// online run never calls both.
	MigrateRepoSettings(ctx context.Context, dryRun bool) (Settings, error)
}

// Left is one group of files a run leaves at `_v: 1`: a table's rows, or
// a folder file. Never empty: a group has at least one file.
type Left struct {
	// What names the group: a table ("entity_tags") or a file path
	// (".lore/data/_meta.json").
	What string `json:"what"`
	// Table is the table of a group of rows ("" for a folder file).
	Table string `json:"table,omitempty"`
	// Files are the rows of a table group ("table/id") or the one path.
	Files []string `json:"files"`
	// Why says why the run does not rewrite it and what converts it
	// (or that nothing has to).
	Why string `json:"why"`
}

// Rewriter rewrites the v1 rows of one table in the repo store. The
// composition root implements it with the store backend: a rewrite changes
// only the format (_v, _hlc), so it must NOT go through the engine's Update,
// which would stamp a new HLC and updated_at and so change the row.
type Rewriter interface {
	// MigratedTables lists the tables the core owns (the migrated nouns),
	// in the order migrate.run rewrites them. The composition root keeps
	// the one list, so a new noun needs no line here.
	MigratedTables() []row.Table
	// RewriteV1 returns the ids of the rows of table still in `_v: 1`, and
	// rewrites them as `_v: 2` unless dryRun. A row changed concurrently
	// is not overwritten (the backend's Expect check): the call fails with
	// E_STALE and can be rerun.
	RewriteV1(ctx context.Context, table row.Table, dryRun bool) ([]ids.AnyID, error)
	// MigrateSettings converts the v0.1.17 settings and stores that are
	// not plain row rewrites (Mode B, the personal store flatten,
	// identity.yaml, v1 store markers, the org roster and defaults, the
	// entity_tags links) and returns one line per change plus how many
	// rows those changes write or move (made, or that would be made under
	// dryRun).
	MigrateSettings(ctx context.Context, dryRun bool) (Settings, error)
	// MigrateLive brings the v0.1.17 live ref's rows of core-owned tables
	// (open tasks and the like, never landed in .lore/data) into the repo
	// store as `_v: 2` rows with the same id and stamp, and returns
	// "table/id" per row (made, or that would be made under dryRun).
	MigrateLive(ctx context.Context, dryRun bool) ([]string, error)
	// EnableFeature declares a known format feature in the repo's
	// _meta.json before its first use (`lore migrate enable`); it answers
	// E_NEEDS_CONFIRM when teammates would become read-only and the caller
	// has not confirmed.
	EnableFeature(ctx context.Context, name string) (EnableOut, error)
	// LockMigration takes the migration lock (E_MIGRATE_LOCKED when held);
	// the returned func releases it.
	LockMigration(ctx context.Context) (func() error, error)
	// WriteGate refuses (E_OLD_LORE) when this binary may not write any
	// attached store: the same format gate as every engine write. A run
	// rewrites files outside the engine's Update, so it asks here first
	// (INTEG-MIGREST-1): an older lore never changes the format of a
	// project a newer lore owns.
	WriteGate(ctx context.Context) error
}

// Settings is what Rewriter.MigrateSettings did (or would do).
type Settings struct {
	// Steps are the human lines, one per change.
	Steps []string
	// Rows counts every row the steps wrote or moved (a Mode B row copied
	// into its store, a personal row moved to the root, an org roster row
	// rewritten, a routing policy row written, a row whose tag_ids were
	// set). A file that is not a row (a marker, config.yaml) counts 0.
	Rows int
	// Upgraded names ("table/id") the repo-store rows among Rows that the
	// steps wrote (would write, under dryRun), so the run lists them under
	// their table and counts each once (E-2: the entity_tags fold rewrote
	// 75 rows as _v: 2 before the format rewrite, so a dry run counted
	// them twice and the real run's table list left them out).
	Upgraded []string
	// Detail are per-row lines behind Steps (one per row whose tag_ids
	// were set), printed only with --verbose: a 300-row repo printed 75
	// of them and buried the summary.
	Detail []string
}

// ActorRecorder is a Rewriter that writes the running person's actor row
// into every store where the run stamped rows with that actor (E-3:
// settings steps write through the engine, which stamps updated_by, but
// around the write path that lands the writer's actor row first, so
// doctor called the fresh migration DEGRADED). run calls it after the
// rewrites, before Finish, so the actor file is part of the same diff.
type ActorRecorder interface {
	// RecordRunActor writes the actor row where it is missing.
	RecordRunActor(ctx context.Context) error
}

// RunIn is the input of migrate.run.
type RunIn struct {
	DryRun bool `json:"dry_run,omitempty" flag:"dry-run" desc:"list the files that would be rewritten; change nothing"`
	// NoPush keeps the store commits local (rollback is then a local undo).
	NoPush bool `json:"no_push,omitempty" flag:"no-push" desc:"commit the store changes but do not push them"`
	// Offline rewrites the repo's branch files only (FORMAT_EVOLUTION 7.3).
	Offline bool `json:"offline,omitempty" flag:"offline" desc:"branch files only: skip the settings, live rows and stores (they need the network)"`
	// TeamLock also holds the team lock on the remote's refs/lore/signals
	// while the run lasts (FORMAT_EVOLUTION 7.2). Off by default: the lock
	// is a push, and nothing pushes unless the person asks (A-7).
	TeamLock bool `json:"team_lock,omitempty" flag:"team-lock" desc:"also hold the team migration lock: pushes a hold to refs/lore/signals on the remote while the run lasts (removed when it ends), so a teammate cannot migrate at the same time; ignored with --dry-run, --no-push and --offline"`
	// MachineLocal copies the repo's v0.1.17 machine-local rows into this
	// machine's store even from a checkout that is not this machine's own
	// (wire machinecopy.go says when it is).
	MachineLocal bool `json:"machine_local,omitempty" flag:"machine-local" desc:"also copy the repo's v0.1.17 machine-local rows (runs, render histories, sessions) into this machine's store when this checkout is a copy, not this machine's checkout of the repo"`
	// Verbose prints every upgraded row, one per line; without it the
	// text names each table with its count and a few ids (--json always
	// carries every row).
	Verbose bool `json:"verbose,omitempty" flag:"verbose" desc:"list every upgraded row, one per line (default: a count per table and its first ids; --json always lists all)"`
}

// RunOut is the output of migrate.run.
type RunOut struct {
	DryRun bool `json:"dry_run,omitempty"`
	// Count is every row the run wrote or moved (would, under dry_run):
	// the Rows lines plus the rows counted by the settings Steps, so a
	// run that moved rows never reports 0.
	Count int      `json:"count"`
	Rows  []string `json:"rows"`
	// Steps are the settings changes (identity, store markers).
	Steps []string `json:"steps"`
	// Details are the per-row lines behind Steps (Settings.Detail); Text
	// prints them only with --verbose.
	Details []string `json:"details,omitempty"`
	// Journal is the run's own lines: backup, resume, offline, push, undo
	// (kept apart so a dry run and a run list the same Steps).
	Journal []string `json:"journal,omitempty"`
	// BackupRef is the local backup of this run ("" for a dry run or a
	// Rewriter without Journal).
	BackupRef string `json:"backup_ref,omitempty"`
	// Uncommitted counts the files of the code repo's .lore/data the run
	// changed, waiting for the person's commit (Text ends with
	// CommitLine; lore never commits a code repo).
	Uncommitted int `json:"uncommitted,omitempty"`
	// Left names what the run leaves at `_v: 1` and why (LeftReporter);
	// empty when every file is in the current format.
	Left []Left `json:"left,omitempty"`
	// Verbose is RunIn.Verbose: Text lists every row.
	Verbose bool `json:"-"`
}

// rowsShown is how many ids Text names per table before "… N more"
// (owner item C2: a 5,000-row repo printed 5,000 lines).
const rowsShown = 5

// RowLines is the human form of rows ("table/id" each): one line per
// table, in the order the tables first appear, with its count and its
// first rowsShown ids ("memories: 4832 rows (mem_1, mem_2, … 4827
// more)"); verbose keeps one line per row. A line that names no table
// (no "/") stays as it is.
func RowLines(rows []string, verbose bool) []string {
	if verbose {
		return slices.Clone(rows)
	}
	var order []string
	byTable := map[string][]string{}
	var out []string
	for _, r := range rows {
		t, id, ok := strings.Cut(r, "/")
		if !ok || t == "" || id == "" {
			out = append(out, r)
			continue
		}
		if _, seen := byTable[t]; !seen {
			order = append(order, t)
		}
		byTable[t] = append(byTable[t], id)
	}
	lines := make([]string, 0, len(order)+len(out))
	for _, t := range order {
		got := byTable[t]
		shown := got[:min(len(got), rowsShown)]
		line := t + ": " + plural.Count(len(got), "row") + " (" + strings.Join(shown, ", ")
		if more := len(got) - len(shown); more > 0 {
			line += " … " + strconv.Itoa(more) + " more"
		}
		lines = append(lines, line+")")
	}
	return append(lines, out...)
}

// LeftText is the human form of what a run leaves at `_v: 1`: a header
// with the file count, then one line per group with its count, its first
// files (every file with verbose) and why ("" when nothing is left).
func LeftText(left []Left, dryRun, verbose bool) string {
	n := 0
	for _, l := range left {
		n += len(l.Files)
	}
	if n == 0 {
		return ""
	}
	var b strings.Builder
	verb := "Left"
	if dryRun {
		verb = "Would leave"
	}
	b.WriteString(verb + " " + plural.Count(n, "file") + " at _v: 1:\n")
	for _, l := range left {
		shown := l.Files
		if !verbose && len(shown) > rowsShown {
			shown = shown[:rowsShown]
		}
		names := make([]string, 0, len(shown))
		for _, f := range shown {
			if l.Table != "" {
				f = strings.TrimPrefix(f, l.Table+"/") // a table's rows by id
			}
			names = append(names, f)
		}
		line := "  " + l.What
		if len(l.Files) != 1 || l.Files[0] != l.What { // a folder file is its own name
			line += ": " + plural.Count(len(l.Files), "file") + " (" + strings.Join(names, ", ")
			if more := len(l.Files) - len(shown); more > 0 {
				line += " … " + strconv.Itoa(more) + " more"
			}
			line += ")"
		}
		b.WriteString(line + " — " + l.Why + "\n")
	}
	return b.String()
}

var _ op.Texter = RunOut{}

// Text implements op.Texter: the human form of a run, one line per item
// (FORMAT_EVOLUTION 4 example), never an empty "rows:" line. The count
// line comes first ("Upgraded N rows", "Would upgrade N rows", or
// "Nothing to migrate" when the run found nothing), then the rows (a line
// per table, RowLines; one per row with --verbose) and each settings step
// indented, then the run's own journal lines.
func (o RunOut) Text() string {
	var b strings.Builder
	verb := "Upgraded"
	if o.DryRun {
		verb = "Would upgrade"
	}
	switch {
	case o.Count == 0 && len(o.Rows) == 0 && len(o.Steps) == 0:
		b.WriteString("Nothing to migrate: every row is already in the current format.\n")
	default:
		b.WriteString(verb + " " + plural.Count(o.Count, "row") + "\n")
	}
	for _, r := range RowLines(o.Rows, o.Verbose) {
		b.WriteString("  " + r + "\n")
	}
	for _, s := range o.Steps {
		b.WriteString("  " + s + "\n")
	}
	if o.Verbose {
		for _, d := range o.Details {
			b.WriteString("    " + d + "\n")
		}
	}
	b.WriteString(LeftText(o.Left, o.DryRun, o.Verbose))
	for _, j := range o.Journal {
		b.WriteString(j + "\n")
	}
	if o.Uncommitted > 0 && !o.DryRun {
		b.WriteString(CommitLine(o.Uncommitted) + "\n")
	}
	if o.DryRun {
		b.WriteString("Nothing written (dry run).\n")
	}
	return b.String()
}

// OpEnable is `lore migrate enable <feature>`, POST /v1/migrate/enable.
const OpEnable = "migrate.enable"

// EnableIn is the input of migrate.enable. Feature is required, but
// checked by the op itself (required:"after-lookup", op/required.go) so a
// missing one answers the op's own hint naming the command, not the
// decoder's bare "missing required field feature" (INTEG-MIGREST-2).
type EnableIn struct {
	Feature string `json:"feature,omitempty" required:"after-lookup" arg:"0" desc:"the feature to declare (hlc, meta, policy, resources, …; required)"`
}

// EnableOut is the output of migrate.enable.
type EnableOut struct {
	Feature string `json:"feature"`
	// Added is false when the project already declared it.
	Added          bool     `json:"added"`
	Features       []string `json:"features"`
	MinLoreVersion string   `json:"min_lore_version,omitempty"`
}

// Ops returns the migration operations bound to rw (plus status, rollback
// and unlock when rw also implements Journal).
func Ops(rw Rewriter) []op.Entry {
	out := baseOps(rw)
	if j, ok := rw.(Journal); ok {
		out = append(out, journalOps(rw, j)...)
	}
	return out
}

// run is migrate.run (FORMAT_EVOLUTION rule 4, steps 2-7): lock (M) →
// Begin (backup ref + journal, or resume) → settings → live → rewrites
// (--offline: the repo's rows only) → Finish (push unless --no-push or
// --offline). Lock order M then T (7.5); T only for a run that pushes
// (not --dry-run, --no-push or --offline: nothing reaches a teammate) and
// was asked for it (--team-lock: T itself is a push to the remote).
func run(ctx context.Context, rw Rewriter, in RunIn) (RunOut, error) {
	out := RunOut{DryRun: in.DryRun, Rows: []string{}, Steps: []string{}, Verbose: in.Verbose}
	if !in.DryRun { // a dry run only reads, and reading works on a gated project
		if err := rw.WriteGate(ctx); err != nil {
			return RunOut{}, err
		}
	}
	unlock, err := rw.LockMigration(ctx)
	if err != nil {
		return RunOut{}, err
	}
	defer func() { _ = unlock() }() // a failed release leaves a lock file `lore migrate unlock` clears
	if !in.DryRun {
		ctx = WithRun(ctx) // F-MIGRATE-1: this run's own writes answer the O6 question, never ask it
	}
	if in.MachineLocal {
		ctx = WithMachineLocal(ctx)
	}
	pushes := !in.DryRun && !in.NoPush && !in.Offline
	if !in.DryRun && !pushes {
		ctx = WithNoPush(ctx) // --no-push / --offline: no write or sync of the run pushes
	}
	hold, err := takeTeamLock(ctx, pushes, in.TeamLock)
	if err != nil {
		return RunOut{}, err
	}
	defer func() { _ = hold.release() }() // released before M (reverse order); an unreleased hold expires (7.2)
	// teamLines are the run's team-lock lines: what taking T said (or that
	// this surface has none), and the release when T was really taken.
	// They survive a no-op run (OV38: the run says whether a team lock
	// guarded it), and a surface without T never prints a release.
	var teamLines []string
	if hold.line != "" {
		teamLines = append(teamLines, hold.line)
	}
	out.Journal = append(out.Journal, teamLines...)
	if err := At(ctx, PointTeamLocked); err != nil {
		return RunOut{}, err
	}
	j, journaled := rw.(Journal)
	if journaled && !in.DryRun {
		b, err := j.Begin(ctx, in.Offline)
		if err != nil {
			return RunOut{}, err
		}
		out.BackupRef = b.BackupRef
		out.Journal = append(out.Journal, b.Lines...)
		if err := At(ctx, PointBegun); err != nil {
			return RunOut{}, err
		}
	}
	// failed answers a step's error after Begin: the run's changes are put
	// back first (Aborter, E-5), so a failed run leaves no mixed repo
	failed := func(err error) (RunOut, error) {
		if ab, ok := rw.(Aborter); ok && journaled && !in.DryRun {
			return RunOut{}, abortRun(ctx, ab, err)
		}
		return RunOut{}, err
	}
	rewrite := rw.RewriteV1
	repoOnly := in.Offline && journaled
	if repoOnly {
		rewrite = j.RewriteRepoV1
		out.Journal = append(out.Journal, "offline: no team lock taken; branch files only (settings, live rows and stores skipped: run `lore migrate run` again when online)")
		if rs, ok := rw.(RepoSettingsMigrator); ok {
			settings, err := rs.MigrateRepoSettings(ctx, in.DryRun)
			if err != nil {
				return failed(err)
			}
			out.addSettings(settings)
		}
	} else {
		settings, err := rw.MigrateSettings(ctx, in.DryRun)
		if err != nil {
			return failed(err)
		}
		out.addSettings(settings)
		if err = At(ctx, PointSettings); err != nil {
			return RunOut{}, err
		}
		live, err := rw.MigrateLive(ctx, in.DryRun)
		if err != nil {
			return failed(err)
		}
		out.addRows(live)
		if err := At(ctx, PointLive); err != nil {
			return RunOut{}, err
		}
	}
	tables := rw.MigratedTables()
	if all, ok := rw.(AllTablesRewriter); ok {
		// one pass per store for every table (owner item 27): the same
		// ids per table as the per-table calls, in the same order
		got, err := all.RewriteAllV1(ctx, tables, repoOnly, in.DryRun)
		if err != nil {
			return failed(err)
		}
		rewrite = func(_ context.Context, t row.Table, _ bool) ([]ids.AnyID, error) { return got[t], nil }
	}
	for _, t := range tables {
		got, err := rewrite(ctx, t, in.DryRun)
		if err != nil {
			return failed(err)
		}
		lines := make([]string, 0, len(got))
		for _, id := range got {
			lines = append(lines, t.String()+"/"+id.String())
		}
		out.addRows(lines)
		if len(got) > 0 && !in.DryRun { // a table with nothing to rewrite is no boundary
			if err := At(ctx, PointTable); err != nil {
				return RunOut{}, err
			}
		}
	}
	out.Count += len(out.Rows)
	sortRows(out.Rows, tables)
	if ar, ok := rw.(ActorRecorder); ok && !in.DryRun {
		if err := ar.RecordRunActor(ctx); err != nil {
			return RunOut{}, err
		}
	}
	if lr, ok := rw.(LeftReporter); ok {
		// T-2: never leave a mixed repo silently; name every file the run
		// does not rewrite, why, and what converts it
		left, err := lr.LeftV1(ctx, tables, repoOnly, in.DryRun)
		if err != nil {
			return failed(err)
		}
		out.Left = left
	}
	if !in.DryRun {
		if err := At(ctx, PointFinish); err != nil {
			return RunOut{}, err
		}
	}
	if journaled && !in.DryRun {
		fin, err := j.Finish(ctx, !in.NoPush && !in.Offline, out.Count)
		if err != nil {
			return RunOut{}, err
		}
		lines := fin.Lines
		out.Journal = append(out.Journal, lines...)
		out.Uncommitted = fin.Uncommitted
		if len(lines) == 0 { // nothing changed: the backup was dropped; the team-lock lines stay
			out.BackupRef, out.Journal = "", teamLines
		}
		if hold.taken { // a host that took no hold (no remote) never prints a release
			out.Journal = append(out.Journal, teamLockReleasedLine)
		}
	}
	return out, nil
}

// addSettings adds what a settings step did: its lines, its rows named
// by id once (addRows) and the rest of its row count.
func (o *RunOut) addSettings(s Settings) {
	o.Steps = append(o.Steps, s.Steps...)
	o.Details = append(o.Details, s.Detail...)
	o.addRows(s.Upgraded)
	o.Count += max(s.Rows-len(s.Upgraded), 0) // the named rows are counted with Rows
}

// addRows adds "table/id" lines not listed yet. A row is one file
// however many steps write it: a v0.1.17 live row that also sits at v1 in
// .lore/data is written by the live step and then skipped by the rewrite,
// a row whose tag_ids the fold sets is skipped by the rewrite too; a dry
// run writes nothing, so its rewrite still finds those files at v1, and
// without this it promised more rows than the real run then reported.
func (o *RunOut) addRows(lines []string) {
	for _, l := range lines {
		if !slices.Contains(o.Rows, l) {
			o.Rows = append(o.Rows, l)
		}
	}
}

// sortRows orders "table/id" lines by the run's table order, then id, so
// a dry run and the real run list the same tables in the same order
// whichever step wrote a row (E-2). A table the run does not rewrite goes
// last, in name order.
func sortRows(rows []string, tables []row.Table) {
	rank := func(l string) int {
		t, _, _ := strings.Cut(l, "/")
		for i, x := range tables {
			if x.String() == t {
				return i
			}
		}
		return len(tables)
	}
	slices.SortStableFunc(rows, func(x, y string) int {
		if rx, ry := rank(x), rank(y); rx != ry {
			return rx - ry
		}
		return strings.Compare(x, y)
	})
}

// baseOps are run and enable.
func baseOps(rw Rewriter) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpRun, Summary: "Rewrite v0.1.17 row files (_v: 1) as _v: 2 (the migrated nouns)",
			Permission: op.PermAdmin, Access: op.AccessWrite, RESTNoun: restNoun,
			Errors: []errs.Code{errs.Stale, errs.StoreLocked, errs.StoreReadOnly, errs.InvalidInput, errs.OldLore, errs.MigrateLocked, errs.ConfigInvalid, errs.NotAStore, errs.Internal},
			Examples: []op.Example{
				{Title: "see what would change", CLI: "lore migrate run --dry-run", Want: "count:"},
				{Title: "rewrite once the whole team runs 0.1.18", CLI: "lore migrate run", Want: "count:"},
			},
		}, func(ctx context.Context, in RunIn) (RunOut, error) {
			return run(ctx, rw, in)
		}),
		// migrate.enable (FORMAT_EVOLUTION rule 2): declare → (ask while
		// teammates on v0.1.17 wrote the rows: E_NEEDS_CONFIRM, FQ2 as
		// changed by the agents override) → raise features and
		// min_lore_version in .lore/data/_meta.json. Writes no row.
		op.Define(op.Spec{
			Name: OpEnable, Summary: "Declare a format feature before its first use (raises min_lore_version)",
			Permission: op.PermAdmin, Access: op.AccessWrite, RESTNoun: restNoun,
			Confirm: op.ConfirmRule{MayAsk: true},
			Errors:  []errs.Code{errs.InvalidInput, errs.NeedsConfirm, errs.OldLore, errs.NotFound, errs.Internal},
			Examples: []op.Example{
				{Title: "declare the 0.1.18 row format once the team has upgraded", CLI: "lore migrate enable --feature hlc --yes", Want: "feature:"},
			},
		}, func(ctx context.Context, in EnableIn) (EnableOut, error) {
			if in.Feature == "" {
				return EnableOut{}, errs.New(errs.InvalidInput, "migrate: name the feature to enable").WithHint("lore migrate enable --feature <feature>")
			}
			return rw.EnableFeature(ctx, in.Feature)
		}),
	}
}

// Run is migrate.run over rw, for a caller that runs the migration as one
// step of a larger command (lore fix, the first write in a v0.1.17
// project): the same lock, journal, steps and push as `lore migrate run`.
func Run(ctx context.Context, rw Rewriter, in RunIn) (RunOut, error) { return run(ctx, rw, in) }

// abortRun puts back what the failed run changed and returns its error
// with the outcome appended: "nothing changed" when every place is back,
// else what stays and why.
func abortRun(ctx context.Context, ab Aborter, cause error) error {
	lines, err := ab.AbortRun(context.WithoutCancel(ctx))
	if err != nil {
		return errors.Join(cause, errs.Prefix("migrate: put back the failed run's changes (lore migrate rollback undoes them)", err))
	}
	what := "the run's changes were put back; the data is as it was before the run"
	if len(lines) > 0 {
		what += " (" + strings.Join(lines, "; ") + ")"
	}
	var e *errs.Error
	if errors.As(cause, &e) {
		out := *e
		out.Message += "; " + what
		return &out
	}
	return errs.Wrap(errs.Internal, "migrate: "+cause.Error()+"; "+what, cause)
}
