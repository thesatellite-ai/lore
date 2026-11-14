package migrate

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// foldRewriter is a fakeRewriter whose settings step writes one v1 row as
// `_v: 2` on a real run (as the entity_tags fold does), so the rewrite
// that follows no longer finds it; it also counts RecordRunActor calls.
type foldRewriter struct {
	fakeRewriter
	folded   ids.AnyID
	recorded int
}

func (f *foldRewriter) MigrateSettings(_ context.Context, dryRun bool) (Settings, error) {
	if !dryRun {
		f.v1[tblMemories] = slices.DeleteFunc(slices.Clone(f.v1[tblMemories]), func(id ids.AnyID) bool { return id == f.folded })
	}
	return Settings{
		Steps:    []string{"tags: folded 1 entity_tags link into the tag_ids of 1 row (memories 1)"},
		Rows:     1,
		Upgraded: []string{tblMemories.String() + "/" + f.folded.String()},
		Detail:   []string{"tags: " + f.folded.String() + ".tag_ids = tag_x"},
	}, nil
}

func (f *foldRewriter) RecordRunActor(context.Context) error {
	f.recorded++
	return nil
}

const tblMemories row.Table = "memories"

// TestRunCountsAFoldedRowOnce (E-2): a row the settings steps rewrite and
// the format rewrite would rewrite too is one row: the dry run and the
// real run give the same count and the same per-table lines, the
// per-row fold lines show only with --verbose, and only the real run
// records the running actor (E-3).
func TestRunCountsAFoldedRowOnce(t *testing.T) {
	t.Parallel()
	m1, err := ids.ParseAny(fixedMem)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := ids.ParseAny("mem_01a1a59b891c70b789144cd3e711fb93")
	if err != nil {
		t.Fatal(err)
	}
	newRW := func() *foldRewriter {
		return &foldRewriter{fakeRewriter: fakeRewriter{
			tables: []row.Table{tblMemories},
			v1:     map[row.Table][]ids.AnyID{tblMemories: {m1, m2}},
		}, folded: m1}
	}
	dryRW := newRW()
	dry, err := run(context.Background(), dryRW, RunIn{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	realRW := newRW()
	got, err := run(context.Background(), realRW, RunIn{})
	if err != nil {
		t.Fatal(err)
	}
	if dry.Count != 2 || got.Count != 2 {
		t.Fatalf("count: dry run %d, run %d; want 2 and 2", dry.Count, got.Count)
	}
	if !slices.Equal(dry.Rows, got.Rows) {
		t.Fatalf("rows: dry run %v, run %v; want the same", dry.Rows, got.Rows)
	}
	dryLines, realLines := RowLines(dry.Rows, false), RowLines(got.Rows, false)
	if !slices.Equal(dryLines, realLines) || len(realLines) != 1 || !strings.HasPrefix(realLines[0], "memories: 2 rows") {
		t.Fatalf("table lines: dry run %v, run %v", dryLines, realLines)
	}
	if strings.Contains(got.Text(), ".tag_ids = ") {
		t.Fatalf("text without --verbose lists the per-row fold lines:\n%s", got.Text())
	}
	got.Verbose = true
	if !strings.Contains(got.Text(), ".tag_ids = tag_x") {
		t.Fatalf("--verbose text leaves out the fold lines:\n%s", got.Text())
	}
	if dryRW.recorded != 0 || realRW.recorded != 1 {
		t.Fatalf("RecordRunActor: dry run %d, run %d; want 0 and 1", dryRW.recorded, realRW.recorded)
	}
}
