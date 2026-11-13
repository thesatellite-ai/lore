// Package trash defines the `trash` noun (layer L5, DELETION_DESIGN 2.2-2.6,
// CLI_REFERENCE 3.9): list, show, restore and purge of rows in the trash.
//
//	lore trash list [--type --by --since --reason --batch --to --with-dependents]
//	    └► Env.Trashed ─► filter ─► roots (dependents counted) ─► newest first
//	lore trash list --overruled ─► Env.Overruled (alive rows with delete_overruled, 3.7)
//	lore trash stats ─► Env.TrashStats (per type/store/deleter, expired, tombstones, 4.5)
//	lore trash show <id> [--show-sensitive]
//	    └► Env.Trashed ─► the one row (body kept; sensitive masked unless asked)
//	lore trash restore <id|bat_> [--parent|--tasklist|--mission|--plan <id>, --no-parent|--no-tasklist]
//	    └► engine.WithRestoreHome (4.2) ─► Env.Restore (same ids, E_PARENT_*)
//	lore trash purge <id|bat_> | --older-than <d> | --expired | --archived-before <date|age>  --confirm
//	    └► no --confirm: E_NEEDS_CONFIRM naming --confirm (DQ2, never --yes)
//	    └► Env.PurgeTrash / Env.PurgeOlder ─► tombstones kept forever (DQ4)
//	    └► each purged row's comments and links (live or trashed) purged
//	       with it (INTEG-TRASH-6); --dry-run lists them (Env.PurgePlan)
//	    └► --archived-before: ArchivedPurger.PurgeArchived (DEL-ARCH, 3.12):
//	       archived rows of the row files ─► tombstones (not lore.db)
//	lore trash purge --local --purged [--dry-run | --confirm]
//	    └► this machine's safety copies of ids in .lore/data/_purged.json
//	       ─► LocalCopyDropper (local_purged.go, DELETION 3.10)
//
// Nothing here purges in the background (DQ3): retention acts only through
// `purge --expired --confirm`.
package trash

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/resmask"
	"github.com/thesatellite-ai/lore/ops/internal/textcut"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (`lore trash …`, REST /v1/trash).
const (
	Noun       = "trash"
	OpList     = "trash.list"
	OpShow     = "trash.show"
	OpRestore  = "trash.restore"
	OpPurge    = "trash.purge"
	OpStats    = "trash.stats"
	timeLayout = "2006-01-02 15:04"
	// jsonTimeLayout is deleted_at in --json and REST: UTC RFC 3339 with
	// milliseconds, like every row time (the text keeps timeLayout).
	jsonTimeLayout = "2006-01-02T15:04:05.000Z07:00"
	dateLayout     = "2006-01-02"
	daySuffix      = "d"
	hoursInDay     = 24
	// titleCut bounds the title column.
	titleCut = 40
)

// titleFields are tried in order for a row's label.
var titleFields = []string{"title", "name", "body"}

// Env is what the trash operations need; *wire.App implements it.
type Env interface {
	Trashed(ctx context.Context) ([]engine.Trashed, error)
	Restore(ctx context.Context, target string) (engine.TrashResult, error)
	PurgeTrash(ctx context.Context, target string) (engine.TrashResult, error)
	PurgeOlder(ctx context.Context, cutoff time.Time, expired, dryRun bool) (engine.TrashResult, error)
	TrashTarget(ctx context.Context, target string) (engine.TrashResult, error)
	// PurgePlan is TrashTarget plus the comments and links a purge of
	// those rows would take with them (INTEG-TRASH-6), for `purge <id> --dry-run`.
	PurgePlan(ctx context.Context, target string) (engine.TrashResult, error)
	TrashStats(ctx context.Context) (engine.TrashStats, error)
	Overruled(ctx context.Context) ([]engine.Overruled, error)
	// TableSchema returns the definition of a table (sensitive fields stay hidden in show).
	TableSchema(ctx context.Context, table row.Table) (schema.TableDef, bool)
}

// ListIn is the input of trash.list.
type ListIn struct {
	Type   string      `json:"type,omitempty" flag:"type" desc:"only rows of this table (tasks, memories, …)"`
	By     op.ActorRef `json:"by,omitempty" flag:"by" desc:"only rows deleted by this actor (@handle, stable key or act_ id)"`
	Since  string      `json:"since,omitempty" flag:"since" desc:"only rows deleted since a date (YYYY-MM-DD) or a duration (36h, 7d)"`
	Reason string      `json:"reason,omitempty" flag:"reason" desc:"only rows whose delete reason holds this text"`
	Batch  string      `json:"batch,omitempty" flag:"batch" desc:"only rows of this batch (bat_…)"`
	To     string      `json:"to,omitempty" flag:"to" alias:"store" desc:"only rows of this store (repo, me, an attached store name)"`
	// WithDependents lists the rows that went into the trash as dependents
	// of another row instead of counting them under it (CLI_REFERENCE 3.9).
	// Dependents are the rows of a cascade relation (sub-tasks, the tasks of
	// a mission, tasklist or plan, a rule's verifiers). Comments, knowledge
	// links and commit links are tombstone_only (schema danglingEntity):
	// they stay live, pointing at the trashed row, so a restore finds its
	// thread intact (INTEG-TRASH-1).
	WithDependents bool `json:"with_dependents,omitempty" flag:"with-dependents" desc:"list dependents (sub-tasks, the tasks of a mission, tasklist or plan) instead of counting them; comments and links stay live and are not in the trash"`
	// Overruled lists the alive rows a merge kept although one side had
	// deleted them (DELETION 3.7, delete_overruled) instead of the trash.
	Overruled bool `json:"overruled,omitempty" flag:"overruled" desc:"list rows a merge kept alive over a delete (the other side edited them)"`
	// Search, Limit and Page are the huge-trash reads (DELETION 4.5).
	Search string `json:"search,omitempty" flag:"search" desc:"only rows whose id, title or delete reason holds this text"`
	Limit  int    `json:"limit,omitempty" flag:"limit" desc:"rows per page (default: all; 50 when --page is given)"`
	Page   int    `json:"page,omitempty" flag:"page" desc:"page number, from 1 (with --limit)"`
	// FromHistory lists purged and hard-deleted rows from git history (3.6).
	FromHistory bool `json:"from_history,omitempty" flag:"from-history" desc:"list rows purged or hard-deleted, read from this place's git history"`
	// Local lists this machine's safety copies instead (local.go, DQ1).
	Local bool `json:"local,omitempty" flag:"local" desc:"this machine's safety copies (_lore_sync_trash), this machine only; replaces lore sync trash"`
}

// defaultPageRows is --limit when only --page is given (DELETION 4.5:
// "1–50 of 4,812").
const defaultPageRows = 50

// ShowIn is the input of trash.show.
type ShowIn struct {
	ID            string `json:"id" arg:"0" desc:"the row id"`
	ShowSensitive bool   `json:"show_sensitive,omitempty" flag:"show-sensitive" desc:"print sensitive field values (asks first)"`
	FromHistory   bool   `json:"from_history,omitempty" flag:"from-history" desc:"show a purged row's last content from git history (3.6)"`
}

// showSensitiveField is the json name of ShowIn.ShowSensitive: trash.show
// asks first when it is set (op.ConfirmRule.When), like lore sql.
const showSensitiveField = "show_sensitive"

// TargetIn is the input of trash.restore. The home flags give a row whose
// parent was purged a new home, or none, in the same write (DELETION 4.2).
type TargetIn struct {
	Target     string `json:"target" arg:"0" desc:"a row id or a batch id (bat_…)"`
	Parent     string `json:"parent,omitempty" flag:"parent" desc:"new parent task (tsk_…) for a sub-task whose parent was purged"`
	NoParent   bool   `json:"no_parent,omitempty" flag:"no-parent" desc:"restore a sub-task whose parent was purged as a top-level task"`
	Tasklist   string `json:"tasklist,omitempty" flag:"tasklist" nonempty:"a tasklist id (tlt_…); --no-tasklist restores with none" desc:"new tasklist (tlt_…) for a task whose tasklist was purged"`
	NoTasklist bool   `json:"no_tasklist,omitempty" flag:"no-tasklist" desc:"restore a task whose tasklist was purged with no tasklist"`
	Mission    string `json:"mission,omitempty" flag:"mission" desc:"new mission id for the restored row"`
	Plan       string `json:"plan,omitempty" flag:"plan" desc:"new plan id for the restored row"`
	// FromHistory is refused: purged rows never come back (DQ4, 3.6).
	FromHistory bool `json:"from_history,omitempty" flag:"from-history" desc:"refused: a purged row never comes back; read it with trash show --from-history"`
	// Local takes Target as the number of this machine's safety copy
	// (local.go, DQ1).
	Local bool `json:"local,omitempty" flag:"local" desc:"restore this machine's safety copy <n> (lore trash list --local lists them); replaces lore sync trash restore"`
}

// PurgeIn is the input of trash.purge.
type PurgeIn struct {
	Target    string `json:"target,omitempty" arg:"0" desc:"a row id or a batch id (bat_…); or use --older-than / --expired"`
	OlderThan string `json:"older_than,omitempty" flag:"older-than" desc:"purge rows deleted longer ago than this (180d, 720h)"`
	Expired   bool   `json:"expired,omitempty" flag:"expired" desc:"purge rows past trash.retention_days"`
	// ArchivedBefore purges archived rows (archived_at before a date or
	// an age), the v0.1.17 `lore sync purge --archived-before` (DEL-ARCH).
	ArchivedBefore string `json:"archived_before,omitempty" flag:"archived-before" desc:"purge archived rows archived before a date (2006-01-02) or an age (90d)"`
	DryRun         bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"show what would be purged; write nothing"`
	Confirm        bool   `json:"confirm,omitempty" flag:"confirm" desc:"required: purge removes content for good"`
	// Local and Purged together drop this machine's safety copies of rows
	// the repo's _purged.json lists (local_purged.go, DELETION 3.10).
	Local  bool `json:"local,omitempty" flag:"local" desc:"with --purged: this machine's safety copies (_lore_sync_trash) only"`
	Purged bool `json:"purged,omitempty" flag:"purged" desc:"with --local: drop this machine's safety copies of rows purged elsewhere (listed in .lore/data/_purged.json)"`
}

// Item is one row in the trash as listed.
type Item struct {
	ID        string `json:"id"`
	Table     string `json:"table"`
	Store     string `json:"store"`
	Title     string `json:"title"`
	DeletedAt string `json:"deleted_at"`
	DeletedBy string `json:"deleted_by"`
	Reason    string `json:"reason,omitempty"`
	Batch     string `json:"batch"`
	Dependent bool   `json:"dependent,omitempty"`
	// Overruled is the delete_overruled note of a row listed by
	// --overruled (JSON as stored).
	Overruled string `json:"overruled,omitempty"`
	// LastIn is where --from-history read the last content ("<sha>^ (date)").
	LastIn string `json:"last_content_in,omitempty"`
	at     time.Time
	// byLabel is DeletedBy as the text shows it: "@handle" when the roster
	// knows the actor's handle, else its display name, else "" (the text
	// falls back to the act_ id).
	// --json keeps the id in deleted_by.
	byLabel string
}

// ListOut is the output of trash.list.
type ListOut struct {
	Data  []Item `json:"data"`
	Count int    `json:"count"`
	// Dependents is how many dependents of the listed rows are in the
	// trash and not listed (0 with --with-dependents).
	Dependents int `json:"dependents"`
	// Overruled marks a --overruled listing (alive rows, not the trash).
	Overruled bool `json:"overruled,omitempty"`
	// Total is the number of matching rows before --limit / --page; From
	// is the 1-based position of the first listed row (DELETION 4.5).
	Total int `json:"total"`
	From  int `json:"from,omitempty"`
	// History marks a --from-history listing (purged rows, 3.6).
	History bool `json:"from_history,omitempty"`
	// Local marks a --local listing: Copies holds this machine's safety
	// copies and Data stays empty (local.go).
	Local  bool        `json:"local,omitempty"`
	Copies []LocalCopy `json:"copies,omitempty"`
}

// Warning is one engine warning copied into a trash output (later 542).
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ShowOut is the output of trash.show.
type ShowOut struct {
	Data   Item              `json:"data"`
	Fields map[string]string `json:"fields"`
	// History marks a --from-history show (the last content of a purged row).
	History bool `json:"from_history,omitempty"`
}

// StatsIn is the input of trash.stats (none).
type StatsIn struct{}

// Count is one named count of trash.stats.
type Count struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// StatsOut is the output of trash.stats (DELETION 4.5).
type StatsOut struct {
	Rows       int     `json:"rows"`
	Dependents int     `json:"dependents"`
	Expired    int     `json:"expired"`
	ByTable    []Count `json:"by_table"`
	ByStore    []Count `json:"by_store"`
	ByDeleter  []Count `json:"by_deleter"`
	Tombstones []Count `json:"tombstones"`
}

// ResultOut is the output of restore and purge.
type ResultOut struct {
	Action string `json:"action"`
	Batch  string `json:"batch,omitempty"`
	Rows   []Item `json:"rows"`
	Count  int    `json:"count"`
	// Comments are the comments purged (or, in a dry run, that would be)
	// with Rows: a purged row leaves no orphan comments (INTEG-TRASH-6).
	// They are not counted in Count.
	Comments []Comment `json:"comments,omitempty"`
	// Links are the commit links and knowledge links purged (or that
	// would be) with Rows, like Comments; not counted in Count.
	Links  []Link `json:"links,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
	// Local marks `purge --local --purged`: Copies are this machine's
	// safety copies it dropped (or would drop); Rows stays empty.
	Local  bool        `json:"local,omitempty"`
	Copies []LocalCopy `json:"copies,omitempty"`
	// Warnings are the engine's warnings of the write (W_AUDIT_DEFERRED:
	// the audit record is kept with the rows and added later).
	Warnings []Warning `json:"warnings,omitempty"`
}

// Comment is one comment a purge takes with its row (INTEG-TRASH-6).
type Comment struct {
	ID    string `json:"id"`
	Store string `json:"store"`
}

// Link is one commit link or knowledge link a purge takes with its row
// (INTEG-TRASH-6); Table names which.
type Link struct {
	ID    string `json:"id"`
	Table string `json:"table"`
	Store string `json:"store"`
}

// Ops returns the trash operations bound to env, for op.NewRegistry.
func Ops(env Env) []op.Entry {
	o := ops{env: env}
	read := []errs.Code{errs.InvalidInput, errs.InvalidID, errs.NotFound, errs.Unsupported, errs.Internal} // Unsupported: --from-history without git
	write := []errs.Code{errs.InvalidInput, errs.InvalidID, errs.NotFound, errs.Purged, errs.ParentDeleted, errs.ParentPurged, errs.Stale, errs.Internal}
	return []op.Entry{
		op.Define(op.Spec{Name: OpList, Summary: "List the rows in the trash",
			Permission: op.PermRead, Access: op.AccessRead, Errors: read,
			Examples: []op.Example{{Title: "the trash as JSON", CLI: "lore trash list --json", Want: `"data"`}}}, o.list),
		op.Define(op.Spec{Name: OpShow, Summary: "Show one row in the trash, body included (sensitive values masked)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: append(slices.Clone(read), errs.NeedsConfirm, errs.Cancelled),
			Confirm: op.ConfirmRule{When: showSensitiveField}, Examples: []op.Example{{Title: "one row in the trash", CLI: "lore trash show --id rul_1f3a"}}}, o.show),
		op.Define(op.Spec{Name: OpRestore, Summary: "Bring a row (with its dependents) or a whole batch back from the trash, same ids",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(write), errs.NeedsConfirm, errs.SecretDetected, errs.Unsupported), // Unsupported: --local without safety copies
			// MayAsk: the restore re-scan may ask about a secret (P2-DEL-3,
			// FB-SEC-1), so the answer flags (--secret-ref,
			// --secret-unstored, --not-a-secret --reason) and --yes exist.
			Confirm: op.ConfirmRule{MayAsk: true}, Examples: []op.Example{{Title: "bring a row back", CLI: "lore trash restore --target rul_1f3a"}, {Title: "undo a whole bulk delete", CLI: "lore trash restore --target bat_9e02"}}}, o.restore),
		op.Define(op.Spec{Name: OpPurge, Summary: "Remove rows from the trash for good, with their comments and links (a tombstone stays forever); needs --confirm",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(write), errs.NeedsConfirm, errs.Unsupported), Examples: []op.Example{{Title: "see what a purge would remove", CLI: "lore trash purge --older-than 180d --dry-run"}, {Title: "remove one row for good", CLI: "lore trash purge --target rul_1f3a --confirm"}}}, o.purge),
		op.Define(op.Spec{Name: OpStats, Summary: "Count the trash per type, store and deleter, rows past retention, and tombstones",
			Permission: op.PermRead, Access: op.AccessRead, Errors: read,
			Examples: []op.Example{{Title: "trash counts as JSON", CLI: "lore trash stats --json", Want: `"tombstones"`}}}, o.stats),
	}
}

// ops binds the run functions to one Env.
type ops struct{ env Env }

// list is trash.list: every trashed row of every attached store, newest
// first, filtered (DELETION 2.2).
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	since, err := sinceOf(in.Since, time.Now())
	if err != nil {
		return ListOut{}, err
	}
	by, err := o.actorOf(ctx, string(in.By))
	if err != nil {
		return ListOut{}, err
	}
	if in.Local {
		return o.listLocal(ctx, in)
	}
	if in.Overruled {
		return o.overruled(ctx, in)
	}
	if in.FromHistory {
		return o.listHistory(ctx, in, since, by)
	}
	all, err := o.env.Trashed(ctx)
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{Data: []Item{}}
	shown := map[string]bool{} // batches with a listed root
	var deps []Item
	for _, t := range all {
		it := itemOf(t)
		switch {
		case in.To != "" && it.Store != strings.TrimSpace(in.To):
		case in.Type != "" && !typeMatches(it.Table, in.Type):
		case by != "" && it.DeletedBy != by:
		case in.Batch != "" && it.Batch != strings.TrimSpace(in.Batch):
		case in.Reason != "" && !strings.Contains(strings.ToLower(it.Reason), strings.ToLower(in.Reason)):
		case in.Search != "" && !matches(it, in.Search):
		case !since.IsZero() && it.at.Before(since):
		case it.Dependent && !in.WithDependents:
			deps = append(deps, it)
		default:
			out.Data = append(out.Data, it)
		}
	}
	slices.SortStableFunc(out.Data, func(a, b Item) int { return b.at.Compare(a.at) })
	out.Total = len(out.Data)
	if out.Data, out.From, err = page(out.Data, in.Limit, in.Page); err != nil {
		return ListOut{}, err
	}
	for _, it := range out.Data {
		shown[it.Batch] = true // batches with a listed root on this page
	}
	for _, d := range deps {
		if shown[d.Batch] {
			out.Dependents++ // counted under its root (3.9)
		}
	}
	out.Count = len(out.Data)
	o.labelDeleters(ctx, out.Data)
	return out, nil
}

// typeMatches is --type: the table name or its singular ("task" for
// tasks, "memory" for memories), any case.
func typeMatches(table, typ string) bool {
	typ = strings.ToLower(strings.TrimSpace(typ))
	if stem, ok := strings.CutSuffix(table, "ies"); ok && typ == stem+"y" {
		return true
	}
	return typ == table || typ == strings.TrimSuffix(table, "s")
}

// matches is --search: the text in the id, title or delete reason, any case.
func matches(it Item, text string) bool {
	q := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(strings.ToLower(it.ID), q) || strings.Contains(strings.ToLower(it.Title), q) ||
		strings.Contains(strings.ToLower(it.Reason), q)
}

// page cuts rows to --limit / --page (DELETION 4.5) and returns the
// 1-based position of the first row kept (0 when nothing is cut).
func page(rows []Item, limit, pg int) ([]Item, int, error) {
	if limit < 0 || pg < 0 {
		return nil, 0, errs.New(errs.InvalidInput, "trash: --limit and --page take a positive number").
			WithHint("e.g. lore trash list --limit 50 --page 2")
	}
	if pg > 0 && limit == 0 {
		limit = defaultPageRows
	}
	if limit == 0 {
		return rows, 0, nil
	}
	pg = max(pg, 1)
	start := min((pg-1)*limit, len(rows))
	end := min(start+limit, len(rows))
	return rows[start:end], start + 1, nil
}

// maxShownID bounds how much of a malformed id an error quotes (head and
// tail runes), so a huge value never floods the terminal.
const maxShownID = 48

// show is trash.show: one trashed row with its fields.
func (o ops) show(ctx context.Context, in ShowIn) (ShowOut, error) {
	if in.FromHistory {
		return o.showHistory(ctx, in)
	}
	all, err := o.env.Trashed(ctx)
	if err != nil {
		return ShowOut{}, err
	}
	id := strings.TrimSpace(in.ID)
	// B-ERRS-1: a malformed id is E_INVALID_ID, never "not in the trash".
	if _, perr := ids.ParseAny(id); perr != nil {
		return ShowOut{}, lookup.BadID("trash: "+strconv.Quote(textcut.Cut(id, maxShownID, maxShownID))+" is not a row id", perr)
	}
	for _, t := range all {
		if t.Row.ID.String() != id {
			continue
		}
		it := []Item{itemOf(t)}
		o.labelDeleters(ctx, it)
		return ShowOut{Data: it[0], Fields: o.shownFields(ctx, t.Row, in.ShowSensitive)}, nil
	}
	return ShowOut{}, errs.New(errs.NotFound, "trash: "+strconv.Quote(id)+" is not in the trash").WithHint("lore trash list")
}

// hiddenValue replaces a sensitive value in `lore trash show`.
const hiddenValue = "(sensitive, hidden)"

// shownFields is the field map `lore trash show` prints for r, from the
// trash or from git history (DELETION 3.11: protections stay in the trash
// and in history): a field the table marks sensitive is hidden, and so is
// each sensitive entry of a resource's "fields" JSON (a resource kind's own
// fields, RQ1), unless showSensitive. The resource fields are hidden whole
// when the kind fields cannot be read (fail closed).
func (o ops) shownFields(ctx context.Context, r row.Row, showSensitive bool) map[string]string {
	out := map[string]string{}
	def, _ := o.env.TableSchema(ctx, r.Table)
	for k, v := range r.Fields {
		if f, ok := def.Field(k); ok && f.Sensitive && !showSensitive {
			out[k] = hiddenValue
			continue
		}
		out[k] = v.String()
	}
	if _, has := out[resmask.FieldsField]; has && r.Table == schema.TableResources && !showSensitive {
		out[resmask.FieldsField] = hiddenValue
		if getter, canGet := o.env.(resmask.Getter); canGet {
			if m, _, ok := resmask.Fields(ctx, getter, r, hiddenValue); ok {
				out[resmask.FieldsField] = m
			}
		}
	}
	return out
}

// restore is trash.restore (DELETION 3.3): the engine clears the delete
// fields of the row and its batch dependents, or of the whole batch.
func (o ops) restore(ctx context.Context, in TargetIn) (ResultOut, error) {
	if in.FromHistory {
		return ResultOut{}, purgedForever(in.Target)
	}
	if in.Local {
		return o.restoreLocal(ctx, in)
	}
	homes, err := homesOf(in)
	if err != nil {
		return ResultOut{}, err
	}
	if len(homes) > 0 {
		ctx = engine.WithRestoreHome(ctx, homes...)
	}
	res, err := o.env.Restore(ctx, in.Target)
	if err != nil {
		return ResultOut{}, err
	}
	return resultOf("restored", res, false), nil
}

// purge is trash.purge (DELETION 3.5, 2.6): by target, --older-than or
// --expired; DQ2: only the literal --confirm purges (a dry run needs none).
func (o ops) purge(ctx context.Context, in PurgeIn) (ResultOut, error) {
	if in.Local || in.Purged {
		return o.purgeLocalPurged(ctx, in)
	}
	target := strings.TrimSpace(in.Target)
	modes := 0
	before := strings.TrimSpace(in.ArchivedBefore)
	for _, set := range []bool{target != "", in.OlderThan != "", in.Expired, before != ""} {
		if set {
			modes++
		}
	}
	if modes != 1 {
		return ResultOut{}, errs.New(errs.InvalidInput, "trash purge: give exactly one of <id|batch>, --older-than, --expired or --archived-before").
			WithHint("see what is there with `lore trash list`, then e.g. lore trash purge --target <id> --confirm")
	}
	if before != "" {
		return o.purgeArchived(ctx, before, in.DryRun, in.Confirm)
	}
	if target != "" {
		if in.DryRun {
			res, err := o.env.PurgePlan(ctx, target)
			if err != nil {
				return ResultOut{}, err
			}
			return resultOf("would purge", res, true), nil
		}
		if !in.Confirm {
			return ResultOut{}, needsConfirm("purge removes "+target+" (its dependents in the trash, its comments and links too) for good; only a tombstone stays", "trash", "purge", target, "--confirm")
		}
		res, err := o.env.PurgeTrash(ctx, target)
		if err != nil {
			return ResultOut{}, err
		}
		if err = o.dropLocalCopies(ctx, res); err != nil {
			return ResultOut{}, err
		}
		return resultOf("purged", res, false), nil
	}
	cutoff := time.Time{}
	args := []string{"trash", "purge", "--expired", "--confirm"}
	if in.OlderThan != "" {
		d, err := durationOf(in.OlderThan)
		if err != nil {
			return ResultOut{}, err
		}
		cutoff = time.Now().Add(-d)
		args = []string{"trash", "purge", "--older-than", in.OlderThan, "--confirm"}
	}
	if !in.DryRun && !in.Confirm {
		return ResultOut{}, needsConfirm("purge removes every matching row in the trash (and their comments and links) for good; only tombstones stay", args...)
	}
	res, err := o.env.PurgeOlder(ctx, cutoff, in.Expired, in.DryRun)
	if err != nil {
		return ResultOut{}, err
	}
	if in.DryRun {
		return resultOf("would purge", res, true), nil
	}
	if err = o.dropLocalCopies(ctx, res); err != nil {
		return ResultOut{}, err
	}
	return resultOf("purged", res, false), nil
}

// ArchivedPurger is the optional Env upgrade `purge --archived-before`
// uses (wire.App has it): purge, or with dryRun list, every row of the
// attached stores archived before cutoff (DELETION 3.12, DEL-ARCH).
type ArchivedPurger interface {
	PurgeArchived(ctx context.Context, cutoff time.Time, dryRun bool) (engine.TrashResult, error)
}

// purgeArchived is `trash purge --archived-before <date|age>` (DEL-ARCH):
//
//	cutoff ─► dry run: list ─► else no --confirm: E_NEEDS_CONFIRM (DQ2)
//	       ─► ArchivedPurger.PurgeArchived ─► tombstones in the row files
//
// It replaces the v0.1.17 lore.db purge (lsync); rows in the trash that
// were archived are purged too.
func (o ops) purgeArchived(ctx context.Context, before string, dry, confirm bool) (ResultOut, error) {
	cutoff, err := sinceOf(before, time.Now())
	if err != nil {
		return ResultOut{}, err
	}
	p, ok := upgrade.As[ArchivedPurger](o.env)
	if !ok {
		return ResultOut{}, errs.New(errs.Unsupported, "trash purge: --archived-before is not available here")
	}
	if !dry && !confirm {
		return ResultOut{}, needsConfirm("purge removes every row archived before "+before+" for good; only tombstones stay",
			"trash", "purge", "--archived-before", before, "--confirm")
	}
	res, err := p.PurgeArchived(ctx, cutoff, dry)
	if err != nil {
		return ResultOut{}, err
	}
	if dry {
		return resultOf("would purge", res, true), nil
	}
	if err = o.dropLocalCopies(ctx, res); err != nil {
		return ResultOut{}, err
	}
	return resultOf("purged", res, false), nil
}

// overruled is `trash list --overruled` (DELETION 3.7): the alive rows a
// merge kept over a delete, so the deleter can look again; --to and
// --type narrow it like the trash.
func (o ops) overruled(ctx context.Context, in ListIn) (ListOut, error) {
	rows, err := o.env.Overruled(ctx)
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{Data: []Item{}, Overruled: true}
	for _, r := range rows {
		it := Item{ID: r.Row.ID.String(), Table: r.Row.Table.String(), Store: string(r.Store), Title: titleOf(r.Row), Overruled: r.Note}
		if (in.To != "" && it.Store != strings.TrimSpace(in.To)) || (in.Type != "" && !typeMatches(it.Table, in.Type)) {
			continue
		}
		out.Data = append(out.Data, it)
	}
	out.Count = len(out.Data)
	return out, nil
}

// stats is trash.stats (DELETION 4.5).
func (o ops) stats(ctx context.Context, _ StatsIn) (StatsOut, error) {
	st, err := o.env.TrashStats(ctx)
	if err != nil {
		return StatsOut{}, err
	}
	return StatsOut{Rows: st.Rows, Dependents: st.Dependents, Expired: st.Expired,
		ByTable: counts(st.ByTable), ByStore: counts(st.ByStore), ByDeleter: counts(st.ByDeleter), Tombstones: counts(st.Tombstones)}, nil
}

// counts is m as a list, largest first, then by name.
func counts(m map[string]int) []Count {
	out := make([]Count, 0, len(m))
	for k, v := range m {
		out = append(out, Count{Name: k, Count: v})
	}
	slices.SortFunc(out, func(a, b Count) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// Text implements op.Texter.
func (o StatsOut) Text() string {
	var b strings.Builder
	b.WriteString(count(o.Rows, "row") + " in the trash (" + count(o.Dependents, "dependent") + "), " +
		strconv.Itoa(o.Expired) + " past trash.retention_days\n")
	for _, sec := range []struct {
		name string
		c    []Count
	}{{"by type", o.ByTable}, {"by store", o.ByStore}, {"by deleter", o.ByDeleter}, {"tombstones (kept forever)", o.Tombstones}} {
		if len(sec.c) == 0 {
			continue
		}
		b.WriteString(sec.name + "\n")
		for _, c := range sec.c {
			name := c.Name
			if name == "" {
				name = "(unknown)"
			}
			b.WriteString("  " + name + "  " + strconv.Itoa(c.Count) + "\n")
		}
	}
	return b.String()
}

// homesOf turns the restore home flags into engine.RestoreHomes (4.2):
// --parent and --no-parent (and --tasklist and --no-tasklist) exclude each
// other; an id that does not parse is E_INVALID_ID.
func homesOf(in TargetIn) ([]engine.RestoreHome, error) {
	type flag struct {
		name, field, id string
		clear           bool
	}
	pairs := []flag{
		{"parent", schema.FieldTaskParent, in.Parent, in.NoParent},
		{"tasklist", fieldTasklist, in.Tasklist, in.NoTasklist},
		{"mission", fieldMission, in.Mission, false},
		{"plan", fieldPlan, in.Plan, false},
	}
	var out []engine.RestoreHome
	for _, f := range pairs {
		id := strings.TrimSpace(f.id)
		switch {
		case id != "" && f.clear:
			return nil, errs.New(errs.InvalidInput, "trash restore: give --"+f.name+" or --no-"+f.name+", not both")
		case f.clear:
			out = append(out, engine.RestoreHome{Field: f.field, Clear: true})
		case id != "":
			pid, err := ids.ParseAny(id)
			if err != nil {
				return nil, errs.Wrap(errs.InvalidID, "trash restore: --"+f.name+" "+strconv.Quote(id)+" is not an id", err)
			}
			out = append(out, engine.RestoreHome{Field: f.field, To: pid})
		}
	}
	return out, nil
}

// Pointer fields the restore home flags replace (schema/builtin_work.go).
const (
	fieldTasklist = "tasklist_id"
	fieldMission  = "mission_id"
	fieldPlan     = "plan_id"
)

// Actors is the optional Env upgrade --by uses to find an actor by handle,
// display name or stable key (wire.App has it); without it --by takes an
// act_ id only (XQ8: --by always names an actor).
type Actors interface {
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
	Stores() []store.Source
}

// Actor fields --by matches.
const (
	handleSign = "@"
	fieldHand  = "handle"
	fieldName  = "display_name"
	fieldKey   = "stable_key"
)

// actorOf resolves --by to an act_ id string ("" = no filter).
func (o ops) actorOf(ctx context.Context, by string) (string, error) {
	by = strings.TrimSpace(by)
	if by == "" {
		return "", nil
	}
	if id, err := ids.Parse[ids.Actor](by); err == nil {
		return id.String(), nil
	}
	a, ok := upgrade.As[Actors](o.env)
	if !ok {
		return "", errs.New(errs.InvalidInput, "trash list: --by "+by+": pass the act_ id")
	}
	name := strings.TrimPrefix(by, handleSign)
	for _, src := range engine.ReadStoresOf(a) {
		rows, err := a.List(ctx, src, schema.TableActors)
		if err != nil {
			return "", err
		}
		for _, r := range rows {
			if fieldText(r, fieldHand) == name || fieldText(r, fieldName) == name || fieldText(r, fieldKey) == by {
				return r.ID.String(), nil
			}
		}
	}
	return "", errs.New(errs.NotFound, "trash list: no actor "+by+" in the attached stores").WithHint("lore actor list")
}

// fieldText reads a text field ("" when absent).
func fieldText(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// needsConfirm is E_NEEDS_CONFIRM naming --confirm (DQ2: --yes never purges).
func needsConfirm(msg string, args ...string) error {
	return errs.New(errs.NeedsConfirm, "trash: "+msg).
		WithHint("ask the person, then re-run with --confirm").
		WithQuestion(msg+"?").
		WithNext("lore", args...)
}

// itemOf is one listed row.
func itemOf(t engine.Trashed) Item {
	it := Item{ID: t.Row.ID.String(), Table: t.Row.Table.String(), Store: string(t.Store), Title: titleOf(t.Row),
		DeletedBy: t.Info.By, Reason: t.Info.Reason, Batch: t.Info.Batch, Dependent: t.Dependent, at: t.Info.At}
	if !t.Info.At.IsZero() {
		it.DeletedAt = t.Info.At.UTC().Format(jsonTimeLayout)
	}
	return it
}

// Title is r's label as the `lore sql` trash view lists it (DELETION 3.2):
// titleOf, or the mask when mask is set and the field it came from is
// sensitive in def (3.11: protections stay in the trash).
func Title(r row.Row, def schema.TableDef, mask bool) string {
	s, field := labelOf(r)
	if f, ok := def.Field(field); ok && mask && f.Sensitive && s != "" {
		return maskedTitle
	}
	return s
}

// maskedTitle replaces a sensitive title (the lens's own mask).
const maskedTitle = "•••"

// titleOf is a row's label: its first title-like field, one line, cut.
func titleOf(r row.Row) string {
	s, _ := labelOf(r)
	return s
}

// labelOf is titleOf plus the field it came from.
func labelOf(r row.Row) (string, string) {
	for _, f := range titleFields {
		v, ok := r.Fields[f]
		if !ok || v == nil || v.Kind() == value.KindNull {
			continue
		}
		s := strings.Join(strings.Fields(v.String()), " ")
		if rs := []rune(s); len(rs) > titleCut {
			s = string(rs[:titleCut]) + "…"
		}
		return s, f
	}
	return "", ""
}

// resultOf renders a restore or purge result.
func resultOf(action string, res engine.TrashResult, dry bool) ResultOut {
	out := ResultOut{Action: action, Batch: res.Batch, Rows: []Item{}, DryRun: dry}
	for _, t := range res.Rows {
		it := itemOf(t)
		if it.Batch == "" {
			it.Batch = res.Batch
		}
		out.Rows = append(out.Rows, it)
	}
	out.Count = len(out.Rows)
	for _, c := range res.Taken {
		if c.Row.Table == schema.TableComments {
			out.Comments = append(out.Comments, Comment{ID: c.Row.ID.String(), Store: string(c.Store)})
			continue
		}
		out.Links = append(out.Links, Link{ID: c.Row.ID.String(), Table: string(c.Row.Table), Store: string(c.Store)})
	}
	for _, w := range res.Warnings {
		out.Warnings = append(out.Warnings, Warning{Code: string(w.Code), Message: w.Message})
	}
	return out
}

// sinceOf parses --since: a date, a Go duration or a day count ("7d").
func sinceOf(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.ParseInLocation(dateLayout, s, time.Local); err == nil {
		return t, nil
	}
	d, err := durationOf(s)
	if err != nil {
		return time.Time{}, err
	}
	return now.Add(-d), nil
}

// durationOf parses a Go duration or a day count ("180d").
func durationOf(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if n, ok := strings.CutSuffix(s, daySuffix); ok {
		if d, err := strconv.Atoi(n); err == nil && d >= 0 {
			return time.Duration(d) * hoursInDay * time.Hour, nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil && d >= 0 {
		return d, nil
	}
	return 0, errs.New(errs.InvalidInput, "trash: "+strconv.Quote(s)+": want a duration (36h, 180d) or a date (YYYY-MM-DD)").
		WithHint("write it as 7d, 36h or 2026-01-31")
}

// Text implements op.Texter.
func (o ListOut) Text() string {
	if o.Local {
		return o.localText()
	}
	if o.Overruled {
		var b strings.Builder
		for _, it := range o.Data {
			b.WriteString(it.line())
		}
		b.WriteString(count(o.Count, "row") + " kept alive over a delete; delete one again to settle it\n")
		return b.String()
	}
	if len(o.Data) == 0 && o.Total > 0 {
		return "no rows on this page (" + strconv.Itoa(o.Total) + " match)\n"
	}
	if o.History {
		return o.historyText()
	}
	if len(o.Data) == 0 {
		return "the trash is empty\n"
	}
	var b strings.Builder
	for _, it := range o.Data {
		b.WriteString(it.line())
	}
	b.WriteString(count(o.Count, "row"))
	if o.Dependents > 0 {
		b.WriteString(" (and " + count(o.Dependents, "dependent") + ")")
	}
	b.WriteString(" in the trash")
	if o.From > 0 { // a page of a larger list (4.5): "1–50 of 4812"
		b.WriteString(" (" + strconv.Itoa(o.From) + "–" + strconv.Itoa(o.From+o.Count-1) + " of " + strconv.Itoa(o.Total) + ")")
	}
	b.WriteString("\n")
	return b.String()
}

// when is the deletion time as the text shows it (local, minutes); an
// item decoded from JSON (no at) shows deleted_at as given.
func (it Item) when() string {
	if it.at.IsZero() {
		return it.DeletedAt
	}
	return it.at.Local().Format(timeLayout)
}

// deleter is who deleted the row as the text shows it: the person's
// @handle when the roster knows it, else their display name, else the
// act_ id as stored.
func (it Item) deleter() string {
	if it.byLabel != "" {
		return it.byLabel
	}
	return it.DeletedBy
}

// labelDeleters sets each item's @handle or display name from the actors
// of the attached stores (an Env without the Actors upgrade, or an actor
// with neither, keeps the id). A failed roster read only costs the label.
func (o ops) labelDeleters(ctx context.Context, items []Item) {
	if len(items) == 0 {
		return
	}
	a, ok := upgrade.As[Actors](o.env)
	if !ok {
		return
	}
	handles := map[string]string{}
	for _, src := range engine.ReadStoresOf(a) {
		rows, err := a.List(ctx, src, schema.TableActors)
		if err != nil {
			return
		}
		for _, r := range rows {
			if r.Deleted {
				continue
			}
			// the @handle, else the display name (bug hunt B3-11: an actor
			// with no handle, the common case on a fresh machine, showed
			// as its act_ id while every other list showed the name)
			if h := fieldText(r, fieldHand); h != "" {
				handles[r.ID.String()] = handleSign + h
			} else if n := fieldText(r, fieldName); n != "" {
				handles[r.ID.String()] = n
			}
		}
	}
	for i := range items {
		items[i].byLabel = handles[items[i].DeletedBy]
	}
}

// line is one row of `lore trash list`.
func (it Item) line() string {
	if it.Overruled != "" {
		return it.ID + "  " + it.Table + "  " + strconv.Quote(it.Title) + "  kept over a delete: " + it.Overruled + "\n"
	}
	s := it.ID + "  " + it.Table + "  " + strconv.Quote(it.Title) + "  " + it.when() + "  by " + it.deleter()
	if it.Reason != "" {
		s += "  reason " + strconv.Quote(it.Reason)
	}
	return s + "  " + it.Batch + "\n"
}

// Text implements op.Texter.
func (o ShowOut) Text() string {
	var b strings.Builder
	if o.History {
		b.WriteString(o.Data.Table + " " + o.Data.ID + "  " + strconv.Quote(o.Data.Title) + "  purged " + o.Data.when() + " by " + o.Data.deleter() + "\n")
		b.WriteString("  from commit " + o.Data.LastIn + " (the last commit that had it)\n")
		b.WriteString(fieldLines(o.Fields))
		return b.String()
	}
	b.WriteString(o.Data.Table + " " + o.Data.ID + "  " + strconv.Quote(o.Data.Title) + "  in the trash\n")
	b.WriteString("  deleted   " + o.Data.when() + " by " + o.Data.deleter() + " (store " + o.Data.Store + ")\n")
	if o.Data.Dependent {
		b.WriteString("  dependent of another row of its batch\n")
	}
	if o.Data.Reason != "" {
		b.WriteString("  reason    " + o.Data.Reason + "\n")
	}
	b.WriteString("  batch     " + o.Data.Batch + "\n")
	b.WriteString(fieldLines(o.Fields))
	return b.String()
}

// fieldLines renders fields as "  key: value" lines in key order.
func fieldLines(fields map[string]string) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString("  " + k + ": " + fields[k] + "\n")
	}
	return b.String()
}

// historyText is ListOut.Text of a --from-history listing (DELETION 2.7).
func (o ListOut) historyText() string {
	if len(o.Data) == 0 {
		return "no purged or hard-deleted rows in this place's git history\n"
	}
	var b strings.Builder
	for _, it := range o.Data {
		b.WriteString(it.ID + "  " + it.Table + "  " + strconv.Quote(it.Title) + "  purged " + it.when() + "  by " + it.deleter() + "  last content in " + it.LastIn + "\n")
	}
	b.WriteString(count(o.Count, "row") + " in git history; they never come back (read one: lore trash show --id <id> --from-history)\n")
	return b.String()
}

// Text implements op.Texter.
func (o ResultOut) Text() string {
	if o.Local {
		return o.localPurgedText()
	}
	var b strings.Builder
	b.WriteString(o.Action + " " + count(o.Count, "row"))
	if o.Batch != "" {
		b.WriteString(" (batch " + o.Batch + ")")
	}
	b.WriteString("\n")
	for _, it := range o.Rows {
		b.WriteString("  " + it.ID + " (" + it.Table + ", " + it.Store + ")\n")
	}
	if len(o.Comments) > 0 {
		b.WriteString("with " + count(len(o.Comments), "comment") + " on them\n")
		for _, c := range o.Comments {
			b.WriteString("  " + c.ID + " (comments, " + c.Store + ")\n")
		}
	}
	if len(o.Links) > 0 {
		b.WriteString("with " + count(len(o.Links), "link") + " to them\n")
		for _, l := range o.Links {
			b.WriteString("  " + l.ID + " (" + l.Table + ", " + l.Store + ")\n")
		}
	}
	if o.Action == "purged" {
		b.WriteString("tombstones are kept forever; the old content is still in git history (rotate any leaked secret)\n")
	}
	if o.DryRun {
		b.WriteString("dry run: nothing written\n")
	}
	for _, w := range o.Warnings {
		b.WriteString("note: " + w.Message + "\n")
	}
	return b.String()
}

// count is "1 row" / "2 rows": n and the word, plural unless n is 1 (the
// trash texts used to print "1 rows").
func count(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
