package engine

import (
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// devco T-5: an org store's own name (lore-org.yaml name:) answers for
// the name it is attached under; a store attached under that very name
// keeps it, and an unknown name stays unknown.
func TestStoreAliasNamesTheAttachedStore(t *testing.T) {
	e := &Engine{stores: map[store.Source]Attached{"initech": {}, "clientco": {}}}
	for _, o := range []Option{WithStoreAlias("clientco-notes", "initech"), WithStoreAlias("clientco", "initech")} {
		if err := o(e); err != nil {
			t.Fatal(err)
		}
	}
	for in, want := range map[store.Source]store.Source{"clientco-notes": "initech", "clientco": "clientco", "nope": "nope", "initech": "initech"} {
		if got := e.StoreName(in); got != want {
			t.Errorf("StoreName(%s) = %s, want %s", in, got, want)
		}
	}
}
