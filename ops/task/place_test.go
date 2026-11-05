package task_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// childOrder is the titles of parent's direct sub-tasks in show --tree
// order (rank, TASK 6.3).
func childOrder(t *testing.T, h *opstest.H, parent string) string {
	t.Helper()
	sh, ok := h.Must(opstask.OpShow, `{"id":"`+parent+`","tree":true}`).Value().Interface().(opstask.ShowOut)
	if !ok {
		t.Fatal("show result type")
	}
	var out []string
	for _, l := range sh.Data.Tree {
		if l.Depth == 1 {
			out = append(out, l.Title)
		}
	}
	return strings.Join(out, ",")
}

// TestPlaceAmongSiblings (TASK 6.3, 6.15): edit --first, --last, --before
// and --after-sibling reorder one task among its siblings and nothing
// else; a long run of moves keeps a total order (ranks never collide);
// two placement flags, an anchor that is not a sibling (another parent's
// child, the task itself) are E_INVALID_INPUT with nothing written.
func TestPlaceAmongSiblings(t *testing.T) {
	h := opstest.New(t, repoClient(false))
	p := add(h, "parent", "")
	id := map[string]string{}
	for _, n := range []string{"a", "b", "c"} {
		id[n] = add(h, n, `"parent":"`+p+`"`)
	}
	other := add(h, "other parent", "")
	stranger := add(h, "x", `"parent":"`+other+`"`)
	if got := childOrder(t, h, p); got != "a,b,c" {
		t.Fatalf("creation order %s", got)
	}
	steps := []struct{ task, flag, want string }{
		{"c", `"first":true`, "c,a,b"},
		{"c", `"first":true`, "c,a,b"}, // already first: same order
		{"c", `"last":true`, "a,b,c"},
		{"a", `"last":true`, "b,c,a"},
		{"a", `"before":"` + id["b"] + `"`, "a,b,c"},
		{"c", `"before":"` + id["b"] + `"`, "a,c,b"},
		{"a", `"after_sibling":"` + id["b"] + `"`, "c,b,a"},
		{"c", `"after_sibling":"` + id["b"] + `"`, "b,c,a"},
		{"a", `"after_sibling":"` + id["b"] + `"`, "b,a,c"},
	}
	for _, s := range steps {
		h.Must(opstask.OpEdit, `{"id":"`+id[s.task]+`",`+s.flag+`}`)
		if got := childOrder(t, h, p); got != s.want {
			t.Fatalf("edit %s %s: order %s, want %s", s.task, s.flag, got, s.want)
		}
	}
	// Squeeze the same gap again and again: every new rank sits strictly
	// between its neighbours.
	for i := 0; i < 20; i++ {
		mover, anchor := "c", "a"
		if i%2 == 1 {
			mover, anchor = "a", "c"
		}
		h.Must(opstask.OpEdit, `{"id":"`+id[mover]+`","before":"`+id[anchor]+`"}`)
	}
	if got := childOrder(t, h, p); got != "b,a,c" {
		t.Fatalf("after 20 squeezes: %s, want b,a,c", got)
	}
	before := childOrder(t, h, p)
	for _, bad := range []string{
		`"first":true,"last":true`,
		`"before":"` + id["b"] + `","after_sibling":"` + id["c"] + `"`,
		`"before":"` + stranger + `"`,
		`"after_sibling":"` + id["a"] + `"`, // the task itself
	} {
		h.Fails(opstask.OpEdit, `{"id":"`+id["a"]+`",`+bad+`}`, errs.InvalidInput)
		if got := childOrder(t, h, p); got != before {
			t.Fatalf("refused edit %s reordered: %s", bad, got)
		}
	}
}
