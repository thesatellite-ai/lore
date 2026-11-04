package memory

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/tagref"
	"github.com/thesatellite-ai/lore/schema"
)

// Tags are a list-of-ref field on the memory (decision O11): tag_ids holds
// tag row ids; a tag row is per project, keyed (project_id, name). Natural
// ids are per store, so a name is resolved by key (ops/internal/tagref),
// nearest store first.

// tagRefs resolves tag names of project to their ids. As v0.1.17's `lore
// tag attach`, a tag must exist (create it with `lore tag add`): a typo
// never mints a new tag silently.
func (o ops) tagRefs(ctx context.Context, project string, names []string) ([]ids.AnyID, error) {
	out := make([]ids.AnyID, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			return nil, errs.New(errs.InvalidInput, "memory: an empty --tag")
		}
		found, err := tagref.Find(ctx, o.env, project, n)
		if err != nil {
			return nil, err
		}
		if len(found) == 0 {
			return nil, errs.New(errs.NotFound, "memory: tag "+quoteID(n)+" not found in this project").
				WithHint("create it with `lore tag add --name=" + n + "`")
		}
		id := found[0]
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// tagList builds the tag_ids value of ids (Null when empty: the field is
// then cleared).
func tagList(list []ids.AnyID) (value.Value, error) {
	if len(list) == 0 {
		return value.Null{}, nil
	}
	items := make([]value.Value, 0, len(list))
	for _, id := range list {
		r, err := value.RefOf(id)
		if err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return value.ListOf(value.KindRef, items...)
}

// tagsOf is a row's tag ids (value.ItemStrings: the same ids whether
// the list was built in memory or decoded from its row file).
func tagsOf(r row.Row) []string {
	return value.ItemStrings(r.Fields[schema.FieldTagIDs])
}

// hasTags reports whether r carries every tag named in names (any tag of
// that name in the row's own project, idx loaded once per read).
func hasTags(idx tagref.Index, r row.Row, names []string) bool {
	have := tagsOf(r)
	for _, n := range names {
		if !idx.Has(have, field(r, fieldProject), strings.TrimSpace(n)) {
			return false
		}
	}
	return true
}

// retag returns r's tag ids with add appended and drop removed.
func retag(r row.Row, add, drop []ids.AnyID) []ids.AnyID {
	var out []ids.AnyID
	for _, s := range tagsOf(r) {
		id, err := ids.ParseAny(s)
		if err != nil || slices.Contains(drop, id) {
			continue
		}
		out = append(out, id)
	}
	for _, id := range add {
		if !slices.Contains(out, id) && !slices.Contains(drop, id) {
			out = append(out, id)
		}
	}
	return out
}

// TagLink is one v0.1.17 entity_tags row (dbent/schema/entity_tag.go):
// the tag id on one entity row. MigrateTags reads these.
type TagLink struct {
	EntityTable string
	EntityID    string
	TagID       string
}

// MigrateTags turns v0.1.17 entity_tags rows into each memory's tag_ids
// (decision O11): it returns, per memory id, the sorted, de-duplicated tag
// ids its entity_tags rows name, merged with the ids the memory already
// lists (existing[id], nil when none). Links of other tables, and links
// whose ids are not valid mem_/tag_ ids, are skipped and returned in
// skipped so `lore migrate run` can report them; nothing is dropped
// silently. Pure: the caller writes the results through the engine.
func MigrateTags(links []TagLink, existing map[string][]string) (merged map[string][]string, skipped []TagLink) {
	merged = map[string][]string{}
	for _, l := range links {
		if l.EntityTable != string(schema.TableMemories) {
			continue
		}
		if _, err := ids.Parse[ids.Memory](l.EntityID); err != nil {
			skipped = append(skipped, l)
			continue
		}
		if t, err := ids.ParseAny(l.TagID); err != nil || t.Prefix() != ids.PrefixTag {
			skipped = append(skipped, l)
			continue
		}
		if _, ok := merged[l.EntityID]; !ok {
			merged[l.EntityID] = slices.Clone(existing[l.EntityID])
		}
		merged[l.EntityID] = append(merged[l.EntityID], l.TagID)
	}
	for id, list := range merged {
		slices.Sort(list)
		merged[id] = slices.Compact(list)
	}
	return merged, skipped
}
