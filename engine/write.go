package engine

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/logic/validate"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Ref names one row. Store "" searches every attached store (an id found in
// two is E_AMBIGUOUS_ID, spec 7.4); Table "" takes the table from the id's
// prefix.
type Ref struct {
	Store store.Source
	Table row.Table
	ID    ids.AnyID
}

// origin keys written into _o at create (PROVENANCE_DESIGN owns the full
// shape; the engine records what it knows: the command and the store).
const (
	originCmd   = "cmd"
	originStore = "store"
)

// Create validates fields against table's definition, checks references,
// scans text, stamps a new id, HLC, audit fields and _o, and writes the row
// to target ("" = the call's target, else the default). The returned row is
// exactly what the backend stored.
func (e *Engine) Create(ctx context.Context, target store.Source, table row.Table, fields map[string]value.Value) (Written, error) {
	return e.create(ctx, target, table, ids.AnyID{}, fields)
}

// CreateWithID is Create with an id the caller pins (a natural id it
// derived itself, e.g. the actor row of an identity). The id's prefix must
// be the table's (E_INVALID_ID otherwise), and a row with that id must not
// exist in target yet (E_STALE: the write expects no row), so it never
// overwrites.
func (e *Engine) CreateWithID(ctx context.Context, target store.Source, table row.Table, id ids.AnyID, fields map[string]value.Value) (Written, error) {
	if id.IsZero() {
		return Written{}, errs.New(errs.InvalidID, "engine: CreateWithID needs an id")
	}
	return e.create(ctx, target, table, id, fields)
}

// create is Create's path; pin, when set, is the id to use.
func (e *Engine) create(ctx context.Context, target store.Source, table row.Table, pin ids.AnyID, fields map[string]value.Value) (Written, error) {
	cl := e.callOf(ctx)
	if target == "" {
		target = cl.target
	}
	st, err := e.writable(target)
	if err != nil {
		return Written{}, err
	}
	def, err := e.tableDef(table)
	if err != nil {
		return Written{}, err
	}
	typed, err := validate.Fields(def, fields, validate.Whole)
	if err != nil {
		return Written{}, err
	}
	dropNulls(typed)
	names := slices.Sorted(maps.Keys(typed))
	if err = refuseMarkers(typed, names); err != nil {
		return Written{}, err
	}
	if err = e.checkRefs(ctx, def, typed, names); err != nil {
		return Written{}, err
	}
	if !pin.IsZero() && pin.Prefix() != def.Prefix {
		return Written{}, errs.New(errs.InvalidID, "engine: "+pin.String()+" is not an id of "+string(table))
	}
	keyFields, keyParts, natural := naturalKey(def, typed)
	var id ids.AnyID
	switch {
	case !pin.IsZero():
		id, natural = pin, false
	case natural:
		id, err = NaturalID(def.Prefix, keyParts...)
	default:
		id, err = e.ids.NewID(def.Prefix)
	}
	if err != nil {
		return Written{}, err
	}
	sc, err := e.screen(ctx, cl, target, id, typed, names)
	if err != nil {
		return Written{}, err
	}
	origin, err := originOf(cl.op, target)
	if err != nil {
		return Written{}, err
	}
	actor := e.actorOf(cl)
	stamp := e.clock.Now()
	r := row.Row{
		Table: table, ID: id, HLC: stamp, Origin: origin, Fields: sc.fields,
		CreatedAt: stamp.Wall, CreatedBy: actor, UpdatedAt: stamp.Wall, UpdatedBy: actor,
	}
	got, err := e.put(ctx, st, r, hlc.HLC{})
	if natural && errors.Is(err, store.ErrStale) {
		return Written{}, errs.Wrap(errs.InvalidInput, "engine: a "+string(table)+" row with this "+strings.Join(keyFields, ", ")+" already exists ("+id.String()+")", err).
			WithHint("use the existing row (a deleted one cannot come back)")
	}
	if err != nil {
		return Written{}, err
	}
	ev := Event{Op: cl.op, Action: ActionCreate, Store: target, Table: table, ID: id, HLC: stamp, Actor: actor,
		SecretsAccepted: sc.accepted, SecretsRedacted: sc.redacted}
	return Written{Row: got.row, Warnings: sc.warnings}, e.finish(ctx, target, []store.Row{got.doc}, ev)
}

// Update applies patch to a live row: a value sets a field, value.Null
// clears an optional one. The whole result is validated, changed refs are
// checked, changed text is scanned, and the row is written with Expect set
// to the HLC read. A concurrent write in between (ErrStale) is merged field
// by field and retried once (spec 5.3); a text clash, a second race or a
// concurrent delete is E_STALE, so no edit is ever lost silently.
func (e *Engine) Update(ctx context.Context, ref Ref, patch map[string]value.Value) (Written, error) {
	cl := e.callOf(ctx)
	src, cur, err := e.locate(ctx, ref)
	if err != nil {
		return Written{}, err
	}
	st, err := e.writable(src)
	if err != nil {
		return Written{}, err
	}
	if cur.Deleted {
		return Written{}, errs.New(errs.NotFound, "engine: "+cur.ID.String()+" is deleted")
	}
	def, err := e.tableDef(cur.Table)
	if err != nil {
		return Written{}, err
	}
	typedPatch, err := validate.Fields(def, patch, validate.Patch)
	if err != nil {
		return Written{}, err
	}
	names := slices.Sorted(maps.Keys(typedPatch))
	if err = refuseMarkers(typedPatch, names); err != nil {
		return Written{}, err
	}
	next := retype(def, cur.Fields)
	maps.Copy(next, typedPatch)
	dropNulls(next)
	whole, err := validate.Fields(def, next, validate.Whole)
	if err != nil {
		return Written{}, err
	}
	if err = e.checkRefs(ctx, def, whole, names); err != nil {
		return Written{}, err
	}
	sc, err := e.screen(ctx, cl, src, cur.ID, whole, names)
	if err != nil {
		return Written{}, err
	}
	actor := e.actorOf(cl)
	ours := cur
	ours.Fields = sc.fields
	ours.HLC = e.clock.Now(cur.HLC)
	ours.UpdatedAt, ours.UpdatedBy = ours.HLC.Wall, actor

	got, merged, err := e.putMerging(ctx, st, def, cur, ours)
	if err != nil {
		return Written{}, err
	}
	ev := Event{Op: cl.op, Action: ActionUpdate, Store: src, Table: cur.Table, ID: cur.ID, HLC: got.row.HLC, Actor: actor,
		Merged: merged, SecretsAccepted: sc.accepted, SecretsRedacted: sc.redacted}
	return Written{Row: got.row, Warnings: sc.warnings}, e.finish(ctx, src, []store.Row{got.doc}, ev)
}

// retype returns a copy of a stored row's fields with every field the
// definition declares re-decoded as its declared kind. A row read back
// from a store carries inferred values (value.Infer: a JSON string is
// Text), so a json column holding a string, or a dns/url/date column,
// would fail the whole-row check of an Update that does not patch it. A
// field that does not parse as its kind is kept as read: validation then
// reports it, as before. Fields the definition does not declare are kept.
func retype(def schema.TableDef, fields map[string]value.Value) map[string]value.Value {
	out := make(map[string]value.Value, len(fields))
	for name, v := range fields {
		out[name] = v
		f, ok := def.Field(name)
		if !ok || v.Kind() == f.Kind || v.Kind() == value.KindNull {
			continue
		}
		raw, err := v.MarshalJSON()
		if err != nil {
			continue
		}
		var typed value.Value
		if f.Kind == value.KindList && f.Elem != "" {
			typed, err = value.ParseList(f.Elem, raw)
		} else {
			typed, err = value.Parse(f.Kind, raw)
		}
		if err == nil {
			out[name] = typed
		}
	}
	return out
}

// putMerging writes ours over base; on ErrStale it re-reads, merges
// (logic/merge) and retries once. merged reports the retry.
func (e *Engine) putMerging(ctx context.Context, st Attached, def schema.TableDef, base, ours row.Row) (stored, bool, error) {
	got, err := e.put(ctx, st, ours, base.HLC)
	if !errors.Is(err, store.ErrStale) {
		return got, false, err
	}
	latest, err := st.Backend.Get(ctx, base.Table, base.ID)
	if err != nil {
		return stored{}, false, err
	}
	theirs, err := latest.Decode()
	if err != nil {
		return stored{}, false, err
	}
	res, err := merge.Rows(merge.PolicyFor(def), base, ours, theirs)
	if err != nil {
		return stored{}, false, err
	}
	if res.Row.Deleted {
		return stored{}, false, errs.New(errs.Stale, "engine: "+base.ID.String()+" was deleted while you edited it; your edit was not applied").
			WithHint("restore it from the trash first if the edit should stand")
	}
	if !res.Clean() {
		return stored{}, false, e.staleErr(base.ID, res.Conflicts, def)
	}
	m := res.Row
	whole, err := validate.Fields(def, m.Fields, validate.Whole)
	if err != nil {
		return stored{}, false, err
	}
	m.Fields = whole
	m.HLC = e.clock.Now(theirs.HLC, ours.HLC)
	m.UpdatedAt, m.UpdatedBy = m.HLC.Wall, ours.UpdatedBy
	got, err = e.put(ctx, st, m, theirs.HLC)
	if errors.Is(err, store.ErrStale) {
		return stored{}, false, errs.Wrap(errs.Stale, "engine: "+base.ID.String()+" kept changing while it was merged", err).
			WithHint("rerun the command")
	}
	return got, err == nil, err
}

// Conflict is the detail of an E_STALE text clash: both values of every
// clashing field, reachable with errors.As so a surface can print them.
// Sensitive fields carry no values (only the name).
type Conflict struct {
	ID     ids.AnyID
	Fields []merge.FieldConflict
}

// Error names the clashing fields, never their values.
func (c *Conflict) Error() string {
	names := make([]string, len(c.Fields))
	for i, f := range c.Fields {
		names[i] = f.Field
	}
	return "both sides changed " + strings.Join(names, ", ")
}

// staleErr builds the E_STALE error of a text clash. The message carries
// both values of every clashing field (newer side first) so a person can
// pick one; a sensitive field shows no values, and a value the scanner
// flags is masked (scan.Mask), so the error never leaks a secret.
func (e *Engine) staleErr(id ids.AnyID, fs []merge.FieldConflict, def schema.TableDef) error {
	c := &Conflict{ID: id}
	parts := make([]string, 0, len(fs))
	for _, f := range fs {
		if fd, ok := def.Field(f.Field); ok && fd.Sensitive {
			f.Newer, f.Older = nil, nil
			parts = append(parts, f.Field+" (sensitive: values not shown)")
		} else {
			parts = append(parts, f.Field+": newer "+e.shown(f.Field, f.Newer)+", older "+e.shown(f.Field, f.Older))
		}
		c.Fields = append(c.Fields, f)
	}
	return errs.Wrap(errs.Stale, "engine: "+id.String()+" changed while you edited it; "+strings.Join(parts, "; "), c).
		WithHint("both values are above: rerun the edit with the one to keep (or a merge of both)")
}

// staleShownMax bounds how many characters of one value an E_STALE message
// repeats.
const staleShownMax = 200

// shown renders one clashing value for an error: quoted, cut at
// staleShownMax, masked when the scanner flags it, "(none)" when absent.
func (e *Engine) shown(field string, v value.Value) string {
	if v == nil || v.Kind() == value.KindNull {
		return "(none)"
	}
	s := v.String()
	if found, _ := e.scanText(field, s); len(found) > 0 {
		return strconv.Quote(scan.Mask(s))
	}
	if r := []rune(s); len(r) > staleShownMax {
		s = string(r[:staleShownMax]) + "…"
	}
	return strconv.Quote(s)
}

// Delete writes a tombstone for a live row and applies every relation's
// on-delete rule first (DOCS O2): restrict refuses while live rows point at
// it (E_HAS_DEPENDENTS), cascade tombstones them too, set_null clears their
// field, tombstone_only leaves them. Tombstones are kept forever.
func (e *Engine) Delete(ctx context.Context, ref Ref) (Written, error) {
	cl := e.callOf(ctx)
	src, cur, err := e.locate(ctx, ref)
	if err != nil {
		return Written{}, err
	}
	if _, err = e.writable(src); err != nil {
		return Written{}, err
	}
	if cur.Deleted {
		return Written{}, errs.New(errs.NotFound, "engine: "+cur.ID.String()+" is already deleted")
	}
	plan, err := e.planDelete(ctx, src, cur)
	if err != nil {
		return Written{}, err
	}
	for name := range plan.byStore {
		if _, err := e.writable(name); err != nil { // check every store before writing any
			return Written{}, err
		}
	}
	actor := e.actorOf(cl)
	var w Written
	for _, name := range e.order {
		steps := plan.byStore[name]
		if len(steps) == 0 {
			continue
		}
		st, err := e.writable(name)
		if err != nil {
			return Written{}, err
		}
		changes := make([]store.Change, 0, len(steps))
		rows := make([]store.Row, 0, len(steps))
		events := make([]Event, 0, len(steps))
		for _, s := range steps {
			next, action := e.applyStep(s, actor)
			sr, err := store.FromRow(next)
			if err != nil {
				return Written{}, err
			}
			changes = append(changes, store.Change{Row: sr, Expect: s.row.HLC})
			rows = append(rows, sr)
			ev := Event{Op: cl.op, Action: action, Store: name, Table: next.Table, ID: next.ID, HLC: next.HLC, Actor: actor}
			if next.ID != cur.ID {
				ev.Cascade = cur.ID
				w.Also = append(w.Also, Changed{Store: name, Row: next})
			} else {
				w.Row = next
			}
			events = append(events, ev)
		}
		if err := st.Backend.Write(ctx, changes); err != nil {
			if errors.Is(err, store.ErrStale) {
				return Written{}, errs.Wrap(errs.Stale, "engine: a row changed while "+cur.ID.String()+" was deleted", err).WithHint("rerun the command")
			}
			return Written{}, err
		}
		if err := e.finish(ctx, name, rows, events...); err != nil {
			return w, err
		}
	}
	return w, nil
}

// step is one row change of a delete: a tombstone, or a cleared field.
type step struct {
	row   row.Row
	clear string // set_null: the field to clear; "" = tombstone
	// drop: with clear, remove only this id from the list field clear
	// (a many-to-many link to the deleted row); zero clears the field.
	drop ids.AnyID
}

// deletePlan groups a delete's steps by store.
type deletePlan struct {
	byStore map[store.Source][]step
}

// planDelete walks the relations from the deleted row (cascades
// recursively, each row once) and refuses on the first restrict.
func (e *Engine) planDelete(ctx context.Context, src store.Source, root row.Row) (deletePlan, error) {
	p := deletePlan{byStore: map[store.Source][]step{}}
	type item struct {
		src store.Source
		row row.Row
	}
	seen := map[ids.AnyID]bool{root.ID: true}
	cleared := map[string]bool{}
	queue := []item{{src, root}}
	p.byStore[src] = append(p.byStore[src], step{row: root})
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		deps, err := e.dependents(ctx, it.row.Table, it.row.ID)
		if err != nil {
			return deletePlan{}, err
		}
		for _, d := range deps {
			if seen[d.row.ID] {
				continue
			}
			if d.rule.list {
				switch d.rule.onDelete {
				case schema.Restrict:
					return deletePlan{}, errs.New(errs.HasDependents, "engine: "+d.row.ID.String()+" ("+string(d.rule.table)+"."+d.rule.field+") still lists "+it.row.ID.String()).
						WithHint("remove it from the list first")
				case schema.Cascade, schema.SetNull:
					key := d.row.ID.String() + "." + d.rule.field + "." + it.row.ID.String()
					if !cleared[key] {
						cleared[key] = true
						p.byStore[d.source] = append(p.byStore[d.source], step{row: d.row, clear: d.rule.field, drop: it.row.ID})
					}
				case schema.TombstoneOnly:
				}
				continue
			}
			switch d.rule.onDelete {
			case schema.Restrict:
				return deletePlan{}, errs.New(errs.HasDependents, "engine: "+d.row.ID.String()+" ("+string(d.rule.table)+"."+d.rule.field+") still points at "+it.row.ID.String()).
					WithHint("delete or move the pointing rows first")
			case schema.Cascade:
				seen[d.row.ID] = true
				p.byStore[d.source] = append(p.byStore[d.source], step{row: d.row})
				queue = append(queue, item{d.source, d.row})
			case schema.SetNull:
				key := d.row.ID.String() + "." + d.rule.field
				if !cleared[key] {
					cleared[key] = true
					p.byStore[d.source] = append(p.byStore[d.source], step{row: d.row, clear: d.rule.field})
				}
			case schema.TombstoneOnly:
			}
		}
	}
	// A row both cleared and tombstoned: the tombstone wins, one change per row.
	for name, steps := range p.byStore {
		var keep []step
		for _, s := range steps {
			if s.clear != "" && seen[s.row.ID] {
				continue
			}
			keep = append(keep, s)
		}
		p.byStore[name] = mergeClears(keep)
	}
	return p, nil
}

// mergeClears folds several set_null steps of one row into one step per
// row (a batch may name a row once), clearing every field (or, for a list
// link, dropping just the deleted id).
func mergeClears(steps []step) []step {
	var out []step
	idx := map[ids.AnyID]int{}
	for _, s := range steps {
		if i, ok := idx[s.row.ID]; ok && s.clear != "" {
			out[i].row = clearIn(out[i].row, s)
			continue
		}
		if s.clear != "" {
			s.row = clearIn(s.row, s)
		}
		idx[s.row.ID] = len(out)
		out = append(out, s)
	}
	return out
}

// clearIn returns r with step s applied: the field cleared, or the id
// s.drop removed from the list field (an emptied list is cleared).
func clearIn(r row.Row, s step) row.Row {
	r.Fields = maps.Clone(r.Fields)
	if l, ok := r.Fields[s.clear].(value.List); ok && !s.drop.IsZero() {
		var keep []value.Value
		for _, it := range l.Items() {
			if it.String() != s.drop.String() {
				keep = append(keep, it)
			}
		}
		if len(keep) > 0 {
			if nl, err := value.ListOf(l.Elem(), keep...); err == nil {
				r.Fields[s.clear] = nl
				return r
			}
		}
	}
	delete(r.Fields, s.clear)
	return r
}

// applyStep stamps one delete step. s.row already has cleared fields
// removed (mergeClears); its HLC is still the read one (the Expect).
func (e *Engine) applyStep(s step, actor ids.ID[ids.Actor]) (row.Row, Action) {
	h := e.clock.Now(s.row.HLC)
	if s.clear == "" {
		return row.Row{Table: s.row.Table, ID: s.row.ID, HLC: h, Deleted: true, UpdatedAt: h.Wall, UpdatedBy: actor}, ActionDelete
	}
	r := s.row
	r.HLC, r.UpdatedAt, r.UpdatedBy = h, h.Wall, actor
	return r, ActionUpdate
}

// Get returns one row, tombstones included (a tombstone answers "what was
// this"). The source store is returned with it.
func (e *Engine) Get(ctx context.Context, ref Ref) (store.Source, row.Row, error) {
	return e.locate(ctx, ref)
}

// List returns the live rows of table in one store, sorted by id. It reads
// the backend (the cache port has no query yet); search and sql read the
// cache lens instead.
func (e *Engine) List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error) {
	a, err := e.attached(source)
	if err != nil {
		return nil, err
	}
	rows, _, err := a.Backend.Read(ctx, "")
	if err != nil {
		return nil, err
	}
	var out []row.Row
	for _, sr := range rows {
		if sr.Table != table || sr.Deleted {
			continue
		}
		r, err := sr.Decode()
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b row.Row) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	return out, nil
}

// locate finds ref's row and the store holding it.
func (e *Engine) locate(ctx context.Context, ref Ref) (store.Source, row.Row, error) {
	table := ref.Table
	if table == "" {
		def, ok := e.reg.ByPrefix(ref.ID.Prefix())
		if !ok {
			return "", row.Row{}, errs.New(errs.NotFound, "engine: no table holds ids like "+ref.ID.String())
		}
		table = def.Name
	}
	names := e.order
	if ref.Store != "" {
		if _, err := e.attached(ref.Store); err != nil {
			return "", row.Row{}, err
		}
		names = []store.Source{ref.Store}
	}
	var found []store.Source
	var got row.Row
	for _, name := range names {
		sr, err := e.stores[name].Backend.Get(ctx, table, ref.ID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return "", row.Row{}, err
		}
		if got, err = sr.Decode(); err != nil {
			return "", row.Row{}, err
		}
		found = append(found, name)
	}
	switch len(found) {
	case 0:
		return "", row.Row{}, errs.Wrap(errs.NotFound, "engine: no row "+ref.ID.String(), store.ErrNotFound)
	case 1:
		return found[0], got, nil
	}
	parts := make([]string, len(found))
	for i, f := range found {
		parts[i] = string(f)
	}
	return "", row.Row{}, errs.New(errs.AmbiguousID, "engine: "+ref.ID.String()+" is in "+strings.Join(parts, " and ")).
		WithHint("name the store with --to")
}

// put writes one row with expect and returns it as stored, in both forms:
// the typed model row for the caller (its bytes are exactly doc's) and the
// backend row for the cache.
func (e *Engine) put(ctx context.Context, st Attached, r row.Row, expect hlc.HLC) (stored, error) {
	sr, err := store.FromRow(r)
	if err != nil {
		return stored{}, err
	}
	if err := st.Backend.Write(ctx, []store.Change{{Row: sr, Expect: expect}}); err != nil {
		return stored{}, err
	}
	return stored{row: r, doc: sr}, nil
}

// stored is one row as the backend accepted it.
type stored struct {
	row row.Row
	doc store.Row
}

// finish refreshes the cache with the stored rows and records the audit
// events. The cache cursor is left where it was (the rows are re-read by the
// next Refresh, an idempotent upsert), so a write never moves a cursor past
// changes it has not seen.
func (e *Engine) finish(ctx context.Context, source store.Source, rows []store.Row, evs ...Event) error {
	cur, err := e.cache.Cursor(ctx, source)
	if err != nil {
		return err
	}
	if err := e.cache.Apply(ctx, source, rows, cur); err != nil {
		return err
	}
	for _, ev := range evs {
		if err := e.audit.Record(ctx, ev); err != nil {
			return errs.Wrap(errs.Internal, "engine: the row is stored but its audit record failed", err)
		}
	}
	return nil
}

// dropNulls removes cleared fields: a null field is stored as absent.
func dropNulls(m map[string]value.Value) {
	for k, v := range m {
		if v.Kind() == value.KindNull {
			delete(m, k)
		}
	}
}

// refuseMarkers refuses text that still holds merge conflict markers
// (spec 11 rule 2): marker text is never stored as knowledge.
func refuseMarkers(fields map[string]value.Value, names []string) error {
	for _, n := range names {
		if t, ok := fields[n].(value.Text); ok && merge.HasMarkers(t.String()) {
			return errs.New(errs.InvalidInput, "engine: field "+n+" still holds merge conflict markers").
				WithHint("keep one side, delete the marker lines, then save again")
		}
	}
	return nil
}

// originOf builds the _o block written once at create.
func originOf(opName string, target store.Source) (value.JSON, error) {
	ms := []canon.Member{{Key: originStore, Raw: canon.String(string(target))}}
	if opName != "" {
		ms = append(ms, canon.Member{Key: originCmd, Raw: canon.String(strings.ReplaceAll(opName, ".", " "))})
	}
	obj, err := canon.Object(ms)
	if err != nil {
		return value.JSON{}, errs.Wrap(errs.Internal, "engine: build _o", err)
	}
	return value.JSONOf(obj)
}
