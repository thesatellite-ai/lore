package wire

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/dates"
	opscontent "github.com/thesatellite-ai/lore/ops/content"
	"github.com/thesatellite-ai/lore/schema"
)

// DoctorCheckReminders is the due-reminders line of `lore doctor` (D-148,
// r3 G-5: an overdue reminder showed only in `lore reminder list`).
const DoctorCheckReminders DoctorCheck = "reminders"

// DoctorReminders lists, per attached store, its pending reminders that
// are due by now for this machine's zone (dates.DueBy: a day once it has
// begun, a clock time once passed): one note per store, never a warning
// (a due reminder is work, not an unhealthy store). Nothing when none is
// due.
func (a *App) DoctorReminders(ctx context.Context) ([]DoctorFinding, error) {
	now := a.Now()
	var out []DoctorFinding
	for _, src := range a.Stores() {
		rows, err := a.List(ctx, src, schema.TableReminders)
		if err != nil {
			return nil, err
		}
		var due []string
		for _, r := range rows {
			if r.Deleted {
				continue
			}
			if !opscontent.Time(r, schema.FieldReminderDoneAt).IsZero() || opscontent.Field(r, schema.FieldReminderDoneAt) != "" {
				continue
			}
			at := opscontent.Time(r, schema.FieldReminderDueAt)
			if at.IsZero() || !dates.DueBy(at, now, time.Local) {
				continue
			}
			due = append(due, r.ID.String())
		}
		if len(due) == 0 {
			continue
		}
		sort.Strings(due)
		out = append(out, DoctorFinding{Check: DoctorCheckReminders, Level: DoctorNote, Store: src,
			Message: strconv.Itoa(len(due)) + " reminder(s) due or overdue: " + strings.Join(due, ", "), // text output caps the list (listcap), --json keeps it whole
			Fix:     "lore reminder list, then lore reminder done --id <id> for each one handled"})
	}
	return out, nil
}
