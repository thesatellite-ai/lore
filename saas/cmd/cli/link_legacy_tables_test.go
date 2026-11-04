package main

import (
	"testing"

	"dbent/gen/ent/migrate"

	opslink "github.com/thesatellite-ai/lore/ops/link"
	"github.com/thesatellite-ai/lore/schema"
)

// Restored from TestUC_MiscTableForOpaqueID: every table the link noun's
// v0.1.17 prefix map names is a real table (lore.db's schema or the core's).
func TestLinkLegacyTablesAreRealTables(t *testing.T) {
	t.Parallel()
	known := map[string]bool{}
	for _, tbl := range migrate.Tables {
		known[tbl.Name] = true
	}
	for _, d := range schema.Builtin() {
		known[string(d.Name)] = true
	}
	for p, table := range opslink.LegacyTables() {
		if !known[string(table)] {
			t.Fatalf("%s maps to %q, which is not a table", p, table)
		}
	}
}
