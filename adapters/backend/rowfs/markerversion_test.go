package rowfs

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestMarkerDeclaresPlainLayout (brief 1.6, D-ad): a marker this lore
// writes says lore_store 4, the plain store layout with the root folder's
// rows in _store/; ones of earlier 0.1.18 builds (2, 3) still read; a lore
// that knows only 3 refuses 4 the way this one refuses 5 (E_OLD_LORE),
// instead of writing rows into the old place.
func TestMarkerDeclaresPlainLayout(t *testing.T) {
	m, err := NewMarker(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "lore_store: 4\n") {
		t.Fatalf("new marker:\n%s", b)
	}
	old, err := ParseMarker([]byte("lore_store: 2\nid: " + m.ID.String() + "\n"))
	if err != nil || old.Version != MarkerFirstStores {
		t.Fatalf("an earlier build's marker: %+v %v", old, err)
	}
	if flat, err := ParseMarker([]byte("lore_store: 3\nid: " + m.ID.String() + "\n")); err != nil || flat.Version != 3 {
		t.Fatalf("a first-plain-layout marker: %+v %v", flat, err)
	}
	if _, err := ParseMarker([]byte("lore_store: 5\n")); errs.CodeOf(err) != errs.OldLore {
		t.Fatalf("a newer marker: %v, want E_OLD_LORE", err)
	}
}
