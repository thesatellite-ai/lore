package trash_test

// born_test.go — trash.add --born-branch and trash.list --born-branch
// (D-392): rows are found by their provenance branch.

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/ops/trash"
)

// git runs git in the harness repo with a made-up identity.
func git(t *testing.T, h *opstest.H, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Test Person", "-c", "user.email=test@example.test"}, args...)...)
	cmd.Dir = h.Repo
	cmd.Env = append(os.Environ(), "LORE_LAYER=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// bornWorld adds a memory and a task on feat/x and a memory on main.
func bornWorld(t *testing.T) (h *opstest.H, onX, taskX, onMain string) {
	t.Helper()
	t.Setenv("LORE_LAYER", "off") // the shared notes: this test is about provenance only
	h = opstest.New(t)
	git(t, h, "commit", "-q", "--allow-empty", "-m", "start")
	git(t, h, "switch", "-q", "-c", "feat/x")
	// PROV8: a code repo's rows keep a branch name only when the branch
	// has an upstream, so feat/x gets one (a bare remote on disk)
	remote := t.TempDir()
	git(t, h, "init", "-q", "--bare", remote)
	git(t, h, "remote", "add", "origin", remote)
	git(t, h, "push", "-q", "-u", "origin", "feat/x")
	h = h.Reopen() // the App reads HEAD once per open, like one lore command
	onX = h.ID(h.Must(memory.OpAdd, `{"body":"made on x"}`))
	taskX = h.ID(h.Must(task.OpAdd, `{"title":"task made on x"}`))
	git(t, h, "switch", "-q", "-")
	h = h.Reopen()
	onMain = h.ID(h.Must(memory.OpAdd, `{"body":"made on main"}`))
	return h, onX, taskX, onMain
}

func addOf(t *testing.T, h *opstest.H, input string, yes bool) trash.AddOut {
	t.Helper()
	run := h.Must
	if yes {
		run = h.MustYes
	}
	var out trash.AddOut
	if err := json.Unmarshal([]byte(h.JSON(run(trash.OpAdd, input))), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTrashAddBornBranch(t *testing.T) {
	h, onX, taskX, onMain := bornWorld(t)
	dry := addOf(t, h, `{"born_branch":"feat/x","dry_run":true}`, false)
	// the memory, the task and the default task list made for it
	if dry.Count != 3 || !dry.DryRun || len(dry.Kinds) != 3 || len(dry.Kept) != 0 {
		t.Fatalf("dry run = %+v, want the memory, the task and its task list in three kinds", dry)
	}
	if inTrash(t, h, onX) {
		t.Fatal("a dry run moved a row")
	}
	h.Fails(trash.OpAdd, `{"born_branch":"feat/x"}`, errs.NeedsConfirm)
	out := addOf(t, h, `{"born_branch":"feat/x","kind":["memory"]}`, true)
	if out.Count != 1 || out.Rows[0].ID != onX || !strings.HasPrefix(out.Batch, "bat_") {
		t.Fatalf("add = %+v, want only %s in one batch", out, onX)
	}
	if !inTrash(t, h, onX) || inTrash(t, h, onMain) || inTrash(t, h, taskX) {
		t.Fatal("trash add --born-branch --kind memory moved the wrong rows")
	}
	for _, it := range listOf(t, h, `{"born_branch":"feat/x"}`).Data {
		if it.ID != onX {
			t.Fatalf("trash list --born-branch feat/x lists %s", it.ID)
		}
	}
	h.Must(trash.OpRestore, `{"target":`+opstest.Quote(onX)+`}`)
	if inTrash(t, h, onX) {
		t.Fatal("trash restore did not bring the row back")
	}
}

func TestTrashAddBornBranchRefusals(t *testing.T) {
	h, _, _, _ := bornWorld(t)
	h.Fails(trash.OpAdd, `{}`, errs.InvalidInput)
	h.Fails(trash.OpAdd, `{"born_branch":"feat x"}`, errs.InvalidInput)
	h.Fails(trash.OpAdd, `{"born_branch":"feat/x","kind":["actor"]}`, errs.InvalidInput)
	if out := addOf(t, h, `{"born_branch":"feat/none"}`, true); out.Count != 0 || out.Batch != "" {
		t.Fatalf("no rows born on feat/none, got %+v", out)
	}
}

func TestBornBranchListFilter(t *testing.T) {
	h, onX, taskX, onMain := bornWorld(t)
	got := opstest.Text(h.Must(memory.OpList, `{"born_branch":"feat/x"}`))
	if !strings.Contains(got, onX) || strings.Contains(got, onMain) {
		t.Fatalf("memory list --born-branch feat/x:\n%s", got)
	}
	got = opstest.Text(h.Must(task.OpList, `{"born_branch":"feat/x"}`))
	if !strings.Contains(got, "task made on x") {
		t.Fatalf("task list --born-branch feat/x:\n%s (want %s)", got, taskX)
	}
	if got = opstest.Text(h.Must(task.OpList, `{"born_branch":"feat/none"}`)); strings.Contains(got, "task made on x") {
		t.Fatalf("task list --born-branch feat/none lists the feat/x task:\n%s", got)
	}
	h.Fails(memory.OpList, `{"born_branch":"a:b"}`, errs.InvalidInput)
}

// TestTrashAddBornBranchKeepsShared: a task list made on the branch that
// main's tasks now use stays; trashing it would take main's task.
func TestTrashAddBornBranchKeepsShared(t *testing.T) {
	h, _, taskX, _ := bornWorld(t)
	mainTask := h.ID(h.Must(task.OpAdd, `{"title":"task made on main"}`))
	out := addOf(t, h, `{"born_branch":"feat/x"}`, true)
	if len(out.Kept) != 1 || out.Kept[0].Table != "task_lists" || !strings.Contains(out.Kept[0].Reason, mainTask) {
		t.Fatalf("kept = %+v, want the task list main's task %s uses", out.Kept, mainTask)
	}
	if !inTrash(t, h, taskX) || inTrash(t, h, mainTask) {
		t.Fatal("the branch's task should be in the trash and main's task live")
	}
}
