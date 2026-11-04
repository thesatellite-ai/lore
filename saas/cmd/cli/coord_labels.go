// coord_labels.go — naming actors in team views (mission status, inbox,
// task show) by label instead of id.
package main

import (
	"context"
	"strings"

	"dbent/gen/ent"
	"saas/pkg/aicoder/identity"

	"github.com/thesatellite-ai/lore/schema"
)

// actorLabels names actors for views. An agent started with `lore agent
// start` is written to the org roster (the org folder's project), not to
// the project a view reads, so a view that only looks in its own project
// prints the agent's id (lore-docs BUG-D4). Lookup order: the actors rows
// the core reads (every attached store: the repo, and a 0.1.18 org's roster
// folder), then a v0.1.17 org store's roster, opened once on the first miss.
type actorLabels struct {
	roster *roster
	// tried is true once the v0.1.17 roster was opened (or failed to open).
	tried bool
}

// Actor fields a label is built from.
const (
	fieldHandle      = "handle"
	fieldLabel       = "label"
	fieldDisplayName = "display_name"
)

// label is the actor's name for views, or the id when no project knows it.
func (l *actorLabels) label(ctx context.Context, id string) string {
	if id == "" {
		return ""
	}
	if d, ok, err := coreDoc(ctx, schema.TableActors, id); err == nil && ok {
		return labelOf(d.Str(fieldHandle), d.Str(fieldLabel), d.Str(fieldDisplayName))
	}
	if !l.tried {
		l.tried = true
		// A 0.1.18 org roster is attached to the core (read above); only a
		// v0.1.17 org store still keeps its roster in a store mirror.
		if cr, err := openCoreRoster(ctx); err == nil && cr == nil {
			if r, err := openRoster(ctx, &commonFlags{}); err == nil {
				l.roster = r
			}
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
	var handle, label string
	if a.Handle != nil {
		handle = *a.Handle
	}
	if a.Label != nil {
		label = *a.Label
	}
	return labelOf(handle, label, a.DisplayName)
}

// labelOf is actorDisplay over plain fields.
func labelOf(handle, label, displayName string) string {
	if handle != "" {
		return handlePrefix + handle
	}
	if label != "" {
		return label
	}
	return strings.TrimPrefix(displayName, identity.KindAgent+":")
}
