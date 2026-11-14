package rowfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
)

// TestBadFileSaysCauseAndNextStep (H-2, H-4, H-5): Get (show) and the read
// problems of an unusable row file name the file, the cause in the
// person's words and the next step. Before, an unreadable file failed
// show with a bare OS error (E_INTERNAL "internal error" at the CLI),
// conflict markers read as "invalid JSON", and no error carried a hint.
func TestBadFileSaysCauseAndNextStep(t *testing.T) {
	f := folder(t, openRoot(t), FolderRoot)
	g := newGen(t)
	other := newID(t)
	writeRaw(t, f, RowPath(memories, other), string(g.row(other, "kept", hlc.HLC{}).Doc))
	good := string(g.row(newID(t), "fine", hlc.HLC{}).Doc)
	cases := []struct {
		name  string
		doc   func(id string) string
		cause FileCause
		says  []string
	}{
		{"conflict markers", func(string) string {
			return "{\n<<<<<<< HEAD\n  \"a\": 1\n=======\n  \"a\": 2\n>>>>>>> origin/main\n}\n"
		},
			CauseConflict, []string{"unresolved git merge conflict", "from line 2", "lore sync conflicts", "--theirs"}},
		{"cut short", func(string) string { return good[:40] }, CauseInvalidJSON, []string{"is not valid JSON: it ends early at line", "git -C", "checkout -- "}},
		{"empty", func(string) string { return "" }, CauseInvalidJSON, []string{"the file is empty"}},
		{"bad character", func(string) string { return "{\n  id: 1\n}" }, CauseInvalidJSON, []string{"at line 2:3"}},
		{"wrong id", func(string) string { return string(g.row(newID(t), "x", hlc.HLC{}).Doc) }, CauseWrongID, []string{"not the one its file name says", "put the id back"}},
		{"duplicate id", func(string) string { return string(g.row(other, "x", hlc.HLC{}).Doc) }, CauseWrongID, []string{"holds that id too (a duplicate id)"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := newID(t)
			rel := RowPath(memories, id)
			writeRaw(t, f, rel, c.doc(id.String()))
			_, err := f.Get(memories, id)
			if err == nil || CauseOf(err) != c.cause || !errs.Has(err, errs.InvalidInput) {
				t.Fatalf("Get: %v (cause %q), want cause %q", err, CauseOf(err), c.cause)
			}
			text := err.Error() + " | " + HintOf(err)
			for _, s := range append(c.says, rel) {
				if !strings.Contains(text, s) {
					t.Fatalf("Get error %q lacks %q", text, s)
				}
			}
			if strings.Contains(err.Error(), "canon:") {
				t.Fatalf("the decoder's own text repeats the cause: %q", err.Error())
			}
			probs, perr := f.Problems(t.Context())
			if perr != nil {
				t.Fatal(perr)
			}
			found := false
			for _, p := range probs {
				if p.Path == rel {
					found = p.Cause == c.cause && p.Hint == HintOf(err)
				}
			}
			if !found {
				t.Fatalf("Problems %+v: want %s with cause %q and the hint", probs, rel, c.cause)
			}
			if err := os.Remove(filepath.Join(f.dataDir, filepath.FromSlash(rel))); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestUnreadableFileSaysChmod (H-2): a row file the OS refuses to read is
// E_BAD_PATH "permission denied" with the chmod that fixes it, not a bare
// OS error (E_INTERNAL at the CLI); reads list it with the same hint.
func TestUnreadableFileSaysChmod(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode 000 file")
	}
	f := folder(t, openRoot(t), FolderRoot)
	g := newGen(t)
	id := newID(t)
	rel := RowPath(memories, id)
	writeRaw(t, f, rel, string(g.row(id, "locked", hlc.HLC{}).Doc))
	full := filepath.Join(f.dataDir, filepath.FromSlash(rel))
	if err := os.Chmod(full, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(full, 0o600) })
	_, err := f.Get(memories, id)
	if CauseOf(err) != CauseUnreadable || !errs.Has(err, errs.BadPath) || !strings.Contains(err.Error(), "permission denied") || HintOf(err) != "make it readable again: chmod u+rw "+full {
		t.Fatalf("Get: %v / hint %q", err, HintOf(err))
	}
	if _, _, err := f.Read(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if p := f.ReadProblems(); len(p) != 1 || p[0].Cause != CauseUnreadable || !strings.Contains(p[0].Hint, "chmod u+rw") {
		t.Fatalf("ReadProblems %+v", p)
	}
}
