package memory

import (
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// fakeLabeler is a StoreLabel upgrade: the mapped label, else the name.
type fakeLabeler map[store.Source]string

func (f fakeLabeler) StoreLabel(name store.Source) string {
	if l, ok := f[name]; ok {
		return l
	}
	return string(name)
}

// TestLabel: the repo store (and an unset source) has no label; a store is
// "[<label>]" with the Env's label when it has the upgrade and the bare
// name when not; LabelerOf finds the upgrade only where it exists.
func TestLabel(t *testing.T) {
	lab := fakeLabeler{"northwind": "northwind/acme"}
	cases := []struct {
		name string
		l    Labeler
		src  store.Source
		want string
	}{
		{"repo, labeler", lab, store.SourceRepo, ""},
		{"repo, none", nil, store.SourceRepo, ""},
		{"empty source", lab, "", ""},
		{"folder-attached", lab, "northwind", "[northwind/acme]"},
		{"root-attached", lab, "shared", "[shared]"},
		{"no upgrade", nil, "northwind", "[northwind]"},
		{"personal", lab, "me", "[me]"},
		{"unicode name", nil, "café", "[café]"},
	}
	for _, c := range cases {
		if got := Label(c.l, c.src); got != c.want {
			t.Errorf("%s: Label = %q, want %q", c.name, got, c.want)
		}
	}
	if LabelerOf(lab) == nil {
		t.Error("LabelerOf missed the upgrade")
	}
	if LabelerOf(struct{}{}) != nil {
		t.Error("LabelerOf invented an upgrade")
	}
}

// describer is a Labeler that also describes a store (wire.App's FLAG1
// upgrade).
type describer struct{ fakeLabeler }

func (describer) DescribeSource(name store.Source) store.SourceRef {
	return store.SourceRef{Store: string(name), Folder: "acme"}
}

// TestSourceOf: FLAG1 `_source` is the zero (omitted) object for the repo store and an unset
// source (repo rows keep their bytes), the describer's object when the
// labeler has the upgrade, and the store name alone otherwise.
func TestSourceOf(t *testing.T) {
	d := describer{fakeLabeler{}}
	cases := []struct {
		name string
		l    Labeler
		src  store.Source
		want store.SourceRef
	}{
		{"repo", d, store.SourceRepo, store.SourceRef{}},
		{"empty", d, "", store.SourceRef{}},
		{"describer", d, "northwind", store.SourceRef{Store: "northwind", Folder: "acme"}},
		{"labeler only", fakeLabeler{}, "northwind", store.SourceRef{Store: "northwind"}},
		{"none", nil, "me", store.SourceRef{Store: "me"}},
	}
	for _, c := range cases {
		got := SourceOf(c.l, c.src)
		if got != c.want {
			t.Errorf("%s: SourceOf = %+v, want %+v", c.name, got, c.want)
		}
	}
}
