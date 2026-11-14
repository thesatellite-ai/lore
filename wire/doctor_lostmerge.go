package wire

// doctor_lostmerge.go — the "lost merge" note of `lore doctor` (devco
// O-12). A done task waits for its merge (F9): lore stamps merged_at when
// its done_commit reaches the default branch or a commit there carries its
// `Lore-Task:` trailer. A squash merge whose message dropped the trailer,
// followed by deleting the branch, defeats both checks for good: the done
// commit is on no branch any more and no trailer exists, so the task stayed
// "done" forever and no command said so. This check names each such task
// and the command that settles it (lore task merged --id <id>, docs
// tasks/link-commits.md Step 5).
//
//	DoctorLostMerges
//	  ├► repo tasks: status done, no merged_at, done_commit set   ── none: no git
//	  ├► git rev-list --branches --remotes (one call)  ─► reachable set
//	  ├► none left: done, else git log of every Lore-Task trailer (one call)
//	  └► per task whose done_commit is not in the set and that no commit
//	     on a branch carries a trailer for ─► one note naming the fix

import (
	"context"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/mergewatch"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// DoctorCheckLostMerge is the check name of the lost-merge notes.
const DoctorCheckLostMerge DoctorCheck = "lost_merge"

// lostMergeShort is how much of the done commit a note prints.
const lostMergeShort = 7

// DoctorLostMerges notes every done task of this repo whose merge lore can
// no longer find (see the file comment). A note, not a warning: the store
// is healthy, a person has one step to take. Nothing when the repo has no
// such task or is not a git checkout.
func (a *App) DoctorLostMerges(ctx context.Context) ([]DoctorFinding, error) {
	rows, err := a.List(ctx, store.SourceRepo, schema.TableTasks)
	if err != nil {
		return nil, err
	}
	type cand struct{ id, commit string }
	var cands []cand
	for _, r := range rows {
		if r.Deleted || !unmergedRow(r) {
			continue
		}
		if c := strings.ToLower(strings.TrimSpace(rowDocStr(r, fieldDoneCommit))); c != "" {
			cands = append(cands, cand{id: r.ID.String(), commit: c})
		}
	}
	if len(cands) == 0 {
		return nil, nil
	}
	top := gitTopLevel(ctx, a.projectDir())
	if top == "" {
		return nil, nil
	}
	reach, err := gitRun{dir: top, args: []string{"rev-list", "--branches", "--remotes"}}.output(ctx)
	if err != nil {
		return nil, nil // an empty repo (no branch yet) has nothing to lose
	}
	onBranch := map[string]bool{}
	for _, l := range strings.Fields(string(reach)) {
		onBranch[l] = true
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].id < cands[j].id })
	var lost []cand
	for _, c := range cands {
		if !reachable(onBranch, c.commit) {
			lost = append(lost, c)
		}
	}
	if len(lost) == 0 {
		return nil, nil
	}
	trailers := taskTrailers(ctx, top)
	var out []DoctorFinding
	for _, c := range lost {
		if trailers[c.id] {
			continue
		}
		out = append(out, DoctorFinding{Check: DoctorCheckLostMerge, Level: DoctorNote, Store: store.SourceRepo,
			Message: "task " + c.id + " is done, but its commit " + shortOf(c.commit) + " is on no branch and no commit on a branch carries its " + mergewatch.TrailerKey + " trailer (a squash merge that dropped the trailer, or a deleted branch), so lore cannot see it merge",
			Fix:     "if the work reached the default branch, run lore task merged --id " + c.id + "; if not, push its branch again"})
	}
	return out, nil
}

// reachable reports whether commit (a full or abbreviated sha) is in set.
func reachable(set map[string]bool, commit string) bool {
	if set[commit] {
		return true
	}
	if len(commit) >= 40 {
		return false
	}
	for oid := range set {
		if strings.HasPrefix(oid, commit) {
			return true
		}
	}
	return false
}

// taskTrailers is every task id a `Lore-Task: <id>` line names in a
// commit of any branch, read with one git log (B1-16: one git process per
// task took minutes in a repo with hundreds of done tasks). A task with a
// trailer is not lost: the lazy merged check stamps it once the commit
// reaches the default branch. A failed read returns nil, so every task
// whose commit is gone is named (a note, never a write).
func taskTrailers(ctx context.Context, dir string) map[string]bool {
	out, err := gitRun{dir: dir, args: []string{"log", "--branches", "--remotes", "--format=%B", "--extended-regexp", "--grep", "^" + mergewatch.TrailerKey + ": "}}.output(ctx)
	if err != nil {
		return nil
	}
	ids := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		if id, ok := strings.CutPrefix(strings.TrimSpace(l), mergewatch.TrailerKey+": "); ok && id != "" {
			ids[id] = true
		}
	}
	return ids
}

// shortOf is the first lostMergeShort characters of a sha.
func shortOf(s string) string {
	if len(s) > lostMergeShort {
		return s[:lostMergeShort]
	}
	return s
}
