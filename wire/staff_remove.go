package wire

// staff_remove.go — `lore member remove` runs the existing actor.offboard
// op (ops/period) as the same caller, so its checks (agents.person_only,
// the admin's roster write) and its writes are exactly offboard's.

import (
	"context"
	"encoding/json"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsperiod "github.com/thesatellite-ai/lore/ops/period"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	opsteam "github.com/thesatellite-ai/lore/ops/team"
)

var _ opsteam.Remover = (*App)(nil)

// OffboardPerson implements opsteam.Remover: it returns the open tasks
// offboard listed (TP-17, read here), so member remove can hand them over.
func (a *App) OffboardPerson(ctx context.Context, who, reason string) (opsteam.Handover, error) {
	e, ok := a.Registry().Lookup(profilestore.OpActorOffboard)
	if !ok {
		return opsteam.Handover{}, errs.New(errs.Unsupported, "member remove: this lore build has no actor offboard").WithHint("lore actor offboard --actor " + who)
	}
	body, err := json.Marshal(map[string]string{"actor": who, "reason": reason})
	if err != nil {
		return opsteam.Handover{}, errs.Wrap(errs.Internal, "member remove: encode the offboard input", err)
	}
	c, _ := engine.CallerOf(ctx)
	// member remove is a Local op: its --yes reaches it as op.Confirmed,
	// never as the engine's call (B2-11: without it the inner offboard
	// asked again on every --yes run)
	c.Yes = c.Yes || engine.Confirmed(ctx) || op.Confirmed(ctx)
	res, err := a.Run(ctx, e, op.JSONDecoder{Body: body}, c)
	if err != nil {
		return opsteam.Handover{}, err
	}
	h := handoverOf(res)
	if len(h.OpenTasks) == 0 && !insideWorkTree(ctx, a.opts.RepoDir) {
		// each repo keeps its own tasks: outside one none are read
		h.TasksNote = handoverOutsideRepo
	}
	return h, nil
}

// handoverOutsideRepo is member remove's open-tasks line outside any repo.
const handoverOutsideRepo = "not listed outside a repo (each repo keeps its own tasks); run the line below in each repo that holds theirs"

// handoverOf reads offboard's open tasks out of its result. A result that
// does not decode lists none and says so: the person is already removed,
// and the hand-over command is printed either way.
func handoverOf(res op.Result) opsteam.Handover {
	raw, err := res.JSON()
	var out opsperiod.OffboardOut
	if err == nil {
		err = json.Unmarshal(raw, &out)
	}
	if err != nil {
		return opsteam.Handover{TasksNote: "could not be read here (" + err.Error() + ")"}
	}
	h := opsteam.Handover{TasksNote: out.TasksNote, OpenTasks: make([]opsteam.HandoverTask, 0, len(out.OpenTasks))}
	for _, t := range out.OpenTasks {
		h.OpenTasks = append(h.OpenTasks, opsteam.HandoverTask{ID: t.ID, Title: t.Title, Store: t.Store})
	}
	return h
}
