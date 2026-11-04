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

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/textnorm"
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
	// The memory is written by the 0.1.18 core (memory.add) into the repo
	// store. v0.1.17 routed it to the store holding the task or mission
	// (D8); old-store routing has no 0.1.18 home yet (phase 2 gap), so a
	// lesson on a store task is refused rather than written to the wrong
	// place.
	to, err := rowHomeTo(ctx, sourceRef)
	if err != nil {
		return "", err
	}
	if to.To != "" {
		return "", errcodes.New(errcodes.Unsupported, "a lesson or finding on a task that lives in a store cannot be saved yet").
			WithHint("save it in the store's repo with `lore memory add --to <store>`")
	}
	_ = c // the scope flags no longer apply: the core writes to the repo store
	return coreAddMemory(ctx, body, sourceKind, sourceRef)
}

// rowHomeTo is the --to that addresses the project holding row id: this
// repo when the row is one of its own (live or branch data), else the
// linked store project (group work, and a client repo's own task unless
// the org sets agent_details: client — lessons are agent work details, D8).
//
// When this checkout has no store links (or no team coordination at all)
// it returns no --to, so the ordinary F14 resolver decides exactly as for
// any write without --to: this repo for an own repo or no org, a refusal
// for an unlinked client repo or an unknown link status (fixed rule 6).
// Every other failure is returned: a row is never routed into the repo
// because lore could not tell where it belongs.
func rowHomeTo(ctx context.Context, id string) (toFlags, error) {
	w, err := openCoordWorld(ctx)
	if err != nil {
		if isNoTeamCoordination(err) {
			return toFlags{}, nil
		}
		return toFlags{}, fmt.Errorf("decide where the note on %s lives: %w", id, err)
	}
	if w.LinkUnknown || len(w.Links) == 0 {
		return toFlags{}, nil
	}
	t, err := openRepoTarget(ctx, w)
	switch {
	case err == nil:
		_, found, gerr := t.Rows.Get(ctx, id)
		t.Close()
		if gerr != nil {
			return toFlags{}, fmt.Errorf("decide where the note on %s lives: read the repo's row: %w", id, gerr)
		}
		internal := w.Role == orgresolve.RoleClient && (w.Org == nil || w.Org.EffectiveAgentDetails() != lorecfg.AgentDetailsClient)
		if found && !internal {
			return toFlags{To: toRepo}, nil
		}
	case errcodes.IsCode(err, errcodes.NotImplemented):
		// This checkout has no live rows (errRowsNotWired), so the row is
		// not one this repo coordinates: the linked store project, the
		// internal side, is its home.
	default:
		return toFlags{}, fmt.Errorf("decide where the note on %s lives: %w", id, err)
	}
	if w.Org == nil {
		return toFlags{}, errcodes.New(errcodes.LinkUnknown, "this repo has store links but no org resolved; refusing to choose where the note on "+id+" lives").
			WithHint("`lore whoami` shows the org and link status")
	}
	link, err := defaultLink(w.Links)
	if err != nil {
		return toFlags{}, err
	}
	return toFlags{To: w.Org.Name + "/" + link.Folder}, nil
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
// does without --allow-secrets).
func refuseSecrets(body string) error {
	scanner, err := secretScanner()
	if err != nil {
		return err
	}
	if m := scanner.Scan(body); len(m) > 0 {
		return errcodes.New(errcodes.SecretDetected, fmt.Sprintf("text contains a credential pattern: %s (preview: %s)", m[0].PatternName, m[0].Preview)).
			WithHint("remove the secret; lore never stores credentials in shared rows")
	}
	return nil
}
