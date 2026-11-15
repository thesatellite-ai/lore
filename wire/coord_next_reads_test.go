package wire

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/gitenv"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// repoRefs is every ref of repo with its commit: a write of any kind (a
// claim on refs/lore/signals, a live row, a data commit) changes it.
func repoRefs(t *testing.T, repo string) string {
	t.Helper()
	cmd := exec.Command("git", "for-each-ref", "--format=%(refname) %(objectname)")
	cmd.Dir = repo
	cmd.Env = gitenv.Clean(cmd.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("for-each-ref: %v %s", err, out)
	}
	return string(out)
}

// taskStatus is the task's status as task.show reads it.
func taskStatus(t *testing.T, a *App, id string) string {
	t.Helper()
	out, err := run(t, a, opstask.OpShow, `{"id":"`+id+`"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var shown struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &shown); err != nil {
		t.Fatalf("task show: %v %s", err, out)
	}
	return shown.Data.Status
}

// TestTaskNextReadsByDefault (devco:D-426): a plain task next ranks and
// explains the task it would take and writes nothing, no ref moves and
// the task stays todo; --dry-run with --claim still writes nothing; only
// --claim claims, and the release afterwards leaves the task todo again
// (devco:D-427).
func TestTaskNextReadsByDefault(t *testing.T) {
	a, repo, id := standaloneTask(t)
	before := repoRefs(t, repo)
	for _, in := range []string{`{}`, `{"why":true}`, `{"claim":true,"dry_run":true}`} {
		out, err := run(t, a, opscoord.OpTaskNext, in, false)
		if err != nil {
			t.Fatalf("task next %s: %v", in, err)
		}
		var got opscoord.NextOut
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		if !got.DryRun || got.Claimed != "" || len(got.Ready) != 1 || got.Ready[0].ID != id {
			t.Fatalf("task next %s must only rank %s: %s", in, id, out)
		}
	}
	if after := repoRefs(t, repo); after != before {
		t.Fatalf("a task next without --claim wrote:\nbefore %s\nafter  %s", before, after)
	}
	if st := taskStatus(t, a, id); st != "todo" {
		t.Fatalf("after a plain task next the task is %q, want todo", st)
	}
	out, err := run(t, a, opscoord.OpTaskNext, `{"claim":true}`, false)
	if err != nil || !strings.Contains(out, `"claimed":"`+id+`"`) {
		t.Fatalf("task next --claim: %v %s", err, out)
	}
	if st := taskStatus(t, a, id); st != "in_progress" {
		t.Fatalf("after task next --claim the task is %q, want in_progress", st)
	}
	if _, err := run(t, a, opscoord.OpTaskRelease, `{"id":"`+id+`"}`, false); err != nil {
		t.Fatal(err)
	}
	if st := taskStatus(t, a, id); st != "todo" {
		t.Fatalf("after claim+release the task is %q, want todo", st)
	}
}
