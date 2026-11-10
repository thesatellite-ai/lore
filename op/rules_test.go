package op

import (
	"context"
	"strings"
	"testing"
)

type byString struct {
	By string `flag:"by" json:"by,omitempty"`
}

type byActor struct {
	By      ActorRef `flag:"by" json:"by,omitempty"`
	GroupBy string   `flag:"group-by" json:"group_by,omitempty"`
}

type noIn struct{}

type noOut struct{}

func defineWith[In any](name string) Entry {
	return Define(Spec{Name: name, Summary: "x", Permission: PermRead, Access: AccessRead},
		func(context.Context, In) (noOut, error) { return noOut{}, nil })
}

// TestWordRules: NewRegistry refuses a non-actor --by (XQ8) and a
// root-level import/export command (XQ9); the allowed shapes pass.
func TestWordRules(t *testing.T) {
	cases := []struct {
		name  string
		entry Entry
		want  string // "" = accepted
	}{
		{"by as text", defineWith[byString]("task.time"), "flag --by must name an actor (Go type op.ActorRef, XQ8); for grouping use --group-by"},
		{"by as actor", defineWith[byActor]("task.time"), ""},
		{"root import", defineWith[noIn]("import.github"), "no root-level `lore import` (XQ9)"},
		{"root export", defineWith[noIn]("export.csv"), "no root-level `lore export` (XQ9)"},
		{"noun import", defineWith[noIn]("task.import"), ""},
		{"noun export", defineWith[noIn]("resource.export"), ""},
		{"importer noun", defineWith[noIn]("importer.list"), ""},
	}
	for _, c := range cases {
		_, err := NewRegistry(c.entry)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: refused: %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
	if f := defineWith[byActor]("task.time").InputFields(); !f[0].Actor || f[1].Actor {
		t.Errorf("Actor flags: %+v", f)
	}
}
