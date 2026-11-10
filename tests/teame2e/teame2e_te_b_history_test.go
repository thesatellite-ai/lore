//go:build teame2e

package teame2e

// OV59 (TASK 17.2, TSQ10) on real git with two people: `task reassign`
// appends history entries without applying tasks.history_max, so a task
// reassigned often sits over the cap; the next core write of that task,
// made from another person's clone, folds the list to the cap with one
// summary entry, and the first person's clone reads the folded list.

import (
	"encoding/json"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// teBHistoryMax is the cap the test sets (small, so a few reassigns pass it).
const teBHistoryMax = 3

// teBHistory is the field and summary lines of `lore task history --json`
// (claim lines are not part of the capped list).
func teBHistory(t *testing.T, c *th.Clone, id string) []teBHistLine {
	t.Helper()
	r := c.MustLore("task", "history", id, "--json")
	var out struct {
		Lines []teBHistLine `json:"lines"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		t.Fatalf("decode task history --json: %v\n%s", err, r)
	}
	var kept []teBHistLine
	for _, l := range out.Lines {
		if l.Kind == "field" || l.Kind == "summary" {
			kept = append(kept, l)
		}
	}
	return kept
}

// teBHistLine is one line of `lore task history --json`.
type teBHistLine struct {
	Kind  string `json:"kind"`
	Field string `json:"field"`
	To    string `json:"to"`
	Note  string `json:"note"`
}

func TestTeB_ReassignOverHistoryMaxFoldedByNextWrite(t *testing.T) {
	cr := newCrew(t, th.Cmd("task", "reassign"), th.Cmd("task", "history"), th.Flag("assign", "task", "add"))
	cr.toolsC.MustLore("policy", "set", "tasks.history_max", "3", "--to", "repo", "--yes")
	id := cr.task(t, "teb handover", "--assign", "@dev1")
	for _, to := range []string{"@alice", "@dev1", "@alice"} {
		cr.toolsC.MustLore("task", "reassign", id, "--assign", to).Says(t, id+" reassigned to "+to)
	}
	before := teBHistory(t, cr.toolsC, id)
	if len(before) <= teBHistoryMax {
		t.Fatalf("premise: three reassigns leave more than %d entries (reassign does not fold): %+v", teBHistoryMax, before)
	}
	for _, l := range before {
		if l.Kind == "summary" {
			t.Fatalf("reassign folded the history itself: %+v", before)
		}
	}
	if s := cr.toolsC.Status(); s != "" {
		cr.toolsC.CommitAll("reassigns")
		cr.toolsC.AdminPush("origin", "main")
	}

	// dev1's clone makes the next core write; it folds the whole list.
	devC := cr.dev1.Clone(cr.tools, "tools")
	devC.MustLore("actor", "register")
	if got := teBHistory(t, devC, id); len(got) != len(before) {
		t.Fatalf("dev1's clone reads the same over-cap history: %d entries, want %d", len(got), len(before))
	}
	devC.MustLore("task", "edit", id, "--priority", "high")
	after := teBHistory(t, devC, id)
	teBCheckFolded(t, "dev1", after)

	// The fold reaches alice's clone.
	if s := devC.Status(); s != "" {
		devC.CommitAll("edit")
		devC.AdminPush("origin", "main")
	}
	cr.toolsC.Git("pull", "-q")
	cr.toolsC.MustLore("sync")
	teBCheckFolded(t, "alice", teBHistory(t, cr.toolsC, id))
}

// teBCheckFolded: the list is at the cap, has exactly one summary, and its
// newest entry is the priority change.
func teBCheckFolded(t *testing.T, who string, got []teBHistLine) {
	t.Helper()
	summaries := 0
	for _, l := range got {
		if l.Kind == "summary" {
			summaries++
		}
	}
	if len(got) != teBHistoryMax || summaries != 1 {
		t.Fatalf("%s: after the core write the history holds %d entries with %d summaries, want %d with 1: %+v", who, len(got), summaries, teBHistoryMax, got)
	}
	if last := got[len(got)-1]; last.Field != "priority" || last.To != "high" {
		t.Fatalf("%s: newest entry %+v, want the priority change", who, last)
	}
}
