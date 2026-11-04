// task_corerows.go — the row side of task coordination on the 0.1.18 core.
//
// tasks, missions (and comments, for task notes) are core-owned tables
// (lsync.CoreOwned): their row files belong to the core engine, and a
// lore.db-only or live-ref-only change is never exported. So the
// coordination verbs (claim, next, heartbeat, release, revoke, reassign,
// note, merged, gate, done/cancel of a held task) keep their lease
// mechanics on the signals ref, but read and write the ROW through the core:
//
//   - reads: coreDoc / coreDocs turn a core row into the v0.1.17 document
//     shape coord works on (same column names; id, created_at, updated_at
//     from the envelope);
//   - writes: coreCompanion is a signals.RemoteCompanion. The executor
//     pushes the signals ref first (the lease fences the decision) and then
//     calls PushSelf, which applies each staged row as an engine Update (or
//     CreateWithID for a new row, e.g. a note). A failure there is reported
//     as signals.ErrPartial: the same documented window as v0.1.17's
//     ordered fallback and its other-remote companions (TEAM-036).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"

	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitremote"
	"saas/pkg/aicoder/live"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/signals/coord"
)

// Envelope keys of a v0.1.17 row document (not row fields on the core).
const (
	docKeyID        = "id"
	docKeyCreatedAt = "created_at"
	docKeyUpdatedAt = "updated_at"
	// fieldCreatedByActor is the author column of a coordination row.
	fieldCreatedByActor = "created_by_actor_id"
)

// coreRowTable reports whether coordination must read and write table
// through the core.
func coreRowTable(table string) bool { return lsync.CoreOwned(table) }

// coreTableDef is the core's definition of table.
func coreTableDef(table string) (schema.TableDef, bool) {
	for _, d := range schema.Builtin() {
		if string(d.Name) == table {
			return d, true
		}
	}
	return schema.TableDef{}, false
}

// coreDocOf is the v0.1.17 document of a core row (created_by_actor_id
// filled from the envelope author when the row has no such field).
func coreDocOf(r row.Row) (coord.Doc, error) {
	m := map[string]json.RawMessage{}
	for k, v := range r.Fields {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("encode %s.%s: %w", r.ID, k, err)
		}
		m[k] = b
	}
	for k, s := range map[string]string{
		docKeyID: r.ID.String(), docKeyCreatedAt: r.CreatedAt.UTC().Format(time.RFC3339Nano), docKeyUpdatedAt: r.UpdatedAt.UTC().Format(time.RFC3339Nano),
	} {
		b, err := json.Marshal(s)
		if err != nil {
			return nil, err
		}
		m[k] = b
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	d, err := canonjson.Decode(raw, canonjson.DefaultMaxDepth)
	if err != nil {
		return nil, err
	}
	doc := coord.Doc(d)
	// A row written through the core names its author in the envelope
	// (created_by), not in v0.1.17's created_by_actor_id column: the
	// document gets the column from the envelope when it lacks it, so
	// ownership (inbox, mission owner) reads the same for both writers.
	if doc.Str(fieldCreatedByActor) == "" && !r.CreatedBy.IsZero() {
		doc[fieldCreatedByActor] = r.CreatedBy.String()
	}
	return doc, nil
}

// coreDoc reads one task, mission or comment row from the core (any
// attached store). ok=false when the id is unknown or deleted.
func coreDoc(ctx context.Context, table, id string) (coord.Doc, bool, error) {
	if coreApp == nil {
		return nil, false, errcodes.New(errcodes.Internal, "the core is not mounted")
	}
	_, r, found, err := coreLocate(ctx, table, id)
	if err != nil || !found {
		return nil, false, err
	}
	d, err := coreDocOf(r)
	if err != nil {
		return nil, false, errcodes.New(errcodes.Internal, "read "+id).WithCause(err)
	}
	return d, true, nil
}

// coreLocate finds one row and the store holding it. A row present in
// several stores under the same id (an actor's natural id is the same in
// the repo and in the org folder) is the repo store's copy: coordination
// writes a repo task's supporting rows (an owner projection) there.
// found=false for an unknown id, a non-id or a tombstone.
func coreLocate(ctx context.Context, table, id string) (store.Source, row.Row, bool, error) {
	aid, err := ids.ParseAny(id)
	if err != nil {
		return "", row.Row{}, false, nil
	}
	ref := engine.Ref{Table: row.Table(table), ID: aid}
	src, r, err := coreApp.Get(ctx, ref)
	if errs.Has(err, errs.AmbiguousID) {
		ref.Store = store.SourceRepo
		src, r, err = coreApp.Get(ctx, ref)
	}
	if errs.Has(err, errs.NotFound) || (err == nil && r.Deleted) {
		return "", row.Row{}, false, nil
	}
	if err != nil {
		return "", row.Row{}, false, coreErr(err)
	}
	return src, r, true, nil
}

// coreDocs lists the live rows of table in every attached store.
func coreDocs(ctx context.Context, table string) ([]coord.Doc, error) {
	if coreApp == nil {
		return nil, errcodes.New(errcodes.Internal, "the core is not mounted")
	}
	var out []coord.Doc
	for _, src := range coreApp.Stores() {
		rows, err := coreApp.List(ctx, src, row.Table(table))
		if err != nil {
			return nil, coreErr(err)
		}
		for _, r := range rows {
			if r.Deleted {
				continue
			}
			d, err := coreDocOf(r)
			if err != nil {
				return nil, errcodes.New(errcodes.Internal, "read "+r.ID.String()).WithCause(err)
			}
			out = append(out, d)
		}
	}
	return out, nil
}

// coreCompanion writes staged coordination rows through the core after the
// signals push was confirmed.
type coreCompanion struct {
	rows []coord.RowWrite
}

var _ signals.RemoteCompanion = (*coreCompanion)(nil)

// Prepare checks every row can be written (known table) before anything is
// pushed; there is no ref to add to the signals push.
func (c *coreCompanion) Prepare(context.Context) (gitremote.RefUpdate, error) {
	for _, w := range c.rows {
		if _, ok := coreTableDef(coreTableFor(w)); !ok {
			return gitremote.RefUpdate{}, fmt.Errorf("core rows: unknown table %q", coreTableFor(w))
		}
	}
	return gitremote.RefUpdate{}, nil
}

// PushSelf applies every row through the engine: an update of the fields
// that changed, or a create with the row's id when the core has none yet.
func (c *coreCompanion) PushSelf(ctx context.Context) error {
	for _, w := range c.rows {
		if err := coreWriteDoc(ctx, coreTableFor(w), w.ID, w.Doc); err != nil {
			return err
		}
	}
	return nil
}

// Finalize has nothing left to do: PushSelf already wrote the rows.
func (c *coreCompanion) Finalize(context.Context) error { return nil }

// coreTableFor is a staged row's table ("" = the id's kind).
func coreTableFor(w coord.RowWrite) string {
	if w.Table != "" {
		return w.Table
	}
	return tableOf(w.ID)
}

// coreWriteDoc writes doc as the next version of table/id through the
// engine: a field the document leaves out is cleared (a whole staged row).
func coreWriteDoc(ctx context.Context, table, id string, doc coord.Doc) error {
	return coreWrite(ctx, table, id, doc, true)
}

// corePatchDoc writes only the fields doc names (a partial stamp).
func corePatchDoc(ctx context.Context, table, id string, doc coord.Doc) error {
	return coreWrite(ctx, table, id, doc, false)
}

// coreWrite is coreWriteDoc (whole) or corePatchDoc.
func coreWrite(ctx context.Context, table, id string, doc coord.Doc, whole bool) error {
	def, ok := coreTableDef(table)
	if !ok {
		return errcodes.New(errcodes.Internal, "core rows: unknown table "+table)
	}
	aid, err := ids.ParseAny(id)
	if err != nil {
		return errcodes.New(errcodes.InvalidID, id+" is not an id").WithCause(err)
	}
	src, curRow, found, err := coreLocate(ctx, table, id)
	if err != nil {
		return err
	}
	var cur coord.Doc
	if found {
		if cur, err = coreDocOf(curRow); err != nil {
			return errcodes.New(errcodes.Internal, "read "+id).WithCause(err)
		}
	}
	patch := map[string]value.Value{}
	for _, f := range def.Fields {
		nv, inNew := doc[f.Name]
		ov, inOld := cur[f.Name]
		if (!inNew && !inOld) || (!inNew && !whole) {
			continue
		}
		if inNew && inOld && reflect.DeepEqual(normDoc(nv), normDoc(ov)) {
			continue
		}
		if !inNew || nv == nil {
			if found && inOld && ov != nil {
				patch[f.Name] = value.Null{}
			}
			continue
		}
		v, perr := docValue(f, nv)
		if perr != nil {
			return errcodes.New(errcodes.InvalidInput, "core rows: "+table+"."+f.Name).WithCause(perr)
		}
		patch[f.Name] = v
	}
	if !found {
		for k, v := range patch {
			if v.Kind() == value.KindNull {
				delete(patch, k)
			}
		}
		// A new row (a note) names its author; when that actor has no row
		// in the core yet (an agent that never wrote through it), the
		// envelope's created_by records the writer instead of the write
		// being refused after the signals push.
		if a, ok := patch[fieldCreatedByActor].(value.Ref); ok {
			if _, _, known, gerr := coreLocate(ctx, string(schema.TableActors), a.ID().String()); gerr != nil || !known {
				delete(patch, fieldCreatedByActor)
			}
		}
		_, err = coreApp.CreateWithID(ctx, store.Source(""), row.Table(table), aid, patch)
		return coreErr(err)
	}
	if len(patch) == 0 {
		return nil
	}
	_, err = coreApp.Update(ctx, engine.Ref{Table: row.Table(table), ID: aid, Store: src}, patch)
	return coreErr(err)
}

// normDoc makes two decoded JSON values comparable (json.Number vs float).
func normDoc(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return strings.TrimSpace(string(b))
}

// docValue types one document value by its field definition.
func docValue(f schema.FieldDef, v any) (value.Value, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if f.Kind == value.KindList {
		return value.ParseList(f.Elem, raw)
	}
	return value.Parse(f.Kind, raw)
}

// coreLiveCompanion is coreCompanion for a repo whose rows are live and
// whose live ref is on the signals ref's remote: Prepare runs the engine
// writes with a liveCollector, so every live-kind row becomes part of ONE
// staged live commit (live.Ref.Stage) that joins the atomic signals push;
// Finalize applies it after the remote confirmed both. Nothing reaches the
// live ref or the remote when the lease was lost (v0.1.17's
// stagedLiveRows, F7). Rows of other tables (none today) are written by
// the engine in Prepare.
type coreLiveCompanion struct {
	rows []coord.RowWrite
	// refresh fetches and integrates the live ref before staging, so the
	// staged commit fast-forwards the remote tip after a lost race.
	refresh func(ctx context.Context) error
	staged  live.Staged
	s       *liveSession
}

var _ signals.Companion = (*coreLiveCompanion)(nil)

// Prepare implements signals.Companion. It is re-run after a lost race,
// from a fresh integrate: nothing of an earlier attempt was applied.
func (c *coreLiveCompanion) Prepare(ctx context.Context) (gitremote.RefUpdate, error) {
	if c.refresh != nil {
		if err := c.refresh(ctx); err != nil {
			return gitremote.RefUpdate{}, err
		}
	}
	col := &liveCollector{rows: map[live.RowKey]live.File{}}
	cctx := withLiveCollector(ctx, col)
	for _, w := range c.rows {
		if _, ok := coreTableDef(coreTableFor(w)); !ok {
			return gitremote.RefUpdate{}, fmt.Errorf("core rows: unknown table %q", coreTableFor(w))
		}
		if err := coreWriteDoc(cctx, coreTableFor(w), w.ID, w.Doc); err != nil {
			return gitremote.RefUpdate{}, err
		}
	}
	c.staged, c.s = live.Staged{}, col.s
	if col.s == nil || len(col.rows) == 0 {
		return gitremote.RefUpdate{}, nil
	}
	st, err := col.s.ref.Stage(ctx, live.WriteRequest{Folder: col.folder, Rows: col.rows, BinaryVersion: version})
	if err != nil {
		return gitremote.RefUpdate{}, fmt.Errorf("stage the live rows: %w", err)
	}
	c.staged = st
	if st.New == st.Old {
		return gitremote.RefUpdate{}, nil
	}
	return st.Update(col.s.ref.Name), nil
}

// Finalize implements signals.Companion: the remote holds the staged
// commit; move the local live ref (and its tracking copy) to it.
func (c *coreLiveCompanion) Finalize(ctx context.Context) error {
	if c.s == nil || c.staged.New == c.staged.Old {
		return nil
	}
	if err := c.s.ref.CommitStaged(ctx, c.staged); err != nil {
		return fmt.Errorf("move %s: %w", c.s.ref.Name, err)
	}
	c.s.wrote += len(c.staged.Written)
	return nil
}
