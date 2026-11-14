package rowfs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestIndexedReadNamesMisnamedRow (devco Q-12): after a case-only rename
// (mem_… → MEM_…) the indexed read (git's index view, the hot path in a
// repo) left the file out without a word, so `memory list` showed a
// silent gap while only a full scan (doctor) named it. Every read now
// lists it in ReadProblems, with the lowercase name as the next step,
// whether git reports the file clean or to check.
func TestIndexedReadNamesMisnamedRow(t *testing.T) {
	t.Parallel()
	r, g, ctx := openRoot(t, WithLayout(LayoutRepo)), newGen(t), context.Background()
	f := folder(t, r, FolderRoot)
	a := g.row(newID(t), "a", hlc.HLC{})
	apply(t, f, store.Change{Row: a})
	low := RowPath(memories, a.ID)
	table, name, _ := strings.Cut(low, "/")
	up := table + "/" + strings.ToUpper(name[:len(name)-len(".json")]) + ".json"
	if err := os.Rename(filepath.Join(f.dataDir, filepath.FromSlash(low)), filepath.Join(f.dataDir, filepath.FromSlash(up))); err != nil {
		t.Fatal(err)
	}
	for _, view := range []IndexView{
		{Clean: map[string]string{up: "blob-up"}},
		{Clean: map[string]string{}, Check: []string{up}},
	} {
		f.SetIndexSource(func(context.Context, string) (IndexView, bool) { return view, true })
		rows, _, err := f.Read(ctx, "")
		if err != nil || len(rows) != 0 {
			t.Fatalf("read = %d rows, %v; want the misnamed file left out", len(rows), err)
		}
		p := f.ReadProblems()
		if len(p) != 1 || p[0].Path != up || !strings.Contains(p[0].Reason, "lowercase ids") || !strings.Contains(p[0].Hint, name) {
			t.Fatalf("view %+v: problems = %+v, want %s named with %s as the next step", view, p, up, name)
		}
	}
}
