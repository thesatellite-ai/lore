package incident

import (
	"context"

	"github.com/thesatellite-ai/lore/ops/internal/expand"
	"github.com/thesatellite-ai/lore/ops/internal/metashow"
)

// ShowIn is the input of the show operation: the id flags plus --expand.
type ShowIn struct {
	ID string `json:"id" arg:"0" desc:"the incident id (inc_…)"`
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
	if me, ok := o.c.Env.(metashow.Env); ok && err == nil {
		b := metashow.Of(ctx, me, in.ID) // MQ13: metadata on every read
		out.Meta, out.MetaMasked = b.Meta, b.MetaMasked
	}
	if err != nil || (!in.Expand && !in.Reveal && !in.Force) {
		return out, err
	}
	ex, _ := o.c.Env.(expand.Env)
	blk, err := expand.ShowWith(ctx, ex, in.ID, in.RevealFlag)
	if err != nil {
		return ShowOut{}, err
	}
	out.Expanded, out.ExpandedRefs = blk.Expanded, blk.Refs
	out.Revealed, out.Forced, out.Warning = blk.Revealed, blk.Forced, blk.Warning
	return out, nil
}

// Text is the human form of show, with the --expand block when set.
func (o ShowOut) Text() string {
	return o.baseText() + metashow.Text(o.Meta, o.MetaMasked) + expand.Text(o.Expanded, o.ExpandedRefs) + expand.WarningText(o.Warning)
}
