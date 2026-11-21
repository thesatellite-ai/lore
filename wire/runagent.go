package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/op"
)

// runAsCaller runs e on eng for c with the agent the command runs for on
// the caller. Every path that opens a store alone (a write naming its
// store from a repo with no lore project or a plain folder, a store clone,
// the roster and personal reads there) runs the engine through it.
//
// Why (devco D-872, bug hunt H-134): App.run fills op.Caller.Agent from
// the opened repo's identity (CallerAgent) only on its main path, after
// these paths have returned, so they reached the engine with no agent on
// the caller: agents.person_only (checked in engine.Run for an agent
// caller) let a detected agent run a command listed for people only, and
// every engine.AgentOf reader (task add's --commitment rule, a metadata
// key's added_by and review, the reveal gate) took the agent for a person.
// The agent comes from the identity chain with detection (writerAgent), not
// from the repo's state, which these paths never open. A surface that
// decided the agent (REST, AgentFixed), named one or set an explicit actor
// keeps it, as on the main path. wire's TestEngineRunsFillTheAgent keeps
// every new store-only path on this funnel.
func (a *App) runAsCaller(ctx context.Context, eng *engine.Engine, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	if !c.AgentFixed && c.Agent == "" && c.Actor.IsZero() {
		c.Agent = a.writerAgent(ctx)
	}
	return eng.Run(ctx, e, d, c)
}
