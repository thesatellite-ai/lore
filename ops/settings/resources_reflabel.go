package settings

// Ref labels (RES 3.4, OQ17; P3-RES-1) and `lore resource rename
// --rewrite-rows` (PASS3-A-3).
//
//	every resource write ─► o.valuesPatch
//	                          └─ refLabels: each ref field's target by id
//	                               ├─ found   → "<kind>/<name>" (fresh)
//	                               └─ unseen  → the row's stored label kept
//	                                            (a dangling ref keeps it)
//	resource rename <name> <new> [--rewrite-rows]
//	  patch name ─► refsTo(target): live rows whose ref holds its id with a
//	                another label
//	                 ├─ no flag → count only ("refresh on next write")
//	                 └─ flag    → one bat_ batch, ordinary writes (FA-RES-4)
//
// The label sits beside the id as `v_label` ({"name", "v": "res_…",
// "v_label": "host/hub_prod"}), the OQ-AV v_name precedent, so every reader
// that takes the id from `v` (graph, repoint, lint, the cache's res__edges,
// the engine's move checks) is unchanged. The id stays the truth.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/schema"
)

// labelSep joins a ref label's kind and name: the `<kind>/<name>` short
// form input already accepts (RES 3.4). No place prefix: the cache keeps
// no per-row scope label (OV34-SCOPE), and a client repo row must name no
// other place (RES 3.9).
const labelSep = "/"

// valuesPatch is valuesPatchLabeled with fresh ref labels, the one path
// every resource write takes (a label is rewritten on the row's next real
// write, RES 3.4).
func (o ops) valuesPatch(ctx context.Context, d kindDef, vals map[string]json.RawMessage, prev resValues) (map[string]value.Value, error) {
	return valuesPatchLabeled(d, vals, prev, o.refLabels(ctx, d, vals, prev))
}

// refLabels is the label of every ref value of vals by fld_ id: the
// target's current "<kind>/<name>", or the label prev stored when the
// target is not visible here.
func (o ops) refLabels(ctx context.Context, d kindDef, vals map[string]json.RawMessage, prev resValues) map[string]string {
	out := map[string]string{}
	for fid, raw := range vals {
		f, ok := d.byID[fid]
		if !ok || text(f.Row, fType) != string(value.KindRef) {
			continue
		}
		if lb, found := o.refLabel(ctx, display(fromJSONRaw(raw))); found {
			out[fid] = lb
		} else if old := prev.labels[fid]; old != "" {
			out[fid] = old
		}
	}
	return out
}

// refLabel is a resource id's ref label; found is false when no live
// resource has that id here.
func (o ops) refLabel(ctx context.Context, id string) (string, bool) {
	if id == "" {
		return "", false
	}
	r, err := o.resourceByName(ctx, id, "")
	if err != nil || r.Row.ID.String() != id {
		return "", false
	}
	return o.kindName(ctx, text(r.Row, rKind)) + labelSep + text(r.Row, rName), true
}

// referrer is one live resource holding a ref to the renamed target whose
// stored label differs from the new one.
type referrer struct {
	r      located
	d      kindDef
	stored resValues
}

// refsTo lists the live resources of every visible store with a ref field
// holding targetID under a label other than label. Rows of a kind this
// place cannot load are skipped; a conflicted kind's rows are counted in
// blocked (writes of that kind are refused until it is resolved, RES 3.13).
func (o ops) refsTo(ctx context.Context, targetID, label string) (hits []referrer, blocked int, err error) {
	rows, err := liveRows(ctx, o.env, o.resStores(), schema.TableResources)
	if err != nil {
		return nil, 0, err
	}
	defs := map[string]kindDef{}
	bad := map[string]bool{}
	for _, r := range rows {
		kid := text(r.Row, rKind)
		if bad[kid] {
			continue
		}
		d, seen := defs[kid]
		if !seen {
			var derr error
			if d, derr = o.kindDef(ctx, kid); derr != nil {
				bad[kid] = true
				if errs.Has(derr, errs.KindConflict) && holdsRef(r, targetID) {
					blocked++
				}
				continue
			}
			defs[kid] = d
		}
		stored, derr := d.decode(r)
		if derr != nil {
			return nil, 0, derr
		}
		for fid, raw := range stored.vals {
			f, ok := d.byID[fid]
			if ok && text(f.Row, fType) == string(value.KindRef) && display(fromJSONRaw(raw)) == targetID && stored.labels[fid] != label {
				hits = append(hits, referrer{r: r, d: d, stored: stored})
				break
			}
		}
	}
	return hits, blocked, nil
}

// holdsRef reports whether r's stored fields name id anywhere as a value
// (used only to count a conflicted kind's rows, whose fields cannot be
// typed).
func holdsRef(r located, id string) bool {
	v, err := decodeValues(r)
	if err != nil {
		return false
	}
	for _, raw := range v.vals {
		if display(fromJSONRaw(raw)) == id {
			return true
		}
	}
	return false
}

// rewriteRefLabels writes label into every referrer's ref entries for
// targetID, ordinary writes in one bat_ batch (FA-RES-4), and returns the
// batch id ("" when nothing needed a write).
func (o ops) rewriteRefLabels(ctx context.Context, hits []referrer, targetID, label string) (string, error) {
	if len(hits) == 0 {
		return "", nil
	}
	bctx, bid, err := bulk.Begin(ctx, bulk.KindResource)
	if err != nil {
		return "", err
	}
	for _, h := range hits {
		labels := o.refLabels(bctx, h.d, h.stored.vals, h.stored)
		for fid, raw := range h.stored.vals {
			if display(fromJSONRaw(raw)) == targetID {
				labels[fid] = label // the rename's label, never a stale cache read
			}
		}
		patch, perr := valuesPatchLabeled(h.d, h.stored.vals, h.stored, labels)
		if perr != nil {
			return "", perr
		}
		if perr = o.updateResource(bctx, h.d, h.r, patch); perr != nil {
			return "", errs.Wrap(errs.ResourceInvalid, "resource rename --rewrite-rows: stopped; the rows written are in batch "+bid.String(), perr)
		}
	}
	return bid.String(), nil
}

// renameLabelLines is the rename's report of its referrers' labels.
func renameLabelLines(n, blocked int, rewrite bool, batch string) string {
	var s string
	switch {
	case n == 0 && rewrite:
		s = "  no ref labels to rewrite\n"
	case n == 0:
	case rewrite:
		s = fmt.Sprintf("  rewrote %d ref labels (batch %s)\n", n, batch)
	default:
		s = fmt.Sprintf("  %d ref labels refresh on each row's next write (--rewrite-rows rewrites them now)\n", n)
	}
	if blocked > 0 {
		s += fmt.Sprintf("  %d referring rows of a kind with conflict markers were left (their labels refresh on each row's next write once the kind is resolved)\n", blocked)
	}
	return s
}
