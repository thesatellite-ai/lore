package engine

// The trash (DELETION_DESIGN 3.1-3.5, DQ1-DQ10): a delete is soft. The row
// file keeps its body and gains the delete fields (logic/trash); the row and
// its cascade dependents go in together under one bat_ id; restore clears
// the fields of the same ids; purge replaces the files with tombstones that
// are kept forever (DQ4).
//
//	Delete ─► softDelete ─► planTrash (cascade deps, restrict refuses)
//	          │              └► confirm (tasks in the group, or > preview rows)
//	          └► per store: trash.Mark → Backend.Write (Expect old HLC)
//	                        → cache gets the hidden form (a tombstone)
//	reads ─► getRow/readRows ─► hideTrashed: a trashed row reads as a
//	         tombstone everywhere (list, show, cache, search, sql, render)
//	Trashed ─► raw Backend.Read of every store (purged ids stay hidden)
//	Restore(id|bat) ─► restoreSet ─► parent check ─► trash.Clear → Write
//	PurgeTrash(id|bat) ─► restoreSet ─► remove(raw, purge=true) per row

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Trash actions on the audit event (the batch log shows them).
const (
	ActionTrash   Action = "trash"   // the delete fields were set (soft delete)
	ActionRestore Action = "restore" // the delete fields were cleared
)

// BatchKindDelete is the kind of a delete batch (XQ1, `lore batch list --kind delete`).
const BatchKindDelete = "delete"

// defaultPreviewRows is batch.preview_rows when no level sets it (XQ2).
const defaultPreviewRows = 20

// trashDay prints a delete date in messages.
const trashDay = "2006-01-02 15:04"

// deletedAtKey is the bytes a trashed row file must hold; a cheap filter
// before decoding every row on every read.
var deletedAtKey = []byte(`"` + trash.FieldDeletedAt + `"`)

// reasonKey is the context key of a delete's --reason.
type reasonKey struct{}

// WithDeleteReason sets the deleted_reason of every delete made with ctx
// (DELETION 2.1 `--reason`); the noun operations pass their flag through it.
func WithDeleteReason(ctx context.Context, reason string) context.Context {
	return context.WithValue(ctx, reasonKey{}, reason)
}

func reasonOf(ctx context.Context) string {
	s, _ := ctx.Value(reasonKey{}).(string)
	return s
}

// Trashed is one row in the trash: where it lives, the row (body kept) and
// its delete fields.
type Trashed struct {
	Store store.Source
	Row   row.Row
	Info  trash.Info
}

// TrashResult is what a restore or a trash purge did.
type TrashResult struct {
	Batch string
	Rows  []Trashed
}

// trashItem is one row of a trash plan.
type trashItem struct {
	src store.Source
	row row.Row
}

// softDelete is Delete for the live row cur of src (DQ8: every delete is
// soft; D8: cascade dependents join the batch, referrers are never
// touched, a restrict relation refuses).
func (e *Engine) softDelete(ctx context.Context, src store.Source, cur row.Row) (Written, error) {
	reason, err := trash.Reason(reasonOf(ctx))
	if err != nil {
		return Written{}, err
	}
	plan, err := e.planTrash(ctx, src, cur)
	if err != nil {
		return Written{}, err
	}
	if err = e.confirmGroup(ctx, src, cur, plan); err != nil {
		return Written{}, err
	}
	b, ok := BatchOf(ctx)
	if !ok {
		id, nerr := e.ids.NewID(ids.PrefixBatch)
		if nerr != nil {
			return Written{}, nerr
		}
		bid, perr := ids.Parse[ids.Batch](id.String())
		if perr != nil {
			return Written{}, perr
		}
		b = Batch{ID: bid, Kind: BatchKindDelete}
		ctx = WithBatch(ctx, b)
	}
	cl := e.callOf(ctx)
	actor := e.actorOf(cl)
	byStore := map[store.Source][]row.Row{}
	for _, it := range plan {
		byStore[it.src] = append(byStore[it.src], it.row)
	}
	for name := range byStore {
		if _, werr := e.writable(name); werr != nil { // check every store before writing any
			return Written{}, werr
		}
	}
	var w Written
	for _, name := range e.order {
		rows := byStore[name]
		if len(rows) == 0 {
			continue
		}
		st, _ := e.writable(name)
		changes := make([]store.Change, 0, len(rows))
		docs := make([]store.Row, 0, len(rows))
		events := make([]Event, 0, len(rows))
		marked := make([]row.Row, 0, len(rows))
		for _, r := range rows {
			h := e.clock.Now(r.HLC)
			next := r
			next.HLC, next.UpdatedAt, next.UpdatedBy = h, h.Wall, actor
			if next, err = trash.Mark(next, trash.Stamp{At: h.Wall, By: actor, Reason: reason, Batch: b.ID}); err != nil {
				return Written{}, err
			}
			next = stampAs(st, next)
			if next, err = e.stampWrite(st, next); err != nil {
				return Written{}, err
			}
			marked = append(marked, next)
			sr, ferr := store.FromRow(next)
			if ferr != nil {
				return Written{}, ferr
			}
			hidden, herr := hiddenForm(sr)
			if herr != nil {
				return Written{}, herr
			}
			changes = append(changes, store.Change{Row: sr, Expect: r.HLC})
			docs = append(docs, hidden) // the cache drops a trashed row (3.2)
			ev := Event{Op: cl.op, Action: ActionTrash, Store: name, Table: next.Table, ID: next.ID, HLC: next.HLC, Actor: actor}
			if next.ID != cur.ID {
				ev.Cascade = cur.ID
				w.Also = append(w.Also, Changed{Store: name, Row: next})
			} else {
				w.Row = next
			}
			events = append(events, ev)
		}
		if err = e.gate(ctx, st, marked...); err != nil {
			return Written{}, err // FORMAT_EVOLUTION rule 2
		}
		if werr := st.Backend.Write(ctx, changes); werr != nil {
			if errors.Is(werr, store.ErrStale) {
				return Written{}, errs.Wrap(errs.Stale, "engine: a row changed while "+cur.ID.String()+" was deleted", werr).WithHint("rerun the command")
			}
			return Written{}, werr
		}
		if ferr := e.finish(ctx, name, docs, events...); ferr != nil {
			return w, ferr
		}
	}
	return w, nil
}

// planTrash is the group a soft delete moves: root plus every cascade
// dependent, recursively (3.4). Set-null and many-to-many links are
// referrers and stay as they are (D8), so a restore needs no undo of them.
func (e *Engine) planTrash(ctx context.Context, src store.Source, root row.Row) ([]trashItem, error) {
	plan := []trashItem{{src, root}}
	seen := map[ids.AnyID]bool{root.ID: true}
	for i := 0; i < len(plan); i++ {
		it := plan[i]
		deps, err := e.dependents(ctx, it.row.Table, it.row.ID)
		if err != nil {
			return nil, err
		}
		for _, d := range deps {
			if seen[d.row.ID] {
				continue
			}
			if d.rule.list { // a many-to-many link: restrict refuses (3.15), other rules leave the list (D8)
				if d.rule.onDelete == schema.Restrict {
					return nil, errs.New(errs.HasDependents, "engine: "+d.row.ID.String()+" ("+string(d.rule.table)+"."+d.rule.field+") still lists "+it.row.ID.String()).
						WithHint("remove it from the list first")
				}
				continue
			}
			switch d.rule.onDelete {
			case schema.Restrict:
				return nil, errs.New(errs.HasDependents, "engine: "+d.row.ID.String()+" ("+string(d.rule.table)+"."+d.rule.field+") still points at "+it.row.ID.String()).
					WithHint("delete or move the pointing rows first")
			case schema.Cascade:
				seen[d.row.ID] = true
				plan = append(plan, trashItem{d.source, d.row})
			case schema.SetNull, schema.TombstoneOnly:
			}
		}
	}
	return plan, nil
}

// confirmGroup asks before a group delete (3.4, TSQ3, DQ6; a soft delete is
// --yes, never --confirm, DQ2): always when the group holds a task besides
// the root (a parent's sub-tasks, a tasklist's tasks), else when it is
// larger than batch.preview_rows.
func (e *Engine) confirmGroup(ctx context.Context, src store.Source, root row.Row, plan []trashItem) error {
	if len(plan) < 2 {
		return nil
	}
	tasks := 0
	for _, it := range plan[1:] {
		if it.row.Table == schema.TableTasks {
			tasks++
		}
	}
	limit := int64(defaultPreviewRows)
	if set, _, err := e.settings.Policy(src); err == nil {
		if n, ok := set.Limit(policy.KeyBatchPreviewRows); ok {
			limit = n
		}
	}
	if tasks == 0 && int64(len(plan)) <= limit {
		return nil
	}
	q := "deleting " + root.ID.String() + " also moves " + strconv.Itoa(len(plan)-1) + " rows to the trash"
	if tasks > 0 {
		q += " (" + strconv.Itoa(tasks) + " tasks)"
	}
	return e.confirm(ctx, e.callOf(ctx), q+"; continue?")
}

// hiddenForm is sr as every read sees a trashed row: a tombstone with the
// file's HLC (a later write's Expect still matches the file).
func hiddenForm(sr store.Row) (store.Row, error) {
	return store.FromRow(row.Row{Table: sr.Table, ID: sr.ID, HLC: sr.HLC, Deleted: true, UpdatedAt: sr.HLC.Wall})
}

// hideTrashed is sr as the engine reads it: a trashed row reads as a
// tombstone (3.2: one read filter, so no command can forget it).
func hideTrashed(sr store.Row) (store.Row, error) {
	if sr.Deleted || !bytes.Contains(sr.Doc, deletedAtKey) {
		return sr, nil
	}
	r, err := sr.Decode()
	if err != nil {
		return store.Row{}, err
	}
	if !trash.Is(r) {
		return sr, nil
	}
	return hiddenForm(sr)
}

// rawTrashed returns the trashed row file of (table, id) in store name, if
// the file holds one (a purged id never answers).
func (e *Engine) rawTrashed(ctx context.Context, name store.Source, table row.Table, id ids.AnyID) (row.Row, bool, error) {
	st := e.stores[name]
	sr, err := st.Backend.Get(ctx, table, id)
	if errors.Is(err, store.ErrNotFound) {
		return row.Row{}, false, nil
	}
	if err != nil || sr.Deleted || !bytes.Contains(sr.Doc, deletedAtKey) {
		return row.Row{}, false, err
	}
	set, err := purgedSet(ctx, st)
	if err != nil || set[id.String()] {
		return row.Row{}, false, err
	}
	r, err := sr.Decode()
	if err != nil {
		return row.Row{}, false, err
	}
	return r, trash.Is(r), nil
}

// inTrashErr is E_IN_TRASH for the trashed row r (3.2: "is in the trash
// (deleted by mira …); lore trash show <id>").
func inTrashErr(r row.Row) error {
	msg := "engine: " + r.ID.String() + " is in the trash"
	if in, ok := trash.Read(r); ok {
		var parts []string
		if in.By != "" {
			parts = append(parts, "deleted by "+in.By)
		}
		if !in.At.IsZero() {
			parts = append(parts, "on "+in.At.Format(trashDay))
		}
		if len(parts) > 0 {
			msg += " (" + strings.Join(parts, " ") + ")"
		}
	}
	return errs.New(errs.InTrash, msg).WithHint("lore trash show " + r.ID.String() + "; bring it back with lore trash restore " + r.ID.String())
}

// Trashed lists every row in the trash of every attached store
// (precedence order; one place attached twice is read once).
func (e *Engine) Trashed(ctx context.Context) ([]Trashed, error) {
	var out []Trashed
	seen := map[string]bool{}
	for _, name := range e.order {
		st := e.stores[name]
		if st.Location != "" {
			if seen[st.Location] {
				continue
			}
			seen[st.Location] = true
		}
		set, err := purgedSet(ctx, st)
		if err != nil {
			return nil, err
		}
		rows, _, err := st.Backend.Read(ctx, "")
		if err != nil {
			return nil, err
		}
		for _, sr := range rows {
			if sr.Deleted || set[sr.ID.String()] || !bytes.Contains(sr.Doc, deletedAtKey) {
				continue
			}
			r, derr := sr.Decode()
			if derr != nil {
				return nil, derr
			}
			if in, ok := trash.Read(r); ok {
				out = append(out, Trashed{Store: name, Row: r, Info: in})
			}
		}
	}
	return out, nil
}

// TrashTarget resolves a restore or purge target (a row id or a bat_ id)
// to the rows it moves: a batch is every row carrying it; a row id is the
// row plus the rows of its batch that depend on it (3.4: its links,
// comments and sub-tasks, not its siblings).
func (e *Engine) TrashTarget(ctx context.Context, target string) (TrashResult, error) {
	all, err := e.Trashed(ctx)
	if err != nil {
		return TrashResult{}, err
	}
	target = strings.TrimSpace(target)
	if b, berr := ids.Parse[ids.Batch](target); berr == nil {
		res := TrashResult{Batch: b.String()}
		for _, t := range all {
			if t.Info.Batch == b.String() {
				res.Rows = append(res.Rows, t)
			}
		}
		if len(res.Rows) == 0 {
			return TrashResult{}, errs.New(errs.NotFound, "engine: no row of batch "+b.String()+" is in the trash").
				WithHint("lore trash lists what is in the trash")
		}
		return res, nil
	}
	id, err := ids.ParseAny(target)
	if err != nil {
		return TrashResult{}, errs.Wrap(errs.NotFound, "engine: "+strconv.Quote(target)+" is neither a row id nor a batch id", err)
	}
	i := slices.IndexFunc(all, func(t Trashed) bool { return t.Row.ID == id })
	if i < 0 {
		return TrashResult{}, e.notInTrash(ctx, id)
	}
	root := all[i]
	res := TrashResult{Batch: root.Info.Batch, Rows: []Trashed{root}}
	in := map[ids.AnyID]bool{id: true}
	for grew := true; grew; {
		grew = false
		for _, t := range all {
			if in[t.Row.ID] || t.Info.Batch != root.Info.Batch {
				continue
			}
			if e.dependsOnSet(t.Row, in) {
				in[t.Row.ID] = true
				res.Rows = append(res.Rows, t)
				grew = true
			}
		}
	}
	return res, nil
}

// notInTrash explains why id is not in the trash: purged (E_PURGED, DQ4),
// a tombstone, alive, or unknown.
func (e *Engine) notInTrash(ctx context.Context, id ids.AnyID) error {
	src, cur, err := e.locate(ctx, Ref{ID: id})
	if err != nil {
		return err
	}
	if !cur.Deleted {
		return errs.New(errs.NotFound, "engine: "+id.String()+" is not in the trash (it is alive in "+string(src)+")")
	}
	return deletedErr(ctx, e.stores[src], cur, "is deleted for good (a tombstone), not in the trash")
}

// parentsOf yields every cascade parent r points at: (field, parent id).
func (e *Engine) parentsOf(r row.Row) []ids.AnyID {
	var out []ids.AnyID
	for field, v := range r.Fields {
		for _, s := range value.ItemStrings(v) {
			pid, err := ids.ParseAny(s)
			if err != nil {
				continue
			}
			def, ok := e.reg.ByPrefix(pid.Prefix())
			if !ok {
				continue
			}
			for _, rule := range e.dependentRules(def.Name) {
				if rule.table == r.Table && rule.field == field && rule.onDelete == schema.Cascade && !rule.list && points(r, rule, def.Name, pid) {
					out = append(out, pid)
				}
			}
		}
	}
	return out
}

// dependsOnSet reports whether r is a cascade dependent of a row in set.
func (e *Engine) dependsOnSet(r row.Row, set map[ids.AnyID]bool) bool {
	for _, p := range e.parentsOf(r) {
		if set[p] {
			return true
		}
	}
	return false
}

// Restore brings target (a row id or a bat_ id) back from the trash with
// the same ids (3.3): a row whose cascade parent is still in the trash
// outside the set is refused with E_PARENT_DELETED, one whose parent was
// purged with E_PARENT_PURGED (4.2); nothing changes then. One write per
// store.
func (e *Engine) Restore(ctx context.Context, target string) (TrashResult, error) {
	res, err := e.TrashTarget(ctx, target)
	if err != nil {
		return TrashResult{}, err
	}
	set := map[ids.AnyID]bool{}
	for _, t := range res.Rows {
		set[t.Row.ID] = true
	}
	for _, t := range res.Rows {
		if err := e.checkParents(ctx, t.Row, set); err != nil {
			return TrashResult{}, err
		}
	}
	if b, perr := ids.Parse[ids.Batch](res.Batch); perr == nil {
		if _, ok := BatchOf(ctx); !ok {
			ctx = WithBatch(ctx, Batch{ID: b, Kind: BatchKindDelete})
		}
	}
	cl := e.callOf(ctx)
	actor := e.actorOf(cl)
	byStore := map[store.Source][]row.Row{}
	for _, t := range res.Rows {
		byStore[t.Store] = append(byStore[t.Store], t.Row)
	}
	for name := range byStore {
		if _, werr := e.writable(name); werr != nil {
			return TrashResult{}, werr
		}
	}
	var out TrashResult
	out.Batch = res.Batch
	for _, name := range e.order {
		rows := byStore[name]
		if len(rows) == 0 {
			continue
		}
		st, _ := e.writable(name)
		changes := make([]store.Change, 0, len(rows))
		docs := make([]store.Row, 0, len(rows))
		events := make([]Event, 0, len(rows))
		next := make([]row.Row, 0, len(rows))
		for _, r := range rows {
			if err := refusePurged(ctx, st, r.Table, r.ID, false); err != nil {
				return TrashResult{}, err // DQ4: a purged id never comes back
			}
			h := e.clock.Now(r.HLC)
			n := trash.Clear(r)
			n.HLC, n.UpdatedAt, n.UpdatedBy = h, h.Wall, actor
			n = stampAs(st, n)
			var serr error
			if n, serr = e.stampWrite(st, n); serr != nil {
				return TrashResult{}, serr
			}
			sr, ferr := store.FromRow(n)
			if ferr != nil {
				return TrashResult{}, ferr
			}
			changes = append(changes, store.Change{Row: sr, Expect: r.HLC})
			docs = append(docs, sr)
			next = append(next, n)
			events = append(events, Event{Op: cl.op, Action: ActionRestore, Store: name, Table: n.Table, ID: n.ID, HLC: n.HLC, Actor: actor})
		}
		if err := e.gate(ctx, st, next...); err != nil {
			return TrashResult{}, err
		}
		if err := st.Backend.Write(ctx, changes); err != nil {
			if errors.Is(err, store.ErrStale) {
				return TrashResult{}, errs.Wrap(errs.Stale, "engine: a row changed during the restore", err).WithHint("rerun the command")
			}
			return TrashResult{}, err
		}
		if err := e.finish(ctx, name, docs, events...); err != nil {
			return out, err
		}
		for _, n := range next {
			out.Rows = append(out.Rows, Trashed{Store: name, Row: n})
		}
	}
	return out, nil
}

// checkParents refuses to restore r while a cascade parent outside set is
// in the trash (E_PARENT_DELETED, 3.4) or was deleted for good
// (E_PARENT_PURGED, 4.2).
func (e *Engine) checkParents(ctx context.Context, r row.Row, set map[ids.AnyID]bool) error {
	for _, pid := range e.parentsOf(r) {
		if set[pid] {
			continue
		}
		for _, name := range e.order {
			def, _ := e.reg.ByPrefix(pid.Prefix())
			if pr, ok, err := e.rawTrashed(ctx, name, def.Name, pid); err != nil {
				return err
			} else if ok {
				in, _ := trash.Read(pr)
				return errs.New(errs.ParentDeleted, "engine: "+pid.String()+" is in the trash; restore it with its batch "+in.Batch).
					WithHint("lore trash restore " + in.Batch)
			}
			sr, err := e.stores[name].Backend.Get(ctx, def.Name, pid)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if sr.Deleted {
				return errs.New(errs.ParentPurged, "engine: "+pid.String()+" was deleted for good; "+r.ID.String()+" cannot come back under it").
					WithHint("give the row a new parent after restoring is supported, or add it again as a new row")
			}
		}
	}
	return nil
}

// PurgeTrash removes target (a row id or a bat_ id) for good (3.5): each
// row of the set is replaced by a tombstone and its id listed in
// _purged.json (DQ4). The caller has already asked for --confirm (DQ2).
func (e *Engine) PurgeTrash(ctx context.Context, target string) (TrashResult, error) {
	res, err := e.TrashTarget(ctx, target)
	if err != nil {
		return TrashResult{}, err
	}
	return e.purgeRows(ctx, res)
}

// purgeRows purges every row of res one by one: a row's own remove plan
// reaches only live dependents (trashed ones read as tombstones), and the
// trashed dependents are already in res.
func (e *Engine) purgeRows(ctx context.Context, res TrashResult) (TrashResult, error) {
	out := TrashResult{Batch: res.Batch}
	for _, t := range res.Rows {
		if _, err := e.remove(ctx, t.Store, t.Row, true); err != nil {
			return out, err
		}
		out.Rows = append(out.Rows, t)
	}
	return out, nil
}

// PurgeOlder purges every trashed row deleted before cutoff (`lore trash
// purge --older-than`). With expired, each row's own store retention
// (trash.retention_days, a maximum, DQ7) decides instead and cutoff is
// ignored; a store with no retention keeps its trash (DQ3: never in the
// background, only through this named command).
func (e *Engine) PurgeOlder(ctx context.Context, cutoff time.Time, expired bool, dryRun bool) (TrashResult, error) {
	all, err := e.Trashed(ctx)
	if err != nil {
		return TrashResult{}, err
	}
	now := e.clock.Now(hlc.HLC{}).Wall
	var res TrashResult
	for _, t := range all {
		old := !t.Info.At.IsZero() && t.Info.At.Before(cutoff)
		if expired {
			days := int64(0)
			if set, _, perr := e.settings.Policy(t.Store); perr == nil {
				days, _ = set.Limit(policy.KeyTrashRetentionDays)
			}
			old = trash.Expired(t.Info.At, now, days)
		}
		if old {
			res.Rows = append(res.Rows, t)
		}
	}
	if dryRun {
		return res, nil
	}
	return e.purgeRows(ctx, res)
}
