package engine

import (
	"context"
	"maps"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/ids"
)

// RelabelMeta is the label-only write of METADATA 3.12 (`lore meta rename
// --rewrite-rows`): it rewrites the name label of key keyID inside one
// row's meta entry and nothing else. The row keeps its hlc, updated_at and
// updated_by, so a label refresh is not an edit (3.12: "a label-only
// rewrite never bumps updated_at or the live hlc"); merge treats the
// label as derived from the key id. Flow:
//
//	locate ─► live, carries keyID, label differs? (else changed=false)
//	  ─► entry.Name = name ─► put with Expect = the hlc read (a race is
//	     E_STALE) ─► cache + audit (an update event, same hlc)
func (e *Engine) RelabelMeta(ctx context.Context, ref Ref, keyID ids.ID[ids.MetaKey], name string) (changed bool, err error) {
	src, cur, err := e.locate(ctx, ref)
	if err != nil {
		return false, err
	}
	j, ok := cur.Meta[keyID]
	if !ok || cur.Deleted {
		return false, nil
	}
	en, err := meta.DecodeEntry(j)
	if err != nil {
		return false, err
	}
	if en.Name == name {
		return false, nil
	}
	en.Name = name
	nj, err := en.Encode()
	if err != nil {
		return false, err
	}
	st, err := e.writable(src)
	if err != nil {
		return false, err
	}
	ours := cur
	ours.Meta = maps.Clone(cur.Meta)
	ours.Meta[keyID] = nj
	got, err := e.put(ctx, st, ours, cur.HLC) // same hlc: label-only (3.12)
	if err != nil {
		return false, err
	}
	cl := e.callOf(ctx)
	ev := Event{Op: cl.op, Action: ActionUpdate, Store: src, Table: cur.Table, ID: cur.ID, HLC: got.row.HLC, Actor: e.actorOf(cl)}
	return true, e.finish(ctx, src, got.docs(), ev)
}
