package task

// The close fields matrix (tasks area): completed_at and close_reason
// follow every status transition, from every status, whether the caller
// already set them or not (closeFields, behind setStatus). A reopened task
// carries no close fields; a close by edit stamps them like `task done`; a
// field the caller put in the patch (done's stamp, cancel's --reason) wins.

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
)

// TestTasksCloseFieldsMatrix walks prev status × new status and checks the
// patch closeFields leaves.
func TestTasksCloseFieldsMatrix(t *testing.T) {
	t.Parallel()
	const stamp = "2026-11-02T09:00:00Z"
	now := txt(stamp)
	closedRow := func(status, reason string) row.Row {
		f := map[string]value.Value{work.FieldStatus: txt(status)}
		if reason != "" {
			f[fieldCloseReason] = txt(reason)
			f[work.FieldCompleted] = txt("2026-10-01T00:00:00Z")
		}
		return row.Row{Fields: f}
	}
	// want: "" = field untouched, "null" = cleared, else the value set.
	type want struct{ completed, reason string }
	prevs := map[string]row.Row{
		"todo":        closedRow(schema.TaskStatusTodo, ""),
		"in_progress": closedRow(schema.TaskStatusInProgress, ""),
		"blocked":     closedRow(schema.TaskStatusBlocked, ""),
		"done":        closedRow(schema.TaskStatusDone, schema.TaskCloseCompleted),
		"cancelled":   closedRow(schema.TaskStatusCancelled, schema.TaskCloseDuplicate),
	}
	cases := map[[2]string]want{}
	for prev := range prevs {
		open := prev != "done" && prev != "cancelled"
		for _, next := range schema.TaskStatuses() {
			var w want
			switch {
			case next == schema.TaskStatusDone && prev != "done":
				w = want{stamp, schema.TaskCloseCompleted}
			case next == schema.TaskStatusCancelled && prev != "cancelled":
				w = want{"null", schema.TaskCloseCancelled}
			case next != schema.TaskStatusDone && next != schema.TaskStatusCancelled && !open:
				w = want{"null", "null"}
			}
			cases[[2]string{prev, next}] = w
		}
	}
	got := func(p map[string]value.Value, field string) string {
		v, ok := p[field]
		switch {
		case !ok:
			return ""
		case v.Kind() == value.KindNull:
			return "null"
		default:
			return v.String()
		}
	}
	for k, w := range cases {
		patch := map[string]value.Value{}
		if err := closeFields(prevs[k[0]], k[1], now, patch); err != nil {
			t.Fatalf("%s → %s: %v", k[0], k[1], err)
		}
		if g := (want{got(patch, work.FieldCompleted), got(patch, fieldCloseReason)}); g != w {
			t.Errorf("%s → %s: completed_at/close_reason %+v, want %+v", k[0], k[1], g, w)
		}
	}
	// The caller's own values win: a cancel --reason and done's stamp.
	patch := map[string]value.Value{fieldCloseReason: txt(schema.TaskCloseWontDo), work.FieldCompleted: txt("2026-12-01T00:00:00Z")}
	if err := closeFields(prevs["todo"], schema.TaskStatusDone, now, patch); err != nil {
		t.Fatal(err)
	}
	if got(patch, fieldCloseReason) != schema.TaskCloseWontDo || got(patch, work.FieldCompleted) != "2026-12-01T00:00:00Z" {
		t.Fatalf("closeFields never overwrites what the caller set: %v", patch)
	}
}
