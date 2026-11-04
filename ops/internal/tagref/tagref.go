// Package tagref resolves tag names to tag row ids for the ops that keep
// tags as a tag_ids list (memories, the knowledge nouns, `lore tag
// attach|detach`).
//
// Natural ids are per store (the target store's namespace is hashed into
// them), so a tag is found by its key (project_id, name) through
// engine.FindByKey, never by recomputing its id: the same name in two
// stores is two rows, and the nearest store's row is the one attached.
package tagref

import (
	"context"
	"slices"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// Key fields of the tags table (schema: UniqueOn("project_id", "name")).
const (
	fieldProject = "project_id"
	fieldName    = "name"
)

// Find returns the ids of the live tags named name in project, nearest
// store first (empty when none exists).
func Find(ctx context.Context, l engine.KeyLister, project, name string) ([]ids.AnyID, error) {
	found, err := engine.FindByKey(ctx, l, schema.TableTags, map[string]string{fieldProject: project, fieldName: name})
	if err != nil {
		return nil, err
	}
	out := make([]ids.AnyID, 0, len(found))
	for _, f := range found {
		out = append(out, f.Row.ID)
	}
	return out, nil
}

// Legacy is the v0.1.17 id of the tag named name in project (the natural id
// without a store namespace). Rows written before per-store ids, and tags
// already deleted, are matched by it when a tag is removed by name.
func Legacy(project, name string) (ids.AnyID, error) {
	return engine.NaturalID(ids.PrefixTag, project, name)
}

// Droppable is every id a "remove tag <name>" must take off a row of
// project: the live tags of that name in every store plus the legacy id
// (the tag row may be deleted already).
func Droppable(ctx context.Context, l engine.KeyLister, project, name string) ([]ids.AnyID, error) {
	out, err := Find(ctx, l, project, name)
	if err != nil {
		return nil, err
	}
	legacy, err := Legacy(project, name)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(out, legacy) {
		out = append(out, legacy)
	}
	return out, nil
}

// Index maps project → tag name → the ids of live tags of that name (in
// every attached store), read once for a filter over many rows.
type Index map[string]map[string][]string

// Load reads every live tag of every attached store into an Index.
func Load(ctx context.Context, l engine.KeyLister) (Index, error) {
	idx := Index{}
	for _, src := range l.Stores() {
		rows, err := l.List(ctx, src, schema.TableTags)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if r.Deleted {
				continue
			}
			p, n := text(r, fieldProject), text(r, fieldName)
			if idx[p] == nil {
				idx[p] = map[string][]string{}
			}
			idx[p][n] = append(idx[p][n], r.ID.String())
		}
	}
	return idx, nil
}

// Has reports whether have (a row's tag ids) holds a tag named name of
// project: any live tag of that name, or the legacy id.
func (idx Index) Has(have []string, project, name string) bool {
	for _, id := range idx[project][name] {
		if slices.Contains(have, id) {
			return true
		}
	}
	legacy, err := Legacy(project, name)
	return err == nil && slices.Contains(have, legacy.String())
}

// text is a field's string form ("" when unset).
func text(r row.Row, f string) string {
	if v, ok := r.Fields[f]; ok && v != nil {
		return v.String()
	}
	return ""
}
