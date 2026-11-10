// Package row defines the generic row commands (layer L5, CLI_REFERENCE
// 2.7 and 2.13, PROVENANCE_DESIGN 9.1): commands that take any row id,
// whatever its noun.
//
//	lore show <id> [--origin] [--expand]   (row.show)
//	    └► Env.Get (table from the id prefix) ─► fields (sensitive hidden)
//	        └► --expand: ops/internal/expand (RefResolver + OutputScanner) ─► refpoint
//	           Show point: var/res/row/env filled, secrets and scanner
//	           hits masked ─► "expanded" + "refs" (SECRETS 2.7, 12, 13.2)
//	        └► --origin: _o, audit fields, _w, moves read from the row file
//	           (names looked up now from actors/projects/repos/machines)
//	lore move <id> --to <t> [--drop-internal] [--with-time totals|none]
//	    └► Mover.MoveWith (engine.MoveWith: same id, PROV5/XQ5) ─► written
//
// The CLI words `lore show` and `lore move` are shortcuts the host binary
// mounts over the registry's `lore row show|move`.
package row
