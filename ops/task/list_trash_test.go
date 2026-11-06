package task_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestListTrashFlags (OV46/OV42, DELETION 3.2): task list hides trashed
// tasks; --include-deleted adds them marked (trashed in --json, a mark in
// the text); --deleted-only lists only them and wins over
// --include-deleted; a sub-task trashed with its parent comes along; the
// flags combine with a status filter; nothing trashed lists nothing for
// --deleted-only.
func TestListTrashFlags(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	empty := listIDs(t, h, context.Background(), `{"deleted_only":true}`)
	if len(empty) != 0 {
		t.Fatalf("--deleted-only with an empty trash: %v", empty)
	}
	live := add(h, "live", "")
	gone := add(h, "gone", "")
	parent := add(h, "parent", "")
	child := add(h, "child", `"parent":"`+parent+`"`)
	done := add(h, "done then trashed", "")
	h.Must(opstask.OpDone, `{"id":"`+done+`","no_lesson":true}`)
	h.MustYes(opstask.OpDelete, `{"id":"`+gone+`"}`)
	h.MustYes(opstask.OpDelete, `{"id":"`+parent+`"}`)
	h.MustYes(opstask.OpDelete, `{"id":"`+done+`"}`)

	sorted := func(s ...string) []string { slices.Sort(s); return s }
	cases := []struct {
		in   string
		want []string
	}{
		{`{"all":true}`, sorted(live)},
		{`{"all":true,"include_deleted":true}`, sorted(live, gone, parent, child, done)},
		{`{"all":true,"deleted_only":true}`, sorted(gone, parent, child, done)},
		{`{"all":true,"deleted_only":true,"include_deleted":true}`, sorted(gone, parent, child, done)},
		{`{"status":"done","include_deleted":true}`, sorted(done)},
		{`{"status":"done"}`, nil},
	}
	for _, c := range cases {
		if got := listIDs(t, h, context.Background(), c.in); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v want %v", c.in, got, c.want)
		}
	}
	txt := opstest.Text(h.Must(opstask.OpList, `{"all":true,"include_deleted":true}`))
	if !strings.Contains(txt, gone) || !strings.Contains(txt, "trash") {
		t.Fatalf("--include-deleted text does not mark the trashed task: %q", txt)
	}
}
