package engine

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/thesatellite-ai/lore/logic/validate"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Project-scoped pointers on a move (F-MOVE-1, te-a's finding).
//
//	belongs-to ref (tasklist_id, mission_id, plan_id, repo_id, …)
//	  target live in to ─────────────► kept (it moved first, or lives there)
//	  held by a third place ─────────► kept; checkRefs / F15 decide
//	  held only by src, or nowhere ──► cleared + W_LEFT_CONTAINER
//	project_id, to not a client ─────► to's one project when it holds
//	                                    exactly one, else kept (rehomeProject
//	                                    is the strict client form)
//	parent_id (self), to not a client, no holder ─► carried as is (TASK 6.8
//	                                    keeps it), not a missing ref
//
// Without this a task moved out of its own repo kept the own repo's
// tasklist_id, and the next move from a checkout that cannot see that repo
// answered E_NOT_FOUND "tasks refers to rows no attached store holds".

// WarnLeftContainer names a container (tasklist, mission, plan, …) a moved
// row left behind: the place it moved to does not hold it, so the pointer
// was cleared rather than left dangling (F-MOVE-1).
const WarnLeftContainer WarnCode = "W_LEFT_CONTAINER"

// ownMoveRule are the belongs-to targets a move handles by its own rule,
// not as a container: the project (rehomeProject), a resource's kind and
// its field rows (clientKind, BATCH 3) and metadata keys (a move never
// creates or drops a key row, G-ENGINE-5).
var ownMoveRule = map[row.Table]bool{
	schema.TableProjects: true, schema.TableKinds: true, schema.TableKindFields: true, schema.TableMetaKeys: true,
}

// leaveContainers clears every belongs-to pointer of a row moving from src
// into to whose target to does not hold live and that only src (or no
// attached place) holds, one warning each. Targets with their own move
// rule (ownMoveRule) and actor pointers (roster, never hidden content) are
// left alone, and a relation that allows dangling keeps its id by design.
func (e *Engine) leaveContainers(ctx context.Context, src, to store.Source, def schema.TableDef, fields map[string]value.Value) (map[string]value.Value, []Warning, error) {
	out := fields
	var ws []Warning
	for _, rel := range def.Relations {
		if rel.Kind != schema.BelongsTo || rel.AllowDangling || ownMoveRule[rel.Target] || rosterTables[rel.Target] {
			continue
		}
		v, ok := fields[rel.Field]
		if !ok || v == nil || v.Kind() == value.KindNull || v.String() == "" {
			continue
		}
		id, err := ids.ParseAny(v.String())
		if err != nil {
			continue // not an id: validation reports it
		}
		live, err := e.liveIn(ctx, to, rel.Target, id)
		if err != nil {
			return nil, nil, err
		}
		if live {
			continue
		}
		holders, err := e.holders(ctx, validate.Target{Field: rel.Field, ID: id, Table: rel.Target})
		if err != nil {
			return nil, nil, err
		}
		if len(holders) > 0 && !slices.Contains(holders, src) {
			continue
		}
		if len(ws) == 0 {
			out = maps.Clone(fields)
		}
		delete(out, rel.Field)
		ws = append(ws, Warning{Code: WarnLeftContainer, Field: rel.Field,
			Message: "left " + rel.Name + " " + id.String() + " behind in " + string(src) + " (" + string(to) + " does not hold it); file it there with lore " + string(rel.Target) + " or edit " + rel.Field})
	}
	return out, ws, nil
}

// liveIn reports whether store name holds the row table/id live.
func (e *Engine) liveIn(ctx context.Context, name store.Source, table row.Table, id ids.AnyID) (bool, error) {
	got, gone, err := e.getRow(ctx, name, table, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return false, nil
	case err != nil:
		return false, err
	}
	return !gone && !got.Deleted, nil
}

// carriedEdges drops from names the self pointers (parent_id) of a row
// moving into an internal place whose target no attached place holds: the
// edge was checked when it was written, TASK 6.8 keeps it on a move, and a
// checkout that cannot see the parent must still be able to move the
// child on. A client target never gets here with one (refuseOrphanChild).
func (e *Engine) carriedEdges(ctx context.Context, def schema.TableDef, fields map[string]value.Value, names []string) ([]string, error) {
	out := names
	for _, rel := range def.Relations {
		if rel.Kind != schema.Self || rel.AllowDangling {
			continue
		}
		v, ok := fields[rel.Field]
		if !ok || v == nil || v.String() == "" {
			continue
		}
		id, err := ids.ParseAny(v.String())
		if err != nil {
			continue
		}
		holders, err := e.holders(ctx, validate.Target{Field: rel.Field, ID: id, Table: def.Name})
		if err != nil {
			return nil, err
		}
		if len(holders) == 0 {
			out = slices.DeleteFunc(slices.Clone(out), func(n string) bool { return n == rel.Field })
		}
	}
	return out, nil
}
