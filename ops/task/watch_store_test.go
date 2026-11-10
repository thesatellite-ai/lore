package task_test

import (
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
)

// TestWatchNoOpNamesItsStore: task watch on a task already watched (the
// adder watches it) and task unwatch on one not watched name the store the
// task lives in, as every other task write does (the machine sweep saw
// `"store": ""` from lore task watch right after task add).
func TestWatchNoOpNamesItsStore(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	id := addID(h, h.Must("task.add", `{"title":"watched"}`))
	for _, step := range []string{"task.watch", "task.unwatch", "task.unwatch", "task.watch", "task.watch"} {
		var out struct {
			Store string `json:"store"`
		}
		if err := json.Unmarshal([]byte(h.JSON(h.Must(step, `{"id":"`+id+`"}`))), &out); err != nil {
			t.Fatal(err)
		}
		if out.Store == "" {
			t.Errorf("%s answered with no store", step)
		}
	}
}
