// actor_shared.go — shared helpers for resolving actor IDs across `add` commands
//
// Two flavors:
//   - resolveCurrentActorID: find-or-create the CLI invoker's actor via
//     identity.Resolve + upsertActor. Cached per-client for the process lifetime
//   - resolveActorIDFlag: validate that a user-supplied --created-by /
//     --assigned-to / --from / --to / --validated-by flag refers to an
//     existing actor row
package main

import (
	"context"

	"dbent/gen/ent"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/identity"
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
