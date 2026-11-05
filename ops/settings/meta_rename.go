package settings

// `lore meta rename <key> <new_name> [--rewrite-rows]` (METADATA 3.12,
// CLI_REFERENCE; OQ3, D21):
//
//	metaRename ─► checkKeyName ─► the key row ─► name free in its place?
//	  (E_META_TAKEN) ─► patch the key row's name (the id stays)
//	  └ --rewrite-rows: one batch (bulk.Begin) ─► every live row carrying
//	     the key id ─► engine.RelabelMeta: the entry's name label only, no
//	     hlc or updated_at bump (3.12) ─► "rewrote N row labels"

import (
	"context"
	"fmt"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// MetaRenameIn is the input of meta.rename.
type MetaRenameIn struct {
	Old         string `json:"id" arg:"0" desc:"the current name (or id)"`
	New         string `json:"new_name" arg:"1" desc:"the new name"`
	RewriteRows bool   `json:"rewrite_rows,omitempty" flag:"rewrite-rows" desc:"rewrite every row's label of the key now, in one batch (labels only; no updated_at or hlc change)"`
}

// relabeler is the Env upgrade --rewrite-rows needs (wire.App).
type relabeler interface {
	RelabelMeta(ctx context.Context, ref engine.Ref, keyID ids.ID[ids.MetaKey], name string) (bool, error)
}

func (o ops) metaRename(ctx context.Context, in MetaRenameIn) (RowOut, error) {
	if err := checkKeyName(in.New); err != nil {
		return RowOut{}, err
	}
	k, err := o.metaKey(ctx, in.Old)
	if err != nil {
		return RowOut{}, err
	}
	rows, err := liveRows(ctx, o.env, []store.Source{k.Src}, schema.TableMetaKeys)
	if err != nil {
		return RowOut{}, err
	}
	if len(where(rows, map[string]string{mName: in.New, mScope: text(k.Row, mScope)})) > 0 {
		return RowOut{}, errs.New(errs.MetaTaken, "a metadata key named "+quote(in.New)+" already exists there")
	}
	v, err := txt(in.New)
	if err != nil {
		return RowOut{}, err
	}
	old := text(k.Row, mName)
	if !in.RewriteRows {
		return o.patchRow(ctx, k, map[string]value.Value{mName: v},
			"renamed "+old+" → "+in.New+" in the registry; row labels refresh on each row's next write; no values changed")
	}
	rl, ok := upgrade.As[relabeler](o.env)
	if !ok {
		return RowOut{}, errs.New(errs.Unsupported, "meta rename --rewrite-rows needs the engine's label-only write").WithHint("run it from the lore CLI")
	}
	ctx, batch, err := bulk.Begin(ctx, bulk.KindMeta)
	if err != nil {
		return RowOut{}, err
	}
	out, err := o.patchRow(ctx, k, map[string]value.Value{mName: v}, "")
	if err != nil {
		return RowOut{}, err
	}
	kid, err := ids.Parse[ids.MetaKey](k.Row.ID.String())
	if err != nil {
		return RowOut{}, err
	}
	data, err := o.metaRows(ctx)
	if err != nil {
		return RowOut{}, err
	}
	n := 0
	for _, r := range data {
		if _, has := r.entries[kid]; !has {
			continue
		}
		changed, rerr := rl.RelabelMeta(ctx, engine.Ref{Store: r.Src, Table: r.Row.Table, ID: r.Row.ID}, kid, in.New)
		if rerr != nil {
			return out, errs.Wrap(errs.CodeOf(rerr), fmt.Sprintf("meta rename: %d row labels rewritten before %s failed", n, r.Row.ID), rerr)
		}
		if changed {
			n++
		}
	}
	out.Human = fmt.Sprintf("renamed %s → %s in the registry; rewrote %d row labels (batch %s; labels only, no values or dates changed)\n", old, in.New, n, batch)
	return out, nil
}
