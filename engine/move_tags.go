package engine

import (
	"context"
	"maps"
	"strings"

	"github.com/thesatellite-ai/lore/logic/validate"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Tags on a move (devco D-891, bug hunt H-152).
//
//	tag id on the moved row
//	  live in to ───────────────────────────► kept
//	  held elsewhere (src, another store) ──► to's tag of the same name
//	                                           and project, written when
//	                                           to has none (W_TAG_COPIED)
//	  held by no attached store ────────────► kept as is (W_TAG_UNSEEN):
//	                                           an older lore left it in a
//	                                           place this checkout does
//	                                           not read
//
// A tag belongs to its place, not to the rows on it: a tag is per project
// (one name per project), a bulk move leaves the place's tags where they
// are (ops/row placeTables) and one id is never live in two places (the
// E_AMBIGUOUS_ID rule). So the moved row takes the target's own tag of
// that name, and the source keeps its tag for the rows that stay. Before,
// the row kept the source's tag ids: a move from a store's _store into a
// folder left them pointing at _store/tags, and the next move of those
// rows from a checkout that reads only the folder was refused "tag_ids
// refers to rows no attached store holds".

// tagNameField is the tags column holding the tag's name (unique per
// project).
const tagNameField = "name"

// WarnTagCopied names a tag a move wrote into the target place for the
// moved row (the target had no tag of that name).
const WarnTagCopied WarnCode = "W_TAG_COPIED"

// WarnTagUnseen names a tag id a moved row carried that no attached store
// holds; it is kept as it was so the move is not refused for a reference
// it did not make.
const WarnTagUnseen WarnCode = "W_TAG_UNSEEN"

// placeTags points every tag of a row moving into the internal place to
// at to's own tag of the same name (see the file comment). It returns the
// fields, the warnings and the names of tag fields whose kept unseen ids
// checkRefs must not refuse.
func (e *Engine) placeTags(ctx context.Context, to store.Source, def schema.TableDef, fields map[string]value.Value) (map[string]value.Value, []Warning, []string, error) {
	out := fields
	cloned := false
	var ws []Warning
	var carried []string
	for _, rel := range def.Relations {
		if rel.Kind != schema.ManyToMany || rel.Target != schema.TableTags {
			continue
		}
		v, ok := fields[rel.Field]
		if !ok || v == nil || v.Kind() != value.KindList {
			continue
		}
		targets := validate.Targets(def, map[string]value.Value{rel.Field: v})
		if len(targets) == 0 {
			continue
		}
		items := make([]value.Value, 0, len(targets))
		seen := make(map[ids.AnyID]bool, len(targets))
		changed, unseen := false, false
		for _, t := range targets {
			id, w, err := e.placeTag(ctx, to, fields, t.ID)
			if err != nil {
				return nil, nil, nil, err
			}
			if w.Code != "" {
				ws = append(ws, w)
			}
			unseen = unseen || w.Code == WarnTagUnseen
			changed = changed || id != t.ID
			if seen[id] {
				// two source tags of one name (other projects) become one
				// tag of to: the row lists it once
				changed = true
				continue
			}
			seen[id] = true
			ref, err := value.RefOf(id)
			if err != nil {
				return nil, nil, nil, err
			}
			items = append(items, ref)
		}
		if unseen {
			carried = append(carried, rel.Field)
		}
		if !changed {
			continue
		}
		list, err := value.ListOf(value.KindRef, items...)
		if err != nil {
			return nil, nil, nil, err
		}
		if !cloned {
			out, cloned = maps.Clone(fields), true
		}
		out[rel.Field] = list
	}
	return out, ws, carried, nil
}

// placeTag is the id the moved row's tag id takes in to, and the warning
// it earns (zero Code: none).
func (e *Engine) placeTag(ctx context.Context, to store.Source, moved map[string]value.Value, id ids.AnyID) (ids.AnyID, Warning, error) {
	live, err := e.liveIn(ctx, to, schema.TableTags, id)
	if err != nil || live {
		return id, Warning{}, err
	}
	holders, err := e.holders(ctx, validate.Target{Field: schema.FieldTagIDs, ID: id, Table: schema.TableTags})
	if err != nil {
		return id, Warning{}, err
	}
	var tag row.Row
	for _, h := range holders {
		if sr, ok := stagedLive(ctx, h, schema.TableTags, id); ok {
			if tag, err = sr.Decode(); err != nil {
				return id, Warning{}, err
			}
			break
		}
		sr, gone, gerr := e.getRow(ctx, h, schema.TableTags, id)
		if gerr != nil || gone || sr.Deleted {
			continue
		}
		if tag, err = sr.Decode(); err != nil {
			return id, Warning{}, err
		}
		break
	}
	if tag.ID.IsZero() {
		return id, Warning{Code: WarnTagUnseen, Field: schema.FieldTagIDs,
			Message: "kept tag " + id.String() + " as it was: no store this checkout reads holds it; a move from a checkout that reads it puts the row on " + string(to) + "'s own tag"}, nil
	}
	name := str(tag.Fields[tagNameField])
	fields := map[string]value.Value{tagNameField: tag.Fields[tagNameField]}
	for _, f := range []string{"color", "globs"} {
		if v, ok := tag.Fields[f]; ok && v != nil && v.Kind() != value.KindNull && v.String() != "" {
			fields[f] = v
		}
	}
	// the moved row's project is to's by now (placeFolderProject /
	// rehomeProject ran first); a row without one gives the tag its own
	if p, ok := moved[projectField]; ok && p != nil && p.String() != "" {
		fields[projectField] = p
	} else {
		fields[projectField] = tag.Fields[projectField]
	}
	def, err := e.tableDef(schema.TableTags)
	if err != nil {
		return id, Warning{}, err
	}
	typed := retype(def, fields)
	if typed, err = e.placeFolderProject(ctx, to, def, typed); err != nil {
		return id, Warning{}, err
	}
	if have, ok, err := e.tagNamed(ctx, to, def, typed); err != nil || ok {
		return have, Warning{}, err
	}
	w, err := e.create(ctx, to, schema.TableTags, ids.AnyID{}, typed)
	if err != nil {
		return id, Warning{}, err
	}
	return w.Row.ID, Warning{Code: WarnTagCopied, Field: schema.FieldTagIDs,
		Message: "tag " + name + ": " + string(to) + " gets its own copy (" + w.Row.ID.String() + "); " + id.String() + " stays where it was"}, nil
}

// tagNamed finds to's live tag with fields' project and name: the natural
// id a create would give it (a tag staged earlier in the same bulk move
// included), else a committed row with that key (a re-created tag has a
// random id).
func (e *Engine) tagNamed(ctx context.Context, to store.Source, def schema.TableDef, fields map[string]value.Value) (ids.AnyID, bool, error) {
	if _, parts, ok := naturalKey(def, fields); ok {
		nid, err := NaturalID(def.Prefix, naturalParts(e.stores[to], def, parts)...)
		if err != nil {
			return ids.AnyID{}, false, err
		}
		live, err := e.liveIn(ctx, to, schema.TableTags, nid)
		if err != nil || live {
			return nid, live, err
		}
	}
	rows, err := e.List(ctx, to, schema.TableTags)
	if err != nil {
		return ids.AnyID{}, false, err
	}
	name, project := str(fields[tagNameField]), str(fields[projectField])
	for _, r := range rows {
		if strings.EqualFold(str(r.Fields[tagNameField]), name) && str(r.Fields[projectField]) == project {
			return r.ID, true, nil
		}
	}
	return ids.AnyID{}, false, nil
}
