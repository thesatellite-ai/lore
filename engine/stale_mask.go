package engine

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// staleHidden reports whether an E_STALE message must not repeat the
// values of clash f (staleErr). A clash is hidden when:
//   - its field is sensitive in the table definition;
//   - it is an entry of a field merged entry by entry (a resource's
//     "fields" JSON, "fields.fld_…"), or that whole field: the values are
//     the whole object, whose entries a resource kind may mark sensitive
//     in rows the engine does not read here (RQ1, DELETION 3.11), so they
//     are never shown (fail closed);
//   - it is a metadata entry ("meta.<key id>") whose entry label or key
//     row says sensitive (METADATA 3.20), or whose key rows cannot be read
//     (fail closed).
func (e *Engine) staleHidden(ctx context.Context, f merge.FieldConflict, def schema.TableDef) bool {
	name, entry, dotted := strings.Cut(f.Field, ".")
	if name == merge.FieldMeta && dotted {
		return e.metaClashSensitive(ctx, entry, f.Newer, f.Older)
	}
	fd, ok := def.Field(name)
	if !ok {
		return dotted // an entry of an undeclared field: nothing says it is safe
	}
	return fd.Sensitive || fd.Merge == schema.MergeEntriesByID
}

// metaClashSensitive is staleHidden's metadata rule for key id keyID.
func (e *Engine) metaClashSensitive(ctx context.Context, keyID string, sides ...value.Value) bool {
	for _, v := range sides {
		j, ok := v.(value.JSON)
		if !ok {
			continue // absent on this side
		}
		en, err := meta.DecodeEntry(j)
		if err != nil || en.Sensitive {
			return true
		}
	}
	id, err := ids.Parse[ids.MetaKey](keyID)
	if err != nil {
		return true
	}
	keys, err := e.metaRegistry(ctx)
	if err != nil {
		return true
	}
	for _, k := range keys {
		if k.id == id && k.sensitive {
			return true
		}
	}
	return false
}
