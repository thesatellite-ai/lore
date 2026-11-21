package sqlite

import (
	"context"
	"slices"
	"testing"
)

// TestLensTablesBuildsOnlyTheNamedViews (devco D-813): a lens opened with
// LensOptions.Tables creates the "<table>" and "<table>_all" views of the
// named tables alone (no other table's views, no derived view), reads the
// same rows through them as a full lens, and masks the same values.
func TestLensTablesBuildsOnlyTheNamedViews(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, srcs, _, host := sensitiveWorld(t)
	full := openMasked(t, srcs, true)
	narrow, err := OpenLensWith(ctx, srcs, LensOptions{MaskSensitive: true, Tables: []string{"resources", "memories"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = narrow.Close() })
	if got := narrow.Views(); !slices.Equal(got, []string{"memories_all", "resources_all"}) {
		t.Fatalf("narrowed lens views = %v (full lens has %d), want only memories_all and resources_all", got, len(full.Views()))
	}
	for _, q := range []string{
		`SELECT source, name, fields FROM resources_all ORDER BY name`,
		`SELECT json_extract(fields, '$."` + host.String() + `".v') FROM resources_all WHERE name = 'vault_prod'`,
		`SELECT source, body FROM memories_all ORDER BY body`,
		`SELECT body FROM memories ORDER BY body`,
	} {
		want, got := dump(must(full.Query(ctx, q))), dump(must(narrow.Query(ctx, q)))
		if got != want {
			t.Fatalf("%s:\nnarrowed %s\nfull     %s", q, got, want)
		}
	}
	if res := must(narrow.Query(ctx, `SELECT json_extract(fields, '$."`+host.String()+`".v') FROM resources_all WHERE name = 'vault_prod'`)); res.Rows[0][0].String() != MaskedValue {
		t.Fatalf("narrowed lens shows a sensitive value: %v", res.Rows)
	}
	for _, q := range []string{`SELECT * FROM decisions_all`, `SELECT * FROM res_vault_all`, `SELECT * FROM time_spent`, `SELECT * FROM res__all`} {
		if _, err := narrow.Query(ctx, q); err == nil {
			t.Fatalf("%s: a narrowed lens exposed a table it was not given", q)
		}
	}
}
