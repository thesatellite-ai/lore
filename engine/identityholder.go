package engine

// identityholder.go — a reference to a row whose identity lives outside
// the rows (devco D-736: a linked repo's .lore/data holds no rows).
//
//	linked repo after `lore doctor --fix`: no repos row anywhere; its id is
//	_meta.json repo_id, and the store link names the repo
//	  scopes.repo_id / memories.repo_id → rep_<own>   no store holds a row
//	  checkRefs ─▶ holders() finds none ─▶ IdentityHolder: the linked
//	               folder holds that identity ─▶ the ref is accepted, and F15
//	               direction is checked against that folder
//
// Only the composition root knows which identity a link holds, so the
// holder is bound after New (SetIdentityHolder), as the write guard is.

import (
	"context"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// IdentityHolder names the attached store that holds the identity of the
// row table/id when no attached store holds that row (ok false: nobody
// does, the reference dangles). wire answers for the repo's own repos id
// in a linked repo whose identity is the store link (D-736).
type IdentityHolder func(ctx context.Context, table row.Table, id ids.AnyID) (src store.Source, ok bool)

// SetIdentityHolder binds the holder after New (nil: none). Call it before
// the engine serves any write.
func (e *Engine) SetIdentityHolder(h IdentityHolder) { e.identity = h }

// identityHolders is holders' answer when no store holds the row: the
// store the IdentityHolder names, if it is attached here.
func (e *Engine) identityHolders(ctx context.Context, table row.Table, id ids.AnyID) []store.Source {
	if e.identity == nil {
		return nil
	}
	src, ok := e.identity(ctx, table, id)
	if !ok {
		return nil
	}
	if _, attached := e.stores[src]; !attached {
		return nil
	}
	return []store.Source{src}
}
