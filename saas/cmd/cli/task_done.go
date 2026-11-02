// task_done.go — `lore task done` and `lore task cancel` for lore for teams
// (LORE_TEAM_SPEC F7 table, F9 "record", §7.4 "No prompts"): a done on a
// claimed task is a fenced holder write pushed atomically with the signals
// ref; a cancel of a held task is a human override that fences the holder
// out (E_CANCELLED). Unclaimed tasks keep the plain row path.
package main

import (
	"context"
	"fmt"
	"time"

	entTask "dbent/gen/ent/task"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/security"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/signals/coord"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"

	"github.com/spf13/cobra"
)

// Flags of `task done` (§1 "Flags"); --commit is org_cmd.go's flagCommit.
const (
	flagPR       = "pr"
	flagLesson   = "lesson"
	flagNoLesson = "no-lesson"
)

// Memory source kinds for rows coordination creates, so views can find
// them (inbox, mission status).
const (
	sourceKindLesson  = "lesson"
	sourceKindFinding = "finding"
)

// taskDoneFlags are the flags of `task done`.
type taskDoneFlags struct {
	commonFlags
	commit   string
	pr       int
	lesson   string
	noLesson bool
	from     string
}

func newTaskDoneCommand() *cobra.Command {
	f := &taskDoneFlags{}
	cmd := &cobra.Command{
		Use:   "done <id>",
		Short: "Mark a task done (with the code that finished it)",
		Long: `Done finishes a task. On a task you hold, it is fenced: your claim token must still
be on the signals ref (else exit 12/13/14) and the row change is pushed atomically
with the claim's removal. --commit/--pr record the code so lore can tell when it
merged (F9). Never prompts: --lesson saves a lesson, --no-lesson skips the reminder.

Examples:
  lore task done tsk_30 --pr 412 --lesson "invite tokens expire in 48h"
  lore task done tsk_31 --commit 9c3e1f0 --no-lesson`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskDone(cmd.Context(), f, args[0])
		},
	}
	bindCommonFlags(cmd, &f.commonFlags)
	cmd.Flags().StringVar(&f.commit, flagCommit, "", "the commit that finished the task")
	cmd.Flags().IntVar(&f.pr, flagPR, 0, "the pull request that finished the task")
	cmd.Flags().StringVar(&f.lesson, flagLesson, "", "save a lesson learned (a memory pointing at the task)")
	cmd.Flags().BoolVar(&f.noLesson, flagNoLesson, false, "skip the lesson reminder")
	bindFromFlag(cmd, &f.from)
	return cmd
}

// claimState is what the signals ref and this session know about one task.
type claimState struct {
	sig     signals.Signal
	claimed bool
	active  bool
	mine    bool
	// hadClaim: this session claimed the task before (held record).
	hadClaim bool
}

// readClaim reads the task's claim as this reader sees it (fetching when
// online; the last fetched copy offline).
func readClaim(ctx context.Context, w *coordWorld, t coordTarget, id string) (claimState, error) {
	v, err := t.Signals.Read(ctx)
	if err != nil {
		return claimState{}, err
	}
	var cs claimState
	p := signals.ClaimPath(t.Folder, id)
	if s, ok := v.Board[p]; ok {
		_, active := v.Active(p)
		cs = claimState{sig: s, claimed: true, active: active, mine: s.Holder == w.ActorID && s.Session == w.Session()}
	}
	_, cs.hadClaim = w.held().Get(w.Session(), id)
	return cs, nil
}

func runTaskDone(ctx context.Context, f *taskDoneFlags, id string) error {
	if err := refuseIfReadOnly(&f.commonFlags); err != nil {
		return err
	}
	if f.pr < 0 {
		return errcodes.New(errcodes.InvalidInput, "--pr must be a positive number")
	}
	w, t, coordErr := openTaskCoord(ctx, id, f.from)
	if coordErr == nil {
		defer t.Close()
		cs, err := readClaim(ctx, w, t, id)
		if err != nil {
			return coordError(err)
		}
		switch {
		case cs.mine || (cs.hadClaim && !cs.mine):
			// The holder's done (or a former holder's, which fencing refuses).
			commit := f.commit
			if commit == "" {
				commit = workBranchHead(ctx, w)
			}
			if _, err := coord.HolderWrite(ctx, w.env(t), id, true, func(d coord.Doc, now time.Time) ([]coord.RowWrite, error) {
				return nil, coord.MarkDone(w.ActorID, commit, f.pr)(d, now)
			}); err != nil {
				return coordError(err)
			}
			return finishDone(ctx, f, id)
		case cs.claimed && cs.active:
			return errcodes.New(errcodes.InvalidInput, fmt.Sprintf("%s is held by %s until %s; only the holder can finish it",
				id, holderLabel(cs.sig), cs.sig.Expires.UTC().Format(time.Kitchen))).
				WithHint("a person can `lore task revoke " + id + "` first")
		case cs.claimed:
			// An expired claim: finishing it is a human override that
			// fences the old holder out.
			if err := requireHuman(w, "finishing a task someone else claimed"); err != nil {
				return err
			}
			if _, err := coord.Override(ctx, w.env(t), id, coord.OverrideRevoke, func(d coord.Doc) {
				_ = coord.MarkDone(w.ActorID, f.commit, f.pr)(d, w.Clock.Now())
			}); err != nil {
				return coordError(err)
			}
			return finishDone(ctx, f, id)
		}
	}
	// Unclaimed: a task of the store project is written there; one of this
	// repo goes through lore.db like any row.
	if coordErr == nil && isStoreTarget(t) {
		if _, err := coord.Update(ctx, w.env(t), id, func(d coord.Doc, now time.Time) error {
			return coord.MarkDone(w.ActorID, f.commit, f.pr)(d, now)
		}); err != nil {
			return coordError(err)
		}
		return finishDone(ctx, f, id)
	}
	if err := plainDone(ctx, f, id); err != nil {
		return err
	}
	return finishDone(ctx, f, id)
}

// workBranchHead is the commit a holder's work ends at when it runs done on
// its work branch without --commit: recorded as done_commit so a merge of
// that branch is found by ancestry (F9 check 1) — the only check for a
// store-only task in a client repo, which never gets a Lore-Task trailer
// (Q13). "" when not on a work branch.
func workBranchHead(ctx context.Context, w *coordWorld) string {
	if currentWorkBranch(ctx, w.Repo) == "" {
		return ""
	}
	oid, ok, err := w.Repo.ResolveCommit(ctx, "HEAD")
	if err != nil || !ok {
		return ""
	}
	return oid
}

// isStoreTarget reports whether t coordinates a store project's rows (not
// this repo's lore.db).
func isStoreTarget(t coordTarget) bool {
	_, ok := t.Rows.(*storeRows)
	return ok
}

// plainDone marks an unclaimed task done through lore.db (it then travels
// like any row write: live by default for tasks).
func plainDone(ctx context.Context, f *taskDoneFlags, idArg string) error {
	rctx, client, err := resolveContext(&f.commonFlags)
	if err != nil {
		return err
	}
	defer client.Close()
	projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
	if err != nil {
		return err
	}
	t, err := lookupTask(ctx, client, projectID, idArg)
	if err != nil {
		return err
	}
	by, err := resolveCurrentActorID(ctx, client)
	if err != nil {
		return err
	}
	upd := client.Task.UpdateOne(t).SetStatus(entTask.StatusDone).SetCompletedAt(cliClock().Now()).
		SetCommitment(entTask.CommitmentAccepted).ClearDeferredUntil().SetDoneBy(by)
	if f.commit != "" {
		upd.SetDoneCommit(f.commit)
	}
	if f.pr > 0 {
		upd.SetDonePr(f.pr)
	}
	if _, err := upd.Save(ctx); err != nil {
		return errcodes.New(errcodes.Internal, "update task").WithCause(err)
	}
	return nil
}

// finishDone prints the outcome and saves the lesson.
func finishDone(ctx context.Context, f *taskDoneFlags, id string) error {
	code := ""
	switch {
	case f.pr > 0:
		code = fmt.Sprintf(" (pr #%d); waiting for merge", f.pr)
	case f.commit != "":
		code = fmt.Sprintf(" (commit %s); waiting for merge", shortSHA(f.commit))
	}
	fmt.Printf("%s %s done%s\n", style.Success("✓"), id, code)
	if f.lesson != "" {
		memID, err := addLinkedMemory(ctx, &f.commonFlags, f.lesson, sourceKindLesson, id)
		if err != nil {
			return err
		}
		fmt.Printf("  %s → %s\n", sourceKindLesson, memID)
		return nil
	}
	if !f.noLesson {
		fmt.Println(style.Muted("  learned something? `lore memory add --body \"…\"` (or pass --lesson next time)"))
	}
	return nil
}

// addLinkedMemory saves a memory whose source points at a task or mission
// (a lesson, a finding). The body is secret-scanned like `memory add`.
func addLinkedMemory(ctx context.Context, c *commonFlags, text, sourceKind, sourceRef string) (string, error) {
	body, err := textnorm.Normalize(text)
	if err != nil {
		return "", errcodes.New(errcodes.EmptyBody, err.Error())
	}
	if err := refuseSecrets(body); err != nil {
		return "", err
	}
	// The memory lives where the task or mission it points at lives (a
	// finding "inherits the mission's project", §7.2): never in a client
	// repo for work coordinated in the store (D8).
	to, err := rowHomeTo(ctx, sourceRef)
	if err != nil {
		return "", err
	}
	if _, err := routeWrite(ctx, c, to, kindMemory, body); err != nil {
		return "", err
	}
	rctx, client, err := resolveContext(c)
	if err != nil {
		return "", err
	}
	defer client.Close()
	projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
	if err != nil {
		return "", err
	}
	by, err := resolveCurrentActorID(ctx, client)
	if err != nil {
		return "", err
	}
	m, err := client.Memory.Create().SetProjectID(projectID).SetBody(body).SetKind(memoryKind("")).
		SetSourceKind(sourceKind).SetSourceRef(sourceRef).SetCreatedByActorID(by).Save(ctx)
	if err != nil {
		return "", errcodes.New(errcodes.Internal, "create memory").WithCause(err)
	}
	return m.ID, nil
}

func newTaskCancelCommand() *cobra.Command {
	var f commonFlags
	var from string
	cmd := &cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel a task (a held task's holder is fenced out: E_CANCELLED)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id := args[0]
			if w, t, err := openTaskCoord(ctx, id, from); err == nil {
				defer t.Close()
				cs, err := readClaim(ctx, w, t, id)
				if err != nil {
					return coordError(err)
				}
				if cs.claimed {
					if err := requireHuman(w, "cancelling a claimed task"); err != nil {
						return err
					}
					if _, err := coord.Override(ctx, w.env(t), id, coord.OverrideCancel, func(d coord.Doc) {
						d["status"] = string(entTask.StatusCancelled)
					}); err != nil {
						return coordError(err)
					}
					fmt.Printf("%s %s cancelled (claim by %s dropped)\n", style.Success("✓"), id, holderLabel(cs.sig))
					return nil
				}
			}
			if w, t, err := openTaskCoord(ctx, id, from); err == nil && isStoreTarget(t) {
				defer t.Close()
				if _, err := coord.Update(ctx, w.env(t), id, func(d coord.Doc, _ time.Time) error {
					d["status"] = string(entTask.StatusCancelled)
					return nil
				}); err != nil {
					return coordError(err)
				}
				fmt.Printf("%s %s cancelled\n", style.Success("✓"), id)
				return nil
			}
			return updateTaskStatus(ctx, &f, id, entTask.StatusCancelled, "cancelled")
		},
	}
	bindCommonFlags(cmd, &f)
	bindFromFlag(cmd, &from)
	return cmd
}

// kindMemory is the routeWrite kind of lessons and findings.
const kindMemory = "memory"

// rowHomeTo is the --to that addresses the project holding row id: this
// repo when the row is one of its own (live or branch data), else the
// linked store project (group work, and a client repo's own task unless
// the org sets agent_details: client — lessons are agent work details, D8).
// A repo without links is always "repo".
func rowHomeTo(ctx context.Context, id string) (toFlags, error) {
	w, err := openCoordWorld(ctx)
	if err != nil || len(w.Links) == 0 {
		return toFlags{To: toRepo}, nil
	}
	if t, err := openRepoTarget(ctx, w); err == nil {
		_, found, _ := t.Rows.Get(ctx, id)
		t.Close()
		internal := w.Role == orgresolve.RoleClient && (w.Org == nil || w.Org.EffectiveAgentDetails() != lorecfg.AgentDetailsClient)
		if found && !internal {
			return toFlags{To: toRepo}, nil
		}
	}
	link, err := defaultLink(w.Links)
	if err != nil {
		return toFlags{}, err
	}
	return toFlags{To: w.Org.Name + "/" + link.Folder}, nil
}

// toRepo is the --to value for this repo's own project.
const toRepo = "repo"

// refuseSecrets refuses text holding a credential pattern (as `memory add`
// does without --allow-secrets).
func refuseSecrets(body string) error {
	if m := security.NewScanner().Scan(body); len(m) > 0 {
		return errcodes.New(errcodes.SecretDetected, fmt.Sprintf("text contains a credential pattern: %s (preview: %s)", m[0].PatternName, m[0].Preview)).
			WithHint("remove the secret; lore never stores credentials in shared rows")
	}
	return nil
}
