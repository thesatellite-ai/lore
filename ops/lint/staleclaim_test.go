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

// TestStaleClaimFinding is bug hunt H-289 (devco:D-1027): lint reports a
// live row saying the retry queue is planned when a newer live row says
// it shipped, once, on the older row, naming the newer one, with a fix
// that archives the older row by its own kind's verb; an archived planned
// row, a superseded one and a planned row nothing contradicts are not
// reported.
func TestStaleClaimFinding(t *testing.T) {
	d1 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	d2 := d1.AddDate(0, 0, 19)
	mk := func(p ids.Prefix, n byte, at time.Time, f map[string]string) sourced {
		fields := map[string]value.Value{}
		for k, v := range f {
			fields[k] = tx(t, v)
		}
		return sourced{src: store.Source("acme"), r: row.Row{ID: id(t, p, n), Table: schema.TableMemories, CreatedAt: at, Fields: fields}}
	}
	all := map[row.Table][]sourced{
		schema.TableMemories: {
			mk(ids.PrefixMemory, 1, d1, map[string]string{"body": "Planned: a retry queue for failed webhook deliveries (not built yet)."}),
			mk(ids.PrefixMemory, 2, d2, map[string]string{"body": "The retry queue for webhook deliveries shipped in v0.4."}),
			mk(ids.PrefixMemory, 3, d1, map[string]string{"body": "Planned: a retry queue for webhook deliveries.", "archived_at": "2026-03-02T00:00:00Z"}),
			mk(ids.PrefixMemory, 4, d1, map[string]string{"body": "Planned: the retry queue for webhook deliveries.", "superseded_by_id": id(t, ids.PrefixMemory, 2).String()}),
			mk(ids.PrefixMemory, 5, d1, map[string]string{"body": "Planned: CSV export for the billing page."}),
		},
	}
	got := staleClaims(all, d2.AddDate(0, 0, 5))
	m1, m2 := id(t, ids.PrefixMemory, 1).String(), id(t, ids.PrefixMemory, 2).String()
	if len(got) != 1 || got[0].Code != StaleClaim || got[0].ID != m1 || got[0].Store != "acme" {
		t.Fatalf("want one stale_claim on %s, got %+v", m1, got)
	}
	if !strings.Contains(got[0].Message, "the newer "+m2+" (acme, 2026-03-20) says it is done") || !strings.Contains(got[0].Message, "lore show --id "+m2) {
		t.Fatalf("message: %s", got[0].Message)
	}
	if want := "when the newer row holds: lore memory archive --id " + m1; got[0].Fix != want {
		t.Fatalf("fix = %q, want %q", got[0].Fix, want)
	}
	// an architecture note is scanned too, its fix by its own noun
	a1 := sourced{src: store.Source("acme"), r: row.Row{ID: id(t, ids.PrefixArchitectureNote, 9), Table: schema.TableArchitectureNotes, CreatedAt: d1,
		Fields: map[string]value.Value{"title": tx(t, "Widget catalog"), "body": tx(t, "There is no app-facing widget catalog read yet (the catalog read is planned).")}}}
	m6 := mk(ids.PrefixMemory, 6, d2, map[string]string{"body": "The app-facing widget catalog read shipped."})
	got = staleClaims(map[row.Table][]sourced{schema.TableArchitectureNotes: {a1}, schema.TableMemories: {m6}}, d2.AddDate(0, 0, 5))
	if len(got) != 1 || got[0].ID != a1.r.ID.String() || got[0].Fix != "when the newer row holds: lore architecturenote archive --id "+a1.r.ID.String() {
		t.Fatalf("architecture note: %+v", got)
	}
}
