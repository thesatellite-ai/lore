package wheredel

import (
	"github.com/thesatellite-ai/lore/logic/filter"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// KeyTable is the --where key a bulk write across tables adds: the row's
// table name (`table=memories`).
const KeyTable = "table"

// RowMatch reports whether a row of its table matches a filter.
type RowMatch func(r row.Row) bool

// Across binds where to each of tables for a bulk write that spans tables
// (devco D-712, `lore move --from <folder> --where …`): the same grammar
// as `<noun> delete --where`, each table's own fields plus the envelope
// keys and KeyTable. A table that lacks a field the filter names matches
// nothing (a `severity=must` move takes rules and hotfixes, never
// memories); a field no table has is E_INVALID_INPUT naming the fields.
// Empty where matches every row of every table. Why one helper: the
// per-table binding stays the one Run and Archive use (Keys, getter).
func Across(where []string, tables []row.Table, verb string) (map[row.Table]RowMatch, error) {
	out := make(map[row.Table]RowMatch, len(tables))
	if len(where) == 0 {
		for _, t := range tables {
			out[t] = func(row.Row) bool { return true }
		}
		return out, nil
	}
	f, err := filter.Parse(where)
	if err != nil {
		return nil, err
	}
	union := map[string]filter.Field{KeyTable: {Kind: value.KindText}}
	var unknown []filter.Key
	for _, t := range tables {
		keys := Keys(t)
		keys[KeyTable] = filter.Field{Kind: value.KindText}
		for k, fd := range keys {
			union[k] = fd
		}
		missing := false
		m, berr := f.Bind(func(k filter.Key) (filter.Field, error) {
			if k.Meta {
				return filter.Field{}, errs.New(errs.InvalidInput, verb+" --where: "+k.String()+": metadata terms are not taken here")
			}
			fd, ok := keys[k.Name]
			if !ok {
				missing = true
				unknown = append(unknown, k)
				return filter.Field{Kind: value.KindText}, nil
			}
			return fd, nil
		})
		if berr != nil {
			return nil, berr
		}
		if missing {
			continue // this table has no such field: none of its rows match
		}
		cols := columns(keys, nil)
		out[t] = func(r row.Row) bool {
			get := getter(r, cols)
			return m.Match(func(k filter.Key) []string {
				if k.Name == KeyTable {
					return []string{r.Table.String()}
				}
				return get(k)
			})
		}
	}
	if len(out) == 0 && len(unknown) > 0 {
		return nil, filter.UnknownKey(unknown[0], sortedKeys(union))
	}
	return out, nil
}
