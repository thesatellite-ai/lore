package reminder

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/schema"
)

// TestBumpRecurrence is saas/cmd/cli TestUC_Tasks_BumpRecurrence moved with
// the code: every recurrence advances the due date as in v0.1.17, and an
// unknown pattern is refused (v0.1.17 panicked; now ok=false).
func TestBumpRecurrence(t *testing.T) {
	t.Parallel()
	from := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	want := map[string]string{
		schema.Recurrence7d:  "2026-03-17",
		schema.Recurrence30d: "2026-04-09",
		schema.Recurrence1m:  "2026-04-10",
		schema.Recurrence3m:  "2026-06-10",
		schema.Recurrence6m:  "2026-09-10",
		schema.Recurrence1y:  "2027-03-10",
	}
	if len(want) != len(schema.ReminderRecurrences()) {
		t.Fatalf("test covers %d patterns, enum has %d", len(want), len(schema.ReminderRecurrences()))
	}
	for p, w := range want {
		got, ok := bumpRecurrence(from, p)
		if !ok || got.Format(time.DateOnly) != w {
			t.Fatalf("%s: %s %v, want %s", p, got, ok, w)
		}
	}
	if _, ok := bumpRecurrence(from, "2w"); ok {
		t.Fatal("an unknown pattern must be refused")
	}
}
