// task_done.go — `lore task done` and `lore task cancel` for lore for teams
// (LORE_TEAM_SPEC F7 table, F9 "record", §7.4 "No prompts"): a done on a
// claimed task is a fenced holder write pushed atomically with the signals
// ref; a cancel of a held task is a human override that fences the holder
// out (E_CANCELLED). Unclaimed tasks keep the plain row path.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/signals/coord"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
	coreschema "github.com/thesatellite-ai/lore/schema"
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

// workBranchHead is the commit a holder's work ends at when it runs done on
// its work branch without --commit: recorded as done_commit so a merge of
// that branch is found by ancestry (F9 check 1) — the only check for a
// store-only task in a client repo, which never gets a Lore-Task trailer
// (Q13). "" when not on a work branch, and "" when HEAD is already on the
// fetched default branch: such a commit cannot be the unmerged work of the
// task being finished (a coordinator finishing a no-code task on a branch
// left over from another task), and recording it would mark the task merged
// at once, with a merge time before its done time (trial finding F17). The
// task then shows "done (merge unknown)" until `lore task merged`, as F9
// says for a task with no recorded commit.
func workBranchHead(ctx context.Context, r *gitref.Repo) string {
	if currentWorkBranch(ctx, r) == "" {
		return ""
	}
	oid, ok, err := r.ResolveCommit(ctx, "HEAD")
	if err != nil || !ok {
		return ""
	}
	target := mergedTarget(ctx, r, defaultRemoteName)
	tip, ok, err := r.ResolveRef(ctx, target)
	if err != nil || !ok {
		return "" // cannot tell whether HEAD is merged already: record nothing (fail closed)
	}
	merged, err := r.IsAncestor(ctx, oid, tip)
	if err != nil || merged {
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

// isNoTeamCoordination reports whether an openCoordWorld/openTaskCoord
// failure is one of the documented "this checkout has no team
// coordination" refusals, the only failures a task command may answer by
// using lore.db directly:
//   - E_UNSUPPORTED: not a git repository (openCoordWorld);
//   - E_NOT_IMPLEMENTED: no live rows here (errRowsNotWired: lore not
//     initialised in git, or LORE_LIVE=0);
//   - E_IDENTITY_FAILED: no roster actor (a solo person on the fallback
//     identity, requireActor).
//
// Whether the checkout is linked to a store is checked separately
// (plainRowFallback): the same codes from a linked checkout are refusals.
func isNoTeamCoordination(err error) bool {
	for _, c := range []errcodes.Code{errcodes.Unsupported, errcodes.NotImplemented, errcodes.IdentityFailed} {
		if errcodes.IsCode(err, c) {
			return true
		}
	}
	return false
}

// plainRowFallback decides whether a task command whose coordination
// could not be opened (coordErr) may write the task through this repo's
// lore.db instead. It may only when coordErr is a no-team refusal
// (isNoTeamCoordination) AND the org situation, read again, shows no
// store link and a known link status. Otherwise it returns the error to
// report — coordErr itself, or why the situation could not be read — and
// nothing is written (fixed rule 6: a linked client task or a store task
// is never changed behind the store's back).
func plainRowFallback(ctx context.Context, coordErr error) error {
	if !isNoTeamCoordination(coordErr) {
		return coordError(coordErr)
	}
	dir, err := os.Getwd()
	if err != nil {
		return errcodes.New(errcodes.Internal, "getwd").WithCause(err)
	}
	sit, err := gatherOrgSituation(ctx, dir)
	if err != nil {
		return err
	}
	if sit.ResolveErr != nil {
		return orgResolveCLIError(sit.ResolveErr)
	}
	if sit.Result.LinkUnknown {
		return errcodes.New(errcodes.LinkUnknown, "cannot tell whether this repo is a client repo; refusing to write the task here").
			WithHint("`lore whoami` shows the link status; fetch the store and retry")
	}
	if len(sit.Result.Links) > 0 {
		return coordError(coordErr)
	}
	return nil
}

// toRepo is the --to value for this repo's own project.
const toRepo = "repo"

// refuseSecrets refuses text holding a credential pattern (as `memory add`
// does without --allow-secrets). Flow:
//
//	core mounted → coreApp.OutputScanner: the engine's write-time rule set
//	               (defaults + every attached store's secret_rules rows,
//	               a personal off included: this is the person's own
//	               capture, BATCH 8) + the unmigrated pii-patterns.txt
//	no core      → the v0.1.17 scanner (unit tests without a core)
//
// Refuses on any finding whatever secrets.on_detect says: this is the
// pre-check of a text the old commands then write; the message carries
// only the rule id and the masked preview, never the value.
func refuseSecrets(body string) error {
	// no ctx on this signature (task_note.go callers): the scanner build
	// reads row files only and is short
	return refuseSecretsCtx(context.Background(), body)
}

// refuseSecretsCtx is refuseSecrets with the caller's context.
func refuseSecretsCtx(ctx context.Context, body string) error {
	if coreApp == nil {
		scanner, err := secretScanner()
		if err != nil {
			return err
		}
		if m := scanner.Scan(body); len(m) > 0 {
			return secretRefusal(m[0].PatternName, m[0].Preview)
		}
		return nil
	}
	user, err := userPatterns() // a bad line refuses (fail closed)
	if err != nil {
		return err
	}
	sc, err := coreApp.OutputScanner(ctx)
	if err != nil {
		return err
	}
	if user != nil {
		sc = bothScanners{sc, user}
	}
	if f := sc.Scan("body", body); len(f) > 0 {
		return secretRefusal(f[0].RuleID, f[0].Preview)
	}
	return nil
}

// secretRefusal is the E_SECRET_DETECTED of refuseSecrets.
func secretRefusal(rule, preview string) error {
	return errcodes.New(errcodes.SecretDetected, fmt.Sprintf("text contains a credential pattern: %s (preview: %s)", rule, preview)).
		WithHint("remove the secret; lore never stores credentials in shared rows")
}

// guardTaskDone wraps the core's `lore task done` (ops/task) with v0.1.17's
// claim rules (LORE_TEAM_SPEC F7): the holder's done is a fenced holder
// write on the signals ref (its row change goes through the core,
// task_corerows.go) and drops the claim; a task held by someone else is
// refused; an expired claim needs a person, whose done fences the old
// holder out. The core op then records the done fields, prints and saves
// the lesson. Without team coordination (plainRowFallback) the core op
// runs alone, as v0.1.17's plain path did.
func guardTaskDone(run func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return run(cmd, args)
		}
		ctx, id := cmd.Context(), args[0]
		commit, _ := cmd.Flags().GetString(flagCommit)
		pr, _ := cmd.Flags().GetInt(flagPR)
		w, t, coordErr := openTaskCoord(ctx, id, "")
		if coordErr != nil {
			if err := plainRowFallback(ctx, coordErr); err != nil {
				return err
			}
			return run(cmd, args)
		}
		defer t.Close()
		cs, err := readClaim(ctx, w, t, id)
		if err != nil {
			return coordError(err)
		}
		switch {
		case cs.mine || (cs.hadClaim && !cs.mine):
			if commit == "" {
				if commit = workBranchHead(ctx, w.Repo); commit != "" {
					_ = cmd.Flags().Set(flagCommit, commit) // say which commit was recorded
				}
			}
			if _, err := coord.HolderWrite(ctx, w.env(t), id, true, func(d coord.Doc, now time.Time) ([]coord.RowWrite, error) {
				return nil, coord.MarkDone(w.ActorID, commit, pr)(d, now)
			}); err != nil {
				return coordError(err)
			}
		case cs.claimed && cs.active:
			return errcodes.New(errcodes.InvalidInput, fmt.Sprintf("%s is held by %s until %s; only the holder can finish it",
				id, holderLabel(cs.sig), cs.sig.Expires.UTC().Format(time.Kitchen))).
				WithHint("a person can `lore task revoke " + id + "` first")
		case cs.claimed:
			if err := requireHuman(w, "finishing a task someone else claimed"); err != nil {
				return err
			}
			if _, err := coord.OverrideChecked(ctx, w.env(t), id, coord.OverrideRevoke, coord.MarkDone(w.ActorID, commit, pr)); err != nil {
				return coordError(err)
			}
		}
		return run(cmd, args)
	}
}

// guardTaskCancel wraps the core's `lore task cancel` with v0.1.17's rule:
// cancelling a claimed task is a human override that fences the holder out
// (E_CANCELLED); an unclaimed task is cancelled by the core op alone.
func guardTaskCancel(run func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return run(cmd, args)
		}
		ctx, id := cmd.Context(), args[0]
		w, t, coordErr := openTaskCoord(ctx, id, "")
		if coordErr != nil {
			if err := plainRowFallback(ctx, coordErr); err != nil {
				return err
			}
			return run(cmd, args)
		}
		defer t.Close()
		cs, err := readClaim(ctx, w, t, id)
		if err != nil {
			return coordError(err)
		}
		if !cs.claimed {
			return run(cmd, args)
		}
		if err := requireHuman(w, "cancelling a claimed task"); err != nil {
			return err
		}
		if _, err := coord.Override(ctx, w.env(t), id, coord.OverrideCancel, func(d coord.Doc) {
			d["status"] = coreschema.TaskStatusCancelled
		}); err != nil {
			return coordError(err)
		}
		fmt.Printf("%s %s cancelled (claim by %s dropped)\n", style.Success("✓"), id, holderLabel(cs.sig))
		return nil
	}
}

// guardTaskCoreCommands installs guardTaskDone / guardTaskCancel on the
// core-mounted `lore task done|cancel` under root (core_mount.go calls it
// after cli.Mount).
func guardTaskCoreCommands(root *cobra.Command) {
	for name, guard := range map[string]func(func(*cobra.Command, []string) error) func(*cobra.Command, []string) error{
		"done": guardTaskDone, "cancel": guardTaskCancel,
	} {
		c, _, err := root.Find([]string{"task", name})
		if err != nil || c.Name() != name || c.RunE == nil {
			continue
		}
		c.RunE = guard(c.RunE)
	}
	withCLITeam(root)
}
