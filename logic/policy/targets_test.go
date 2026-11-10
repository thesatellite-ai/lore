package policy

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
)

// TestContextTargets (Q-R2-3): no row means "not set" (the caller falls
// back to pointer-file detection); a repo row is the answer; a team level
// adds to it (set union, lower levels only add); an empty list counts as
// not set; a non-list value fails closed like every malformed policy row.
func TestContextTargets(t *testing.T) {
	t.Parallel()
	empty, err := value.ListOf(value.KindText)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		entries []Entry
		want    []string
		ok      bool
	}{
		{"no rows", nil, nil, false},
		{"repo row", []Entry{{Level: LevelRepo, Key: KeyContextTargets, Value: list("cursor", "codex"), Source: src(1)}},
			[]string{"codex", "cursor"}, true},
		{"folder adds", []Entry{
			{Level: LevelRepo, Key: KeyContextTargets, Value: list("codex"), Source: src(1)},
			{Level: LevelFolder, Key: KeyContextTargets, Value: list("claude_code", "codex"), Source: src(2)},
		}, []string{"claude_code", "codex"}, true},
		{"empty list", []Entry{{Level: LevelRepo, Key: KeyContextTargets, Value: empty, Source: src(1)}}, nil, false},
	}
	for _, c := range cases {
		set, rerr := Resolve(c.entries)
		if rerr != nil {
			t.Fatalf("%s: %v", c.name, rerr)
		}
		got, ok := set.ContextTargets()
		if ok != c.ok || !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v %v, want %v %v", c.name, got, ok, c.want, c.ok)
		}
	}
	if _, rerr := Resolve([]Entry{{Level: LevelRepo, Key: KeyContextTargets, Value: value.BoolOf(true), Source: src(1)}}); rerr == nil {
		t.Error("a non-list context.targets row resolved; want E_CONFIG_INVALID")
	}
}
