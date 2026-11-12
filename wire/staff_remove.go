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
	"github.com/thesatellite-ai/lore/ops/profilestore"
	opsteam "github.com/thesatellite-ai/lore/ops/team"
)

var _ opsteam.Remover = (*App)(nil)

// OffboardPerson implements opsteam.Remover.
func (a *App) OffboardPerson(ctx context.Context, who, reason string) error {
	e, ok := a.Registry().Lookup(profilestore.OpActorOffboard)
	if !ok {
		return errs.New(errs.Unsupported, "member remove: this lore build has no actor offboard").WithHint("lore actor offboard " + who)
	}
	body, err := json.Marshal(map[string]string{"actor": who, "reason": reason})
	if err != nil {
		return errs.Wrap(errs.Internal, "member remove: encode the offboard input", err)
	}
	c, _ := engine.CallerOf(ctx)
	// member remove is a Local op: its --yes reaches it as op.Confirmed,
	// never as the engine's call (B2-11: without it the inner offboard
	// asked again on every --yes run)
	c.Yes = c.Yes || engine.Confirmed(ctx) || op.Confirmed(ctx)
	_, err = a.Run(ctx, e, op.JSONDecoder{Body: body}, c)
	return err
}
