// Package trash defines the `trash` noun (layer L5, DELETION_DESIGN 2.2-2.6,
// CLI_REFERENCE 3.9): list, show, restore and purge of rows in the trash.
//
//	lore trash list [--type --by --since --reason --batch --to --with-dependents]
//	    └► Env.Trashed ─► filter ─► roots (dependents counted) ─► newest first
//	lore trash show <id> [--show-sensitive]
//	    └► Env.Trashed ─► the one row (body kept; sensitive masked unless asked)
//	lore trash restore <id|bat_> [--parent|--tasklist|--mission|--plan <id>, --no-parent|--no-tasklist]
//	    └► engine.WithRestoreHome (4.2) ─► Env.Restore (same ids, E_PARENT_*)
//	lore trash purge <id|bat_> | --older-than <d> | --expired  --confirm
//	    └► no --confirm: E_NEEDS_CONFIRM naming --confirm (DQ2, never --yes)
//	    └► Env.PurgeTrash / Env.PurgeOlder ─► tombstones kept forever (DQ4)
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
	timeLayout = "2006-01-02 15:04"
	dateLayout = "2006-01-02"
	daySuffix  = "d"
	hoursInDay = 24
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
	To     string      `json:"to,omitempty" flag:"to" desc:"only rows of this store (repo, me, an attached store name)"`
	// WithDependents lists the rows that went into the trash as dependents
	// of another row (links, comments, tag attachments) instead of counting
	// them under it (CLI_REFERENCE 3.9).
	WithDependents bool `json:"with_dependents,omitempty" flag:"with-dependents" desc:"list dependents (links, comments, tag attachments) instead of counting them"`
}

// ShowIn is the input of trash.show.
type ShowIn struct {
	ID            string `json:"id" arg:"0" desc:"the row id"`
	ShowSensitive bool   `json:"show_sensitive,omitempty" flag:"show-sensitive" desc:"print sensitive field values (asks first)"`
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
	Tasklist   string `json:"tasklist,omitempty" flag:"tasklist" desc:"new tasklist (tl_…) for a task whose tasklist was purged"`
	NoTasklist bool   `json:"no_tasklist,omitempty" flag:"no-tasklist" desc:"restore a task whose tasklist was purged with no tasklist"`
	Mission    string `json:"mission,omitempty" flag:"mission" desc:"new mission id for the restored row"`
	Plan       string `json:"plan,omitempty" flag:"plan" desc:"new plan id for the restored row"`
}

// PurgeIn is the input of trash.purge.
type PurgeIn struct {
	Target    string `json:"target,omitempty" arg:"0" desc:"a row id or a batch id (bat_…); or use --older-than / --expired"`
	OlderThan string `json:"older_than,omitempty" flag:"older-than" desc:"purge rows deleted longer ago than this (180d, 720h)"`
	Expired   bool   `json:"expired,omitempty" flag:"expired" desc:"purge rows past trash.retention_days"`
	DryRun    bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"show what would be purged; write nothing"`
	Confirm   bool   `json:"confirm,omitempty" flag:"confirm" desc:"required: purge removes content for good (DQ2)"`
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
	at        time.Time
}

// ListOut is the output of trash.list.
type ListOut struct {
	Data  []Item `json:"data"`
	Count int    `json:"count"`
	// Dependents is how many dependents of the listed rows are in the
	// trash and not listed (0 with --with-dependents).
	Dependents int `json:"dependents"`
}

// ShowOut is the output of trash.show.
type ShowOut struct {
	Data   Item              `json:"data"`
	Fields map[string]string `json:"fields"`
}

// ResultOut is the output of restore and purge.
type ResultOut struct {
	Action string `json:"action"`
	Batch  string `json:"batch,omitempty"`
	Rows   []Item `json:"rows"`
	Count  int    `json:"count"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// Ops returns the trash operations bound to env, for op.NewRegistry.
func Ops(env Env) []op.Entry {
	o := ops{env: env}
	read := []errs.Code{errs.InvalidInput, errs.NotFound, errs.Internal}
	write := []errs.Code{errs.InvalidInput, errs.NotFound, errs.Purged, errs.ParentDeleted, errs.ParentPurged, errs.Stale, errs.Internal}
	return []op.Entry{
		op.Define(op.Spec{Name: OpList, Summary: "List the rows in the trash",
			Permission: op.PermRead, Access: op.AccessRead, Errors: read,
			Examples: []op.Example{{Title: "the trash as JSON", CLI: "lore trash list --json", Want: `"data"`}}}, o.list),
		op.Define(op.Spec{Name: OpShow, Summary: "Show one row in the trash, body included (sensitive values masked)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: append(slices.Clone(read), errs.NeedsConfirm, errs.Cancelled),
			Confirm: op.ConfirmRule{When: showSensitiveField}}, o.show),
		op.Define(op.Spec{Name: OpRestore, Summary: "Bring a row (with its dependents) or a whole batch back from the trash, same ids",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: write}, o.restore),
		op.Define(op.Spec{Name: OpPurge, Summary: "Remove rows from the trash for good (a tombstone stays forever); needs --confirm",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(write), errs.NeedsConfirm)}, o.purge),
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
		case in.Type != "" && it.Table != in.Type && strings.TrimSuffix(it.Table, "s") != in.Type:
		case by != "" && it.DeletedBy != by:
		case in.Batch != "" && it.Batch != strings.TrimSpace(in.Batch):
		case in.Reason != "" && !strings.Contains(strings.ToLower(it.Reason), strings.ToLower(in.Reason)):
		case !since.IsZero() && it.at.Before(since):
		case it.Dependent && !in.WithDependents:
			deps = append(deps, it)
		default:
			out.Data = append(out.Data, it)
			shown[it.Batch] = true
		}
	}
	for _, d := range deps {
		if shown[d.Batch] {
			out.Dependents++ // counted under its root (3.9)
		}
	}
	slices.SortStableFunc(out.Data, func(a, b Item) int { return b.at.Compare(a.at) })
	out.Count = len(out.Data)
	return out, nil
}

// show is trash.show: one trashed row with its fields.
func (o ops) show(ctx context.Context, in ShowIn) (ShowOut, error) {
	all, err := o.env.Trashed(ctx)
	if err != nil {
		return ShowOut{}, err
	}
	id := strings.TrimSpace(in.ID)
	for _, t := range all {
		if t.Row.ID.String() != id {
			continue
		}
		out := ShowOut{Data: itemOf(t), Fields: map[string]string{}}
		def, _ := o.env.TableSchema(ctx, t.Row.Table)
		for k, v := range t.Row.Fields {
			if f, ok := def.Field(k); ok && f.Sensitive && !in.ShowSensitive {
				out.Fields[k] = "(sensitive, hidden)" // 3.11: protections stay in the trash
				continue
			}
			out.Fields[k] = v.String()
		}
		return out, nil
	}
	return ShowOut{}, errs.New(errs.NotFound, "trash: "+strconv.Quote(id)+" is not in the trash").WithHint("lore trash list")
}

// restore is trash.restore (DELETION 3.3): the engine clears the delete
// fields of the row and its batch dependents, or of the whole batch.
func (o ops) restore(ctx context.Context, in TargetIn) (ResultOut, error) {
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
	target := strings.TrimSpace(in.Target)
	modes := 0
	for _, set := range []bool{target != "", in.OlderThan != "", in.Expired} {
		if set {
			modes++
		}
	}
	if modes != 1 {
		return ResultOut{}, errs.New(errs.InvalidInput, "trash purge: give exactly one of <id|batch>, --older-than or --expired")
	}
	if target != "" {
		if in.DryRun {
			res, err := o.env.TrashTarget(ctx, target)
			if err != nil {
				return ResultOut{}, err
			}
			return resultOf("would purge", res, true), nil
		}
		if !in.Confirm {
			return ResultOut{}, needsConfirm("purge removes "+target+" (and its dependents in the trash) for good; only a tombstone stays", "trash", "purge", target, "--confirm")
		}
		res, err := o.env.PurgeTrash(ctx, target)
		if err != nil {
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
		return ResultOut{}, needsConfirm("purge removes every matching row in the trash for good; only tombstones stay", args...)
	}
	res, err := o.env.PurgeOlder(ctx, cutoff, in.Expired, in.DryRun)
	if err != nil {
		return ResultOut{}, err
	}
	if in.DryRun {
		return resultOf("would purge", res, true), nil
	}
	return resultOf("purged", res, false), nil
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
	for _, src := range a.Stores() {
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
		it.DeletedAt = t.Info.At.Local().Format(timeLayout)
	}
	return it
}

// titleOf is a row's label: its first title-like field, one line, cut.
func titleOf(r row.Row) string {
	for _, f := range titleFields {
		v, ok := r.Fields[f]
		if !ok || v == nil || v.Kind() == value.KindNull {
			continue
		}
		s := strings.Join(strings.Fields(v.String()), " ")
		if rs := []rune(s); len(rs) > titleCut {
			s = string(rs[:titleCut]) + "…"
		}
		return s
	}
	return ""
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
	return 0, errs.New(errs.InvalidInput, "trash: "+strconv.Quote(s)+": want a duration (36h, 180d) or a date (YYYY-MM-DD)")
}

// Text implements op.Texter.
func (o ListOut) Text() string {
	if len(o.Data) == 0 {
		return "the trash is empty\n"
	}
	var b strings.Builder
	for _, it := range o.Data {
		b.WriteString(it.line())
	}
	b.WriteString(strconv.Itoa(o.Count) + " rows")
	if o.Dependents > 0 {
		b.WriteString(" (and " + strconv.Itoa(o.Dependents) + " dependents)")
	}
	b.WriteString(" in the trash\n")
	return b.String()
}

// line is one row of `lore trash list`.
func (it Item) line() string {
	s := it.ID + "  " + it.Table + "  " + strconv.Quote(it.Title) + "  " + it.DeletedAt + "  by " + it.DeletedBy
	if it.Reason != "" {
		s += "  reason " + strconv.Quote(it.Reason)
	}
	return s + "  " + it.Batch + "\n"
}

// Text implements op.Texter.
func (o ShowOut) Text() string {
	var b strings.Builder
	b.WriteString(o.Data.Table + " " + o.Data.ID + "  " + strconv.Quote(o.Data.Title) + "  in the trash\n")
	b.WriteString("  deleted   " + o.Data.DeletedAt + " by " + o.Data.DeletedBy + " (store " + o.Data.Store + ")\n")
	if o.Data.Dependent {
		b.WriteString("  dependent of another row of its batch\n")
	}
	if o.Data.Reason != "" {
		b.WriteString("  reason    " + o.Data.Reason + "\n")
	}
	b.WriteString("  batch     " + o.Data.Batch + "\n")
	keys := make([]string, 0, len(o.Fields))
	for k := range o.Fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		b.WriteString("  " + k + ": " + o.Fields[k] + "\n")
	}
	return b.String()
}

// Text implements op.Texter.
func (o ResultOut) Text() string {
	var b strings.Builder
	b.WriteString(o.Action + " " + strconv.Itoa(o.Count) + " rows")
	if o.Batch != "" {
		b.WriteString(" (batch " + o.Batch + ")")
	}
	b.WriteString("\n")
	for _, it := range o.Rows {
		b.WriteString("  " + it.ID + " (" + it.Table + ", " + it.Store + ")\n")
	}
	if o.Action == "purged" {
		b.WriteString("tombstones are kept forever; the old content is still in git history (rotate any leaked secret)\n")
	}
	if o.DryRun {
		b.WriteString("dry run: nothing written\n")
	}
	return b.String()
}
