package row_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/row"
)

// moveOut decodes a row.move result.
func moveOut(t *testing.T, h *opstest.H, js string) row.MoveOut {
	t.Helper()
	var out row.MoveOut
	if err := json.Unmarshal([]byte(js), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestMoveIDListsNearDuplicates (devco D-854, owner org-setup item 13): a
// one-row `lore move --id` into a store that already holds a rule of the
// same name lists the pair as the bulk move does (both ids, the read of
// both, the delete that drops one); both rules stay. A row with nothing
// like it in --to lists none. Break-verification: without the
// oneNearDuplicates call the move listed nothing and moved silently.
func TestMoveIDListsNearDuplicates(t *testing.T) {
	h := opstest.WithStores(t, []string{"me"})
	curated := h.ID(h.Must("rule.add", `{"body":"Use portless for dev URLs.","to":"me"}`))
	copied := h.ID(h.Must("rule.add", `{"body":"use portless for dev urls\nso every dev server has a stable name"}`))
	other := h.ID(h.Must("rule.add", `{"body":"Run the linter before every push"}`))

	res := h.MustYes(row.OpMove, `{"id":"`+copied+`","to":"me"}`)
	out := moveOut(t, h, h.JSON(res))
	if len(out.Duplicates) != 1 || out.Duplicates[0].ID != copied || out.Duplicates[0].Existing != curated || out.Duplicates[0].Why != "same title" {
		t.Fatalf("move --id duplicates: %s", h.JSON(res))
	}
	txt := opstest.Text(res)
	for _, want := range []string{
		"1 near-duplicate: moved rows that repeat a row already in me (both kept",
		copied + " ~ " + curated + ": rule",
		"compare: lore show --id " + copied + " --store me && lore show --id " + curated + " --store me",
		"drop one: lore rule delete --id " + curated + " --yes (the folder's), or the moved one: lore rule delete --id " + copied + " --yes",
	} {
		if !strings.Contains(txt, want) {
			t.Fatalf("move --id text lacks %q:\n%s", want, txt)
		}
	}
	for _, id := range []string{curated, copied} {
		if got := showJSON(t, h, `{"id":"`+id+`"}`); got.Store != "me" {
			t.Fatalf("%s is in %s, want me (both kept, nothing merged)", id, got.Store)
		}
	}

	res = h.MustYes(row.OpMove, `{"id":"`+other+`","to":"me"}`)
	if out = moveOut(t, h, h.JSON(res)); len(out.Duplicates) != 0 || strings.Contains(opstest.Text(res), "near-duplicate") {
		t.Fatalf("a row with no near-duplicate is listed:\n%s", opstest.Text(res))
	}
}

// TestMoveIDWithChildrenListsSubtaskNearDuplicates (devco D-854): a task
// moved with --with-children brings its sub-tasks too, so a sub-task that
// repeats a task --to already holds is listed; the parent, like nothing
// there, is not. Break-verification: with only the named row checked the
// sub-task's pair is missing.
func TestMoveIDWithChildrenListsSubtaskNearDuplicates(t *testing.T) {
	h := opstest.WithStores(t, []string{"me"})
	held := h.ID(h.MustYes("task.add", `{"title":"Rotate the staging token","to":"me"}`))
	parent := h.ID(h.MustYes("task.add", `{"title":"harden the deploy"}`))
	kid := h.ID(h.MustYes("task.add", `{"title":"rotate the staging token","parent":"`+parent+`"}`))

	res := h.MustYes(row.OpMove, `{"id":"`+parent+`","to":"me","with_children":true}`)
	out := moveOut(t, h, h.JSON(res))
	if len(out.Children) != 1 || out.Children[0] != kid {
		t.Fatalf("sub-task not moved along: %s", h.JSON(res))
	}
	if len(out.Duplicates) != 1 || out.Duplicates[0].ID != kid || out.Duplicates[0].Existing != held {
		t.Fatalf("--with-children duplicates: %s", h.JSON(res))
	}
	if txt := opstest.Text(res); !strings.Contains(txt, kid+" ~ "+held+": task") {
		t.Fatalf("text lacks the sub-task's pair:\n%s", txt)
	}
}
