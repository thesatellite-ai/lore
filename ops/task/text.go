package task

import (
	"fmt"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"strings"

	"github.com/thesatellite-ai/lore/logic/tasktree"
	"github.com/thesatellite-ai/lore/model/row"
	rowexpand "github.com/thesatellite-ai/lore/ops/internal/expand"
	"github.com/thesatellite-ai/lore/ops/internal/metashow"
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
func BriefOf(lab work.Labeler, src store.Source, r row.Row) Brief {
	b := Brief{
		ID: r.ID.String(), Title: work.Field(r, work.FieldTitle), Status: work.Field(r, work.FieldStatus),
		Priority: work.Field(r, fieldPriority), Commitment: work.Field(r, fieldCommitment),
		DeferredUntil: work.DateText(r, fieldDeferred), DueAt: work.DateText(r, fieldDue),
		MissionID: work.Field(r, work.FieldMission), Store: work.SourceOf(lab, src), Label: work.Label(lab, src), CreatedBy: auditrec.Of(r).CreatedBy, UpdatedBy: auditrec.Of(r).UpdatedBy,
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
	b.base = line
	b.Line = line + storeLabel(b.Label)
	return b
}

// storeLabel is the "  [<label>]" suffix of a store task's line, from its
// `_label` (already bracketed; "" for a repo task): v0.1.17 printed the
// label, so a store attached at a linked folder shows [northwind/acme].
func storeLabel(label string) string {
	if label == "" {
		return ""
	}
	return "  " + label
}

// withState puts a STATE column (v0.1.17 taskListLine) after the line's
// own fields, before the store label.
func withState(b Brief, state string) Brief {
	if state != "" {
		b.Line = b.base + "  " + state + storeLabel(b.Label)
	}
	return b
}

// fullOf is the v0.1.17 taskFull of a task row.
func fullOf(lab work.Labeler, src store.Source, r row.Row) Full {
	return Full{
		ID: r.ID.String(), Title: work.Field(r, work.FieldTitle), Body: work.Field(r, work.FieldBody),
		Status: work.Field(r, work.FieldStatus), Priority: work.Field(r, fieldPriority), Commitment: work.Field(r, fieldCommitment),
		DeferredUntil: work.DateText(r, fieldDeferred), DueAt: work.DateText(r, fieldDue),
		StartedAt: work.TimeText(r, fieldStarted), CompletedAt: work.TimeText(r, work.FieldCompleted),
		MissionID: work.Field(r, work.FieldMission), TaskListID: work.Field(r, work.FieldTasklist), PlanID: work.Field(r, work.FieldPlan),
		ProjectID: work.Field(r, work.FieldProject), RepoID: work.Field(r, work.FieldRepo),
		ParentID: work.Field(r, fieldParent), Rank: work.Field(r, fieldRank), CloseReason: work.Field(r, fieldCloseReason),
		CreatedAt: r.CreatedAt.UTC().Format(work.TimeLayout), TagIDs: work.TagIDs(r), Store: work.SourceOf(lab, src), Label: work.Label(lab, src), UpdatedAt: r.UpdatedAt.UTC().Format(work.TimeLayout), CreatedBy: auditrec.Of(r).CreatedBy, UpdatedBy: auditrec.Of(r).UpdatedBy,
	}
}

// Text prints one list line per task; "(no tasks)" when there is none.
func (o ListOut) Text() string {
	if len(o.Columns) > 0 {
		return o.columnsText() + work.NotesText(o.Notes)
	}
	return o.rowsText() + work.NotesText(o.Notes)
}

// rowsText is the list lines.
func (o ListOut) rowsText() string {
	if len(o.Data) == 0 {
		return "(no tasks)\n"
	}
	var b strings.Builder
	for _, t := range o.Data {
		b.WriteString(withSLAColumn(t) + "\n")
	}
	return b.String()
}

// withSLAColumn is t's list line with an SLA column before the store
// label when the SLA is at risk or breached (18.4); "ok" adds nothing, so
// a list without SLA trouble prints v0.1.17's lines.
func withSLAColumn(t Brief) string {
	if t.SLA != SLAAtRisk && t.SLA != SLABreached {
		return t.Line
	}
	label := storeLabel(t.Label)
	return strings.TrimSuffix(t.Line, label) + "  SLA " + slaWords(t.SLA) + label
}

// Text prints v0.1.17's task show: id, the fields, a blank line and the
// body.
func (o ShowOut) Text() string {
	return o.baseText() + auditrec.Line(o.Data.CreatedAt, o.Data.CreatedBy, o.Data.UpdatedAt, o.Data.UpdatedBy) + // INTEG-AUDIT-2
		metashow.Text(o.Data.Meta, o.Data.MetaMasked) + rowexpand.Text(o.Expanded, o.ExpandedRefs)
}

func (o ShowOut) baseText() string {
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
	if t.CloseReason != "" {
		b.WriteString("  closed:   " + t.CloseReason + "\n")
	}
	if t.ParentID != "" {
		b.WriteString("  parent:   " + t.ParentID + "\n")
	}
	if t.Rollup != (tasktree.Rollup{}) {
		fmt.Fprintf(&b, "  progress: %d/%d sub-tasks, %d/%d leaves\n", t.Rollup.ChildrenDone, t.Rollup.ChildrenTotal, t.Rollup.LeavesDone, t.Rollup.LeavesTotal)
	}
	for _, w := range t.TreeWarnings {
		b.WriteString("  warning:  " + w.Code + " " + w.Message + "\n")
	}
	if len(t.Tree) > 0 {
		b.WriteString("  tree:\n")
		for _, l := range t.Tree {
			b.WriteString("    " + strings.Repeat("  ", l.Depth-1) + badge(orDefault(l.Status, schema.TaskStatusTodo)) + " " + l.ID + " " + l.Title + "\n")
		}
	}
	for _, r := range t.Refs {
		b.WriteString("  ref:      " + r.Where() + "\n")
	}
	if t.TimeLine != "" {
		b.WriteString("  time:     " + t.TimeLine + "\n")
	}
	if t.Why.shown {
		b.WriteString(t.Why.text())
	}
	if t.Who.shown {
		b.WriteString(t.Who.text())
	}
	if t.Relations.shown {
		b.WriteString(t.Relations.text())
	}
	if t.EstimateLine != "" {
		b.WriteString("  estimate: " + t.EstimateLine + "\n")
	}
	if t.SLALine != "" {
		b.WriteString("  sla:      " + t.SLALine + "\n")
	}
	if t.PeopleLine != "" {
		b.WriteString("  " + t.PeopleLine + "\n")
	}
	if len(t.Checklist) > 0 {
		done := 0
		for _, it := range t.Checklist {
			if it.Done {
				done++
			}
		}
		fmt.Fprintf(&b, "  checklist: %d/%d\n", done, len(t.Checklist))
		for _, it := range t.Checklist {
			fmt.Fprintf(&b, "    %d. %s %s  %s\n", it.Position, box(it.Done), it.Text, it.ID)
		}
	}
	for _, f := range t.Detail.Fields {
		if f.Value != "" {
			fmt.Fprintf(&b, "  %-9s %s\n", f.Name+":", f.Value)
		}
	}
	if t.Body != "" {
		b.WriteString("\n" + t.Body + "\n")
	}
	if len(t.Notes) > 0 {
		b.WriteString("\n  notes:\n")
		for _, n := range t.Notes {
			b.WriteString("    " + n + "\n")
		}
	}
	b.WriteString(t.Detail.Tail)
	return b.String()
}
