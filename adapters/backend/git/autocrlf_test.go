package git

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestRowsKeepTheirBytesUnderAutocrlf: a person whose git converts line
// endings (core.autocrlf=true, Git for Windows' default) still reads every
// row exactly as it was written, through a second clone too. Before, the
// clone's checkout turned the row files' LF into CRLF, so the second clone
// read different bytes: rows did not converge and a clone "lost its own
// edit" on Windows. Not parallel: it points GIT_CONFIG_GLOBAL at a config
// with autocrlf on, which git honours on every system.
func TestRowsKeepTheirBytesUnderAutocrlf(t *testing.T) {
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[core]\n\tautocrlf = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	b := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	x := g.row(newID(t), "line one\nline two", hlc.HLC{})
	write(t, a, store.Change{Row: x})
	syncOK(t, a)
	syncOK(t, b)
	if got := get(t, b, x.ID); !bytes.Equal(got.Doc, x.Doc) {
		t.Fatalf("the second clone reads other bytes:\n got %q\nwant %q", got.Doc, x.Doc)
	}
	if got := get(t, a, x.ID); !bytes.Equal(got.Doc, x.Doc) {
		t.Fatalf("the writing clone reads other bytes:\n got %q\nwant %q", got.Doc, x.Doc)
	}
}
