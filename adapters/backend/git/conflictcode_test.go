package git

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// TestConflictCode (later 33, RES 3.13/3.19): a row the Merger cannot
// merge is refused E_RESOURCE_CONFLICT when it is a resource, a kind or a
// kind field (the local commit waits for a person), E_STALE for any other
// table.
func TestConflictCode(t *testing.T) {
	cases := map[row.Table]errs.Code{
		schema.TableResources:  errs.ResourceConflict,
		schema.TableKinds:      errs.ResourceConflict,
		schema.TableKindFields: errs.ResourceConflict,
		schema.TableMemories:   errs.Stale,
		schema.TableTasks:      errs.Stale,
		schema.TableMetaKeys:   errs.Stale,
		row.Table("unknown"):   errs.Stale,
	}
	for table, want := range cases {
		if got := conflictCode(table); got != want {
			t.Errorf("conflictCode(%s) = %s, want %s", table, got, want)
		}
	}
}
