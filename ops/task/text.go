package task

import (
	"strings"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Human (op.Texter) forms of the task outputs: v0.1.17's lines from
// saas/cmd/cli/task.go and stores_tasks.go at 4e6ed1c, without colours.

// Status badges of the list line (v0.1.17 taskStatusStyle).
const (
	badgeDone       = "✓"
	badgeInProgress = "▶"
	badgeCancelled  = "✗"
	badgeBlocked    = "⛔"
	badgeOpen       = "○"
	// deferMark prefixes the deferral wake date.
	deferMark = "⏾"
)

// badge is the status badge of status.
func badge(status string) string {
	switch status {
	case schema.TaskStatusDone:
		return badgeDone
	case schema.TaskStatusInProgress:
		return badgeInProgress
	case schema.TaskStatusCancelled:
		return badgeCancelled
	case schema.TaskStatusBlocked:
		return badgeBlocked
	}
	return badgeOpen
}

// BriefOf is the v0.1.17 taskBrief of a task row, with its human list
// line (taskListLine: badge, id, title, priority, due, a non-accepted
// commitment chip, the deferral wake date).
func BriefOf(src store.Source, r row.Row) Brief {
	b := Brief{
		ID: r.ID.String(), Title: work.Field(r, work.FieldTitle), Status: work.Field(r, work.FieldStatus),
		Priority: work.Field(r, fieldPriority), Commitment: work.Field(r, fieldCommitment),
		DeferredUntil: work.DateText(r, fieldDeferred), DueAt: work.DateText(r, fieldDue),
		MissionID: work.Field(r, work.FieldMission), Store: work.SourceName(src),
	}
	status, priority := orDefault(b.Status, schema.TaskStatusTodo), orDefault(b.Priority, DefaultPriority)
	line := badge(status) + " " + b.ID + " " + b.Title + " [" + priority + "]"
	if b.DueAt != "" {
		line += " due:" + b.DueAt
	}
	if b.Commitment != "" && b.Commitment != schema.TaskCommitmentAccepted {
		line += " (" + b.Commitment + ")"
	}
	if b.DeferredUntil != "" {
		line += " " + deferMark + b.DeferredUntil
	}
	if b.Store != "" {
		line += "  [" + b.Store + "]"
	}
	b.Line = line
	return b
}

// fullOf is the v0.1.17 taskFull of a task row.
func fullOf(src store.Source, r row.Row) Full {
	return Full{
		ID: r.ID.String(), Title: work.Field(r, work.FieldTitle), Body: work.Field(r, work.FieldBody),
		Status: work.Field(r, work.FieldStatus), Priority: work.Field(r, fieldPriority), Commitment: work.Field(r, fieldCommitment),
		DeferredUntil: work.DateText(r, fieldDeferred), DueAt: work.DateText(r, fieldDue),
		StartedAt: work.TimeText(r, fieldStarted), CompletedAt: work.TimeText(r, work.FieldCompleted),
		MissionID: work.Field(r, work.FieldMission), TaskListID: work.Field(r, work.FieldTasklist), PlanID: work.Field(r, work.FieldPlan),
		ProjectID: work.Field(r, work.FieldProject), RepoID: work.Field(r, work.FieldRepo),
		CreatedAt: r.CreatedAt.UTC().Format(work.TimeLayout), TagIDs: work.TagIDs(r), Store: work.SourceName(src),
	}
}

// Text prints one list line per task; "(no tasks)" when there is none.
func (o ListOut) Text() string {
	if len(o.Data) == 0 {
		return "(no tasks)\n"
	}
	var b strings.Builder
	for _, t := range o.Data {
		b.WriteString(t.Line + "\n")
	}
	return b.String()
}

// Text prints v0.1.17's task show: id, the fields, a blank line and the
// body.
func (o ShowOut) Text() string {
	t := o.Data
	var b strings.Builder
	b.WriteString(t.ID + "\n")
	b.WriteString("  title:    " + t.Title + "\n")
	b.WriteString("  status:   " + t.Status + "\n")
	b.WriteString("  priority: " + t.Priority + "\n")
	b.WriteString("  commit:   " + t.Commitment + "\n")
	if t.DeferredUntil != "" {
		b.WriteString("  deferred: " + t.DeferredUntil + "\n")
	}
	if t.DueAt != "" {
		b.WriteString("  due:      " + t.DueAt + "\n")
	}
	if t.MissionID != "" {
		b.WriteString("  mission:  " + t.MissionID + "\n")
	}
	if t.DoneBy != "" {
		b.WriteString("  done by:  " + t.DoneBy + "\n")
	}
	for _, r := range t.Refs {
		b.WriteString("  ref:      " + r.Table + "/" + r.ID + "\n")
	}
	if t.TimeLine != "" {
		b.WriteString("  time:     " + t.TimeLine + "\n")
	}
	if t.Body != "" {
		b.WriteString("\n" + t.Body + "\n")
	}
	return b.String()
}
