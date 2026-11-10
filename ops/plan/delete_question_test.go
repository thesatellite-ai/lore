package plan_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
)

// TestPlanDeleteQuestion (later 244, DELETION 3.4, OV46-D1): a plan with
// nothing under it asks the plain question and prints the one-row trash
// line; a plan with tasks names the counts in the question (tasks and
// missions: a mission's plan_id is set_null, so a mission under the plan
// stays live, DEL-R3, and counts 0), --yes prints the batch line, and
// restoring that batch brings the plan and its tasks back.
func TestPlanDeleteQuestion(t *testing.T) {
	h := opstest.New(t)
	empty := planID(t, h, "Empty")
	msg := h.Fails("plan.delete", `{"id":"`+empty+`"}`, errs.NeedsConfirm)
	if !strings.Contains(msg, `delete plan `+empty+` "Empty"?`) || strings.Contains(msg, "also moves") {
		t.Fatalf("plain question: %s", msg)
	}
	got := opstest.Text(h.MustYes("plan.delete", `{"id":"`+empty+`"}`))
	if !strings.Contains(got, "moved to the trash (restore: lore trash restore "+empty+")") || strings.Contains(got, "batch") {
		t.Fatalf("plain line: %q", got)
	}

	full := planID(t, h, "Full")
	t1 := h.Seed(schema.TableTasks, map[string]string{"plan_id": opstest.Quote(full)})
	t2 := h.Seed(schema.TableTasks, map[string]string{"plan_id": opstest.Quote(full)})
	m := h.ID(h.Must("mission.add", `{"title":"under the plan","plan":"`+full+`"}`))
	msg = h.Fails("plan.delete", `{"id":"`+full+`"}`, errs.NeedsConfirm)
	if !strings.Contains(msg, "also moves 2 rows to the trash (2 tasks, 0 missions); delete them all?") {
		t.Fatalf("counted question: %s", msg)
	}
	got = opstest.Text(h.MustYes("plan.delete", `{"id":"`+full+`"}`))
	batch := regexp.MustCompile(`batch (bat_[0-9a-f]{32}); restore: lore trash restore (bat_[0-9a-f]{32})`).FindStringSubmatch(got)
	if batch == nil || batch[1] != batch[2] || !strings.Contains(got, "+ 2 rows moved to the trash") {
		t.Fatalf("batch line: %q", got)
	}
	for _, id := range []string{t1, t2} {
		h.Fails("task.show", `{"id":"`+id+`"}`, errs.InTrash)
	}
	h.Must("mission.show", `{"id":"`+m+`"}`) // set_null: not in the batch
	h.Must("trash.restore", `{"target":"`+batch[1]+`"}`)
	h.Must("plan.show", `{"id":"`+full+`"}`)
	h.Must("mission.show", `{"id":"`+m+`"}`)
	for _, id := range []string{t1, t2} {
		h.Must("task.show", `{"id":"`+id+`"}`)
	}
}
