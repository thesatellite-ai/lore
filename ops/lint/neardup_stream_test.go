package lint

// neardup_stream_test.go — devco:D-1016 (bug hunt H-278): lore doctor's
// near_duplicate check held every scanned row decoded for the whole check
// (111 MB live at 20k notes). It now reads one store's table at a time and
// keeps each row as its id, version and word numbers.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/neardup"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// listEnv is an Env that lists rows per store and table and nothing else.
type listEnv struct {
	Env
	rows  map[store.Source]map[row.Table][]row.Row
	lists *int
}

func (e listEnv) Stores() []store.Source { return []store.Source{"acme", "acme/app"} }

func (e listEnv) List(_ context.Context, src store.Source, t row.Table) ([]row.Row, error) {
	*e.lists++
	return e.rows[src][t], nil
}

// TestFindNearDuplicatesStreamsRows: the doctor's near_duplicate check
// lists each scanned table of each store once, keeps no row text (no
// fields, no body in its docs) and still reports today's pairs across
// stores, on the older row.
func TestFindNearDuplicatesStreamsRows(t *testing.T) {
	day := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	mk := func(table row.Table, p ids.Prefix, n byte, at time.Time, body string) row.Row {
		return row.Row{ID: id(t, p, n), Table: table, CreatedAt: at, Fields: map[string]value.Value{"body": tx(t, body)}}
	}
	old := mk(schema.TableMemories, ids.PrefixMemory, 1, day, "The invoice export runs at two at night and takes about forty minutes on the big accounts.")
	newer := mk(schema.TableMemories, ids.PrefixMemory, 2, day.AddDate(0, 1, 0), "The invoice export runs at two at night and takes about forty minutes for the big accounts.")
	other := mk(schema.TableRules, ids.PrefixRule, 3, day, "Every endpoint checks the caller's role before it reads a row.")
	lists := 0
	env := listEnv{lists: &lists, rows: map[store.Source]map[row.Table][]row.Row{
		"acme":     {schema.TableMemories: {old}, schema.TableRules: {other}},
		"acme/app": {schema.TableMemories: {newer}},
	}}
	got, err := FindNearDuplicates(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != old.ID.String() || got[0].Store != "acme" || !strings.Contains(got[0].Message, newer.ID.String()) {
		t.Fatalf("findings %+v, want one on %s naming %s", got, old.ID, newer.ID)
	}
	if want := 2 * len(archiveNouns); lists != want {
		t.Errorf("%d lists, want %d (each scanned table of each store once)", lists, want)
	}
	v := newNDRow(neardup.NewVocab(), sourced{src: "acme", r: old})
	if v.r.Fields != nil || v.r.Meta != nil || v.doc.Body != "" {
		t.Errorf("a prepared row keeps its text: fields %v, body %q", v.r.Fields, v.doc.Body)
	}
	if v.r.ID != old.ID || !v.r.CreatedAt.Equal(old.CreatedAt) || v.r.Table != old.Table {
		t.Errorf("a prepared row lost what ordering and copies read: %+v", v.r)
	}
}
