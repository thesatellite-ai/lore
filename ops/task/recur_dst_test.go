package task_test

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// inZone runs the test with time.Local set to name (the person's zone,
// as TZ sets it). Not parallel: time.Local is process-wide, and parallel
// tests only start once every sequential one is done.
func inZone(t *testing.T, name string) {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skip("no tz database:", err)
	}
	old := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = old })
}

// TestRecurTimedDueKeepsTimeAcrossDST (devco:T-8, devco:T-9): a task due
// "DATE + 09:00" keeps 09:00 in show, list and --json, and its weekly next
// instance is 09:00 local again after the DST change (New York, Berlin),
// not 7×24 h later in UTC. A late-evening due lists its own local day.
func TestRecurTimedDueKeepsTimeAcrossDST(t *testing.T) {
	for _, c := range []struct {
		zone, due, stored, human, next string
	}{
		{"America/New_York", "2027-03-12 + 09:00", "2027-03-12T14:00:00Z", "2027-03-12 09:00 EST", "2027-03-19T13:00:00Z"},
		{"Europe/Berlin", "2027-03-26 + 09:00", "2027-03-26T08:00:00Z", "2027-03-26 09:00 CET", "2027-04-02T07:00:00Z"},
	} {
		t.Run(c.zone, func(t *testing.T) {
			inZone(t, c.zone)
			h := opstest.New(t)
			out := opstest.Text(h.Must(opstask.OpAdd, `{"title":"standup","due":"`+c.due+`","recur":"every week"}`))
			id := strings.Fields(strings.TrimPrefix(out, "✓ "))[0]
			show := h.JSON(h.Must(opstask.OpShow, `{"id":"`+id+`"}`))
			if !strings.Contains(show, `"due_at":"`+c.stored+`"`) {
				t.Fatalf("show --json drops the time: %s", show)
			}
			if txt := opstest.Text(h.Must(opstask.OpShow, `{"id":"`+id+`"}`)); !strings.Contains(txt, "due:      "+c.human) {
				t.Fatalf("show: %s", txt)
			}
			if list := opstest.Text(h.Must(opstask.OpList, `{}`)); !strings.Contains(list, "due:"+c.human) {
				t.Fatalf("list: %s", list)
			}
			done := h.JSON(h.Must(opstask.OpDone, `{"id":"`+id+`","no_lesson":true}`))
			if !strings.Contains(done, `"due":"`+c.next+`"`) {
				t.Fatalf("next instance: %s", done)
			}
		})
	}
	t.Run("late evening", func(t *testing.T) {
		inZone(t, "America/New_York")
		h := opstest.New(t)
		out := opstest.Text(h.Must(opstask.OpAdd, `{"title":"close books","due":"2027-01-15 + 23:30","recur":"every week"}`))
		if !strings.Contains(out, "next: 2027-01-15, 2027-01-22, 2027-01-29") {
			t.Fatalf("add: %s", out)
		}
	})
}
