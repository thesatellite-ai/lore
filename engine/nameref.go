package engine

// nameref.go — a name where an id belongs (devco D-766, finding H-25).
// An old-format export (a hand-made import, a v0.1.17 store kept as files)
// can hold a ref field whose value is the target's name, not its id: a
// task with tasklist_id "Backlog". It was written as is, so every later
// edit of the row was refused (E_RESOURCE_INVALID "field tasklist_id: want
// a ref value") and the task belonged to no list.
//
//	ref field holds text that is not an id
//	  ├─ one row of the ref's table has that title or name ─▶ its id
//	  │  (in the row's project; exact, then ignoring case)
//	  ├─ the field is optional                            ─▶ cleared (null)
//	  └─ the field is required                            ─▶ left as is
//	every case is one NameRefFix, printed as a warning line
//
// migrate run (format rewrite and tag fold), store adopt and the Mode B
// import call it on every row they convert, before upcasting it.

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// nameRefFields are the fields a row is known by, in the order a name
// ref is matched against them (task lists, missions and plans carry a
// title; projects, tags and the older lists a name).
var nameRefFields = []string{"title", "name"}

// nameRefTarget is one row a name ref can resolve to.
type nameRefTarget struct {
	id      ids.AnyID
	name    string
	project string
}

// NameRefIndex indexes the rows a name ref can point at, by table. The
// zero value resolves nothing (every name ref is cleared or left).
type NameRefIndex struct {
	byTable map[row.Table][]nameRefTarget
}

// NewNameRefIndex indexes rows (deleted rows are skipped) by their title
// or name, for ResolveNameRefs on the rows converted with them.
func NewNameRefIndex(rows []row.Row) NameRefIndex {
	x := NameRefIndex{byTable: map[row.Table][]nameRefTarget{}}
	for _, r := range rows {
		if r.Deleted || r.ID.IsZero() {
			continue
		}
		name := ""
		for _, f := range nameRefFields {
			if t, ok := r.Fields[f].(value.Text); ok && strings.TrimSpace(t.String()) != "" {
				name = strings.TrimSpace(t.String())
				break
			}
		}
		if name == "" {
			continue
		}
		x.byTable[r.Table] = append(x.byTable[r.Table], nameRefTarget{id: r.ID, name: name, project: refText(r.Fields[projectField])})
	}
	return x
}

// NameRefFix is one ref field ResolveNameRefs changed or kept: To is the
// id it now holds (zero when the field was cleared or left as is).
type NameRefFix struct {
	Table row.Table
	ID    ids.AnyID
	Field string
	Was   string
	To    ids.AnyID
	// Kept is true for a required field: it cannot be cleared, so it is
	// left as is (validation names it on the row's next edit).
	Kept bool
	// Ambiguous counts the rows that share the name (0 or 1: not ambiguous).
	Ambiguous int
	// Target is the ref's table ("task_lists").
	Target row.Table
}

// Line is the fix as one warning line of migrate run or store adopt.
func (f NameRefFix) Line() string {
	head := f.Table.String() + "/" + f.ID.String() + ": " + f.Field + ` "` + f.Was + `" is a name, not an id; `
	switch {
	case !f.To.IsZero():
		return head + "now the " + f.Target.String() + " row " + f.To.String() + ` named "` + f.Was + `"`
	case f.Kept:
		return head + "left as is: no single " + f.Target.String() + " row has that name and the field is required; set it to an id by hand"
	case f.Ambiguous > 1:
		return head + "cleared: " + strconv.Itoa(f.Ambiguous) + " " + f.Target.String() + " rows have that name"
	default:
		return head + "cleared: no " + f.Target.String() + " row has that name"
	}
}

// ResolveNameRefs is r with every ref field that holds text which is not
// an id resolved through x: to the id of the one row of the ref's table
// with that title or name (in r's project, exact before ignoring case), else cleared (absent is null), or left as is for
// a required field. r itself is not changed; the fixes say what happened,
// sorted by field.
func ResolveNameRefs(reg *schema.Registry, x NameRefIndex, r row.Row) (row.Row, []NameRefFix) {
	def, ok := reg.Table(r.Table)
	if !ok || len(r.Fields) == 0 {
		return r, nil
	}
	var fixes []NameRefFix
	names := slices.Sorted(maps.Keys(r.Fields))
	var fields map[string]value.Value
	for _, name := range names {
		f, ok := def.Field(name)
		if !ok || f.Kind != value.KindRef || f.RefTable == "" {
			continue
		}
		t, ok := r.Fields[name].(value.Text)
		if !ok || t.String() == "" {
			continue // a ref, null, or v0.1.17's "" (NullEmptyRefs)
		}
		if _, err := ids.ParseAny(t.String()); err == nil {
			continue // an id: the ref as it should be
		}
		fix := NameRefFix{Table: r.Table, ID: r.ID, Field: name, Was: t.String(), Target: f.RefTable}
		hits := x.match(f.RefTable, t.String(), refText(r.Fields[projectField]))
		if fields == nil {
			fields = maps.Clone(r.Fields)
		}
		switch {
		case len(hits) == 1:
			ref, err := value.RefOf(hits[0])
			if err != nil {
				continue
			}
			fields[name] = ref
			fix.To = hits[0]
		case f.Required:
			fix.Kept, fix.Ambiguous = true, len(hits)
		default:
			delete(fields, name)
			fix.Ambiguous = len(hits)
		}
		fixes = append(fixes, fix)
	}
	if fields != nil {
		r.Fields = fields
	}
	return r, fixes
}

// match is the ids of the rows of table named name, exact matches before
// matches ignoring case. A row of another project never matches: a name
// is looked up in the row's own project (and among rows with no project).
func (x NameRefIndex) match(table row.Table, name, project string) []ids.AnyID {
	name = strings.TrimSpace(name)
	for _, fold := range []bool{false, true} {
		var hit []ids.AnyID
		for _, c := range x.byTable[table] {
			if c.project != "" && project != "" && c.project != project {
				continue
			}
			if (!fold && c.name == name) || (fold && strings.EqualFold(c.name, name)) {
				hit = append(hit, c.id)
			}
		}
		if len(hit) > 0 {
			return hit
		}
	}
	return nil
}

// refText is v's id text when v is a ref or text ("" otherwise).
func refText(v value.Value) string {
	switch t := v.(type) {
	case value.Ref:
		return t.ID().String()
	case value.Text:
		return t.String()
	}
	return ""
}
