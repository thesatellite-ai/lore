package task

// checklist.go — `lore task checklist add|tick|untick|edit|rm|list|import|
// promote` (TASK_SYSTEM_DESIGN 7.3, 7.4) and the done rule (TSQ14).
//
//	op ─► getChecklist (row + model/checklist.Decode)
//	   ─► checklist.Resolve (position or chk_ id) ─► mutate item (stamps)
//	   ─► putChecklist ─► engine Update (one write; merge by item id)
//	task done ─► openItemsGuard: E_OPEN_ITEMS | --tick-all (same write) | --force
//
// The noun is `checklist` because `lore check` already means leak checks.
// Ticking never changes the task status (7.4).

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/tasktree"
	"github.com/thesatellite-ai/lore/model/checklist"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Checklist operation names (7.3).
const (
	OpChecklistAdd     = "task.checklist.add"
	OpChecklistTick    = "task.checklist.tick"
	OpChecklistUntick  = "task.checklist.untick"
	OpChecklistEdit    = "task.checklist.edit"
	OpChecklistRm      = "task.checklist.rm"
	OpChecklistList    = "task.checklist.list"
	OpChecklistImport  = "task.checklist.import"
	OpChecklistPromote = "task.checklist.promote"
	// KindChecklist is the output kind of task.checklist.list.
	KindChecklist = "task.checklist"
)

// fieldChecklist is the task row's checklist (schema.FieldTaskChecklist).
const fieldChecklist = schema.FieldTaskChecklist

// importFromBody is the only --from source of checklist import (XQ9).
const importFromBody = "body"

// checklistHelp is the rule of thumb printed in --help (7.1).
const checklistHelp = "; if you would ever claim it, make it a sub-task"

// ChecklistAddIn is the input of task.checklist.add.
type ChecklistAddIn struct {
	ID    string   `json:"id" arg:"0" desc:"the task id (tsk_…)"`
	Text  string   `json:"text,omitempty" arg:"1" desc:"the item text (one line, up to 200 characters)"`
	Items []string `json:"items,omitempty" flag:"item" desc:"another item; repeatable, added in order after the positional one"`
}

// ChecklistItemIn names one item of a task (tick, untick, rm).
type ChecklistItemIn struct {
	ID string `json:"id" arg:"0" desc:"the task id (tsk_…)"`
	// Item is checked by the handler after the task lookup (omitempty), so
	// a bad task id answers E_NOT_FOUND first (ops/internal/lookup).
	Item string `json:"item,omitempty" arg:"1" required:"after-lookup" desc:"the item: its position (1, 2, …) or its chk_ id"`
}

// ChecklistEditIn is the input of task.checklist.edit.
type ChecklistEditIn struct {
	ID string `json:"id" arg:"0" desc:"the task id (tsk_…)"`
	// Item and Text are checked by the handler after the task lookup
	// (omitempty), so a bad task id answers E_NOT_FOUND first.
	Item string `json:"item,omitempty" arg:"1" required:"after-lookup" desc:"the item: its position (1, 2, …) or its chk_ id"`
	Text string `json:"text,omitempty" arg:"2" rest:"true" required:"after-lookup" desc:"the new text"`
}

// ChecklistImportIn is the input of task.checklist.import.
type ChecklistImportIn struct {
	ID   string `json:"id" arg:"0" desc:"the task id (tsk_…)"`
	From string `json:"from" flag:"from" desc:"where to read items: body (the task's - [ ] / - [x] lines)"`
}

// ChecklistPromoteIn is the input of task.checklist.promote.
type ChecklistPromoteIn struct {
	ID     string   `json:"id" arg:"0" desc:"the task id (tsk_…)"`
	Item   string   `json:"item" arg:"1" desc:"the item: its position (1, 2, …) or its chk_ id"`
	Assign []string `json:"assign,omitempty" flag:"assign" desc:"assign the new sub-task to @handle or an actor (repeatable; the first is the lead)"`
}

// ChecklistItemOut is one item of a checklist output.
type ChecklistItemOut struct {
	Position int    `json:"position,omitempty"`
	ID       string `json:"id"`
	Text     string `json:"text"`
	Done     bool   `json:"done"`
	DoneAt   string `json:"done_at,omitempty"`
	DoneBy   string `json:"done_by,omitempty"`
	Deleted  bool   `json:"deleted,omitempty"`
}

// ChecklistData is one task's checklist.
type ChecklistData struct {
	TaskID string             `json:"task_id"`
	Done   int                `json:"done"`
	Total  int                `json:"total"`
	Items  []ChecklistItemOut `json:"items"`
}

// ChecklistOut is the output of task.checklist.list and of the writes
// (the checklist after the change); Human is the write's line.
type ChecklistOut struct {
	SchemaVersion int           `json:"schema_version"`
	Kind          string        `json:"kind"`
	Data          ChecklistData `json:"data"`
	// Changed are the ids the write touched (added, ticked, …).
	Changed []string `json:"changed,omitempty"`
	Store   string   `json:"store,omitempty"`
	Human   string   `json:"-"`
}

// Text implements op.Texter: the write line, else the list.
func (o ChecklistOut) Text() string {
	if o.Human != "" {
		return o.Human
	}
	if len(o.Data.Items) == 0 {
		return "(no checklist items)\n"
	}
	var b strings.Builder
	for _, it := range o.Data.Items {
		b.WriteString(strconv.Itoa(it.Position) + ". " + box(it.Done) + " " + it.Text + "  " + it.ID + "\n")
	}
	b.WriteString(strconv.Itoa(o.Data.Done) + "/" + strconv.Itoa(o.Data.Total) + " done\n")
	return b.String()
}

// box is the tick mark of an item line.
func box(done bool) string {
	if done {
		return "[x]"
	}
	return "[ ]"
}

// checklistOps are the checklist operations, appended to Ops.
func checklistOps(o ops) []op.Entry {
	w := work.WriteErrors
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpChecklistAdd, Summary: "Add checklist items to a task" + checklistHelp,
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: w,
			Examples: []op.Example{{Title: "add two items", CLI: `lore task checklist add tsk_<id> "stream rows" --item "test 50k rows"`, Want: "checklist items"}},
		}, o.checklistAdd),
		op.Define(op.Spec{
			Name: OpChecklistTick, Summary: "Tick a checklist item (by position or chk_ id); the task status stays",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: w,
		}, func(ctx context.Context, in ChecklistItemIn) (ChecklistOut, error) {
			return o.checklistTick(ctx, in, true)
		}),
		op.Define(op.Spec{
			Name: OpChecklistUntick, Summary: "Untick a checklist item",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: w,
		}, func(ctx context.Context, in ChecklistItemIn) (ChecklistOut, error) {
			return o.checklistTick(ctx, in, false)
		}),
		op.Define(op.Spec{
			Name: OpChecklistEdit, Summary: "Change the text of a checklist item",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: w,
		}, o.checklistEdit),
		op.Define(op.Spec{
			Name: OpChecklistRm, Summary: "Remove a checklist item (kept as a tombstone so copies merge)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: w,
		}, o.checklistRm),
		op.Define(op.Spec{
			Name: OpChecklistList, Summary: "List a task's checklist (positions and chk_ ids)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: work.ReadErrors,
		}, o.checklistList),
		op.Define(op.Spec{
			Name: OpChecklistImport, Summary: "Turn the - [ ] / - [x] lines of a task's body into checklist items",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(w), errs.NeedsConfirm),
			Confirm: op.ConfirmRule{MayAsk: true},
		}, o.checklistImport),
		op.Define(op.Spec{
			Name: OpChecklistPromote, Summary: "Turn a checklist item into a sub-task (same text, item removed, one batch)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(w), treeAddErrors...),
		}, o.checklistPromote),
	}
}

// Checklist decodes the checklist of a task row; a bad value reads as no
// items (a read never fails on it; lint reports it).
func Checklist(r row.Row) []checklist.Item {
	v := r.Fields[fieldChecklist]
	if v == nil {
		return nil
	}
	raw, err := v.MarshalJSON()
	if err != nil {
		return nil
	}
	list, err := checklist.Decode(raw)
	if err != nil {
		return nil
	}
	return list
}

// putChecklist writes the checklist patch value.
func putChecklist(patch map[string]value.Value, list []checklist.Item) error {
	raw, err := checklist.Encode(list)
	if err != nil {
		return err
	}
	v, err := value.JSONOf(raw)
	if err != nil {
		return errs.Wrap(errs.Internal, "checklist", err)
	}
	patch[fieldChecklist] = v
	return nil
}

// clHit is one task's checklist being changed.
type clHit struct {
	src  store.Source
	task row.Row
	list []checklist.Item
}

// getChecklist reads the task and its checklist.
func (o ops) getChecklist(ctx context.Context, raw string) (clHit, error) {
	id, err := taskID(raw)
	if err != nil {
		return clHit{}, err
	}
	src, r, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return clHit{}, err
	}
	return clHit{src: src, task: r, list: Checklist(r)}, nil
}

// by is the caller's actor id text ("" when unknown).
func (o ops) by(ctx context.Context) string {
	if c := o.callerOf(ctx).id; !c.IsZero() {
		return c.String()
	}
	return ""
}

// saveChecklist writes h.list back to its task and returns the list output.
func (o ops) saveChecklist(ctx context.Context, h clHit, changed []string, human string) (ChecklistOut, error) {
	patch := map[string]value.Value{}
	if err := putChecklist(patch, h.list); err != nil {
		return ChecklistOut{}, err
	}
	w, err := o.hist().Update(ctx, engine.Ref{Store: h.src, Table: table, ID: h.task.ID}, patch)
	if err != nil {
		return ChecklistOut{}, err
	}
	out := checklistOut(h.task.ID.String(), h.list)
	out.Changed, out.Store, out.Human = changed, work.Out(h.src, w, "").Store, human
	return out, nil
}

// checklistOut is the list output of a task's items.
func checklistOut(taskID string, list []checklist.Item) ChecklistOut {
	d := ChecklistData{TaskID: taskID, Items: []ChecklistItemOut{}}
	d.Done, d.Total = checklist.Progress(list)
	for i, it := range checklist.Live(list) {
		d.Items = append(d.Items, ChecklistItemOut{Position: i + 1, ID: it.ID.String(), Text: it.Text, Done: it.Done, DoneAt: it.DoneAt, DoneBy: it.DoneBy})
	}
	return ChecklistOut{SchemaVersion: work.SchemaVersion, Kind: KindChecklist, Data: d}
}

// appendItems adds texts after the last live item, refusing over
// checklist.MaxItems; it returns the new ids.
func (o ops) appendItems(ctx context.Context, h *clHit, texts []string, done []bool) ([]string, error) {
	live := len(checklist.Live(h.list))
	if live+len(texts) > checklist.MaxItems {
		return nil, errs.New(errs.InvalidInput, "a task holds at most "+strconv.Itoa(checklist.MaxItems)+" checklist items ("+strconv.Itoa(live)+" now)").
			WithHint("split the work into sub-tasks: lore task split")
	}
	now, by := opclock.Now(o.env), o.by(ctx)
	rank := checklist.LastRank(h.list)
	var added []string
	for i, t := range texts {
		rank = tasktree.After(rank)
		it, err := checklist.New(t, rank, by, now)
		if err != nil {
			return nil, err
		}
		if i < len(done) && done[i] {
			it.SetDone(true, by, now)
		}
		h.list = append(h.list, it)
		added = append(added, it.ID.String())
	}
	return added, nil
}

// checklistAdd appends items (7.3): positional text, then every --item.
func (o ops) checklistAdd(ctx context.Context, in ChecklistAddIn) (ChecklistOut, error) {
	texts := slices.DeleteFunc(append([]string{in.Text}, in.Items...), func(s string) bool { return strings.TrimSpace(s) == "" })
	if len(texts) == 0 {
		return ChecklistOut{}, errs.New(errs.InvalidInput, "task checklist add: no item text").
			WithHint(`lore task checklist add <task> "item" [--item "another"]`)
	}
	h, err := o.getChecklist(ctx, in.ID)
	if err != nil {
		return ChecklistOut{}, err
	}
	added, err := o.appendItems(ctx, &h, texts, nil)
	if err != nil {
		return ChecklistOut{}, err
	}
	noun := "checklist items"
	if len(added) == 1 {
		noun = "checklist item"
	}
	human := work.Success + " " + strconv.Itoa(len(added)) + " " + noun + " on " + h.task.ID.String() + " (" + strings.Join(added, ", ") + ")\n"
	return o.saveChecklist(ctx, h, added, human)
}

// checklistTick ticks or unticks one item; the status never changes (7.4).
func (o ops) checklistTick(ctx context.Context, in ChecklistItemIn, done bool) (ChecklistOut, error) {
	h, err := o.getChecklist(ctx, in.ID)
	if err != nil {
		return ChecklistOut{}, err
	}
	i, err := liveIndex(h.list, in.Item)
	if err != nil {
		return ChecklistOut{}, err
	}
	it := h.list[i]
	if it.Done != done {
		it.SetDone(done, o.by(ctx), opclock.Now(o.env))
		h.list[i] = it
	}
	d, t := checklist.Progress(h.list)
	human := work.Success + " " + box(done) + " " + it.Text + " (" + strconv.Itoa(d) + "/" + strconv.Itoa(t) + ")\n"
	return o.saveChecklist(ctx, h, []string{it.ID.String()}, human)
}

// requireField is the op layer's "missing required field" refusal, run
// by a handler AFTER its id lookup for a field tagged omitempty only so
// the lookup answers first (the one lookup rule, ops/internal/lookup).
func requireField(name, v string) error {
	if strings.TrimSpace(v) == "" {
		return errs.New(errs.InvalidInput, "missing required field "+name)
	}
	return nil
}

// liveIndex resolves ref and refuses a removed item.
func liveIndex(list []checklist.Item, ref string) (int, error) {
	i, err := checklist.Resolve(list, ref)
	if err != nil {
		return -1, err
	}
	if list[i].Deleted() {
		return -1, errs.New(errs.NotFound, "checklist item "+list[i].ID.String()+" was removed")
	}
	return i, nil
}

// checklistEdit changes an item's text.
func (o ops) checklistEdit(ctx context.Context, in ChecklistEditIn) (ChecklistOut, error) {
	h, err := o.getChecklist(ctx, in.ID)
	if err != nil {
		return ChecklistOut{}, err
	}
	if err = requireField("item", in.Item); err != nil {
		return ChecklistOut{}, err
	}
	if err = requireField("text", in.Text); err != nil {
		return ChecklistOut{}, err
	}
	text, err := checklist.CleanText(in.Text)
	if err != nil {
		return ChecklistOut{}, err
	}
	i, err := liveIndex(h.list, in.Item)
	if err != nil {
		return ChecklistOut{}, err
	}
	h.list[i].Text = text
	h.list[i].Touch(o.by(ctx), opclock.Now(o.env))
	return o.saveChecklist(ctx, h, []string{h.list[i].ID.String()}, work.Success+" "+h.list[i].ID.String()+" "+text+"\n")
}

// checklistRm tombstones an item (7.2: delete vs edit merges as edit wins).
func (o ops) checklistRm(ctx context.Context, in ChecklistItemIn) (ChecklistOut, error) {
	h, err := o.getChecklist(ctx, in.ID)
	if err != nil {
		return ChecklistOut{}, err
	}
	if err = requireField("item", in.Item); err != nil {
		return ChecklistOut{}, err
	}
	i, err := liveIndex(h.list, in.Item)
	if err != nil {
		return ChecklistOut{}, err
	}
	h.list[i].Remove(o.by(ctx), opclock.Now(o.env))
	return o.saveChecklist(ctx, h, []string{h.list[i].ID.String()}, work.Success+" removed "+h.list[i].ID.String()+" "+h.list[i].Text+"\n")
}

// checklistList lists a task's live items (ids always in --json).
func (o ops) checklistList(ctx context.Context, in IDIn) (ChecklistOut, error) {
	h, err := o.getChecklist(ctx, in.ID)
	if err != nil {
		return ChecklistOut{}, err
	}
	return checklistOut(h.task.ID.String(), h.list), nil
}

// checklistImport turns the body's markdown task lines into items (7.4,
// XQ9): it previews them and asks (--yes); lines already present as an
// item with the same text are skipped, so a re-run adds nothing.
func (o ops) checklistImport(ctx context.Context, in ChecklistImportIn) (ChecklistOut, error) {
	if in.From != importFromBody {
		return ChecklistOut{}, errs.New(errs.InvalidInput, "task checklist import: --from "+work.Quote(in.From)+" is not a source").
			WithHint("use --from body")
	}
	h, err := o.getChecklist(ctx, in.ID)
	if err != nil {
		return ChecklistOut{}, err
	}
	have := map[string]bool{}
	for _, it := range checklist.Live(h.list) {
		have[it.Text] = true
	}
	var texts []string
	var done []bool
	var preview strings.Builder
	for _, b := range checklist.FromBody(work.Field(h.task, work.FieldBody)) {
		if have[b.Text] {
			continue
		}
		have[b.Text] = true
		texts, done = append(texts, b.Text), append(done, b.Done)
		preview.WriteString("  " + box(b.Done) + " " + b.Text + "\n")
	}
	if len(texts) == 0 {
		out := checklistOut(h.task.ID.String(), h.list)
		out.Human = "no new - [ ] lines in the body of " + h.task.ID.String() + "\n"
		return out, nil
	}
	if err = bulk.Ask(ctx, "add "+strconv.Itoa(len(texts))+" checklist items to "+h.task.ID.String()+"?\n"+preview.String()); err != nil {
		return ChecklistOut{}, err
	}
	added, err := o.appendItems(ctx, &h, texts, done)
	if err != nil {
		return ChecklistOut{}, err
	}
	human := work.Success + " " + strconv.Itoa(len(added)) + " checklist items from the body of " + h.task.ID.String() + "\n"
	return o.saveChecklist(ctx, h, added, human)
}

// checklistPromote turns an item into a sub-task under the task (7.1):
// the child is added (inheriting like task add --parent) and the item
// removed in one bat_ batch.
func (o ops) checklistPromote(ctx context.Context, in ChecklistPromoteIn) (ChecklistOut, error) {
	h, err := o.getChecklist(ctx, in.ID)
	if err != nil {
		return ChecklistOut{}, err
	}
	i, err := liveIndex(h.list, in.Item)
	if err != nil {
		return ChecklistOut{}, err
	}
	bctx, _, err := bulk.Begin(ctx, bulk.KindTask)
	if err != nil {
		return ChecklistOut{}, err
	}
	itemID := h.list[i].ID.String()
	child, err := o.add(bctx, AddIn{Title: h.list[i].Text, Parent: h.task.ID.String(), Assign: in.Assign})
	if err != nil {
		return ChecklistOut{}, err
	}
	// re-read the parent: the add may have written it (auto watch, a
	// reopened parent), and the item must be removed from the fresh copy.
	if h, err = o.getChecklist(bctx, in.ID); err != nil {
		return ChecklistOut{}, err
	}
	if i, err = liveIndex(h.list, itemID); err != nil {
		return ChecklistOut{}, err
	}
	item := h.list[i]
	h.list[i].Remove(o.by(ctx), opclock.Now(o.env))
	human := work.Success + " " + item.ID.String() + " became " + child.Data.ID + " " + work.Quote(item.Text) + " under " + h.task.ID.String() + "\n"
	return o.saveChecklist(bctx, h, []string{item.ID.String(), child.Data.ID}, human)
}

// openItemsGuard is TSQ14 for task done: unticked items refuse with
// E_OPEN_ITEMS listing them, unless --tick-all ticks them into patch (the
// same write) or --force closes anyway; forced is the open count.
func (o ops) openItemsGuard(ctx context.Context, cur row.Row, in DoneIn, patch map[string]value.Value) (forced int, err error) {
	list := Checklist(cur)
	open := checklist.Open(list)
	switch {
	case len(open) == 0:
		return 0, nil
	case in.TickAll:
		now, by := opclock.Now(o.env), o.by(ctx)
		for i := range list {
			if !list[i].Deleted() && !list[i].Done {
				list[i].SetDone(true, by, now)
			}
		}
		return 0, putChecklist(patch, list)
	case in.Force:
		return len(open), nil
	}
	id := cur.ID.String()
	var b strings.Builder
	b.WriteString(id + " has " + strconv.Itoa(len(open)) + " unticked checklist items:")
	for _, it := range open {
		b.WriteString("\n    " + it.ID.String() + "  " + it.Text)
	}
	return 0, errs.New(errs.OpenItems, b.String()).
		WithHint("tick them first, or: lore task done " + id + " --tick-all (they were done) | lore task done " + id + " --force (close anyway)")
}
