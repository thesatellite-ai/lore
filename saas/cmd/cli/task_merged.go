// task_merged.go — done → merged (LORE_TEAM_SPEC F9, TEAM-017): the lazy
// merged check run by the views that need it (task list, task next) and
// `lore task merged <id>` for merges lore cannot see.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"dbent/gen/ent"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/mergewatch"
	"saas/pkg/aicoder/signals/coord"
	"saas/pkg/aicoder/style"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
	"github.com/thesatellite-ai/lore/schema"
)

// mergedTarget is the fetched default branch the check reads.
func mergedTarget(ctx context.Context, r *gitref.Repo, remote string) string {
	return "refs/remotes/" + remote + "/" + defaultBranchName(ctx, r)
}

// lazyMergedCheck looks for the merge of every done task without merged_at
// that belongs to this checkout's repo (or any task when an adapter is
// set), and records merged_at once for each found (F9 "check"). It writes
// nothing when nothing is found (principle 1) and never fails the command:
// a broken check is a warning.
//
// Where the stamp goes (F9: "through the normal landing path"): when live
// rows apply here, the row is staged on the repo's live ref (recordMerged,
// the same path as a store task's stamp) and, being finished, lands on the
// default branch by itself. It never goes through lore.db's export into the
// checkout's .lore/data: that left every clone with its own uncommitted
// copy of the row (different updated_at per clone) which blocked the next
// `git pull` and never reached the other developers (trial finding F5).
// Only a checkout without live rows (rows travel with the code) writes the
// branch row, with a deterministic updated_at so two clones that find the
// same merge write the same bytes.
func lazyMergedCheck(ctx context.Context, projectID, repoID string) {
	cands := mergedCandidates(ctx, projectID)
	if len(cands) == 0 {
		return
	}
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	repo, err := gitref.Open(ctx, dir, nil)
	if err != nil {
		return
	}
	adapter, hasAdapter := mergeAdapter(ctx, dir)
	ck := mergewatch.Checker{Repo: repo, Target: mergedTarget(ctx, repo, defaultRemoteName), Adapter: adapter}
	var found []foundMerge
	for _, t := range cands {
		if !hasAdapter && t.RepoID != nil && repoID != "" && *t.RepoID != repoID {
			continue // done in another repo: checked there (or by the adapter)
		}
		c := mergewatch.Candidate{TaskID: t.ID, DoneCommit: deref(t.DoneCommit)}
		if t.DonePr != nil {
			c.DonePR = *t.DonePr
		}
		f, ok, _, err := ck.Check(ctx, c)
		if errors.Is(err, mergewatch.ErrNoTarget) {
			return
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, style.Warn("lore merged check: "+err.Error()))
			continue
		}
		if ok {
			found = append(found, foundMerge{task: t, finding: f})
		}
	}
	if len(found) == 0 {
		return
	}
	if liveFor(ctx, liveProjectRoot(dir)) == nil {
		recordMergedBranch(ctx, nil, found)
		return
	}
	recordMergedLive(ctx, found)
}

// mergedCandidates reads, through the core (tasks are core-owned), the done
// tasks of projectID with no merged_at yet. An unreadable core is no
// candidates: the check never fails the command.
func mergedCandidates(ctx context.Context, projectID string) []*ent.Task {
	docs, err := coreDocs(ctx, tableTasks)
	if err != nil {
		return nil
	}
	var out []*ent.Task
	for _, d := range docs {
		if d.Str(fieldProjectID) != projectID || d.Status() != schema.TaskStatusDone || d.Str(keyMergedAtDoc) != "" {
			continue
		}
		t, err := docInto[ent.Task](d)
		if err != nil {
			continue // a row this binary cannot read is skipped, not fatal
		}
		out = append(out, t)
	}
	return out
}

// foundMerge is one done task whose merge the check found.
type foundMerge struct {
	task    *ent.Task
	finding mergewatch.Finding
}

// recordMergedLive stamps merged_at on the repo's live ref (F9). Each row
// is re-read fresh first (live wins, then the newer of branch and main): a
// teammate who found the same merge first has already stamped it, and a
// second stamp would only repeat the same value. updated_at is left as it
// was, so every clone that records the same merge stages the same row and
// the landing pass finds nothing new to commit for the second one.
func recordMergedLive(ctx context.Context, found []foundMerge) {
	fail := func(err error) {
		for _, m := range found {
			reportMerged(os.Stderr, m.task.ID, m.finding, err)
		}
	}
	w, err := openCoordWorld(ctx)
	if err != nil {
		fail(fmt.Errorf("open coordination: %w", err))
		return
	}
	t, err := openRepoTarget(ctx, w)
	if err != nil {
		fail(fmt.Errorf("open the live ref: %w", err))
		return
	}
	defer t.Close()
	refresh := false
	for _, m := range found {
		ok, err := stampMerged(ctx, t.Rows, t.Signals, m.task.ID, m.finding.At)
		if ok || err != nil {
			reportMerged(os.Stderr, m.task.ID, m.finding, err)
		}
		// Recorded by us, or found already stamped by a teammate who got
		// there first: either way the fresh live ref now holds the stamp.
		refresh = refresh || err == nil
	}
	// The command that ran the check lists the tasks next (task list,
	// mission status): import the stamped live rows into lore.db now, so
	// the same output never says "merged" on one line and "not merged yet"
	// on the next. The import is a live apply (live wins), not a branch
	// write, so the checkout stays clean.
	if lr, ok := t.Rows.(*liveRows); ok && refresh {
		lr.s.apply(ctx, lr.db, lr.folder)
	}
}

// recordMergedBranch stamps merged_at on the task's row file through the
// core, for a checkout without live rows: the row is this branch's data and
// travels with the code.
//
// client is unused: tasks are core-owned, so the stamp always goes into the
// row file through the engine (kept for the existing test seam).
func recordMergedBranch(ctx context.Context, _ *ent.Client, found []foundMerge) {
	for _, m := range found {
		err := corePatchDoc(ctx, tableTasks, m.task.ID, coord.Doc{keyMergedAtDoc: m.finding.At.UTC().Format(time.RFC3339Nano)})
		if err != nil {
			err = fmt.Errorf("record %s: %w", m.task.ID, err)
		}
		reportMerged(os.Stderr, m.task.ID, m.finding, err)
	}
}

// mergedUpdatedAt is the updated_at a branch-row merged stamp writes: the
// later of the row's own and the merge time, a pure function of the row
// and the finding so two clones agree.
func mergedUpdatedAt(old, merged time.Time) time.Time {
	if merged.After(old) {
		return merged.UTC()
	}
	return old.UTC()
}

// mergeAdapter returns the configured merge_check adapter (F9 check 3).
func mergeAdapter(ctx context.Context, dir string) (mergewatch.Adapter, bool) {
	sit, err := gatherOrgSituation(ctx, dir)
	if err != nil {
		return nil, false
	}
	if sit.Config.MergeCheckFor(sit.Result.Org) == lorecfg.MergeCheckGitHub {
		return mergewatch.GitHub{Dir: dir}, true
	}
	return nil, false
}

func newTaskMergedCommand() *cobra.Command {
	var f commonFlags
	var from string
	cmd := &cobra.Command{
		Use:   "merged <id>",
		Short: "Mark a done task as merged by hand (when lore cannot see the merge)",
		Long: `Merged records merged_at for a done task whose merge lore cannot detect (a squash
template that drops the Lore-Task trailer, no recorded commit, no host adapter).
Dependents waiting for :merged become ready.

Example:
  lore task merged tsk_30`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			// A coordinated row (a store task, or this repo's own row when
			// live rows apply) is stamped on its live ref and lands from
			// there; writing it through lore.db would leave an uncommitted
			// .lore/data change in this checkout (trial finding F5).
			if w, t, err := openTaskCoord(ctx, args[0], from); err == nil {
				defer t.Close()
				if d, ok, gerr := t.Rows.Get(ctx, args[0]); gerr == nil && ok && d.Status() == schema.TaskStatusDone {
					if prev := d.Time(keyMergedAtDoc); !prev.IsZero() {
						fmt.Printf("%s %s was already merged at %s\n", style.Success("✓"), args[0], prev.UTC().Format("2006-01-02 15:04"))
						return nil
					}
				}
				at := w.Clock.Now().UTC()
				if _, err := coord.Update(ctx, w.env(t), args[0], func(d coord.Doc, _ time.Time) error {
					if d.Status() != schema.TaskStatusDone {
						return errcodes.New(errcodes.InvalidInput, fmt.Sprintf("%s is %s; only a done task can be merged", args[0], d.Status()))
					}
					if d.Str("merged_at") == "" {
						d.SetTime("merged_at", at)
					}
					return nil
				}); err != nil {
					return coordError(err)
				}
				fmt.Printf("%s %s merged (marked by hand, %s)\n", style.Success("✓"), args[0], at.Format("15:04"))
				return nil
			}
			t, err := lookupTask(ctx, args[0])
			if err != nil {
				return err
			}
			if t.Status() != schema.TaskStatusDone {
				return errcodes.New(errcodes.InvalidInput, fmt.Sprintf("%s is %s; only a done task can be merged", args[0], t.Status()))
			}
			if prev := t.Time(keyMergedAtDoc); !prev.IsZero() {
				fmt.Printf("%s %s was already merged at %s\n", style.Success("✓"), args[0], prev.UTC().Format("2006-01-02 15:04"))
				return nil
			}
			at := cliClock().Now().UTC()
			if err := corePatchDoc(ctx, tableTasks, args[0], coord.Doc{keyMergedAtDoc: at.Format(time.RFC3339Nano)}); err != nil {
				return err
			}
			fmt.Printf("%s %s merged (marked by hand, %s)\n", style.Success("✓"), args[0], at.Format("15:04"))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	bindFromFlag(cmd, &from)
	return cmd
}

// taskListMergedCheck is the merged check `lore task list` runs before it
// lists (v0.1.17's runTaskList: the lazy check for this checkout's repo,
// then the linked store group's). task list is a core command now, so the
// check is attached to it at mount time (wrapTaskListMergedCheck); without
// it a squash merge with a Lore-Task trailer or a merge found by ancestry
// was never recorded by the view that shows it (F9, E7). A read-only run
// (--read-only, LORE_READ_ONLY=1) records nothing. Never fails the command.
func taskListMergedCheck(ctx context.Context, readOnly bool) {
	if readOnly || os.Getenv(envReadOnly) == envValueOn {
		return
	}
	var f commonFlags
	rctx, client, err := resolveContext(&f)
	if err != nil {
		return
	}
	projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
	repoID := ""
	if err == nil {
		repoID, err = resolveRepoID(ctx, client, projectID, rctx.RepoMount)
	}
	_ = client.Close() // the check reads the core from here on; a close error is not actionable
	if err == nil {
		lazyMergedCheck(ctx, projectID, repoID)
	}
	groupMergedCheck(ctx)
}

// wrapTaskListMergedCheck runs taskListMergedCheck before the generated
// `lore task list` command (c).
func wrapTaskListMergedCheck(c *cobra.Command) {
	run := c.RunE
	c.RunE = func(cmd *cobra.Command, args []string) error {
		ro, _ := cmd.Flags().GetBool(constants.FlagReadOnly) // absent flag reads false
		taskListMergedCheck(cmd.Context(), ro)
		return run(cmd, args)
	}
}
