package task_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestSplitWritesOneBatch (run:plat:split-perf): task split stages every
// child and writes them as ONE store batch (one live commit, see journey
// TestJourneySplitOneCommit), so a split is all or none: a child the
// engine refuses (here a secret in its body) leaves no child at all, where
// each child was once its own write and the ones before it stayed. The
// children keep the order and ranks of separate adds, and a parent with
// no task list gets ONE default list shared by every child, not one each
// (staged rows are not read back, so the split names the first child's
// list to the rest).
func TestSplitWritesOneBatch(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	// the parent starts on a named list and leaves it: no default list
	// exists yet, so the split's first child creates it in the batch
	sprint := h.ID(h.Must("tasklist.add", `{"title":"Sprint"}`))
	parent := add(h, "parent", `"tasklist":"`+sprint+`"`)
	h.Must(opstask.OpEdit, `{"id":"`+parent+`","clear_tasklist":true}`)
	lists := dataFiles(t, h, schema.TableTasklists)

	// The fixture key is built at run time (no key-shaped literal in the tree).
	secret := "AKIA" + strings.Repeat("Q", 4) + "7XRT2MNB5KZ3"
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("- one\n- two\n- title: three\n  body: key "+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Run(opstask.OpSplit, `{"id":"`+parent+`","from":`+opstest.Quote(bad)+`}`); err == nil {
		t.Fatal("a split with a secret in a child's body: want a refusal")
	}
	if got := children(h, parent); got != 0 {
		t.Fatalf("a refused split wrote %d children, want 0 (one batch, all or none)", got)
	}
	if got := dataFiles(t, h, schema.TableTasklists); got != lists {
		t.Fatalf("a refused split left %d task list(s)", got-lists)
	}

	h.Must(opstask.OpSplit, `{"id":"`+parent+`","titles":["a","b","c","d"]}`)
	if got := dataFiles(t, h, schema.TableTasklists); got != lists+1 {
		t.Fatalf("split under a parent with no list made %d lists, want 1", got-lists)
	}
	var kids []struct{ title, rank, list string }
	for _, r := range liveChildren(h, parent) {
		kids = append(kids, struct{ title, rank, list string }{field(r, "title"), field(r, "rank"), field(r, "tasklist_id")})
	}
	if len(kids) != 4 {
		t.Fatalf("split wrote %d children, want 4", len(kids))
	}
	for i := range kids {
		if kids[i].list == "" || kids[i].list != kids[0].list {
			t.Errorf("child %q on list %q, want every child on %q", kids[i].title, kids[i].list, kids[0].list)
		}
		if i > 0 && kids[i].rank <= kids[i-1].rank {
			t.Errorf("child %q rank %q is not after %q's %q", kids[i].title, kids[i].rank, kids[i-1].title, kids[i-1].rank)
		}
	}
}

// TestSplitRerunWithKeyFitsWidth (run:plat:split-perf): a re-run of a
// keyed split whose children are all there adds nothing, even when the
// children fill more than half of tasks.max_children: the width check
// counts only the pieces the run will add (it once counted every piece
// again: E_TREE_WIDTH "would have 2n sub-tasks").
func TestSplitRerunWithKeyFitsWidth(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpPolicySet, `{"key":"tasks.max_children","value":"3"}`)
	parent := add(h, "parent", "")
	in := `{"id":"` + parent + `","key":"plan","titles":["a","b"]}`
	h.Must(opstask.OpSplit, in)
	h.Must(opstask.OpSplit, in)
	if got := children(h, parent); got != 2 {
		t.Fatalf("a keyed re-run: %d children, want 2", got)
	}
	// a third piece fits; a fourth one does not
	h.Must(opstask.OpSplit, `{"id":"`+parent+`","key":"plan","titles":["a","b","c"]}`)
	h.Fails(opstask.OpSplit, `{"id":"`+parent+`","key":"plan","titles":["a","b","c","d"]}`, errs.TreeWidth)
	if got := children(h, parent); got != 3 {
		t.Fatalf("after the refused split: %d children, want 3", got)
	}
}

// liveChildren is the live tasks whose parent is id, in rank order.
func liveChildren(h *opstest.H, id string) []row.Row {
	h.T.Helper()
	rows, err := h.App.List(h.T.Context(), store.SourceRepo, schema.TableTasks)
	if err != nil {
		h.T.Fatal(err)
	}
	var out []row.Row
	for _, r := range rows {
		if !r.Deleted && field(r, "parent_id") == id {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b row.Row) int { return strings.Compare(field(a, "rank"), field(b, "rank")) })
	return out
}
