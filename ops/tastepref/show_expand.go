package tastepref

import (
	"context"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/expand"
	"github.com/thesatellite-ai/lore/ops/internal/metashow"
)

// ShowIn is the input of the show operation: the id flags plus --expand.
type ShowIn struct {
	ID string `json:"id" arg:"0" desc:"the taste preference id (tst_…)"`
	expand.Flag
	expand.RevealFlag
}

// showExpand is the show operation: the stored row as before, and with
// --expand its text fields filled in at the show point (secrets and
// scanner hits masked; SECRETS 2.7, 12, 13.2). Stored text never changes.
// --reveal implies --expand and also fills each ${secret:…} written in the
// row after an audited ask (expand.ShowWith; SECRETS 2.7, SQ10, D18).
func (o ops) showExpand(ctx context.Context, in ShowIn) (ShowOut, error) {
	out, err := o.show(ctx, IDIn{ID: in.ID})
	if err != nil || (!in.Expand && !in.Reveal && !in.Force) {
		return out, err
	}
	ex, _ := o.c.Env.(expand.Env)
	blk, err := expand.ShowWith(ctx, ex, in.ID, in.RevealFlag)
	if err != nil {
		return ShowOut{}, err
	}
	out.Expanded, out.Refs = blk.Expanded, blk.Refs
	out.Revealed, out.Forced, out.Warning = blk.Revealed, blk.Forced, blk.Warning
	return out, nil
}

// Text is the human form of show, with the --expand block when set.
func (o ShowOut) Text() string {
	return o.baseText() + auditrec.LineAt(o.Data.CreatedAt, o.Data.CreatedBy, o.Data.UpdatedAt, o.Data.UpdatedBy) + metashow.Text(o.Data.Meta, o.Data.MetaMasked) + expand.Text(o.Expanded, o.Refs) + expand.WarningText(o.Warning)
}

// withMeta is rec with the row's metadata (MQ13, METADATA 3.17: show,
// list --json and search hits carry it; sensitive values masked).
func withMeta(ctx context.Context, env metashow.Env, rec Record, r row.Row) Record {
	b := metashow.Row(ctx, env, r)
	rec.Meta, rec.MetaMasked = b.Meta, b.MetaMasked
	return rec
}
