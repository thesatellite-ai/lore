// Package creator fills a row's created_by_actor_id column from the calling
// actor (INTEG-AUDIT-1). The column predates the engine-stamped envelope
// (row.Row.CreatedBy) and stays for format compatibility with v0.1.17
// stores, but callers no longer set it by hand: there is no --created-by on
// any add, and every add op takes the value from here, which is the same
// identity the engine stamps into the envelope.
package creator

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Getter is the slice of an ops Env this package reads: one row by ref,
// used to check the caller's actor row exists.
type Getter interface {
	Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error)
}

// Actorer is the optional Env upgrade naming the calling identity's actor
// (the wired Env implements it; test envs may not).
type Actorer interface {
	Actor(ctx context.Context) (ids.ID[ids.Actor], error)
}

// Ref is a ref to the calling actor's row for created_by_actor_id, or nil
// when the Env cannot name the actor or its actor row does not exist (the
// engine refuses a dangling ref). Nil leaves the column unset; readers then
// fall back to the envelope's created_by, which always names the writer.
func Ref(ctx context.Context, env Getter) value.Value {
	a, ok := env.(Actorer)
	if !ok {
		return nil
	}
	actor, err := a.Actor(ctx)
	if err != nil || actor.IsZero() {
		return nil
	}
	if _, r, gerr := env.Get(ctx, engine.Ref{Table: schema.TableActors, ID: actor.Any()}); gerr != nil || r.Deleted {
		return nil
	}
	v, err := value.RefOf(actor.Any())
	if err != nil {
		return nil
	}
	return v
}
