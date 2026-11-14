package wire

import (
	"strings"
	"testing"
	"time"

	opsreminder "github.com/thesatellite-ai/lore/ops/reminder"
)

// TestDoctorRemindersNotesDueOnes (D-148, r3 G-5): doctor names the
// pending reminders that are due by now in one note per store (never a
// warning: a due reminder is work, not an unhealthy store); a future or
// done reminder is not named, and none due means no line.
func TestDoctorRemindersNotesDueOnes(t *testing.T) {
	a := newTestApp(t, gitRepo(t))
	withProject(t, a)
	if got, err := a.DoctorReminders(background()); err != nil || len(got) != 0 {
		t.Fatalf("no reminder: %v %v", got, err)
	}
	day := func(d time.Duration) string { return time.Now().Add(d).UTC().Format("2006-01-02") }
	lateOut, err := run(t, a, opsreminder.OpAdd, `{"due":"`+day(-72*time.Hour)+`","message":"rotate the queue credentials"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = run(t, a, opsreminder.OpAdd, `{"due":"`+day(30*24*time.Hour)+`","message":"renew the domain"}`, true); err != nil {
		t.Fatal(err)
	}
	got, err := a.DoctorReminders(background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Level != DoctorNote || got[0].Check != DoctorCheckReminders ||
		!strings.HasPrefix(got[0].Message, "1 reminder(s) due or overdue: rmd_") || !strings.Contains(lateOut, strings.TrimPrefix(got[0].Message, "1 reminder(s) due or overdue: ")) {
		t.Fatalf("findings %+v (add said %s)", got, lateOut)
	}
}
