package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// TestReadKeepingLive (B1-23): a reader without the live overlay (the
// commit hook's leak check) reads a live cursor's store part and hands its
// live part back, so the next live run does not re-read and re-apply every
// row; any other cursor is the backend's own.
func TestReadKeepingLive(t *testing.T) {
	t.Parallel()
	b := memstore.NewData().Open(store.OpenOptions{})
	_, plain, err := b.Read(background(), "")
	if err != nil {
		t.Fatal(err)
	}
	live := store.Cursor(LiveCursorPrefix + "0247a0c7" + LiveCursorSep + string(plain))
	_, next, err := ReadKeepingLive(background(), b, live)
	if err != nil || !strings.HasPrefix(string(next), LiveCursorPrefix+"0247a0c7"+LiveCursorSep) {
		t.Fatalf("a live cursor lost its live part: %q %v", next, err)
	}
	if _, next, err = ReadKeepingLive(background(), b, plain); err != nil || strings.HasPrefix(string(next), LiveCursorPrefix) {
		t.Fatalf("a plain cursor must stay the backend's own: %q %v", next, err)
	}
}
