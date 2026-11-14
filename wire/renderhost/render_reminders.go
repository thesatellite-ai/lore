// render_reminders.go — the reminders block of a rendered context file
// (D-148, r3 G-5): a reminder past its date showed only in `lore reminder
// list`, a command people must remember to run, while the agent reading
// LORE.md never heard of it.
//
//	render sources ─▶ reminders of this project, not done
//	  ─▶ oldest due first, at most renderReminderMax lines:
//	     "- 2026-11-11  rotate the queue credentials (rmd_…)"
//	  ─▶ a tail block of the core, kept only when it fits (FA-CTX-7)
//
// The block names due dates, never "overdue": render gives the same bytes
// for the same rows (6.6, SC-12), so the reader compares the date with
// today. A calendar day prints as YYYY-MM-DD, a clock time in UTC.

package renderhost

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/dates"
	opscontent "github.com/thesatellite-ai/lore/ops/content"
	"github.com/thesatellite-ai/lore/schema"
)

const (
	// renderRemindersHead is the block's heading and how to read it.
	renderRemindersHead = "## Reminders\n\nPending reminders, oldest due first. One due today or earlier is overdue: tell the person, and once it is handled run `lore reminder done --id <id>`.\n\n"
	// renderReminderMax caps the lines; the rest are counted.
	renderReminderMax = 10
)

// renderReminder is one pending reminder of the block.
type renderReminder struct {
	id, message, day string
	due              time.Time
}

// remindersBlock is the block for projectID ("" when no reminder is
// pending).
func (h *Host) remindersBlock(ctx context.Context, projectID string) (string, error) {
	rows, err := h.renderLiveRows(ctx, schema.TableReminders)
	if err != nil {
		return "", err
	}
	var list []renderReminder
	for _, r := range rows {
		if !r.inProject(projectID, false) {
			continue
		}
		if renderField(r.Row, schema.FieldReminderDoneAt) != "" {
			continue
		}
		at := opscontent.Time(r.Row, schema.FieldReminderDueAt)
		if at.IsZero() {
			continue
		}
		day := dates.DayIn(at, nil)
		if !dates.IsDay(at) {
			day = at.UTC().Format("2006-01-02 15:04 UTC")
		}
		list = append(list, renderReminder{id: r.ID.String(), message: oneLine(renderField(r.Row, schema.FieldReminderMessage)), day: day, due: at})
	}
	if len(list) == 0 {
		return "", nil
	}
	sort.SliceStable(list, func(i, j int) bool {
		if !list[i].due.Equal(list[j].due) {
			return list[i].due.Before(list[j].due)
		}
		return list[i].id < list[j].id
	})
	var b strings.Builder
	b.WriteString(renderRemindersHead)
	for i, r := range list {
		if i == renderReminderMax {
			fmt.Fprintf(&b, "- %d more: `lore reminder list`\n", len(list)-renderReminderMax)
			break
		}
		fmt.Fprintf(&b, "- %s  %s (%s)\n", r.day, r.message, r.id)
	}
	return b.String(), nil
}
