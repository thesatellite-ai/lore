package search_test

import (
	"testing"

	"github.com/thesatellite-ai/lore/ops/search"
	"github.com/thesatellite-ai/lore/schema"
)

// TestTargetsHideArchivedWhereTheTableArchives pins devco D-924: a kind
// that gained `archive` (cookbook recipes, workflows, ...) must also drop
// its archived rows from `lore search`; HasArchived follows the table's
// archived_at field, never a hand-kept list that falls behind it.
func TestTargetsHideArchivedWhereTheTableArchives(t *testing.T) {
	t.Parallel()
	defs := map[string]schema.TableDef{}
	for _, d := range schema.Builtin() {
		if d.Kind.IsZero() {
			defs[string(d.Name)] = d
		}
	}
	for _, tg := range search.Targets() {
		if tg.Roster {
			continue
		}
		d, ok := defs[string(tg.Table)]
		if !ok {
			t.Errorf("%s: table %s is not built in", tg.Entity, tg.Table)
			continue
		}
		_, archives := d.Field("archived_at")
		if archives != tg.HasArchived {
			t.Errorf("%s: table %s archived_at=%v but HasArchived=%v", tg.Entity, tg.Table, archives, tg.HasArchived)
		}
	}
}
