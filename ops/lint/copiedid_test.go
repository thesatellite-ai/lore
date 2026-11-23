package lint

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestCopiedIDNamesTheStaleCopy pins devco:D-929 (bug hunt H-187): a row
// live in two stores (store adopt keeps ids) is one copied_id finding on
// the stale copy, naming both stores and the bulk move that keeps the
// newest copy; two rows that only share an id (another created_at) and a
// machine-local table read under every store name are no finding, and
// near_duplicate does not report a row against its own copy.
func TestCopiedIDNamesTheStaleCopy(t *testing.T) {
	created := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	var actor ids.ID[ids.Actor]
	mk := func(table row.Table, p ids.Prefix, n byte, created, at time.Time, body string) row.Row {
		return row.Row{Table: table, ID: id(t, p, n), CreatedAt: created, HLC: hlc.Next(hlc.HLC{}, at, actor), Fields: map[string]value.Value{fBody: tx(t, body)}}
	}
	old := mk(schema.TableMemories, ids.PrefixMemory, 1, created, created, "The invoice queue drains every ten minutes.")
	edited := mk(schema.TableMemories, ids.PrefixMemory, 1, created, created.Add(time.Hour), "The invoice queue drains every hourly batch.")
	one := mk(schema.TableMemories, ids.PrefixMemory, 2, created, created, "one row")
	other := mk(schema.TableMemories, ids.PrefixMemory, 2, created.Add(time.Minute), created, "another row")
	sess := mk(schema.TableSessions, ids.PrefixSession, 3, created, created, "")
	all := map[row.Table][]sourced{
		schema.TableMemories: {{src: store.SourceRepo, r: edited}, {src: "notes/billing", r: old}, {src: store.SourceRepo, r: one}, {src: "notes/billing", r: other}},
		schema.TableSessions: {{src: store.SourceRepo, r: sess}, {src: "notes", r: sess}},
	}
	got := copiedIDs(all)
	if len(got) != 1 {
		t.Fatalf("findings %+v, want one", got)
	}
	f := got[0]
	id := edited.ID.String()
	if f.Code != CopiedID || f.ID != id || f.Store != "notes/billing" {
		t.Fatalf("finding %+v, want copied_id %s on notes/billing", f, id)
	}
	if want := "lore row move --from notes/billing --where id=" + id + " --to repo --yes"; !strings.Contains(f.Fix, want) {
		t.Fatalf("fix %q, want %q", f.Fix, want)
	}
	if !strings.Contains(f.Message, "repo and notes/billing") {
		t.Fatalf("message %q names neither store", f.Message)
	}
	for _, nd := range nearDuplicates(nearDupRows(all), false) {
		if nd.ID == id {
			t.Fatalf("near_duplicate reports a row against its own copy: %+v", nd)
		}
	}
}
