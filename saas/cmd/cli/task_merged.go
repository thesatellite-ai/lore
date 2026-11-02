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
	entTask "dbent/gen/ent/task"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/mergewatch"
	"saas/pkg/aicoder/signals/coord"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
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
func lazyMergedCheck(ctx context.Context, client *ent.Client, projectID, repoID string) {
	cands, err := client.Task.Query().Where(entTask.ProjectID(projectID), entTask.StatusEQ(entTask.StatusDone), entTask.MergedAtIsNil()).All(ctx)
	if err != nil || len(cands) == 0 {
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
		if !ok {
			continue
		}
		if _, err := client.Task.UpdateOne(t).SetMergedAt(f.At).Save(ctx); err != nil {
			fmt.Fprintln(os.Stderr, style.Warn("lore merged check: record "+t.ID+": "+err.Error()))
			continue
		}
		fmt.Fprintf(os.Stderr, "%s %s merged %s (found by: %s, %s)\n", style.Success("✓"), t.ID, f.At.UTC().Format("15:04"), f.Method, shortSHA(f.Commit))
	}
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
			if w, t, err := openTaskCoord(ctx, args[0], from); err == nil && isStoreTarget(t) {
				defer t.Close()
				at := w.Clock.Now().UTC()
				if _, err := coord.Update(ctx, w.env(t), args[0], func(d coord.Doc, _ time.Time) error {
					if d.Status() != string(entTask.StatusDone) {
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
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
			if err != nil {
				return err
			}
			t, err := lookupTask(ctx, client, projectID, args[0])
			if err != nil {
				return err
			}
			if t.Status != entTask.StatusDone {
				return errcodes.New(errcodes.InvalidInput, fmt.Sprintf("%s is %s; only a done task can be merged", t.ID, t.Status))
			}
			if t.MergedAt != nil {
				fmt.Printf("%s %s was already merged at %s\n", style.Success("✓"), t.ID, t.MergedAt.UTC().Format("2006-01-02 15:04"))
				return nil
			}
			at := cliClock().Now().UTC()
			if _, err := client.Task.UpdateOne(t).SetMergedAt(at).Save(ctx); err != nil {
				return errcodes.New(errcodes.Internal, "record merged_at").WithCause(err)
			}
			fmt.Printf("%s %s merged (marked by hand, %s)\n", style.Success("✓"), t.ID, at.Format("15:04"))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	bindFromFlag(cmd, &from)
	return cmd
}
