package lint

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestNearDuplicateImportThenRewrite is devco D-926: a store imported
// once and rewritten a month later keeps both copies live. The default
// lint run (not only --context, not only pinned rows) reports each pair
// once on the older row, with a fix that archives it by its own kind's
// verb; an archived copy and an unrelated row are not reported.
func TestNearDuplicateImportThenRewrite(t *testing.T) {
	month1 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	month2 := month1.AddDate(0, 1, 0)
	mk := func(table row.Table, p ids.Prefix, n byte, at time.Time, f map[string]string) sourced {
		fields := map[string]value.Value{}
		for k, v := range f {
			fields[k] = tx(t, v)
		}
		return sourced{src: store.Source("acme"), r: row.Row{ID: id(t, p, n), Table: table, CreatedAt: at, Fields: fields}}
	}
	imported := mk(schema.TableRules, ids.PrefixRule, 1, month1, map[string]string{"severity": "must", "source_ref": "notes/import.md:12",
		"body": "Money amounts are stored as integer cents and never as floats in any service or table."})
	all := map[row.Table][]sourced{
		schema.TableRules: {
			// the rewrite first in store order: age decides which is older
			// (findings come oldest first, then by id)
			mk(schema.TableRules, ids.PrefixRule, 2, month2, map[string]string{"severity": "must", "source_ref": imported.r.ID.String(),
				"body": "Store every money amount as integer cents in every service and table; never as floats."}),
			imported,
			mk(schema.TableRules, ids.PrefixRule, 3, month1, map[string]string{"severity": "must", "body": "Every endpoint checks the caller's role before it reads a row."}),
		},
		schema.TableHotfixes: {
			mk(schema.TableHotfixes, ids.PrefixHotfix, 4, month1, map[string]string{"severity": "high", "title": "Staging deploy needs a cache flush", "body": "After a staging deploy run the flush job."}),
			mk(schema.TableHotfixes, ids.PrefixHotfix, 5, month2, map[string]string{"severity": "high", "title": "Staging deploy needs a cache flush", "body": "Flush the cache after each staging deploy."}),
		},
		schema.TableMemories: {
			mk(schema.TableMemories, ids.PrefixMemory, 6, month1, map[string]string{"body": "The invoice export runs at two at night and takes about forty minutes on the big accounts."}),
			mk(schema.TableMemories, ids.PrefixMemory, 7, month2, map[string]string{"body": "The invoice export runs at two at night and takes about forty minutes for the big accounts."}),
			mk(schema.TableMemories, ids.PrefixMemory, 8, month2, map[string]string{"archived_at": "2026-10-02T00:00:00Z", "body": "The invoice export runs at two at night and takes about forty minutes on the big accounts."}),
		},
	}
	got := nearDuplicates(nearDupRows(all), false)
	var lines []string
	for _, f := range got {
		lines = append(lines, string(f.Code)+" "+f.ID+" | "+f.Fix)
	}
	r1, r2 := id(t, ids.PrefixRule, 1), id(t, ids.PrefixRule, 2)
	h4, h5 := id(t, ids.PrefixHotfix, 4), id(t, ids.PrefixHotfix, 5)
	m6, m7 := id(t, ids.PrefixMemory, 6), id(t, ids.PrefixMemory, 7)
	want := []string{
		"near_duplicate " + h4.String() + " | keep the newer: lore hotfix archive --id " + h4.String() + " (or archive " + h5.String() + " instead: lore hotfix archive --id " + h5.String() + ")",
		"near_duplicate " + m6.String() + " | keep the newer: lore memory archive --id " + m6.String() + " (or archive " + m7.String() + " instead: lore memory archive --id " + m7.String() + ")",
		"near_duplicate " + r1.String() + " | keep the newer: lore rule archive --id " + r1.String() + " (or archive " + r2.String() + " instead: lore rule archive --id " + r2.String() + ")",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("findings:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(got[2].Message, "rewrite of") || !strings.Contains(got[2].Message, r2.String()) {
		t.Errorf("message names neither the reason nor the newer row: %s", got[2].Message)
	}
	// the superseded-but-pinned fix is a command that runs (no `lore archive <id>`)
	for _, f := range contextFindings(map[row.Table][]sourced{schema.TableRules: {mk(schema.TableRules, ids.PrefixRule, 9, month1,
		map[string]string{"severity": "must", "body": "x", "superseded_by_id": r1.String()})}}) {
		if f.Code == SupersededPinned && f.Fix != "lore rule archive --id "+id(t, ids.PrefixRule, 9).String() {
			t.Errorf("superseded_pinned fix = %q", f.Fix)
		}
	}
}
