package mission

import (
	"context"

	"github.com/thesatellite-ai/lore/ops/internal/expand"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// ShowIn is the input of the show operation: the id flags plus --expand.
type ShowIn struct {
	ID string `json:"id" arg:"0" desc:"the mission id (msn_…)"`
	expand.Flag
	expand.RevealFlag
	// TimeFlagsIn: --sort time_spent|elapsed and the --worked-*/--min-time
	// filters on the task table (TIME 10, FA-TASK-5).
	opstask.TimeFlagsIn
}

// showExpand is the show operation: the stored row as before, and with
// --expand its text fields filled in at the show point (secrets and
// scanner hits masked; SECRETS 2.7, 12, 13.2). Stored text never changes.
// --reveal implies --expand and also fills each ${secret:…} written in the
// row after an audited ask (expand.ShowWith; SECRETS 2.7, SQ10, D18).
func (o ops) showExpand(ctx context.Context, in ShowIn) (ShowOut, error) {
	out, err := o.show(ctx, IDIn{ID: in.ID})
	if err != nil {
		return out, err
	}
	// TIME 10: filter and order the task table by tracked time.
	rows, err := o.missionRows(ctx, out.Data.ID)
	if err != nil {
		return ShowOut{}, err
	}
	if out.Data.Tasks, out.timed, err = opstask.TimeTable(ctx, o.env, in.TimeFlagsIn, out.Data.Tasks, rows); err != nil {
		return ShowOut{}, err
	}
	out.Count = len(out.Data.Tasks)
	if !in.Expand && !in.Reveal && !in.Force {
		return out, nil
	}
	ex, _ := o.env.(expand.Env)
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
	return o.baseText() + expand.Text(o.Expanded, o.Refs) + expand.WarningText(o.Warning)
}
