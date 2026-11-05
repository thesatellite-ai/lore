package engine

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/logic/validate"
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
	def, err := e.tableDef(table)
	if err != nil {
		return Written{}, err
	}
	if target, err = e.routeTable(target, def); err != nil { // P3-LOCAL-1
		return Written{}, err
	}
	st, err := e.writable(target)
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
	if err = e.checkRefs(ctx, target, def, typed, names); err != nil {
		return Written{}, err
	}
	if pin.IsZero() { // FB-RES-1: an id pinned on ctx (WithCreateID)
		if p, ok := createIDOf(ctx, def.Prefix); ok {
			pin = p
		}
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
		id, err = NaturalID(def.Prefix, naturalParts(st, def, keyParts)...)
	default:
		id, err = e.ids.NewID(def.Prefix)
	}
	if err != nil {
		return Written{}, err
	}
	// the fields' findings are answered together with the metadata's
	// (one count, one question; P3-METASEC-2): detect now, resolve in the
	// metadata step, or below when that step scanned nothing
	sc, err := e.detect(ctx, cl, target, id, typed, names)
	if err != nil {
		return Written{}, err
	}
	// metadata riding on this create (engine/meta.go; R13)
	// (createMeta also applies the 3.19 defaults and required keys)
	var rmeta map[ids.ID[ids.MetaKey]]value.JSON
	mw, hasMeta := metaFor(ctx, table, ids.AnyID{})
	var mwarn []Warning
	if rmeta, mwarn, err = e.createMetaScreened(ctx, cl, target, def, id, mw, hasMeta, &sc); err != nil {
		return Written{}, err
	}
	sc.warnings = append(sc.warnings, mwarn...)
	if !sc.resolved {
		if err = e.resolve(ctx, cl, target, id, &sc, nil); err != nil {
			return Written{}, err
		}
	}
	if err = enforceRow(sc.pol, def, sc.fields); err != nil {
		return Written{}, err
	}
	origin, err := e.originOf(ctx, cl.op, st)
	if err != nil {
		return Written{}, err
	}
	actor := e.actorOf(cl)
	stamp := e.clock.Now()
	r := row.Row{
		Table: table, ID: id, HLC: stamp, Origin: origin, Fields: sc.fields, Meta: rmeta,
		CreatedAt: stamp.Wall, CreatedBy: actor, UpdatedAt: stamp.Wall, UpdatedBy: actor,
	}
	allows, err := e.allowRows(cl, sc.accepted, id, actor, stamp)
	if err != nil {
		return Written{}, err
	}
	got, err := e.put(ctx, st, r, hlc.HLC{}, allows...)
	if natural && (errors.Is(err, store.ErrStale) || errs.Has(err, errs.Purged)) {
		taken := err // why the id is taken (kept: allowRows below reuses err)
		// the re-created row gets a fresh id: its allow entries follow it
		if allows, err = e.allowRows(cl, sc.accepted, ids.AnyID{}, actor, stamp); err != nil {
			return Written{}, err
		}
		got, err = e.recreateNatural(ctx, st, def, keyFields, r, taken, allows)
		id = got.row.ID
	}
	if err != nil {
		return Written{}, err
	}
	ev := Event{Op: cl.op, Action: ActionCreate, Store: target, Table: table, ID: id, HLC: got.row.HLC, Actor: actor,
		SecretsAccepted: sc.accepted, SecretsRedacted: sc.redacted}
	aw, err := e.finishW(ctx, target, got.docs(), append(got.allowEvents(cl.op, target), ev)...)
	return Written{Row: got.row, Warnings: append(sc.warnings, aw...)}, err
}

// recreateNatural handles a natural-id create whose id is taken (stale is
// the put's ErrStale; natural-id re-create, L89 + L93). Tombstones are
// kept forever (DQ4), so a deleted tag, commit link, var, policy or kind
// could otherwise never be added again: when the row at the natural id is
// a tombstone and no LIVE row of the table in that store holds the same
// key (an earlier re-create got a random id), the row is written under a fresh random id instead. A live
// row with the key stays "already exists". Two machines re-creating the
// same key offline get two rows (spec-deltas; a merge by key is later
// work).
func (e *Engine) recreateNatural(ctx context.Context, st Attached, def schema.TableDef, keyFields []string, r row.Row, stale error, allows []row.Row) (stored, error) {
	exists := func(id ids.AnyID) error {
		return errs.Wrap(errs.InvalidInput, "engine: a "+string(def.Name)+" row with this "+strings.Join(keyFields, ", ")+" already exists ("+id.String()+")", stale).
			WithHint("use the existing row")
	}
	_, cur, err := e.locate(ctx, Ref{Store: st.Name, Table: def.Name, ID: r.ID})
	switch {
	case errs.Has(err, errs.NotFound) && errs.Has(stale, errs.Purged):
		// purged and its tombstone file gone: the id is still taken for good
	case err != nil:
		return stored{}, err
	case !cur.Deleted && sameKey(cur.Fields, r.Fields, keyFields):
		return stored{}, exists(r.ID)
	}
	// A live row at the natural id whose key fields were edited since
	// (`lore scope move` relabels a scope: ids never follow a label)
	// does not hold this key any more: like a tombstone, the new row
	// takes a fresh random id unless another live row holds the key.
	live, err := e.List(ctx, st.Name, def.Name)
	if err != nil {
		return stored{}, err
	}
	for _, l := range live {
		if sameKey(l.Fields, r.Fields, keyFields) {
			return stored{}, exists(l.ID)
		}
	}
	if r.ID, err = e.ids.NewID(def.Prefix); err != nil {
		return stored{}, err
	}
	if allows, err = pointAllows(allows, r.ID); err != nil {
		return stored{}, err
	}
	return e.put(ctx, st, r, hlc.HLC{}, allows...)
}

// sameKey reports whether a and b hold equal values in every key field.
func sameKey(a, b map[string]value.Value, keyFields []string) bool {
	for _, f := range keyFields {
		x, okx := a[f]
		y, oky := b[f]
		if !okx || !oky || x.String() != y.String() {
			return false
		}
	}
	return true
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
		return Written{}, deletedErr(ctx, st, cur, "is deleted")
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
	next, overruled := setAsideOverruled(next) // a merge's note, not a table field (overruled.go)
	whole, err := validate.Fields(def, next, validate.Whole)
	if err != nil {
		return Written{}, err
	}
	if err = e.checkRefs(ctx, src, def, whole, names); err != nil {
		return Written{}, err
	}
	// fields and metadata answered together (P3-METASEC-2, as Create)
	sc, err := e.detect(ctx, cl, src, cur.ID, whole, names)
	if err != nil {
		return Written{}, err
	}
	actor := e.actorOf(cl)
	ours := cur
	// metadata riding on this update (engine/meta.go; R13)
	if mw, ok := metaFor(ctx, cur.Table, cur.ID); ok {
		var mwarn []Warning
		if ours.Meta, mwarn, err = e.applyMetaScreened(ctx, cl, src, def, cur.ID, cur.Meta, mw, &sc); err != nil {
			return Written{}, err
		}
		sc.warnings = append(sc.warnings, mwarn...)
	}
	if !sc.resolved {
		if err = e.resolve(ctx, cl, src, cur.ID, &sc, nil); err != nil {
			return Written{}, err
		}
	}
	if err = enforceRow(sc.pol, def, sc.fields); err != nil {
		return Written{}, err
	}
	ours.Fields = putBackOverruled(sc.fields, overruled)
	if unchangedUpdate(ctx, def, cur, ours, sc) {
		// the same write twice is a no-op (LORE_TEAM_SPEC F2 step 3): no
		// new HLC, no updated_at, so the store sees no change and a live
		// ref makes no commit and no push
		return Written{Row: cur, Warnings: sc.warnings}, nil
	}
	ours.HLC = e.clock.Now(cur.HLC)
	ours.UpdatedAt, ours.UpdatedBy = ours.HLC.Wall, actor

	allows, err := e.allowRows(cl, sc.accepted, cur.ID, actor, ours.HLC)
	if err != nil {
		return Written{}, err
	}
	got, merged, err := e.putMerging(ctx, st, def, cur, ours, allows)
	if err != nil {
		return Written{}, err
	}
	ev := Event{Op: cl.op, Action: ActionUpdate, Store: src, Table: cur.Table, ID: cur.ID, HLC: got.row.HLC, Actor: actor,
		Merged: merged, SecretsAccepted: sc.accepted, SecretsRedacted: sc.redacted}
	aw, err := e.finishW(ctx, src, got.docs(), append(got.allowEvents(cl.op, src), ev)...)
	return Written{Row: got.row, Warnings: append(sc.warnings, aw...)}, err
}

// unchangedUpdate reports an Update that changes nothing: every field and
// every metadata value equals the stored row's, no secret finding was
// accepted or redacted, and no explicit --live moves the row (an edit with
// --live and no field change still moves a branch row into live, F1).
func unchangedUpdate(ctx context.Context, def schema.TableDef, cur, ours row.Row, sc screened) bool {
	if len(sc.accepted) > 0 || len(sc.redacted) > 0 {
		return false
	}
	if store.LiveChoiceFrom(ctx).For(cur.Table) != store.LiveDefault {
		return false
	}
	was := retype(def, cur.Fields)
	if len(was) != len(ours.Fields) || len(cur.Meta) != len(ours.Meta) {
		return false
	}
	for k, v := range ours.Fields {
		if !value.Equal(was[k], v) {
			return false
		}
	}
	for k, v := range ours.Meta {
		old, ok := cur.Meta[k]
		if !ok || !value.Equal(old, v) {
			return false
		}
	}
	return true
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
func (e *Engine) putMerging(ctx context.Context, st Attached, def schema.TableDef, base, ours row.Row, allows []row.Row) (stored, bool, error) {
	got, err := e.put(ctx, st, ours, base.HLC, allows...)
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
	// Rows read back carry inferred kinds (a JSON array reads as a list):
	// retype base and theirs as Update retypes cur, or the merged row fails
	// the whole-row check on every json field (found by PC-LEAK-1's merge
	// matrix: a merged task update with time_entries was E_RESOURCE_INVALID).
	base.Fields, theirs.Fields = retype(def, base.Fields), retype(def, theirs.Fields)
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
	rest, overruled := setAsideOverruled(m.Fields) // a merge's note, not a table field (overruled.go)
	whole, err := validate.Fields(def, rest, validate.Whole)
	if err != nil {
		return stored{}, false, err
	}
	m.Fields = putBackOverruled(whole, overruled)
	m.HLC = e.clock.Now(theirs.HLC, ours.HLC)
	m.UpdatedAt, m.UpdatedBy = m.HLC.Wall, ours.UpdatedBy
	got, err = e.put(ctx, st, m, theirs.HLC, allows...)
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

// Delete moves a live row into the trash (DQ8, DELETION 3.1; softDelete in
// trash.go): restrict refuses while live rows point at it
// (E_HAS_DEPENDENTS), cascade dependents join its batch, referrers stay as
// they are (D8). A row already in the trash answers E_IN_TRASH. remove
// below is the tombstone write purge runs: every relation's on-delete rule
// applies there (DOCS O2), and tombstones are kept forever (DQ4).
func (e *Engine) Delete(ctx context.Context, ref Ref) (Written, error) {
	src, cur, err := e.locate(ctx, ref)
	if err != nil {
		return Written{}, err
	}
	if err = refuseActorDelete(cur); err != nil {
		return Written{}, err
	}
	st, err := e.writable(src)
	if err != nil {
		return Written{}, err
	}
	if cur.Deleted {
		return Written{}, deletedErr(ctx, st, cur, "is already deleted") // E_IN_TRASH for a trashed row
	}
	return e.softDelete(ctx, src, cur) // DQ8: every delete is soft, into the trash (DELETION 3.1)
}

// remove is Delete's write for the live row cur of store src. With purge
// every store the plan tombstones in must be a Purger, and each tombstoned
// id is also added to that folder's _purged.json in the same batch.
func (e *Engine) remove(ctx context.Context, src store.Source, cur row.Row, purge bool) (Written, error) {
	cl := e.callOf(ctx)
	plan, err := e.planDelete(ctx, src, cur)
	if err != nil {
		return Written{}, err
	}
	for name := range plan.byStore {
		st, err := e.writable(name) // check every store before writing any
		if err != nil {
			return Written{}, err
		}
		if _, ok := st.Backend.(Purger); purge && !ok {
			return Written{}, errs.New(errs.Unsupported, "engine: store "+string(name)+" cannot record purged ids")
		}
	}
	actor := e.actorOf(cl)
	var w Written
	for _, name := range e.allStores() { // P3-LOCAL-1: a plan may tombstone machine-local rows
		steps := plan.byStore[name]
		if len(steps) == 0 {
			continue
		}
		st, err := e.writable(name)
		if err != nil {
			return Written{}, err
		}
		if err = e.gate(ctx, st, stepRows(steps)...); err != nil {
			return Written{}, err // FORMAT_EVOLUTION rule 2
		}
		changes := make([]store.Change, 0, len(steps))
		rows := make([]store.Row, 0, len(steps))
		events := make([]Event, 0, len(steps))
		var purged []ids.AnyID
		for _, s := range steps {
			next, action := e.applyStep(s, actor)
			if purge && next.Deleted {
				purged = append(purged, next.ID)
				action = ActionPurge
			}
			next = e.forStore(st, next)
			if next, err = e.stampWrite(st, next); err != nil { // a set_null clear changes content: _w (OQ7)
				return Written{}, err
			}
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
		var werr error
		if p, ok := st.Backend.(Purger); purge && ok && len(purged) > 0 {
			werr = p.WritePurge(ctx, changes, purged)
		} else {
			werr = e.writeChanges(ctx, st, changes, events) // CLEANUP Qs: audit in the same batch
		}
		if err := werr; err != nil {
			if errors.Is(err, store.ErrStale) {
				return Written{}, errs.Wrap(errs.Stale, "engine: a row changed while "+cur.ID.String()+" was deleted", err).WithHint("rerun the command")
			}
			return Written{}, err
		}
		aw, err := e.finishW(ctx, name, rows, events...)
		w.Warnings = append(w.Warnings, aw...)
		if err != nil {
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

// stepRows is the rows a delete plan's steps touch (for the format gate).
func stepRows(steps []step) []row.Row {
	out := make([]row.Row, 0, len(steps))
	for _, s := range steps {
		out = append(out, s.row)
	}
	return out
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
	if !s.drop.IsZero() {
		// any form: a List of refs (in memory), of texts, or a JSON array
		// (a row file Infer could not type); the kept ids become refs
		var keep []value.Value
		for _, it := range value.ItemStrings(r.Fields[s.clear]) {
			if it == s.drop.String() {
				continue
			}
			id, err := ids.ParseAny(it)
			if err != nil {
				return clearAll(r, s.clear) // not a list of ids: clearing is the safe answer
			}
			ref, err := value.RefOf(id)
			if err != nil {
				return clearAll(r, s.clear)
			}
			keep = append(keep, ref)
		}
		if len(keep) > 0 {
			if nl, err := value.ListOf(value.KindRef, keep...); err == nil {
				r.Fields[s.clear] = nl
				return r
			}
		}
	}
	return clearAll(r, s.clear)
}

// clearAll removes field f from r (r.Fields already cloned).
func clearAll(r row.Row, f string) row.Row {
	delete(r.Fields, f)
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

// locate finds ref's row and the store holding it. A purged id whose live
// file came back reads as a tombstone (hidePurged).
func (e *Engine) locate(ctx context.Context, ref Ref) (store.Source, row.Row, error) {
	src, r, _, err := e.locateRaw(ctx, ref)
	return src, r, err
}

// locateRaw is locate that also reports whether the row found is a purged
// id's resurrected file shown as a tombstone (Purge repairs it).
func (e *Engine) locateRaw(ctx context.Context, ref Ref) (store.Source, row.Row, bool, error) {
	table := ref.Table
	if table == "" {
		def, ok := e.reg.ByPrefix(ref.ID.Prefix())
		if !ok {
			return "", row.Row{}, false, errs.New(errs.NotFound, "engine: no table holds ids like "+ref.ID.String())
		}
		table = def.Name
	}
	names := e.lookupStores(table) // P3-LOCAL-1: a machine-local table is looked up in the machine store only
	if ref.Store != "" && !e.IsMachineLocal(table) {
		if _, err := e.attached(ref.Store); err != nil {
			return "", row.Row{}, false, err
		}
		names = []store.Source{ref.Store}
	}
	var found []copyHit
	seen := map[string]bool{} // Locations already looked in: one place counts once
	for _, name := range names {
		if loc := e.stores[name].Location; loc != "" {
			if seen[loc] {
				continue
			}
			seen[loc] = true
		}
		sr, res, err := e.getRow(ctx, name, table, ref.ID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return "", row.Row{}, false, err
		}
		got, err := sr.Decode()
		if err != nil {
			return "", row.Row{}, false, err
		}
		found = append(found, copyHit{src: name, row: got, hidden: res})
	}
	if len(found) == 0 {
		return "", row.Row{}, false, errs.Wrap(errs.NotFound, "engine: no row "+ref.ID.String(), store.ErrNotFound)
	}
	h, err := pickCopy(ref.ID, found)
	if err != nil {
		return "", row.Row{}, false, err
	}
	return h.src, h.row, h.hidden, nil
}

// copyHit is one store's copy of an id that locate found.
type copyHit struct {
	src    store.Source
	row    row.Row
	hidden bool // a purged id's resurrected file, shown as a tombstone
}

// pickCopy chooses the copy an unqualified ref means (found is in
// precedence order, never empty). A live copy wins over tombstones: a
// move leaves a tombstone where the row was (spec 11 rule 3), so the
// moved row stays addressable by its bare id. One live copy is the
// answer; two or more are E_AMBIGUOUS_ID naming the stores. With no live
// copy the nearest tombstone answers (`show` says deleted, a purged id
// says E_PURGED on a write).
func pickCopy(id ids.AnyID, found []copyHit) (copyHit, error) {
	var live []copyHit
	for _, h := range found {
		if !h.row.Deleted {
			live = append(live, h)
		}
	}
	switch len(live) {
	case 0:
		return found[0], nil
	case 1:
		return live[0], nil
	}
	parts := make([]string, len(live))
	for i, h := range live {
		parts[i] = string(h.src)
	}
	return copyHit{}, errs.New(errs.AmbiguousID, "engine: "+id.String()+" is in "+strings.Join(parts, " and ")).
		WithHint("name the store with --to")
}

// put writes one row with expect and returns it as stored, in both forms:
// the typed model row for the caller (its bytes are exactly doc's) and the
// backend row for the cache.
//
// extra are new rows written in the SAME backend batch (all or none): the
// secret_allows entries a confirmed write records (SQ2). They expect no
// row (fresh ids).
func (e *Engine) put(ctx context.Context, st Attached, r row.Row, expect hlc.HLC, extra ...row.Row) (stored, error) {
	if err := refusePurged(ctx, st, r.Table, r.ID, r.Deleted); err != nil {
		return stored{}, err // every live write (create, update, merge, move) passes here
	}
	if err := e.gate(ctx, st, append([]row.Row{r}, extra...)...); err != nil {
		return stored{}, err // FORMAT_EVOLUTION rule 2: read-only for a format this lore cannot write
	}
	r = e.forStore(st, r)
	r, err := e.stampWrite(st, r)
	if err != nil {
		return stored{}, err
	}
	sr, err := store.FromRow(r)
	if err != nil {
		return stored{}, err
	}
	changes := []store.Change{{Row: sr, Expect: expect}}
	out := stored{row: r, doc: sr}
	for _, x := range extra {
		x = e.forStore(st, x)
		xr, xerr := store.FromRow(x)
		if xerr != nil {
			return stored{}, xerr
		}
		changes = append(changes, store.Change{Row: xr})
		out.extra = append(out.extra, x)
		out.extraDocs = append(out.extraDocs, xr)
	}
	// CLEANUP Qs: the audit record rides in the row's own batch.
	evs := make([]Event, 0, 1+len(out.extra))
	evs = append(evs, Event{Op: e.callOf(ctx).op, Action: putAction(expect, r), Store: st.Name, Table: r.Table, ID: r.ID, HLC: r.HLC, Actor: r.UpdatedBy})
	evs = append(evs, out.allowEvents(e.callOf(ctx).op, st.Name)...)
	if s := stageOf(ctx); s != nil { // FB-RES-1: held until Commit
		kept, err := s.addWrite(e, st, changes, evs)
		if err != nil {
			return stored{}, err
		}
		if !kept { // a second create of a row the stage already creates
			out.extra, out.extraDocs = nil, nil
		}
		return out, nil
	}
	if err := e.writeChanges(ctx, st, changes, evs); err != nil {
		return stored{}, err
	}
	return out, nil
}

// putAction is the action a put records in the backend's audit journal
// (the caller's full event replaces it once the sink takes it).
func putAction(expect hlc.HLC, r row.Row) Action {
	switch {
	case r.Deleted:
		return ActionDelete
	case expect.IsZero():
		return ActionCreate
	default:
		return ActionUpdate
	}
}

// stored is one row as the backend accepted it, with the extra rows of
// the same batch.
type stored struct {
	row       row.Row
	doc       store.Row
	extra     []row.Row
	extraDocs []store.Row
}

// docs is every backend row of the batch, the main row first.
func (s stored) docs() []store.Row { return append([]store.Row{s.doc}, s.extraDocs...) }

// allowEvents audits each extra row as a create.
func (s stored) allowEvents(opName string, target store.Source) []Event {
	evs := make([]Event, 0, len(s.extra))
	for _, x := range s.extra {
		evs = append(evs, Event{Op: opName, Action: ActionCreate, Store: target, Table: x.Table, ID: x.ID, HLC: x.HLC, Actor: x.CreatedBy})
	}
	return evs
}

// finish refreshes the cache with the stored rows and records the audit
// events (finishW without the warnings: a deferred audit record stays in
// the backend's journal and is replayed, so it is not an error here).
// Only for a write with no output to carry a warning (RelabelMeta, a
// label-only rewrite); every write that returns Written or TrashResult
// uses finishW so W_AUDIT_DEFERRED reaches the person (later 542).
func (e *Engine) finish(ctx context.Context, source store.Source, rows []store.Row, evs ...Event) error {
	_, err := e.finishW(ctx, source, rows, evs...)
	return err
}

// finishW refreshes the cache with the stored rows and records the audit
// events. The cache cursor is left where it was (the rows are re-read by
// the next Refresh, an idempotent upsert), so a write never moves a cursor
// past changes it has not seen.
//
//	cache.Apply → per event: sink.Record
//	  ok     → ack the backend's journal record of that row (CLEANUP Qs)
//	  failed → journaled: W_AUDIT_DEFERRED (kept, replayed by a later write)
//	           not journaled: the error, naming its cause (cl-wire)
//	→ replay older pending records of this store (best effort)
func (e *Engine) finishW(ctx context.Context, source store.Source, rows []store.Row, evs ...Event) ([]Warning, error) {
	if s := stageOf(ctx); s != nil { // FB-RES-1: the cache and audit follow the write at Commit
		s.addFinish(e, source, rows, evs)
		return nil, nil
	}
	cur, err := e.cache.Cursor(ctx, source)
	if err != nil {
		return nil, err
	}
	if err := e.cache.Apply(ctx, source, rows, cur); err != nil {
		return nil, err
	}
	j, _ := e.stores[source].Backend.(store.AuditJournal)
	mine := make(map[store.Key]hlc.HLC, len(rows))
	for _, r := range rows {
		mine[r.Key()] = r.HLC
	}
	var acks []store.AuditRecord
	var warns []Warning
	var pending map[store.Key]bool // lazily read on the first failure
	for _, ev := range evs {
		ev = e.auditView(ctx, source, ev)
		key := store.Key{Table: ev.Table, ID: ev.ID}
		h, ours := mine[key]
		rerr := e.audit.Record(ctx, ev)
		if rerr == nil {
			if j != nil && ours {
				acks = append(acks, store.AuditRecord{Key: key, HLC: h})
			}
			continue
		}
		if j != nil && ours && pending == nil {
			pending = map[store.Key]bool{}
			if recs, perr := j.PendingAudit(ctx); perr == nil {
				for _, rc := range recs {
					if mine[rc.Key].Compare(rc.HLC) == 0 {
						pending[rc.Key] = true
					}
				}
			}
		}
		if pending[key] {
			warns = append(warns, Warning{Code: WarnAuditDeferred, Message: "the audit record of " + ev.ID.String() +
				" is kept with the row and will be added to the audit log by a later write: " + rerr.Error()})
			continue
		}
		return warns, auditFailed(ev.ID, rerr)
	}
	if j != nil {
		if aerr := j.AckAudit(ctx, acks); aerr != nil {
			warns = append(warns, Warning{Code: WarnAuditDeferred, Message: "the audit journal could not be cleared (a later write may record these events again): " + aerr.Error()})
		}
		e.replayJournal(ctx, source, j, mine)
	}
	return warns, nil
}

// auditView is ev as the sink sees it: the store's actor mapping (ActorAs)
// and the running bulk action's batch (XQ1). put uses it too, so the
// journal holds the same event the sink would have got.
func (e *Engine) auditView(ctx context.Context, source store.Source, ev Event) Event {
	if as := e.stores[source].ActorAs; as != nil {
		if !ev.Actor.IsZero() {
			ev.Actor = as(ev.Actor)
		}
		if !ev.HLC.Actor.IsZero() {
			ev.HLC.Actor = as(ev.HLC.Actor)
		}
	}
	if b, ok := BatchOf(ctx); ok {
		ev.Batch = b // XQ1: every row of a bulk action carries its one batch id
	}
	return ev
}

// auditFailed is the error of a row stored without its audit record: the
// message names the row and the cause, and the code is the cause's own
// (cl-wire: the cause was hidden, also under --json).
func auditFailed(id ids.AnyID, cause error) error {
	code := errs.CodeOf(cause)
	return errs.Wrap(code, "engine: the row "+id.String()+" is stored but its audit record failed: "+cause.Error(), cause).
		WithHint("do not rerun the write (it would store the row again); fix the cause, then check `lore audit verify`")
}

// writeChanges writes changes and, when the backend can (store.AuditJournal),
// the audit records of evs in the SAME batch: both land or neither does
// (CLEANUP Qs). Other backends get a plain Write and finish records the
// audit after it, as before.
func (e *Engine) writeChanges(ctx context.Context, st Attached, changes []store.Change, evs []Event) error {
	ctx = withBatchTrailer(ctx) // XQ1: the commit names its bulk action
	j, ok := st.Backend.(store.AuditJournal)
	if !ok || len(evs) == 0 {
		return st.Backend.Write(ctx, changes)
	}
	recs := make([]store.AuditRecord, 0, len(evs))
	machine := e.thisMachine()
	for _, ev := range evs {
		rec, err := journalRecord(e.auditView(ctx, st.Name, ev), machine)
		if err != nil {
			return err
		}
		recs = append(recs, rec)
	}
	return j.WriteAudited(ctx, changes, recs)
}

// journalReplayAge is how old a pending record must be before another
// write replays it: younger ones may belong to a write still on its way
// to its own finish (another process), which records and acks them itself.
const journalReplayAge = time.Minute

// replayJournal records this store's older pending audit records (left by
// a write whose sink failed, or a crash) and acks each one recorded. Best
// effort: a record that fails again stays pending for the next write.
// Only this machine's records are replayed (G-DECIDE-1): a folder store
// on a shared folder may hold another machine's journal, whose records
// belong to that machine's trail; they stay pending, never acked here.
func (e *Engine) replayJournal(ctx context.Context, source store.Source, j store.AuditJournal, mine map[store.Key]hlc.HLC) {
	recs, err := j.PendingAudit(ctx)
	if err != nil || len(recs) == 0 {
		return
	}
	now := e.wall()
	machine := e.thisMachine()
	var done []store.AuditRecord
	for _, rc := range recs {
		if _, ours := mine[rc.Key]; ours || now.Sub(rc.HLC.Wall) < journalReplayAge {
			continue
		}
		ev, by, derr := eventOfRecord(rc)
		if derr != nil {
			done = append(done, rc) // unreadable: never recordable, drop it
			continue
		}
		if by != machine {
			continue // another machine's record: its own writes replay it
		}
		ev.Store = source
		if e.audit.Record(ctx, ev) == nil {
			done = append(done, rc)
		}
	}
	if len(done) > 0 {
		_ = j.AckAudit(ctx, done) // best effort: a failed ack replays them again
	}
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

// actorDeactivateHint is the way out a refused actor delete names.
const actorDeactivateHint = "deactivate the person instead: lore actor deactivate <@handle> (the row stays, marked inactive)"

// refuseActorDelete refuses a delete or purge of an actor row.
//
// BATCH2 #8: actors are never deleted (DELETION 4.1): every *_by field of
// every row, deleted_by included, points at an actor, so a tombstoned actor
// would leave attribution dangling. The refusal is E_REFUSED naming the
// actor and `lore actor deactivate`, here in the engine so every surface
// (a generic delete, REST, a future actor op) gets it, not only the CLI.
func refuseActorDelete(r row.Row) error {
	if r.Table != schema.TableActors {
		return nil
	}
	return errs.New(errs.Refused, "actor "+r.ID.String()+" cannot be deleted: actors are never deleted, every *_by field points at them").
		WithHint(actorDeactivateHint)
}
