//go:build userscen

package userscen

import (
	"encoding/json"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// TestBatchEditUndoTwice: a bulk `task edit --where` is one batch; `lore
// batch undo` puts the old priority back on every row; a second undo of
// the same batch has nothing to do (exit 0, "nothing to undo"), as for a
// delete batch, never E_STALE, and it adds no batch to the list.
func TestBatchEditUndoTwice(t *testing.T) {
	_, _, c := cliSolo(t)
	for _, title := range []string{"alpha job", "beta job"} {
		c.MustLore("task", "add", title, "--priority", "low")
	}
	c.MustLore("task", "edit", "--where", "title~=job", "--priority", "high", "--yes")
	list := th.DecodeJSON[cliBatchList](t, c.Lore("batch", "list", "--json"))
	if list.Count != 1 || list.Data[0].Rows != 2 {
		t.Fatalf("one bulk edit of two tasks: want one batch of 2 rows: %+v", list)
	}
	id := list.Data[0].ID
	c.MustLore("batch", "undo", id, "--yes").Says(t, "restored 2 rows to their values before "+id)
	var tasks struct {
		Data []struct {
			Title    string `json:"title"`
			Priority string `json:"priority"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(c.MustLore("task", "list", "--json").Stdout), &tasks); err != nil || len(tasks.Data) != 2 {
		t.Fatalf("task list --json after undo: %v", err)
	}
	for _, tk := range tasks.Data {
		if tk.Priority != "low" {
			t.Errorf("undo of the bulk edit: %q has priority %q, want low", tk.Title, tk.Priority)
		}
	}
	c.MustLore("batch", "undo", id, "--yes").Says(t, "nothing to undo in batch "+id).Never(t, "E_STALE")
	if again := th.DecodeJSON[cliBatchList](t, c.Lore("batch", "list", "--json")); again.Count != 2 {
		t.Errorf("the edit batch and the one undo batch, nothing more: %+v", again)
	}
}
