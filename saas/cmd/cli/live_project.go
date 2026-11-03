// live_project.go — the client-repo copy of an actor row (D8, F18, §6):
// a client repo may only hold who did something by name, never how to
// reach or recognise them elsewhere. Every actor row lore writes into a
// client repo (live ref or .lore/data) goes through projectActorForClient.
package main

import (
	"context"

	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/orgresolve"
)

// actorClientFields are the only actor fields a client repo may hold: the
// row envelope, the id rows point at, the name people read, and the kind.
// stable_key (an email), handle, label, owner_actor_id and every other
// field stay internal.
var actorClientFields = map[string]bool{"_table": true, "_v": true, "id": true, "display_name": true, "kind": true}

// actorsTable is the actors table name.
const actorsTable = "actors"

// projectActorForClient returns the client-safe copy of an actor document.
func projectActorForClient(doc map[string]any) map[string]any {
	out := make(map[string]any, len(actorClientFields))
	for k, v := range doc {
		if actorClientFields[k] {
			out[k] = v
		}
	}
	return out
}

// projectActorBytes applies projectActorForClient to canonical row bytes
// (a document that does not decode is returned as is: the leak check, not
// the projection, is the guard for anything malformed).
func projectActorBytes(b []byte) []byte {
	doc, err := canonjson.Decode(b, canonjson.DefaultMaxDepth)
	if err != nil {
		return b
	}
	out, err := canonjson.Encode(projectActorForClient(doc))
	if err != nil {
		return b
	}
	return out
}

// repoIsClient reports whether dir resolves to a client repo of an org
// (F10 repo_role). Unknown counts as client: the projection only removes
// fields, so failing closed costs nothing but detail.
func repoIsClient(ctx context.Context, dir string) bool {
	s, err := gatherOrgSituation(ctx, dir)
	if err != nil || s == nil {
		return true
	}
	if s.Result.Org == nil {
		return false
	}
	return s.Result.Role != orgresolve.RoleOwn
}
