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
	"strings"

	"dbent/gen/ent"
	entActor "dbent/gen/ent/actor"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/identity"
	"saas/pkg/constants"
)

var currentActorCache = map[*ent.Client]string{}

// resolveCurrentActorID returns the actor_id for the current CLI invoker,
// upserting an Actor row if needed. Cached per-client
func resolveCurrentActorID(ctx context.Context, client *ent.Client) (string, error) {
	if id, ok := currentActorCache[client]; ok {
		return id, nil
	}
	r := identity.Resolve(identity.Inputs{})
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
		key := identity.Resolve(identity.Inputs{}).StableKey
		a, err := client.Actor.Query().Where(entActor.StableKey(key)).Only(ctx)
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
