package task_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// Two people of the time matrices (fixed ids, 32 hex digits).
const (
	actA = "act_0e45506e64357c94a87e70ef1c6bb0a1"
	actB = "act_0e45506e64357c94a87e70ef1c6bb0b2"
)

// TestTimeShowPeople (FA-TASK-5, TIME 11.1, later 43, 427): the
// --entries WHO column prints each entry's display name, not its actor
// id; task list --worked-by finds only the task with that actor's time; a
// done task moved back to todo shows "reopened <day>" (JSON and text); a
// trashed task's time reads with --include-deleted only.
func TestTimeShowPeople(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	h := opstest.New(t, repoClient(false))
	actorRow(t, h, actA, "Ann")
	actorRow(t, h, actB, "Bea")
	id := add(h, "Ann's time", "")
	other := add(h, "Bea's time", "")
	h.Must(opstask.OpTimeLog, `{"id":"`+id+`","duration":"1h","at":"2026-11-17","by":"`+actA+`"}`)
	h.Must(opstask.OpTimeLog, `{"id":"`+other+`","duration":"15m","at":"2026-11-17","by":"`+actB+`"}`)
	txt := opstest.Text(h.Must(opstask.OpTimeShow, `{"id":"`+id+`","entries":true}`))
	if !strings.Contains(txt, "Ann") || strings.Contains(txt, actA) {
		t.Errorf("WHO column:\n%s", txt)
	}
	var lo opstask.ListOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpList, `{"worked_by":"`+actA+`"}`))), &lo); err != nil {
		t.Fatal(err)
	}
	if len(lo.Data) != 1 || lo.Data[0].ID != id {
		t.Errorf("--worked-by A: %+v", lo.Data)
	}
	d := timeOut(t, h, `{"id":"`+id+`"}`).Data
	if d.Reopened != "" {
		t.Errorf("never reopened, got %q", d.Reopened)
	}
	h.Must(opstask.OpDone, `{"id":"`+id+`","no_lesson":true}`)
	h.Must(opstask.OpEdit, `{"id":"`+id+`","status":"todo"}`)
	if d = timeOut(t, h, `{"id":"`+id+`","tz":"UTC"}`).Data; d.Reopened == "" {
		t.Error("no reopened day after done -> todo")
	}
	if txt = opstest.Text(h.Must(opstask.OpTimeShow, `{"id":"`+id+`","tz":"UTC"}`)); !strings.Contains(txt, "reopened "+d.Reopened) {
		t.Errorf("text has no reopened line:\n%s", txt)
	}
	h.MustYes(opstask.OpDelete, `{"id":"`+other+`"}`)
	if _, err := h.Run(opstask.OpTimeShow, `{"id":"`+other+`"}`); err == nil {
		t.Error("time show of a trashed task without --include-deleted answered")
	}
	if d = timeOut(t, h, `{"id":"`+other+`","include_deleted":true}`).Data; !d.InTrash || d.EffortSeconds != 900 {
		t.Errorf("--include-deleted: in_trash %v effort %d", d.InTrash, d.EffortSeconds)
	}
}
