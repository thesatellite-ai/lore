package engine

// move_personalproject.go — devco P-7: sharing a personal note written in
// a repo with no lore data. Such a write records a project row for the
// repo in the personal store only (`me` `_store/projects/`), so moving the
// note to an org folder carried a project_id that F15 refuses ("a shared
// row cannot point at a personal row"): the note could never be shared.
//
//	project_id held by a shared store, or in to ─▶ unchanged (rehomeProject)
//	to holds exactly one project               ─▶ that one (rehomeProject)
//	held only by personal stores, to shared    ─▶ to's folder project (its
//	                                              _meta.json project_id),
//	                                              its row written in to with
//	                                              the personal row's name
//	                                              when to holds none yet
//	to is the store root (no folder id)        ─▶ unchanged; F15 then names it

import (
	"context"
	"maps"

	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// projectNameField is the projects table's name column.
const projectNameField = "name"

// folderProjectIDer is implemented by the git and folder backends: the
// data folder's permanent prj_ id (zero for a store root).
type folderProjectIDer interface {
	FolderProjectID(ctx context.Context) (ids.ID[ids.Project], error)
}

// backendUnwrapper is a wrapping backend (wire's WrapBackend layers).
type backendUnwrapper interface {
	Unwrap() store.Backend
}

// folderProjectOf is the folder project id of an attached store's
// backend, every wrapper removed; zero when it has none.
func folderProjectOf(ctx context.Context, b store.Backend) ids.ID[ids.Project] {
	for b != nil {
		if f, ok := b.(folderProjectIDer); ok {
			if pid, err := f.FolderProjectID(ctx); err == nil {
				return pid
			}
			return ids.ID[ids.Project]{}
		}
		u, ok := b.(backendUnwrapper)
		if !ok {
			break
		}
		b = u.Unwrap()
	}
	return ids.ID[ids.Project]{}
}

// personalOnlyProject is the live project row id names when only personal
// stores hold it (ok false when a shared store holds it too, or none).
func (e *Engine) personalOnlyProject(ctx context.Context, id ids.AnyID) (row.Row, bool) {
	var found row.Row
	ok := false
	for name := range e.stores {
		_, r, err := e.Get(ctx, Ref{Store: name, Table: schema.TableProjects, ID: id})
		if err != nil || r.Deleted {
			continue
		}
		if e.classOf(name) != direction.Personal {
			return row.Row{}, false
		}
		found, ok = r, true
	}
	return found, ok
}

// rehomePersonalProject points fields' project_id at to's folder project
// when the current one is a personal-store project and to is shared (the
// flow above); fields unchanged otherwise.
func (e *Engine) rehomePersonalProject(ctx context.Context, to store.Source, fields map[string]value.Value, cur ids.AnyID) (map[string]value.Value, error) {
	if e.classOf(to) == direction.Personal {
		return fields, nil
	}
	personal, ok := e.personalOnlyProject(ctx, cur)
	if !ok {
		return fields, nil
	}
	pid := folderProjectOf(ctx, e.stores[to].Backend)
	if pid.IsZero() {
		return fields, nil
	}
	if _, r, err := e.Get(ctx, Ref{Store: to, Table: schema.TableProjects, ID: pid.Any()}); err != nil || r.Deleted {
		// the folder's project row, as a first write there records it
		name := map[string]value.Value{}
		if v, has := personal.Fields[projectNameField]; has {
			name[projectNameField] = v
		}
		if _, cerr := e.CreateWithID(ctx, to, schema.TableProjects, pid.Any(), name); cerr != nil {
			return nil, cerr
		}
	}
	ref, err := value.RefOf(pid.Any())
	if err != nil {
		return nil, err
	}
	out := maps.Clone(fields)
	out[projectField] = ref
	return out, nil
}
