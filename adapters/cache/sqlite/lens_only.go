package sqlite

// lens_only.go — devco D-799: a lens that reads one source's rows
// (LensOptions.Only, `lore sql --store repo`).
//
// `lore sql --store <store>` runs on that store alone (wire routes it), so
// its lens holds no repo. `--store repo` cannot drop the stores the same
// way: a repo resource of a kind defined in a store has no res_<kind>
// table of its own, and its view is an arm built from the store's kind
// (lens_kindarms.go), masked by the store's sensitive fields. So the
// stores stay attached for what they define, and every view keeps the
// Only source's part alone:
//
//	tables[n] = [repo, crew, me]  ─▶ [repo]
//	tables[n] = [crew]            ─▶ [crew, no rows]   (the name still
//	                                 shadows crew's file, so an unqualified
//	                                 `SELECT * FROM n` reads no crew row)

import "slices"

// keepOnly drops from tables every part not read from the source
// l.only (or from the deleted-rows file, which holds only its rows); a table only other sources hold keeps one part that reads no
// row, so its views exist (empty) and its plain name never falls through
// to another source's attached file. No-op when l.only is "".
func (l *Lens) keepOnly(tables map[string][]srcTable) {
	if l.only == "" {
		return
	}
	for n, parts := range tables {
		kept := slices.DeleteFunc(slices.Clone(parts), func(st srcTable) bool {
			// the deleted-rows file is kept: its caller filled it with
			// the Only source's trashed rows alone (--include-deleted)
			return l.sources[st.src].Name != l.only && !l.sources[st.src].Deleted
		})
		if len(kept) == 0 {
			empty := parts[0]
			empty.from = `(SELECT * FROM ` + empty.fromSQL(quoteIdent(n)) + ` WHERE 0)`
			kept = []srcTable{empty}
		}
		tables[n] = kept
	}
}
