// Package lint defines `lore lint` (layer L5): one read-only pass over
// every attached store that hosts the findings the design pages send to
// lint, each with its code, the row, and the command that fixes it.
//
//	lore lint [--code <c>…]                       (lint.run)
//	    └► for each store × built-in table: Env.List
//	        ├─ text fields ─► ${…} refs: unknown_scheme (outside code,
//	        │                 SEC 6), dangling_ref (row / var, DOCS O2),
//	        │                 var_old_name (an alias, SEC 11.2)
//	        ├─ vars        ─► var_shadowed (SQ22), var_shareable_differs (SQ20)
//	        ├─ tasks       ─► parent_cycle (TASK 6.13), long_span (TQ2),
//	        │                 time_entries_many (300, TQ4),
//	        │                 duplicate_import (PROV9, moved from doctor)
//	        │                 (own and plain repos only: client repos drop
//	        │                 _o.import, PROVENANCE 8.1, G-DECIDE-3)
//	        ├─ meta        ─► meta_unknown_key (an entry whose key id no
//	        │                 attached registry defines, METADATA 3.21)
//	        └─ every text  ─► secret_found (secrets.go: re-scan with the
//	                          team rules of HookScanner, secret_allows
//	                          honoured, masked preview only; SQ12, BATCH 8)
//
//	lore lint --context                              (lint.run, Context)
//	    └► contextFindings (context.go): superseded_pinned, no_summary,
//	       entry_too_long, path_no_globs, untagged, near_duplicate
//	       (CONTEXT_BUDGET 6.12)
//
// Nothing is changed: no lint finding has a --fix defined in the design
// (`lore meta lint --fix` and `lore resource lint --fix` belong to their
// nouns); each finding names the command that fixes it.
package lint
