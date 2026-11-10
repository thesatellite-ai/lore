package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/op"
)

// TestNounGroupHelp: a group named in the noun table gets its Short and
// Long at every depth (`lore task`, `lore task time`); a group missing from
// it keeps the "lore x commands" placeholder that the cmd/lore check
// (TestEveryNounGroupHasHelp) looks for (P3-NOUNHELP-1).
func TestNounGroupHelp(t *testing.T) {
	t.Parallel()
	mk := func(name string) op.Entry {
		return op.Define(op.Spec{Name: name, Summary: "leaf " + name, Permission: op.PermRead, Access: op.AccessRead},
			func(_ context.Context, _ grpOut) (grpOut, error) { return grpOut{}, nil })
	}
	reg, err := op.NewRegistry(mk("task.time.show"), mk("zzznoun.show"))
	if err != nil {
		t.Fatal(err)
	}
	root := Build(reg, grpRunner{})
	want := nounDocs()
	cases := []struct {
		path        []string
		short, long string
	}{
		{[]string{"task"}, want["task"].Short, ""},
		{[]string{"task", "time"}, want["task time"].Short, want["task time"].Long},
		{[]string{"zzznoun"}, "lore zzznoun" + genericGroupSuffix, ""},
	}
	for _, c := range cases {
		cmd, _, err := root.Find(c.path)
		if err != nil {
			t.Fatalf("%v: %v", c.path, err)
		}
		if cmd.Short != c.short || cmd.Long != c.long {
			t.Errorf("%v: Short %q Long %q, want %q / %q", c.path, cmd.Short, cmd.Long, c.short, c.long)
		}
	}
}

// TestNounDocsWellFormed: every entry has a one-line Short that is not
// the placeholder, and keys are lower-case word paths.
func TestNounDocsWellFormed(t *testing.T) {
	t.Parallel()
	for path, d := range nounDocs() {
		if d.Short == "" || strings.Contains(d.Short, "\n") || strings.HasSuffix(d.Short, genericGroupSuffix) {
			t.Errorf("%q: bad Short %q", path, d.Short)
		}
		if strings.TrimSpace(path) != path || strings.ToLower(path) != path || strings.Contains(path, "  ") {
			t.Errorf("bad key %q", path)
		}
	}
}
