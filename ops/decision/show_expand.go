package decision

import (
	"context"

	"github.com/thesatellite-ai/lore/ops/internal/expand"
)

// ShowIn is the input of the show operation: the id flags plus --expand.
type ShowIn struct {
	ID      string `json:"id" arg:"0" desc:"the decision id (dec_…)"`
	Project string `json:"project,omitempty" flag:"project" desc:"accepted for v0.1.17 scripts; the id already names its project"`
	expand.Flag
}

// showExpand is the show operation: the stored row as before, and with
// --expand its text fields filled in at the show point (secrets and
// scanner hits masked; SECRETS 2.7, 12, 13.2). Stored text never changes.
func (o ops) showExpand(ctx context.Context, in ShowIn) (ShowOut, error) {
	out, err := o.show(ctx, IDIn{ID: in.ID, Project: in.Project})
	if err != nil || !in.Expand {
		return out, err
	}
	ex, _ := o.env.(expand.Env)
	blk, err := expand.Show(ctx, ex, in.ID)
	if err != nil {
		return ShowOut{}, err
	}
	out.Expanded, out.ExpandedRefs = blk.Expanded, blk.Refs
	return out, nil
}

// Text is the human form of show, with the --expand block when set.
func (o ShowOut) Text() string { return o.baseText() + expand.Text(o.Expanded, o.ExpandedRefs) }
