package sqlite

import (
	"runtime"
	"testing"
	"weak"

	"github.com/thesatellite-ai/lore/schema"
)

// TestRegIDKeepsNoRegistry (devco:D-845): every command builds its own
// schema registry, and regID kept its answer in a map keyed by the
// registry, so a long-lived process (the local read server) held every
// command's registry forever (~150 KB a call, ~600 MB RSS after a few
// hundred calls). Naming many registries keeps none of them but the last;
// the same tables name the same id.
func TestRegIDKeepsNoRegistry(t *testing.T) {
	const n = 20
	olds := make([]weak.Pointer[schema.Registry], 0, n)
	want := ""
	for i := range n {
		reg, err := schema.NewRegistry(schema.Builtin()...)
		if err != nil {
			t.Fatal(err)
		}
		id := regID(reg)
		if again := regID(reg); again != id {
			t.Fatalf("regID of one registry changed: %q then %q", id, again)
		}
		if i == 0 {
			want = id
		} else if id != want {
			t.Fatalf("registry %d of the same tables named %q, the first %q", i, id, want)
		}
		olds = append(olds, weak.Make(reg))
	}
	runtime.GC()
	runtime.GC()
	held := 0
	for _, w := range olds[:n-1] { // the last may stay: it is the one-entry memo
		if w.Value() != nil {
			held++
		}
	}
	if held > 0 {
		t.Fatalf("%d of %d registries regID named are still held after a collection: the memo keeps every registry", held, n-1)
	}
}
