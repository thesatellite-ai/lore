// coord_labels.go — naming actors in team views (mission status, inbox,
// task show) by label instead of id.
package main

import (
	"context"
	"strings"

	"dbent/gen/ent"
	"saas/pkg/aicoder/identity"
)

// actorLabels names actors for views. An agent started with `lore agent
// start` is written to the org roster (the org folder's project), not to
// the project a view reads, so a view that only looks in its own project
// prints the agent's id (lore-docs BUG-D4). Lookup order: the view's own
// project, then the roster, opened once on the first miss.
type actorLabels struct {
	// local is the view's project (nil = none).
	local  *ent.Client
	roster *roster
	// tried is true once the roster was opened (or failed to open).
	tried bool
}

// label is the actor's name for views, or the id when no project knows it.
func (l *actorLabels) label(ctx context.Context, id string) string {
	if id == "" {
		return ""
	}
	if l.local != nil {
		if a, err := l.local.Actor.Get(ctx, id); err == nil {
			return actorDisplay(a)
		}
	}
	if !l.tried {
		l.tried = true
		if r, err := openRoster(ctx, &commonFlags{}); err == nil {
			l.roster = r
		}
	}
	if l.roster != nil {
		if a, err := l.roster.client.Actor.Get(ctx, id); err == nil {
			return actorDisplay(a)
		}
	}
	return id
}

// close releases the roster handle.
func (l *actorLabels) close() {
	if l.roster != nil {
		_ = l.roster.client.Close() // read-only roster handle: a close error is not actionable
		l.roster = nil
	}
}

// actorDisplay is an actor's handle (@h), else its label, else its display
// name with an agent's kind prefix dropped.
func actorDisplay(a *ent.Actor) string {
	if a.Handle != nil && *a.Handle != "" {
		return handlePrefix + *a.Handle
	}
	if a.Label != nil && *a.Label != "" {
		return *a.Label
	}
	return strings.TrimPrefix(a.DisplayName, identity.KindAgent+":")
}
