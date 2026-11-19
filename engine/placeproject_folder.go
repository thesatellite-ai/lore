package engine

// placeproject_folder.go — devco D-718. A write aimed at a store folder
// (`lore task add --to <store>/<folder>`, and every other noun's add with
// --to) resolved its project before it knew the target: Env.Project is the
// calling repo's project, so the row landed in the folder pointing at the
// repo's prj_ id. Readers of that folder (its project filter, the folder's
// render) then missed the row, and the repo's id leaked into the folder.
// Every create passes through here, so one rule serves every noun:
//
//	project_id is a live project of the target        ─▶ unchanged
//	target has a folder project with a live row there ─▶ that project
//	anything else (store root, repo store, no row yet) ─▶ unchanged

import (
	"context"
	"maps"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// placeFolderProject points a new row's project_id at the target folder's
// own project when the one it carries is not a project of that folder.
func (e *Engine) placeFolderProject(ctx context.Context, to store.Source, def schema.TableDef, fields map[string]value.Value) (map[string]value.Value, error) {
	if _, subject := projectSubjectTables[def.Name]; subject {
		return fields, nil
	}
	f, ok := def.Field(projectField)
	cur, has := fields[projectField]
	if !ok || !has || f.RefTable != schema.TableProjects {
		return fields, nil
	}
	at, attached := e.stores[to]
	if !attached {
		return fields, nil
	}
	pid := folderProjectOf(ctx, at.Backend)
	if pid.IsZero() || cur.String() == pid.String() {
		return fields, nil
	}
	if id, perr := ids.ParseAny(cur.String()); perr == nil {
		if e.liveProject(ctx, to, id) {
			return fields, nil // a project the folder holds: the writer chose it
		}
	}
	if !e.liveProject(ctx, to, pid.Any()) {
		return fields, nil // the folder records no project row yet: nothing to point at
	}
	ref, err := value.RefOf(pid.Any())
	if err != nil {
		return nil, err
	}
	out := maps.Clone(fields)
	out[projectField] = ref
	return out, nil
}

// projectSubjectTables are the tables whose project_id names the project
// the row is about (a member of it, a repo of it, a team of it), chosen
// explicitly by the writer (`project member add --project <p>`), never
// the calling repo's default: a write into a folder keeps it as written.
// Repointing them made `lore project member add --project northwind-app`
// land on the folder's project (docs profiles section, v0.1.19 batch).
var projectSubjectTables = map[row.Table]struct{}{
	schema.TableProjectMembers: {},
	schema.TableRepos:          {},
	schema.TableTeams:          {},
}

// liveProject reports whether to holds a live project row id, a row staged
// earlier in the same batch included: the wire stages the folder's
// project row (folderProjectFirst) and task add stages its default list
// with the task, so a committed-only read would miss it.
func (e *Engine) liveProject(ctx context.Context, to store.Source, id ids.AnyID) bool {
	if _, ok := stagedLive(ctx, to, schema.TableProjects, id); ok {
		return true
	}
	_, got, err := e.Get(ctx, Ref{Store: to, Table: schema.TableProjects, ID: id})
	return err == nil && !got.Deleted
}
