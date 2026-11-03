// stores_xref.go — F15 at write time: a command that stores a pointer
// (comment --on-id, knowledge ref, task --ref) calls checkReferences with
// its routed target and the ids it points at; a pointer from a
// client-readable row to an internal or personal row is refused before
// anything is written (the pointer alone reveals the hidden row).
package main

import (
	"context"
	"errors"
	"strings"

	"saas/pkg/aicoder/cachedb"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/xref"
)

// rosterTables are the tables whose ids F15 does not treat as pointers:
// the org roster (actors, teams), which a client row may name.
var rosterTables = map[string]bool{tableActors: true, tableTeams: true}

// tableTeams is the roster's teams table (natural_ids.go "Team").
const tableTeams = "teams"

// routeKind is who can read the project a write is routed to.
func routeKind(route writeRoute) xref.Kind {
	switch {
	case route.Project != nil && route.Label == personalLabel:
		return xref.Personal
	case route.Project != nil:
		return xref.Internal
	case route.Situation != nil && route.Situation.Result.Role == orgresolve.RoleOwn:
		return xref.Internal
	}
	return xref.Client
}

// checkReferences refuses pointers that flow outward (F15). refs are row
// ids or "<project>:<id>" forms; ids not loaded here are treated as this
// repo's own rows.
func checkReferences(ctx context.Context, route writeRoute, refs []string) error {
	if len(refs) == 0 {
		return nil
	}
	from := routeKind(route)
	rows, _ := externalRowsHere(ctx, cachedb.Query{})
	where := map[string]cachedb.Row{}
	for _, r := range rows {
		where[r.ID] = r
	}
	for _, raw := range refs {
		ref, err := xref.Parse(raw)
		if err != nil {
			return errcodes.New(errcodes.InvalidInput, err.Error())
		}
		to := xref.Client
		samePersonal := false
		if r, ok := where[ref.RowID]; ok {
			if rosterTables[r.Table] {
				// A roster id (person, agent, team) is not a pointer into
				// hidden content: a client row may name one, and the client
				// copy of the actor is the F18 projection {id, display name,
				// kind}. The leak check exempts the same ids (leakcheck
				// Index.actors), so both guards agree.
				continue
			}
			switch {
			case strings.HasPrefix(r.Source, cachedb.SourcePersonal+":"):
				to = xref.Personal
				samePersonal = route.Label == personalLabel
			case strings.HasPrefix(r.Source, cachedb.SourceStore+":"):
				to = xref.Internal
			}
		}
		if err := xref.Allowed(from, to, samePersonal); err != nil {
			if errors.Is(err, xref.ErrInward) {
				return errcodes.New(errcodes.LeakBlocked, err.Error()+"; nothing written").
					WithHint("write the pointer on the internal side instead (e.g. --to <org>), where the client cannot read it")
			}
			return err
		}
	}
	return nil
}
