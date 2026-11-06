package task_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestSplitInheritedLine (TASK 6.5, P3-TASK-1): the split's "inherited:"
// line names a parent value only when some sub-task takes it; a value
// every child sets for itself is not listed (the tasklist the test repo
// gives every task is always inherited).
func TestSplitInheritedLine(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false))
	parent := add(h, "parent", `"priority":"high","commitment":"accepted"`)
	cases := []struct {
		name, children string
		want, absent   []string
	}{
		{"all own", `[{"title":"a","priority":"low","commitment":"proposed"},{"title":"b","priority":"urgent","commitment":"proposed"}]`,
			[]string{"inherited: tasklist "}, []string{"priority high", "commitment accepted"}},
		{"priority own only", `[{"title":"a","priority":"low"}]`,
			[]string{", commitment accepted\n"}, []string{"priority high"}},
		{"mixed", `[{"title":"a","priority":"low"},{"title":"b"}]`,
			[]string{", priority high, commitment accepted\n"}, nil},
		{"none own", `[{"title":"a"}]`,
			[]string{", priority high, commitment accepted\n"}, nil},
	}
	for _, c := range cases {
		res := h.Must(opstask.OpSplit, `{"id":"`+parent+`","dry_run":true,"children":`+c.children+`}`)
		text, js := opstest.Text(res), h.JSON(res)
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s: text %q lacks %q", c.name, text, w)
			}
		}
		for _, a := range c.absent {
			if strings.Contains(text, a) {
				t.Errorf("%s: text %q has %q", c.name, text, a)
			}
		}
		if c.name == "all own" && strings.Contains(js, `"priority":"high"`) {
			t.Errorf("%s: --json inherited still names priority high: %s", c.name, js)
		}
	}
}
