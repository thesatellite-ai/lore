package mission_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestMissionApplyRefusedWritesNothing (devco:D-938, H-199): a plan whose
// second task the write refuses (timebox: soon) used to leave the mission
// row and the first task written. The apply is one stage now: the refusal
// names the task and nothing is written; the fixed plan then applies.
func TestMissionApplyRefusedWritesNothing(t *testing.T) {
	h := opstest.New(t)
	const plan = "mission:\n  title: Launch\ntasks:\n  - key: a\n    title: Write the page\n  - key: b\n    title: Ship the page\n    after: [a]\n"
	bad := writePlan(t, plan+"    timebox: soon\n")
	msg := h.Fails("mission.apply", `{"file":`+opstest.Quote(bad)+`}`, errs.InvalidInput)
	if !strings.Contains(msg, "nothing written") || !strings.Contains(msg, "task b") {
		t.Errorf("refusal %q: want it to say nothing was written and name task b", msg)
	}
	if got := opstest.Text(h.Must("mission.list", `{"all":true}`)); strings.Contains(got, "Launch") {
		t.Fatalf("a refused apply wrote the mission:\n%s", got)
	}
	if got := h.JSON(h.Must("task.list", `{"all":true}`)); strings.Contains(got, "Write the page") {
		t.Fatalf("a refused apply wrote its first task: %s", got)
	}
	good := writePlan(t, plan+"    timebox: 30m\n")
	out := opstest.Text(h.Must("mission.apply", `{"file":`+opstest.Quote(good)+`}`))
	if !strings.Contains(out, "Launch (created)") || !strings.Contains(out, "Ship the page") || !strings.Contains(out, "after a") {
		t.Fatalf("the fixed plan: %s", out)
	}
	if again := opstest.Text(h.Must("mission.apply", `{"file":`+opstest.Quote(good)+`}`)); !strings.Contains(again, "Launch (found)") {
		t.Fatalf("a re-run must find the mission: %s", again)
	}
}
