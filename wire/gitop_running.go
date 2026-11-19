package wire

// gitop_running.go — devco:Q-15: a git operation a person has stopped in
// the middle of (a rebase or merge waiting on a conflict, a cherry-pick,
// a revert, a bisect) in a project checkout. The hooks already change no
// tracked file then; `lore sync` and `lore render` used to rewrite the
// tracked .lore/LORE.md, so `git rebase --continue` refused with git's
// "You must edit all merge conflicts" and `git merge --abort` left the
// file dirty. Both now ask GitOpRunning first and leave tracked files as
// they are, naming the operation and what to run after it.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// GitOp is a git operation stopped in a work tree: its name as a person
// says it ("rebase") and the commands that end it.
type GitOp struct {
	// Name is "rebase", "merge", "cherry-pick", "revert" or "bisect".
	Name string
	// End is the git command line(s) that finish or abandon it.
	End string
}

// gitOpMarkers maps the file or folder git keeps in the (worktree's) git
// dir to the operation it means, in the order they are checked.
var gitOpMarkers = []struct {
	path string
	op   GitOp
}{
	{"rebase-merge", GitOp{"rebase", "git rebase --continue (or git rebase --abort)"}},
	{"rebase-apply", GitOp{"rebase", "git rebase --continue (or git rebase --abort)"}},
	{"MERGE_HEAD", GitOp{"merge", "git commit (or git merge --abort)"}},
	{"CHERRY_PICK_HEAD", GitOp{"cherry-pick", "git cherry-pick --continue (or git cherry-pick --abort)"}},
	{"REVERT_HEAD", GitOp{"revert", "git revert --continue (or git revert --abort)"}},
	{"sequencer", GitOp{"cherry-pick", "git cherry-pick --continue (or git cherry-pick --abort)"}},
	{"BISECT_LOG", GitOp{"bisect", "git bisect reset"}},
}

// GitOpRunning reports the git operation stopped in the work tree holding
// dir (ok false when none, or dir is not a git checkout). One git call:
// the markers live in the worktree's own git dir.
func GitOpRunning(ctx context.Context, dir string) (op GitOp, ok bool) {
	out, err := gitRun{dir: dir, args: []string{"rev-parse", "--absolute-git-dir"}}.output(ctx)
	if err != nil {
		return GitOp{}, false
	}
	gd := strings.TrimSpace(string(out))
	if gd == "" {
		return GitOp{}, false
	}
	for _, m := range gitOpMarkers {
		if _, serr := os.Stat(filepath.Join(gd, m.path)); serr == nil {
			return m.op, true
		}
	}
	return GitOp{}, false
}
