// Package expand is the shared `--expand` of every show operation
// (`lore show <id>` and `lore <noun> show <id>`; SECRETS_AND_REFERENCES
// 2.7, 12, 13.2): it fills the references of one row's text fields at the
// show point of logic/refpoint. Stored text is never changed (SQ3).
//
//	<noun>.show --expand ─▶ Row(ctx, env, r)
//	    env.(Env)? no ─▶ E_UNSUPPORTED
//	    RefResolver + OutputScanner (wire/expandport.go)
//	    each non-sensitive text field ─▶ refpoint.Expand(Show)
//	        var/res/row/env filled; secrets and scanner hits ••••••
//	    ─▶ Block{Expanded: changed fields, Refs: every reference met}
//
// Layer L5 helper (ops/internal): imports engine-free lower layers only.
package expand
