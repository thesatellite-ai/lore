package wire

import (
	"encoding/json"
	"testing"

	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// releaseRow is the part of task.show the D-464 test reads.
func releaseRow(t *testing.T, a *App, id string) (status, startedAt string) {
	t.Helper()
	out, err := run(t, a, opstask.OpShow, `{"id":"`+id+`"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var shown struct {
		Data struct {
			Status    string `json:"status"`
			StartedAt string `json:"started_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &shown); err != nil {
		t.Fatalf("task show: %v %s", err, out)
	}
	return shown.Data.Status, shown.Data.StartedAt
}

// TestReleaseKeepsStatusSetDuringClaim (devco:D-464): a claim nobody
// worked goes back to todo (devco:D-427 unchanged); a `task start` during
// the claim stays started after the release, with the claim's started_at;
// an `edit --status` during the claim keeps the status it set.
func TestReleaseKeepsStatusSetDuringClaim(t *testing.T) {
	a, _, id := standaloneTask(t)
	claimRelease := func(during func()) {
		t.Helper()
		if _, err := run(t, a, opscoord.OpTaskClaim, `{"id":"`+id+`"}`, false); err != nil {
			t.Fatal(err)
		}
		if during != nil {
			during()
		}
		if _, err := run(t, a, opscoord.OpTaskRelease, `{"id":"`+id+`"}`, false); err != nil {
			t.Fatal(err)
		}
	}
	do := func(op, in string) func() {
		return func() {
			t.Helper()
			if _, err := run(t, a, op, in, false); err != nil {
				t.Fatalf("%s %s: %v", op, in, err)
			}
		}
	}

	claimRelease(nil)
	if st, at := releaseRow(t, a, id); st != "todo" || at != "" {
		t.Fatalf("claim+release: %s started_at %q, want todo with none", st, at)
	}

	var claimedAt string
	claimRelease(func() {
		_, claimedAt = releaseRow(t, a, id)
		do(opstask.OpStart, `{"id":"`+id+`"}`)()
	})
	if st, at := releaseRow(t, a, id); st != "in_progress" || at == "" || at != claimedAt {
		t.Fatalf("claim+start+release: %s started_at %q, want in_progress started at %q", st, at, claimedAt)
	}

	// The task is started now; a claim of it and an edit to blocked keep blocked.
	claimRelease(do(opstask.OpEdit, `{"id":"`+id+`","status":"blocked"}`))
	if st, _ := releaseRow(t, a, id); st != "blocked" {
		t.Fatalf("claim+edit --status blocked+release: %s, want blocked", st)
	}

	// Back to todo, then a claim whose edit sets in_progress keeps it.
	do(opstask.OpEdit, `{"id":"`+id+`","status":"todo"}`)()
	claimRelease(do(opstask.OpEdit, `{"id":"`+id+`","status":"in_progress"}`))
	if st, _ := releaseRow(t, a, id); st != "in_progress" {
		t.Fatalf("claim+edit --status in_progress+release: %s, want in_progress", st)
	}
}
