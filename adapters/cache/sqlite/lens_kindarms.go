package sqlite

// lens_kindarms.go — devco D-758 (owner 2026-11-20 P-8): a res_<kind> view
// spans every source that holds resources of the kind.
//
// A store's cache builds its typed res_<kind> table only from the kinds in
// that cache (state.go): a resource written to a store folder whose kind
// lives in the repo or another folder (kinds in the org folder, resources
// in each project folder: the usual shape) has no kind table in its own
// cache. Before this, res_server_all listed only the sources that also
// held the kind, while res__all and `lore resource list` listed them all.
//
// The lens sees every source, so it closes the gap: for each kind defined
// in some source of the lens (its res_<name> table and res_<kind id> view
// exist there), every other source holding resources of that kind gets an
// arm read straight from its resources table, one column per field read
// from the resources.fields object by field id. Masking (lens_mask.go)
// works by column name, so a sensitive field stays masked in an arm too.
//
//	source 0 (repo):   kinds, kind_fields, res_server  ──┐
//	source 1 (acme):   resources (kind = server)  ── arm ─┼─▶ res_server_all
//	source 2 (acme/web): resources (kind = server) ─ arm ─┘

import (
	"context"
	"database/sql"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/schema"
)

// lensKind is one kind a lens source defines: its id, name and the column
// name → field id of its fields (gathered from every source, as a nearer
// place may add a field to a kind defined further away).
type lensKind struct {
	id, name string
	fields   map[string]string
}

// addKindArms adds to tables an arm for every source that holds resources
// of a kind whose res_<kind> table it lacks (see the file comment). A
// source that holds none of the kind's resources gets no arm, so the views
// of a lens without such resources are unchanged.
func (l *Lens) addKindArms(ctx context.Context, tables map[string][]srcTable) error {
	kinds, err := l.lensKinds(ctx, tables)
	if err != nil {
		return err
	}
	res := tables[schema.TableResources]
	for _, k := range kinds {
		for _, view := range []string{schema.KindTablePrefix + k.name, schema.KindTablePrefix + k.id} {
			have := tables[view]
			if len(have) == 0 {
				continue
			}
			cols := have[0].cols
			for _, rt := range res {
				src := l.sources[rt.src]
				if src.ownSource() || slices.ContainsFunc(have, func(st srcTable) bool { return st.src == rt.src }) {
					continue
				}
				held, err := l.holdsKind(ctx, rt.src, k.id)
				if err != nil {
					return err
				}
				if !held {
					continue
				}
				tables[view] = append(tables[view], srcTable{src: rt.src, cols: cols, from: kindArmSQL(rt, k, cols)})
			}
			slices.SortStableFunc(tables[view], func(a, b srcTable) int { return a.src - b.src })
		}
	}
	return nil
}

// lensKinds lists the kinds the lens's sources define, by id, nearest
// source first; a kind's fields come from every source's kind_fields.
func (l *Lens) lensKinds(ctx context.Context, tables map[string][]srcTable) ([]lensKind, error) {
	var out []lensKind
	at := map[string]int{}
	for _, st := range tables[schema.TableKinds] {
		if l.sources[st.src].ownSource() {
			continue
		}
		err := l.eachPair(ctx, `SELECT "id", "name" FROM "`+attachedName(st.src)+`"."`+schema.TableKinds+`" WHERE "name" IS NOT NULL`, func(id, name string) {
			if _, dup := at[id]; !dup {
				at[id] = len(out)
				out = append(out, lensKind{id: id, name: name, fields: map[string]string{}})
			}
		})
		if err != nil {
			return nil, err
		}
	}
	for _, st := range tables[schema.TableKindFields] {
		if l.sources[st.src].ownSource() || !slices.Contains(st.cols, fieldKindID) {
			continue
		}
		err := l.eachPair(ctx, `SELECT "kind_id" || char(31) || "id", "name" FROM "`+attachedName(st.src)+`"."`+schema.TableKindFields+`" WHERE "name" IS NOT NULL AND "kind_id" IS NOT NULL`, func(key, name string) {
			kind, fld, ok := strings.Cut(key, "\x1f")
			if i, known := at[kind]; ok && known {
				if _, dup := out[i].fields[name]; !dup {
					out[i].fields[name] = fld
				}
			}
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// eachPair runs query (two text columns) and calls fn per row.
func (l *Lens) eachPair(ctx context.Context, query string, fn func(a, b string)) error {
	rs, err := l.conn.QueryContext(ctx, query)
	if err != nil {
		return dbErr("read kinds", err)
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		var a, b sql.NullString
		if err := rs.Scan(&a, &b); err != nil {
			return dbErr("read kinds", err)
		}
		if a.Valid && b.Valid {
			fn(a.String, b.String)
		}
	}
	if err := rs.Err(); err != nil {
		return dbErr("read kinds", err)
	}
	return nil
}

// holdsKind reports whether source i's resources table holds a resource
// of kind id.
func (l *Lens) holdsKind(ctx context.Context, i int, id string) (bool, error) {
	var n int
	q := `SELECT COUNT(*) FROM (SELECT 1 FROM "` + attachedName(i) + `"."` + schema.TableResources + `" WHERE "` + fieldResKind + `" = ? LIMIT 1)`
	if err := l.conn.QueryRowContext(ctx, q, id).Scan(&n); err != nil {
		return false, dbErr("read resources", err)
	}
	return n > 0, nil
}

// kindArmSQL is the arm of kind k read from the resources table rt: the
// kind table's columns cols, a field column from resources.fields by its
// field id, a resources column by name, else NULL.
func kindArmSQL(rt srcTable, k lensKind, cols []string) string {
	sel := make([]string, len(cols))
	for i, c := range cols {
		switch fld, isField := k.fields[c]; {
		case isField && slices.Contains(rt.cols, fieldResVals):
			sel[i] = `json_extract("` + fieldResVals + `", ` + literal(`$."`+fld+`".`+resValueKey) + `) AS ` + quoteIdent(c)
		case slices.Contains(rt.cols, c):
			sel[i] = quoteIdent(c)
		default:
			sel[i] = `NULL AS ` + quoteIdent(c)
		}
	}
	return `(SELECT ` + strings.Join(sel, ", ") + ` FROM "` + attachedName(rt.src) + `"."` + schema.TableResources +
		`" WHERE "` + fieldResKind + `" = ` + literal(k.id) + `)`
}

// quoteIdent double-quotes a column name taken from a cache's own table
// info (doubling any quote in it).
func quoteIdent(c string) string { return `"` + strings.ReplaceAll(c, `"`, `""`) + `"` }
