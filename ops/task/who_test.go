package task_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestShowWhoTouched: show --who lists the actors that wrote the task
// (creation and history entries, AGENT_IDENTITY 6.2), with a change count.
func TestShowWhoTouched(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	id := add(h, "who", "")
	h.Must(opstask.OpStart, `{"id":"`+id+`"}`)
	h.Must(opstask.OpEdit, `{"id":"`+id+`","priority":"high"}`)
	res := h.Must(opstask.OpShow, `{"id":"`+id+`","who":true}`)
	js := h.JSON(res)
	if !strings.Contains(js, `"touched":[{"actor_id":"act_`) {
		t.Fatalf("no touched block: %s", js)
	}
	if strings.Contains(js, `"changes":1,`) || strings.Contains(js, `"changes":0`) {
		t.Errorf("creation + history entries not counted: %s", js)
	}
	if !strings.Contains(opstest.Text(res), "touched by:") {
		t.Errorf("human: %s", opstest.Text(res))
	}
}
