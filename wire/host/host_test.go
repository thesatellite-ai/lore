package host

import (
	"testing"

	"github.com/thesatellite-ai/lore/wire"
)

// TestOptionsAppliesHooksInOrder: a later hook overwrites a field an
// earlier one set, and hooks with no Options are skipped.
func TestOptionsAppliesHooksInOrder(t *testing.T) {
	var got []string
	hooks := []Hook{
		{Name: "a", Options: func(o *wire.Options) { o.Version = "a"; got = append(got, "a") }},
		{Name: "skip"},
		{Name: "b", Options: func(o *wire.Options) { o.Version += "b"; got = append(got, "b") }},
	}
	o := Options(wire.Options{RepoDir: "r"}, hooks)
	if o.Version != "ab" || o.RepoDir != "r" || len(got) != 2 {
		t.Fatalf("options = %+v, order %v", o, got)
	}
}

// TestDefaultSetsNotice: the default hooks route notices somewhere.
func TestDefaultSetsNotice(t *testing.T) {
	o := Options(wire.Options{}, Default())
	if o.Notice == nil {
		t.Fatal("Default() left Notice unset")
	}
}
