package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// state is the set of typed tables the cache maintains: one plan per
// built-in definition plus one per usable kind.
type state struct {
	plans map[row.Table]plan
	// kinds maps a kind id to the projection of its resources.
	kinds map[ids.ID[ids.Kind]]kindInfo
}

// kindInfo is what projecting a resource of one kind needs.
type kindInfo struct {
	table row.Table
	// fields maps a kind_fields row id (the key of a resource's `fields`
	// object, stable across renames) to the field's definition.
	fields map[string]schema.FieldDef
}

// names returns the plan names sorted, for deterministic statement order.
func (s state) names() []row.Table {
	out := make([]row.Table, 0, len(s.plans))
	for n := range s.plans {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// loadState builds the wanted state: the registry's tables plus the kinds
// whose rows `_rows` holds now.
//
// A kind that cannot be built (a broken definition, a newer field type, a
// name taken by another table) is left out rather than failing: its rows
// are still in `_rows` and in the `resources` table, and lint reports the
// definition; one bad settings row must not stop the whole cache.
// Duplicates of one kind id across sources take the first by source name.
func (c *Cache) loadState(ctx context.Context, tx *sql.Tx) (state, error) {
	st := state{plans: map[row.Table]plan{}, kinds: map[ids.ID[ids.Kind]]kindInfo{}}
	for _, def := range c.base.Tables() {
		p, err := planFor(def, "")
		if err != nil {
			return state{}, err
		}
		st.plans[def.Name] = p
	}
	kinds, err := liveRows(ctx, tx, schema.TableKinds)
	if err != nil {
		return state{}, err
	}
	fieldRows, err := liveRows(ctx, tx, schema.TableKindFields)
	if err != nil {
		return state{}, err
	}
	byKind := map[string][]row.Row{}
	for _, fr := range fieldRows {
		if v, ok := fr.Fields[fieldKindID]; ok {
			byKind[v.String()] = append(byKind[v.String()], fr)
		}
	}
	for _, k := range kinds {
		kindID, err := ids.As[ids.Kind](k.ID)
		if err != nil {
			continue
		}
		if _, dup := st.kinds[kindID]; dup {
			continue
		}
		def, err := schema.KindDefFromRows(k, byKind[k.ID.String()])
		if err != nil {
			continue
		}
		if _, err = c.base.With(def); err != nil {
			continue
		}
		view := schema.KindTablePrefix + kindID.String()
		if _, taken := st.plans[def.Name]; taken || viewTaken(st, view, def.Name) {
			continue
		}
		p, err := planFor(def, view)
		if err != nil {
			continue
		}
		st.plans[def.Name] = p
		info := kindInfo{table: def.Name, fields: map[string]schema.FieldDef{}}
		for _, fr := range byKind[k.ID.String()] {
			name, ok := fr.Fields[fieldName]
			if !ok {
				continue
			}
			if f, ok := def.Field(name.String()); ok {
				info.fields[fr.ID.String()] = f
			}
		}
		st.kinds[kindID] = info
	}
	return st, nil
}

// viewTaken reports whether view collides with a table or another view of
// st, or with the table it would be a view of.
func viewTaken(st state, view string, table row.Table) bool {
	if view == table.String() {
		return true
	}
	for _, p := range st.plans {
		if p.view == view || p.def.Name.String() == view {
			return true
		}
	}
	return false
}

// liveRows decodes every live `_rows` envelope of table, ordered by id then
// source (so "first" is deterministic). A document this binary cannot
// decode (a newer _v) is skipped: it stays in `_rows` for a newer lore.
func liveRows(ctx context.Context, tx *sql.Tx, table row.Table) ([]row.Row, error) {
	rs, err := tx.QueryContext(ctx,
		`SELECT doc FROM "`+tblRows+`" WHERE tbl = ? AND deleted = 0 ORDER BY id, source`, table.String())
	if err != nil {
		return nil, dbErr("read rows", err)
	}
	defer func() { _ = rs.Close() }()
	var out []row.Row
	for rs.Next() {
		var doc []byte
		if err := rs.Scan(&doc); err != nil {
			return nil, dbErr("read rows", err)
		}
		r, err := row.Decode(doc)
		if err != nil {
			continue
		}
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		return nil, dbErr("read rows", err)
	}
	return out, nil
}

// recorded is one `_tables` row.
type recorded struct {
	fingerprint string
	objects     []dbObject
}

// syncTables makes the file match st: a table whose fingerprint differs
// (or that is new) is dropped and refilled from `_rows`; a recorded table
// st no longer wants is dropped with its references.
func (c *Cache) syncTables(ctx context.Context, tx *sql.Tx, st state) error {
	have, err := readRecorded(ctx, tx)
	if err != nil {
		return err
	}
	for name, rec := range have {
		if _, want := st.plans[row.Table(name)]; want {
			continue
		}
		if err := dropObjects(ctx, tx, name, rec.objects); err != nil {
			return err
		}
	}
	for _, name := range st.names() {
		p := st.plans[name]
		rec, ok := have[name.String()]
		if ok && rec.fingerprint == p.fingerprint {
			continue
		}
		if ok {
			if err := dropObjects(ctx, tx, name.String(), rec.objects); err != nil {
				return err
			}
		}
		if err := c.createAndFill(ctx, tx, st, p); err != nil {
			return err
		}
	}
	return nil
}

// readRecorded reads `_tables`.
func readRecorded(ctx context.Context, tx *sql.Tx) (map[string]recorded, error) {
	rs, err := tx.QueryContext(ctx, `SELECT name, fingerprint, objects FROM "`+tblTables+`"`)
	if err != nil {
		return nil, dbErr("read table records", err)
	}
	defer func() { _ = rs.Close() }()
	out := map[string]recorded{}
	for rs.Next() {
		var name, fp, objs string
		if err := rs.Scan(&name, &fp, &objs); err != nil {
			return nil, dbErr("read table records", err)
		}
		objects, err := decodeObjects(objs)
		if err != nil {
			return nil, err
		}
		out[name] = recorded{fingerprint: fp, objects: objects}
	}
	if err := rs.Err(); err != nil {
		return nil, dbErr("read table records", err)
	}
	return out, nil
}

// decodeObjects parses a `_tables.objects` value.
func decodeObjects(s string) ([]dbObject, error) {
	var out []dbObject
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, errs.Wrap(errs.DBCorrupt, "sqlite cache: bad table record", err)
	}
	return out, nil
}

// dropObjects drops a recorded table's objects (views first), its record
// and the references of its rows.
func dropObjects(ctx context.Context, tx *sql.Tx, name string, objs []dbObject) error {
	ordered := slices.Clone(objs)
	slices.SortStableFunc(ordered, func(a, b dbObject) int {
		return boolRank(b.Type == objView) - boolRank(a.Type == objView)
	})
	for _, o := range ordered {
		q, err := dropStatement(o)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return dbErr("drop "+o.Name, err)
		}
	}
	for _, q := range []string{
		`DELETE FROM "` + tblTables + `" WHERE name = ?`,
		`DELETE FROM "` + tblRefs + `" WHERE entity_table = ?`,
		`DELETE FROM "` + tblExpanded + `" WHERE entity_table = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, name); err != nil {
			return dbErr("drop "+name, err)
		}
	}
	return nil
}

// boolRank is 1 for true.
func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// createAndFill creates p's objects, records them and refills the table
// from `_rows`.
func (c *Cache) createAndFill(ctx context.Context, tx *sql.Tx, st state, p plan) error {
	for _, s := range p.create {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return dbErr("create "+p.def.Name.String(), err)
		}
	}
	objs, err := json.Marshal(p.objects)
	if err != nil {
		return errs.Wrap(errs.Internal, "sqlite cache: encode table record", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO "`+tblTables+`" (name, fingerprint, objects) VALUES (?, ?, ?)`,
		p.def.Name.String(), p.fingerprint, string(objs)); err != nil {
		return dbErr("record table", err)
	}
	for _, q := range []string{
		`DELETE FROM "` + tblRefs + `" WHERE entity_table = ?`,
		`DELETE FROM "` + tblExpanded + `" WHERE entity_table = ?`,
	} {
		if _, err = tx.ExecContext(ctx, q, p.def.Name.String()); err != nil {
			return dbErr("clear references", err)
		}
	}
	src := p.def.Name
	if !p.def.Kind.IsZero() {
		src = schema.TableResources
	}
	rs, err := tx.QueryContext(ctx, `SELECT source, doc FROM "`+tblRows+`" WHERE tbl = ? AND deleted = 0 ORDER BY id, source`, src.String())
	if err != nil {
		return dbErr("refill "+p.def.Name.String(), err)
	}
	type held struct {
		source string
		doc    []byte
	}
	var rows []held
	for rs.Next() {
		var h held
		if err := rs.Scan(&h.source, &h.doc); err != nil {
			return errors.Join(dbErr("refill", err), rs.Close())
		}
		rows = append(rows, h)
	}
	if err := errors.Join(rs.Err(), rs.Close()); err != nil {
		return dbErr("refill", err)
	}
	for _, h := range rows {
		r, err := row.Decode(h.doc)
		if err != nil {
			continue
		}
		if p.def.Kind.IsZero() {
			err = c.upsertTyped(ctx, tx, p, h.source, r, r.Fields)
		} else {
			err = c.upsertKind(ctx, tx, st, p.def.Kind, h.source, r)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// kindOf returns the kind id a resource row names, zero when absent.
func kindOf(r row.Row) ids.ID[ids.Kind] {
	v, ok := r.Fields[fieldResKind]
	if !ok || v.Kind() == value.KindNull {
		return ids.ID[ids.Kind]{}
	}
	k, err := ids.Parse[ids.Kind](v.String())
	if err != nil {
		return ids.ID[ids.Kind]{}
	}
	return k
}
