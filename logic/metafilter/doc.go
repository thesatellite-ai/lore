// Package metafilter is the one --meta filter, --sort <meta key> and
// --place helper every entity list uses (METADATA 3.7), plus the import
// quarantine check of a row's meta (3.13). Pure: the caller hands it the
// visible key definitions (meta_keys rows) and the rows' meta maps.
//
// Layer L1: imports model and logic/meta only.
//
// Flow:
//
//	Compile(filters, place, sort, keys)
//	  └ meta.Parse each filter ─ resolve name → visible key ids (place narrows)
//	      ─ coerce the value by each definition's type (E_META_OP on > < of an
//	        unordered type) ─ unknown name → warning "did you mean"
//	Plan.Match(rowID, meta) → keep?, notes (skipped: wrong kind, conflict)
//	Plan.Compare(a, b)       → --sort order, rows without the key last
//	Quarantine(meta)         → problems (3.13); a quarantined row is "not checked"
package metafilter
