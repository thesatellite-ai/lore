package rowfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
)

// TestDecodeImportableMatrix: the import decode refuses a row whose text
// still holds an unresolved conflict block (merge rule 2), names the
// field, and accepts everything else: plain text, a lone marker-like line,
// text that only mentions the markers on one line. DecodeRowFile (Get)
// keeps reading the marked file.
func TestDecodeImportableMatrix(t *testing.T) {
	g := newGen(t)
	block := "<<<<<<< 0001\nmine\n=======\ntheirs\n>>>>>>> 0002"
	cases := []struct {
		name, body string
		refused    bool
	}{
		{"plain text", "use pgx", false},
		{"one marker-like line", "<<<<<<< not a block", false},
		{"markers named inline", "git writes <<<<<<< and >>>>>>> around a hunk", false},
		{"conflict block", block, true},
		{"block inside text", "before\n" + block + "\nafter", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := newID(t)
			r := g.row(id, c.body, hlc.HLC{})
			rel := RowPath(memories, id)
			_, err := decodeImportable(rel, r.Doc)
			if c.refused != (err != nil) {
				t.Fatalf("refused=%v, want %v (%v)", err != nil, c.refused, err)
			}
			if c.refused && (!errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "field body holds unresolved conflict markers")) {
				t.Fatalf("error %v", err)
			}
			if _, derr := DecodeRowFile(rel, r.Doc); derr != nil {
				t.Fatalf("Get's decode must still read it: %v", derr)
			}
		})
	}
}

// writeRaw writes doc as the row file rel of f (test setup only).
func writeRaw(t *testing.T, f *Folder, rel, doc string) {
	t.Helper()
	full := filepath.Join(f.dataDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Clean(full), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestReadProblemsAreComplete: Read leaves an unusable file (not JSON, a
// conflict block) out and lists it in ReadProblems, on every Read (a
// broken file never enters the cursor), and the list empties once the
// file is fixed.
func TestReadProblemsAreComplete(t *testing.T) {
	ctx := t.Context()
	f := folder(t, openRoot(t), FolderRoot)
	g := newGen(t)
	good, broken, marked := newID(t), newID(t), newID(t)
	writeRaw(t, f, RowPath(memories, good), string(g.row(good, "fine", hlc.HLC{}).Doc))
	writeRaw(t, f, RowPath(memories, broken), "{broken")
	writeRaw(t, f, RowPath(memories, marked), string(g.row(marked, "<<<<<<< a\nx\n=======\ny\n>>>>>>> b", hlc.HLC{}).Doc))
	rows, cur, err := f.Read(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != good {
		t.Fatalf("Read returned %d rows, want only the good one", len(rows))
	}
	want := func() {
		t.Helper()
		got := f.ReadProblems()
		if len(got) != 2 {
			t.Fatalf("problems %+v, want the broken and the marked file", got)
		}
		paths := got[0].Path + " " + got[1].Path
		if !strings.Contains(paths, broken.String()) || !strings.Contains(paths, marked.String()) {
			t.Fatalf("problems %+v", got)
		}
	}
	want()
	if _, _, err = f.Read(ctx, cur); err != nil { // incremental: still listed
		t.Fatal(err)
	}
	want()
	writeRaw(t, f, RowPath(memories, broken), string(g.row(broken, "fixed", hlc.HLC{}).Doc))
	writeRaw(t, f, RowPath(memories, marked), string(g.row(marked, "resolved", hlc.HLC{}).Doc))
	if rows, _, err = f.Read(ctx, cur); err != nil || len(rows) != 2 || len(f.ReadProblems()) != 0 {
		t.Fatalf("after the fix: %d rows, problems %+v, %v", len(rows), f.ReadProblems(), err)
	}
}
