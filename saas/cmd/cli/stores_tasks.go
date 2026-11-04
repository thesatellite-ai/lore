// stores_tasks.go — `lore task list` for task rows that live outside the
// checkout (store projects, personal; F20). Store rows go through the same
// filters as the repo's own rows (the default active set, --status,
// --commitment, --include-*, --mission, --assignee) and print in the same
// line format, followed by their source label ([agency/client]) so nobody
// mistakes them for client rows.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"dbent/gen/ent"
	entTask "dbent/gen/ent/task"
	"saas/pkg/aicoder/cachedb"
	"saas/pkg/aicoder/signals/coord"
	"saas/pkg/aicoder/style"
)

// taskListPredicate is the row filter of `lore task list` as a Go
// predicate. Invariant: it keeps exactly the rows runTaskList's lore.db
// query keeps for the same flags (that query filters the repo's own rows;
// this one filters store rows, which are not in lore.db). Change both
// together.
type taskListPredicate struct {
	// status is an explicit --status ("" = the default active set: not
	// done, not cancelled).
	status string
	// commitment is a validated explicit --commitment ("" = none).
	commitment                                            entTask.Commitment
	all, includeProposed, includeSomeday, includeDeferred bool
	mission                                               string
	// assigneeID is the resolved --assignee actor id ("" = any).
	assigneeID string
	now        time.Time
}

// keep reports whether the list shows t.
func (p taskListPredicate) keep(t *ent.Task) bool {
	status := t.Status
	if status == "" {
		status = entTask.DefaultStatus
	}
	commitment := t.Commitment
	if commitment == "" {
		commitment = entTask.DefaultCommitment
	}
	if p.commitment != "" && commitment != p.commitment {
		return false
	}
	if p.status != "" && status != taskStatus(p.status) {
		return false
	}
	if !p.all {
		if p.status == "" && (status == entTask.StatusDone || status == entTask.StatusCancelled) {
			return false
		}
		if p.commitment == "" && !p.commitmentAllowed(commitment) {
			return false
		}
		if !p.includeDeferred && t.DeferredUntil != nil && t.DeferredUntil.After(p.now) {
			return false
		}
	}
	if p.mission != "" && deref(t.MissionID) != p.mission {
		return false
	}
	if p.assigneeID != "" && deref(t.AssignedToActorID) != p.assigneeID {
		return false
	}
	return true
}

// commitmentAllowed is the default commitment set: accepted, widened by
// --include-proposed / --include-someday.
func (p taskListPredicate) commitmentAllowed(c entTask.Commitment) bool {
	switch c {
	case entTask.CommitmentAccepted:
		return true
	case entTask.CommitmentProposed:
		return p.includeProposed
	case entTask.CommitmentSomeday:
		return p.includeSomeday
	}
	return false
}

// externalTask is a store or personal task row with its source label.
type externalTask struct {
	task  *ent.Task
	label string
	row   cachedb.Row
}

// externalTasksHere returns the external task rows of this repo's sources
// that p keeps. Like every external read it never fails the command:
// problems go to stderr as one muted note each, and a row that does not
// decode is skipped.
func externalTasksHere(ctx context.Context, p taskListPredicate) []externalTask {
	rows, notes := externalRowsHere(ctx, cachedb.Query{Table: tableTasks})
	for _, n := range notes {
		fmt.Fprintln(os.Stderr, style.Muted("lore: "+n))
	}
	return filterExternalTasks(rows, p)
}

// filterExternalTasks decodes rows and keeps the ones p keeps (pure; the
// seam the tests use).
func filterExternalTasks(rows []cachedb.Row, p taskListPredicate) []externalTask {
	var out []externalTask
	for _, r := range rows {
		var d coord.Doc
		if err := json.Unmarshal(r.Doc, &d); err != nil {
			continue
		}
		delete(d, "_live")
		t, err := taskFromDoc(d)
		if err != nil || !p.keep(t) {
			continue
		}
		out = append(out, externalTask{task: t, label: r.Label, row: r})
	}
	return out
}

// printExternalTasks prints external tasks in the repo rows' line format
// with the source label at the end. Returns how many it printed.
func printExternalTasks(w io.Writer, tasks []externalTask) int {
	for _, et := range tasks {
		fmt.Fprintln(w, taskListLine(et.task, "")+"  "+style.Muted(et.label))
	}
	return len(tasks)
}

// externalTaskJSON is the --json form of external tasks: the row's own
// fields plus _source, _label, _live_row (as externalJSONRows).
func externalTaskJSON(tasks []externalTask) []map[string]any {
	out := make([]map[string]any, 0, len(tasks))
	for _, et := range tasks {
		var doc map[string]any
		if err := json.Unmarshal(et.row.Doc, &doc); err != nil {
			continue
		}
		delete(doc, "_live")
		// FLAG1: _source is the {store, store_id, folder, folder_id} object
		// (names only on this old path), _label the human [store/folder].
		doc[jsonKeySource], doc[jsonKeyLabel], doc[jsonKeyLive] = externalSource(et.row.Source), et.row.Label, et.row.Live
		out = append(out, doc)
	}
	return out
}

// taskListLine is one line of the human task list, the same for repo and
// store rows: status badge, full id (what every command takes), title,
// priority, then due date, a non-accepted commitment chip, the deferral
// wake date and the coordination state when known.
func taskListLine(t *ent.Task, state string) string {
	status := string(t.Status)
	if status == "" {
		status = string(entTask.DefaultStatus)
	}
	priority := t.Priority
	if priority == "" {
		priority = entTask.DefaultPriority
	}
	due := ""
	if t.DueAt != nil {
		due = " due:" + t.DueAt.Format("2006-01-02")
	}
	commit := ""
	if t.Commitment != "" && t.Commitment != entTask.CommitmentAccepted {
		commit = " " + style.Warn("("+string(t.Commitment)+")")
	}
	deferred := ""
	if t.DeferredUntil != nil {
		deferred = " " + style.Muted("⏾"+t.DeferredUntil.Format("2006-01-02"))
	}
	if state != "" {
		state = "  " + state
	}
	return fmt.Sprintf("%s %s %s [%s]%s%s%s%s", taskStatusStyle(status), t.ID, t.Title, priority, due, commit, deferred, state)
}
