package wire

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// TestWriteLogNamesWhatTheCommitHoldsD691 (devco D-691): a write said
// "wrote 4 row file(s)" over a commit of 1 file. The rows written are named
// only when a commit of the command holds them (rows rewritten with the
// same bytes are not in it), a file the commit added beside them (a
// folder's _meta.json, a row another path committed) is named too, and a
// store that commits nothing keeps its rows.
func TestWriteLogNamesWhatTheCommitHoldsD691(t *testing.T) {
	t.Parallel()
	top := t.TempDir()
	row := func(rel string) string {
		p := filepath.Join(top, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	changed := row("shop/memories/mem_01a1f224846271dd966c17eae2a1dba5.json")
	var same []string
	for _, rel := range []string{"shop/projects/prj_89e9a23c206676229701503f48c99079.json", "shop/repos/rep_89e9a23c206676229701503f48c99079.json", "shop/task_lists/tlt_71ca48d9d21f73eaab81d2b768763868.json"} {
		same = append(same, row(rel))
	}
	elsewhere := filepath.Join(t.TempDir(), "memories", "mem_01a1f2239beb786681502ac74b87ec31.json")

	ctx, l := WithWriteLog(context.Background())
	l.add(Written{Store: "acme/shop", File: changed})
	for _, f := range same {
		l.add(Written{Store: "acme/shop", File: f})
	}
	l.add(Written{Store: "repo", File: elsewhere})
	store.NoteCommitted(ctx, top, []string{"shop/memories/mem_01a1f224846271dd966c17eae2a1dba5.json", "shop/_meta.json", "shop/memories/mem_01a1f2239beb786681502ac74b87ec31.json"})

	var got []string
	for _, w := range l.Files() {
		got = append(got, w.Store+" "+w.File)
	}
	want := []string{
		"acme/shop " + changed,
		"repo " + elsewhere,
		"acme/shop " + filepath.Join(top, "shop", "_meta.json"),
		"acme/shop " + filepath.Join(top, "shop", "memories", "mem_01a1f2239beb786681502ac74b87ec31.json"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("files =\n%q\nwant\n%q", got, want)
	}
}

// TestWrittenLinesNamesExtrasD691: past WrittenShowAll the rows are counted
// per folder, and a file a commit holds beside them (a folder's _meta.json)
// is still named on its own line, never counted as a row file, and one the
// commit took away says "removed".
func TestWrittenLinesNamesExtrasD691(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "_meta.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	var files []Written
	for _, id := range []string{"a", "b", "c", "d"} {
		files = append(files, Written{Store: "acme/shop", File: filepath.Join(dir, "memories", "mem_"+id+".json")})
	}
	files = append(files, Written{Store: "acme/shop", File: filepath.Join(dir, "_meta.json"), extra: true})
	gone := filepath.Join(dir, "notes", "mem_e.json") // a moved row's old place
	files = append(files, Written{Store: "acme/shop", File: gone, extra: true})
	want := []string{
		"lore: wrote 4 row file(s) in " + dir + " (store acme/shop)",
		"lore: wrote " + filepath.Join(dir, "_meta.json") + " (store acme/shop)",
		"lore: removed " + gone + " (store acme/shop)",
	}
	if got := WrittenLines(files); !slices.Equal(got, want) {
		t.Fatalf("lines =\n%q\nwant\n%q", got, want)
	}
}
