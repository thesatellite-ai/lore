package engine

// stale_copy.go — one row listed once when two stores hold copies of it
// (devco:D-929, bug hunt H-187, a leftover of H-178 / #142).
//
// `lore store adopt` copies a repo's rows into a store folder keeping
// their ids and HLCs, and a copied row file does the same: the id is then
// live in two stores. An edit reaches one copy (the one the command
// resolved), the other keeps the old text. Every list read (List,
// ListIDs: list, search, context, render) showed both, so a search for a
// word only the old text holds still answered with it.
//
// The rule: of two live copies of one row (same id, same created_at: a
// copy, never two rows an older lore gave one natural id, D-689) every
// list read keeps the newest (HLC) copy and drops the others; equal
// versions keep the nearest store's. A move record between the two
// (moved_leftover.go) is that file's rule, not this one. Actor rows are
// one person per store and stay. `lore show --id` and the writes keep
// E_AMBIGUOUS_ID for two attached live copies (pickCopy): an edit must
// name the copy it changes; `lore doctor` and `lore lint` name every such
// id with the command that settles it.
//
//	repo: mem_x v2 (edited)      notes/billing: mem_x v1 (adopted copy)
//	  list / search / ask ─▶ v2 only, [repo]

import (
	"context"
	"slices"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// SameRowCopies reports whether a and b are copies of one row: one id
// (not an actor's: each store holds its own copy of a person) and one
// creation time. Why exported: lint and doctor (wire) find the ids live in
// two stores by the same test every list read hides them by.
func SameRowCopies(a, b row.Row) bool {
	if a.ID != b.ID || a.ID.Prefix() == ids.PrefixActor || a.CreatedAt.IsZero() {
		return false
	}
	return a.CreatedAt.Equal(b.CreatedAt)
}

// CopiesHide reports whether two stores' copies of a row of table def are
// one row whose stale copy list reads hide: not a machine-local table
// (one store, the machine's) nor a global-key one (actors: the same
// person's row in every store by design).
func CopiesHide(def schema.TableDef) bool {
	return !def.MachineLocal && !def.GlobalKey
}

// staleCopyOf reports whether r (held in store at) is a copy of a row
// that store other holds newer (o): every list read then lists o alone.
// Equal versions keep the copy of the store earlier in lookup order. Two
// copies a move record links are left to leftoverOf.
func (e *Engine) staleCopyOf(ctx context.Context, at store.Source, r row.Row, other store.Source, o row.Row) bool {
	if everyCopy(ctx) || r.Deleted || o.Deleted || !SameRowCopies(r, o) {
		return false
	}
	if def, ok := e.reg.Table(r.Table); !ok || !CopiesHide(def) {
		return false
	}
	if from := lastMovedFrom(o); from != "" && (from == e.placeOf(at) || from == string(at)) {
		return false
	}
	if from := lastMovedFrom(r); from != "" && (from == e.placeOf(other) || from == string(other)) {
		return false
	}
	switch c := r.HLC.Compare(o.HLC); {
	case c < 0:
		return true
	case c > 0:
		return false
	}
	order := e.lookupStores(r.Table)
	return slices.Index(order, other) < slices.Index(order, at)
}

// everyCopyKey marks a context whose list reads keep every copy.
type everyCopyKey struct{}

// WithEveryCopy makes List and ListIDs under ctx keep the stale copies
// staleCopyOf hides. Why: lint and doctor must see both copies to name
// them, and a bulk write over a store (`lore row move --from`, delete or
// archive --where) must reach the copy that store holds, or the command
// lint prints to settle the copy finds nothing to move.
func WithEveryCopy(ctx context.Context) context.Context {
	return context.WithValue(ctx, everyCopyKey{}, true)
}

// everyCopy reports whether ctx is WithEveryCopy.
func everyCopy(ctx context.Context) bool {
	v, _ := ctx.Value(everyCopyKey{}).(bool)
	return v
}
