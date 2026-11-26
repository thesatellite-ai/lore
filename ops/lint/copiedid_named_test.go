package lint

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// namedEnv is an Env whose stores but the repo name their rows
// (rowNamer); it counts the rows each read returns.
type namedEnv struct {
	Env
	rows   map[store.Source][]row.Row
	order  []store.Source
	listed map[store.Source]int // rows List returned
	byID   int                  // rows ListIDs returned
}

func (e *namedEnv) Stores() []store.Source { return e.order }

func (e *namedEnv) List(_ context.Context, src store.Source, t row.Table) ([]row.Row, error) {
	var out []row.Row
	for _, r := range e.rows[src] {
		if r.Table == t {
			out = append(out, r)
		}
	}
	e.listed[src] += len(out)
	return out, nil
}

func (e *namedEnv) RowNames(_ context.Context, src store.Source) ([]store.RowName, bool, error) {
	if src == store.SourceRepo {
		return nil, false, nil // a code repo's folder: listed
	}
	var out []store.RowName
	for _, r := range e.rows[src] {
		out = append(out, store.RowName{Table: r.Table, ID: r.ID})
	}
	return out, true, nil
}

func (e *namedEnv) ListIDs(_ context.Context, src store.Source, t row.Table, want []ids.AnyID) ([]row.Row, error) {
	var out []row.Row
	for _, r := range e.rows[src] {
		if r.Table == t && slices.Contains(want, r.ID) {
			out = append(out, r)
		}
	}
	e.byID += len(out)
	return out, nil
}

// TestCopiedIDReadsOnlySharedIDs (bug hunt H-276, devco:D-1014): with
// every store's row files named, copied_id reads only the rows whose id
// two stores hold, and finds what reading every row finds. lore doctor
// read every row of every folder of a store attached whole for it: 67 s
// and 2 GB at 100k commits. A store that cannot name its rows (a code
// repo's folder) is still listed whole.
func TestCopiedIDReadsOnlySharedIDs(t *testing.T) {
	created := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	var actor ids.ID[ids.Actor]
	mk := func(n int, at time.Time, body string) row.Row {
		mid, err := ids.ParseAny(fmt.Sprintf("%s_019a0000000070008000%012x", ids.PrefixMemory, n))
		if err != nil {
			t.Fatal(err)
		}
		return row.Row{Table: schema.TableMemories, ID: mid, CreatedAt: created, HLC: hlc.Next(hlc.HLC{}, at, actor), Fields: map[string]value.Value{fBody: tx(t, fmt.Sprintf("%s %d", body, n))}}
	}
	env := &namedEnv{order: []store.Source{store.SourceRepo, "notes/billing", "notes/ops"}, rows: map[store.Source][]row.Row{}, listed: map[store.Source]int{}}
	env.rows[store.SourceRepo] = []row.Row{mk(1, created.Add(time.Hour), "edited in the repo")}
	env.rows["notes/billing"] = []row.Row{mk(1, created, "the adopted copy"), mk(2, created, "billing and ops")}
	env.rows["notes/ops"] = []row.Row{mk(2, created.Add(time.Hour), "billing and ops")}
	for n := 10; n < 1010; n++ { // rows no other store holds
		env.rows["notes/ops"] = append(env.rows["notes/ops"], mk(n, created, "an ops note"))
	}
	all := map[row.Table][]sourced{}
	for _, src := range env.order {
		for _, r := range env.rows[src] {
			all[r.Table] = append(all[r.Table], sourced{src: src, r: r})
		}
	}
	want := copiedIDs(all)
	if len(want) != 2 {
		t.Fatalf("reading every row: %d findings, want 2 (the fixture lost its point)", len(want))
	}
	got, err := FindCopiedIDs(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("findings\n%v\nwant (reading every row)\n%v", got, want)
	}
	if n := env.listed["notes/billing"] + env.listed["notes/ops"]; n != 0 {
		t.Fatalf("copied_id listed %d rows of stores that name theirs, want 0", n)
	}
	if env.byID != 3 {
		t.Fatalf("copied_id read %d rows by id, want the 3 copies two stores hold", env.byID)
	}
	if env.listed[store.SourceRepo] != 1 {
		t.Fatalf("the repo store (no names) listed %d rows, want its 1", env.listed[store.SourceRepo])
	}
}
