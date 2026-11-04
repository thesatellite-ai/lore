// actor_shared.go — shared helpers for resolving actor IDs across `add` commands
//
// Three flavors:
//   - resolveCurrentActorID: find-or-create the CLI invoker's actor via
//     identity.Resolve + upsertActor. Cached per-client for the process lifetime
//   - resolveActorIDFlag: validate that a user-supplied --created-by /
//     --assigned-to / --from / --to / --validated-by flag refers to an
//     existing actor row
//   - resolveActorRef: read-side lookup (e.g. task list --assignee) that also
//     accepts @me, a stable_key or a display name, and never writes
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	coreidentity "github.com/thesatellite-ai/lore/adapters/identity"

	"dbent/gen/ent"
	entActor "dbent/gen/ent/actor"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/constants"
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

// resolveActorIDFlag validates that flag (an opaque act_* id) refers to a
// real actor row. Empty flag = "no override; let caller auto-fill"
func resolveActorIDFlag(ctx context.Context, client *ent.Client, flag string) (string, error) {
	if flag == "" {
		return "", nil
	}
	if _, err := client.Actor.Get(ctx, flag); err != nil {
		return "", errcodes.New(errcodes.NotFound, "actor "+flag+" not found").
			WithHint("use `lore actor list` to see available actor IDs")
	}
	return flag, nil
}

// resolveActorRef turns a user-facing actor reference into an actor_id for
// read-side filters. Forms, tried in order:
//
//	@me           the current invoker (constants.ActorRefMe)
//	act_*         an existing actor id
//	stable_key    exact match, e.g. "human:jane@example.com"
//	display name  case-insensitive, must match exactly one actor
//
// It never writes: unlike resolveCurrentActorID, an @me whose identity has no
// actor row yet is reported as not found instead of being upserted, so a
// filter stays safe under --read-only. display_name is not unique, so a name
// shared by several actors is an E_INVALID_INPUT listing the candidate ids
// rather than a silent pick.
func resolveActorRef(ctx context.Context, client *ent.Client, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errcodes.New(errcodes.InvalidInput, "actor reference is empty").
			WithHint("pass an actor id (act_*), " + constants.ActorRefMe + ", a stable_key or a display name")
	}
	if ref == constants.ActorRefMe {
		r, err := resolveIdentityHere(ctx)
		if err != nil {
			return "", err
		}
		key := r.StableKey
		a, err := actorByKey(ctx, client, key, false)
		if ent.IsNotFound(err) {
			return "", errcodes.New(errcodes.NotFound, "current identity "+key+" has no actor in this project yet").
				WithHint("an actor row is created on its first write, e.g. `lore task add`")
		}
		if err != nil {
			return "", errcodes.New(errcodes.Internal, "resolve current actor").WithCause(err)
		}
		return a.ID, nil
	}
	if a, err := client.Actor.Get(ctx, ref); err == nil {
		return a.ID, nil
	} else if !ent.IsNotFound(err) {
		return "", errcodes.New(errcodes.Internal, "look up actor").WithCause(err)
	}
	if a, err := client.Actor.Query().Where(entActor.StableKey(ref)).Only(ctx); err == nil {
		return a.ID, nil
	} else if !ent.IsNotFound(err) {
		return "", errcodes.New(errcodes.Internal, "look up actor").WithCause(err)
	}
	matches, err := client.Actor.Query().
		Where(entActor.DisplayNameEqualFold(ref)).
		Order(ent.Asc(entActor.FieldID)).
		All(ctx)
	if err != nil {
		return "", errcodes.New(errcodes.Internal, "look up actor").WithCause(err)
	}
	switch len(matches) {
	case 0:
		return "", errcodes.New(errcodes.NotFound, "actor "+ref+" not found").
			WithHint("use `lore actor list` to see available actors")
	case 1:
		return matches[0].ID, nil
	}
	ids := make([]string, len(matches))
	for i, a := range matches {
		ids[i] = a.ID
	}
	return "", errcodes.New(errcodes.InvalidInput, "actor name "+ref+" matches several actors: "+strings.Join(ids, ", ")).
		WithHint("pass one of the actor ids instead")
}

// resolveCreatedBy returns either the validated flag value, or the
// current identity's actor_id if the flag was empty. Use this from every
// `add` command to populate created_by_actor_id
func resolveCreatedBy(ctx context.Context, client *ent.Client, flag string) (string, error) {
	id, err := resolveActorIDFlag(ctx, client, flag)
	if err != nil {
		return "", err
	}
	if id != "" {
		return id, nil
	}
	return resolveCurrentActorID(ctx, client)
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

// coreOrg is wire.Options.Org for the 0.1.18 core: the org entry v0.1.17's
// org resolution picks for the working directory (LORE_ORG, repo_orgs,
// links, own:/repos: patterns), as the identity adapter's input. The core
// then resolves the actor itself (adapters/identity) and writes the actor
// row on its first write to a store. nil when no org resolves.
func coreOrg(ctx context.Context) (*coreidentity.Org, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "getwd").WithCause(err)
	}
	s, err := gatherOrgSituation(ctx, dir)
	if err != nil {
		return nil, coreErr(err)
	}
	if s.ResolveErr != nil {
		return nil, coreErr(orgResolveCLIError(s.ResolveErr))
	}
	o := s.Result.Org
	if o == nil {
		return nil, nil
	}
	return &coreidentity.Org{Name: o.Name, Identity: coreidentity.Entry{Key: o.Identity.Key, Name: o.Identity.Name, Handle: o.Identity.Handle}}, nil
}
