// actor_shared.go — shared helpers for resolving actor IDs across `add` commands
//
// resolveCurrentActorID finds or creates the CLI invoker's actor via
// identity.Resolve + upsertActor, cached per client for the process
// lifetime. The read-side lookups (task list --assignee, --created-by
// validation) moved to the core with the add/list commands; their old
// lore.db versions were removed as dead code.
package main

import (
	"context"
	"fmt"

	"dbent/gen/ent"
	entActor "dbent/gen/ent/actor"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/lsync"
)

var currentActorCache = map[*ent.Client]string{}

// resolveCurrentActorID returns the actor_id for the current CLI invoker,
// upserting an Actor row if needed. Cached per-client
func resolveCurrentActorID(ctx context.Context, client *ent.Client) (string, error) {
	if id, ok := currentActorCache[client]; ok {
		return id, nil
	}
	r, err := resolveIdentityHere(ctx)
	if err != nil {
		return "", err
	}
	actor, err := upsertActor(ctx, client, r)
	if err != nil {
		return "", errcodes.New(errcodes.Internal, "resolve current actor").WithCause(err)
	}
	currentActorCache[client] = actor.ID
	return actor.ID, nil
}

// actorByKey finds the actor row of stableKey in this lore.db.
//
// Why it is more than a stable_key lookup: a client repo (and any repo that
// is not an org's own) only ever holds the projection {id, display_name,
// kind} of an actor (D8, F18). A fresh clone imports that projection with
// the placeholder key lsync.ProjectedKeyPrefix+<id>, so a lookup by the real
// key misses it. The actor id is derived from stable_key (natural_ids.go),
// so the row whose id is Deterministic(stableKey) and whose key is still
// that placeholder IS this person's row: it is the one to reuse.
//
// adopt=true (writers) replaces the placeholder with the real key in this
// lore.db only, so later lookups by key find it directly; the exported file
// keeps the projection, because export projects again. adopt=false
// (read-side filters, which never write) returns the projected row as is.
//
// Dedup rule for two machines (N1): neither machine mints a random id for a
// person. Both derive the same id from the same stable_key, so concurrent
// first writes produce the same .lore/data/actors/<id>.json, which git merges
// as one file (identical add, or the row merge driver for differing
// volatile fields). The naturalIDHook's random-id fallback is never used for
// actors (naturalIDSpec.immutableKey) for the same reason.
//
// Returns an ent NotFound error when no row exists for the key.
func actorByKey(ctx context.Context, client *ent.Client, stableKey string, adopt bool) (*ent.Actor, error) {
	a, err := client.Actor.Query().Where(entActor.StableKey(stableKey)).Only(ctx)
	if err == nil || !ent.IsNotFound(err) {
		return a, err
	}
	notFound := err
	id, derr := ids.Deterministic(ids.PrefixActor, stableKey)
	if derr != nil {
		return nil, notFound
	}
	p, gerr := client.Actor.Get(ctx, id)
	if gerr != nil {
		if ent.IsNotFound(gerr) {
			return nil, notFound
		}
		return nil, gerr
	}
	placeholder := lsync.ProjectedKeyPrefix + id
	if p.StableKey != placeholder {
		// The id is taken by a row with another real key: only a hash
		// collision can do that; never treat it as this person.
		return nil, notFound
	}
	if !adopt {
		return p, nil
	}
	db := rawDBFromClient(client)
	if db == nil {
		// No raw handle (a client built outside resolveContext): reuse the
		// row without upgrading its key; the id is what rows cite.
		return p, nil
	}
	// stable_key is Immutable in the ent schema (no update setter), and it
	// must change only from the placeholder to the key the id was derived
	// from; the WHERE clause makes that the only possible transition.
	if _, err := db.ExecContext(ctx, `UPDATE actors SET stable_key = ? WHERE id = ? AND stable_key = ?`, stableKey, id, placeholder); err != nil {
		return nil, fmt.Errorf("adopt projected actor %s: %w", id, err)
	}
	return client.Actor.Get(ctx, id)
}
