// task_coord.go — `lore task claim|next|heartbeat|release|revoke|reassign`
// and the hidden `lore task hook commit-msg` (LORE_TEAM_SPEC F7, F9, §7.3,
// §7.4). The decisions live in saas/pkg/aicoder/signals (claims on the
// signals ref), .../signals/coord (the fenced task row) and .../queue (the
// ready queue); this file parses flags, opens the world and prints.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/mergewatch"
	"saas/pkg/aicoder/queue"
	"saas/pkg/aicoder/signals"
	"saas/pkg/aicoder/signals/coord"
	"saas/pkg/aicoder/style"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
	"github.com/thesatellite-ai/lore/logic/urgency"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// Flag names of the coordination subcommands (§1 "Flags").
const (
	flagOwns      = "owns"
	flagStrict    = "strict"
	flagDaemon    = "daemon"
	flagAssign    = "assign"
	flagFromActor = "from"
	flagMission   = "mission"
	flagRole      = "role"
)

// taskCoordCommands are the coordination subcommands added to `lore task`.
//
// They are mounted after wireLive ran, so each is wrapped here for the F1
// not-found explanation (the "sync peek" hint) v0.1.17 gave them.
func taskCoordCommands() []*cobra.Command {
	cmds := []*cobra.Command{
		newTaskClaimCommand(), newTaskNextCommand(), newTaskHeartbeatCommand(), newTaskReleaseCommand(),
		newTaskRevokeCommand(), newTaskReassignCommand(), newTaskHookCommand(), newTaskMergedCommand(), newTaskNoteCommand(),
	}
	for _, c := range cmds {
		wrapLeaves(c)
	}
	return cmds
}

// openTaskCoord opens the world and the target of one task, refusing
// callers that are not an agent or a person.
func openTaskCoord(ctx context.Context, id, from string) (*coordWorld, coordTarget, error) {
	w, err := openCoordWorld(ctx)
	if err != nil {
		return nil, coordTarget{}, err
	}
	if err = w.requireActor(); err != nil {
		return nil, coordTarget{}, err
	}
	t, err := openCoordTargetFrom(ctx, w, id, from)
	if err != nil {
		return nil, coordTarget{}, err
	}
	return w, t, nil
}

// flagFromSource is `--from <source>` (fixed rule 5): repo | <org> |
// <org>/<folder>; picks the source of an id found in several, or one the
// normal lookup does not reach (another org entry such as staging).
const flagFromSource = "from"

// bindFromFlag adds --from to a command that takes a task id.
func bindFromFlag(cmd *cobra.Command, from *string) {
	cmd.Flags().StringVar(from, flagFromSource, "", "the source of the id: repo | <org> | <org>/<folder>")
}

// currentWorkBranch is the checked-out branch when it is a work branch
// (not detached, not the default branch), else "".
func currentWorkBranch(ctx context.Context, r *gitref.Repo) string {
	out, err := r.Run(ctx, gitref.Invocation{Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}})
	if err != nil {
		return ""
	}
	b := strings.TrimSpace(string(out))
	if b == "" || b == defaultBranchName(ctx, r) {
		return ""
	}
	return b
}

// defaultBranchName reads origin/HEAD (fixed rule 1), falling back to main.
func defaultBranchName(ctx context.Context, r *gitref.Repo) string {
	out, err := r.Run(ctx, gitref.Invocation{Args: []string{"symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"}})
	if err == nil {
		if b := strings.TrimPrefix(strings.TrimSpace(string(out)), "origin/"); b != "" {
			return b
		}
	}
	return fallbackDefaultBranch
}

func printClaim(out io.Writer, id string, cr coord.ClaimResult, w *coordWorld, ref string) {
	s := cr.Plan.Signal
	if cr.Plan.Resumed {
		_, _ = fmt.Fprintf(out, "you hold %s already (until %s); resume, or `lore task release %s`\n", id, s.Expires.UTC().Format(time.Kitchen), id)
		return
	}
	extra := ""
	if cr.Plan.Reclaim && cr.Plan.Prev != nil {
		extra = fmt.Sprintf("; reclaim, attempt %d; previous holder %s timed out", cr.Plan.Attempts, holderLabel(*cr.Plan.Prev))
	}
	title := ""
	if cr.Doc != nil {
		title = " " + cr.Doc.Str("title")
	}
	_, _ = fmt.Fprintf(out, "%s claimed %s%s (lease %s, token %s…) on %s%s\n", style.Success("✓"), id, title,
		shortDuration(w.Lease.TTL), s.ClaimToken[:min(len(s.ClaimToken), coord.TokenPrefixLen)], ref, extra)
	if s.WorkBranch != "" {
		_, _ = fmt.Fprintf(out, "  work branch %s\n", s.WorkBranch)
	}
	for _, o := range cr.Overlaps {
		_, _ = fmt.Fprintf(out, "%s file ownership overlaps: %s\n", style.Warn("!"), o)
	}
}

// shortDuration prints 20m instead of 20m0s.
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// flagCoordinate is `claim --coordinate` (TASK 6.9).
const flagCoordinate = "coordinate"

func newTaskClaimCommand() *cobra.Command {
	var owns []string
	var strict, coordinate bool
	var from string
	cmd := &cobra.Command{
		Use:   "claim <task-or-mission-id>",
		Short: "Claim a task (or coordinate a mission) on the signals ref, with a lease",
		Long: `Claim takes a lease on a task with a unique token, pushed with a compare-and-swap
on refs/lore/signals. Exactly one session holds a task at a time; renew it with
'lore task heartbeat'. Fails closed (exit 15, E_OFFLINE) when the push cannot be confirmed.

Examples:
  lore task claim tsk_31 --owns src/routes/invite/
  lore task claim mis_12          # a coordinator's lease on a mission`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskClaim(cmd.Context(), os.Stdout, opscoord.ClaimIn{ID: args[0], Owns: owns, Strict: strict, Coordinate: coordinate, From: from})
		},
	}
	cmd.Flags().StringSliceVar(&owns, flagOwns, nil, "paths this claim owns (directory prefixes ending in / or exact files)")
	cmd.Flags().BoolVar(&strict, flagStrict, false, "refuse instead of warning when --owns overlaps another claim")
	cmd.Flags().BoolVar(&coordinate, flagCoordinate, false, "take a coordinator lease on a parent with open sub-tasks (TASK 6.9)")
	bindFromFlag(cmd, &from)
	return cmd
}

// runTaskClaim is task.claim (ops/coord): claim a task, or coordinate a
// parent with open sub-tasks, on its signals ref.
func runTaskClaim(ctx context.Context, out io.Writer, in opscoord.ClaimIn) error {
	w, t, err := openTaskCoord(ctx, in.ID, in.From)
	if err != nil {
		return err
	}
	defer t.Close()
	opts := coord.ClaimOptions{Owns: in.Owns, WorkBranch: currentWorkBranch(ctx, w.Repo), Repo: t.Repo, InRepo: !isStoreTarget(t), Coordinate: in.Coordinate}
	// TASK 6.9: a parent with open sub-tasks is refused unless
	// --coordinate (a coordinator lease, WIP-exempt).
	if list, ok := t.Rows.(coordLister); ok {
		docs, lerr := list.List(ctx)
		if lerr != nil {
			return coordError(lerr)
		}
		opts.OpenChildren = coord.OpenChildrenOf(docs)
	}
	cr, err := coord.Claim(ctx, w.env(t), in.ID, opts)
	var hoc *coord.HasOpenChildrenError
	if errors.As(err, &hoc) {
		return errs.New(errs.HasOpenChildren, hoc.Error()).WithHint("lore task claim " + hoc.Children[0] + "   (or: lore task claim " + hoc.Task + " --coordinate)")
	}
	if err != nil {
		return coordError(err)
	}
	if in.Coordinate && len(opts.OpenChildren(in.ID)) > 0 {
		_, _ = fmt.Fprintf(out, "you now coordinate %s; claim a child to work: lore task claim %s\n", in.ID, opts.OpenChildren(in.ID)[0])
	}
	if in.Strict && len(cr.Overlaps) > 0 && !cr.Plan.Resumed {
		// The claim is taken; --strict gives it back at once.
		if rerr := coord.Release(ctx, w.env(t), in.ID); rerr != nil {
			return coordError(rerr)
		}
		return errcodes.New(errcodes.InvalidInput, "file ownership overlaps: "+strings.Join(cr.Overlaps, "; ")).
			WithHint("drop --strict to claim with a warning")
	}
	printClaim(out, in.ID, cr, w, t.Signals.Ref)
	return nil
}

func newTaskNextCommand() *cobra.Command {
	var f queue.Filter
	var owns []string
	var why, dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:   "next",
		Short: "Claim the next ready task (resume-first, leaves only, dependencies, gates, urgency)",
		Long: `Next returns the task you already hold, or claims the most urgent ready leaf task:
open, no open sub-tasks, not gated, no live claim, every --after dependency (its own
and its ancestors') satisfied. Order is the urgency score (TASK 13): --why explains
every point, --dry-run ranks and explains without claiming. Exit 10 (E_NO_WORK)
lists why each task waits; exit 11 (E_CONTENDED) means every claim lost a race.

Examples:
  lore task next --mission mis_12 --repo backend
  lore task next --mission mis_12 --why --dry-run
  lore task next --mission mis_12 --role coordinator`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, _ := cmd.Flags().GetString(constants.FlagRepo) // registered below: GetString errors only for an unknown flag name
			res, err := runTaskNext(cmd.Context(), opscoord.NextIn{Mission: f.Mission, Role: f.Role, Repo: repo, Owns: owns, Why: why, DryRun: dryRun})
			if asJSON && dryRun {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return errors.Join(enc.Encode(res), err)
			}
			_, werr := io.WriteString(os.Stdout, res.Text())
			return errors.Join(err, werr)
		},
	}
	cmd.Flags().StringVar(&f.Mission, flagMission, "", "only tasks of this mission")
	cmd.Flags().StringVar(&f.Role, flagRole, "", "plan role (e.g. coordinator)")
	cmd.Flags().String(constants.FlagRepo, "", "only tasks of this repo (a repo name of the group)")
	cmd.Flags().StringSliceVar(&owns, flagOwns, nil, "paths the claim owns")
	cmd.Flags().BoolVar(&why, flagWhy, false, "explain the urgency score term by term")
	cmd.Flags().BoolVar(&dryRun, flagDryRun, false, "rank and explain without claiming (nothing written)")
	cmd.Flags().BoolVar(&asJSON, flagJSONOut, false, "with --dry-run: the ranking as JSON (urgency + terms)")
	return cmd
}

// Flags of task next (TASK 13.3).
const (
	flagWhy     = "why"
	flagDryRun  = "dry-run"
	flagJSONOut = "json"
)

// nextTag is the tag whose tasks get the next_tag term (TASK 13.2).
const nextTag = "next"

// urgencyContext resolves the urgency inputs that live on other core rows:
// the `next` tag and mission target dates. A read failure degrades to no
// such terms (the score stays defined), never to an error: ordering is a
// preference, not a safety rule.
func urgencyContext(ctx context.Context) coord.UrgencyContext {
	var u coord.UrgencyContext
	if tags, err := coreDocs(ctx, tableTags); err == nil {
		next := map[string]bool{}
		for _, d := range tags {
			if strings.EqualFold(d.Str(fieldName), nextTag) {
				next[d.Str(docKeyID)] = true
			}
		}
		u.NextTag = func(id string) bool { return next[id] }
	}
	if ms, err := coreDocs(ctx, tableMissions); err == nil {
		target := map[string]time.Time{}
		for _, d := range ms {
			target[d.Str(docKeyID)] = d.Time(fieldTargetDate)
		}
		u.MissionTarget = func(id string) time.Time { return target[id] }
	}
	return u
}

// Core table and field names urgencyContext reads.
const (
	fieldTargetDate = "target_date"
)

// printNextDryRun prints 13.3's ranking: the would-be claim, the ranked
// ready tasks and why every other matched task waits.
func printNextDryRun(out io.Writer, res queue.Result, why bool) {
	if len(res.Ready) == 0 {
		_, _ = fmt.Fprintln(out, "nothing ready")
	} else {
		top := res.Ready[0]
		s := res.Scores[top.ID]
		_, _ = fmt.Fprintf(out, "would claim %s %s   urgency %s\n", top.ID, top.Title, urgency.Format(s.Total))
		if why {
			for _, l := range s.Lines() {
				_, _ = fmt.Fprintln(out, "  "+l)
			}
		}
		_, _ = fmt.Fprintln(out, "ranked:")
		for i, t := range res.Ready {
			_, _ = fmt.Fprintf(out, "  %d  %s  %s\n", i+1, t.ID, urgency.Format(res.Scores[t.ID].Total))
		}
	}
	if len(res.Waiting) > 0 {
		_, _ = fmt.Fprintln(out, "waiting:")
		for _, wt := range res.Waiting {
			_, _ = fmt.Fprintf(out, "  %s  %s\n", wt.Task.ID, wt.Reason)
		}
	}
	_, _ = fmt.Fprintln(out, "nothing written (--dry-run)")
}

// coordLister is the optional listing half of a row store (task next).
type coordLister interface {
	List(ctx context.Context) ([]coord.Doc, error)
	RepoName(repoID string) string
}

// cryptoPick returns a uniformly random number in [0, n).
func cryptoPick(n int) int { return int(signals.CryptoJitter{}.Int63n(int64(n))) }

func newTaskHeartbeatCommand() *cobra.Command {
	var daemon bool
	cmd := &cobra.Command{
		Use:   "heartbeat [id]",
		Short: "Renew your claim's lease (alive, not progress)",
		Long: `Heartbeat renews the lease of a claim you hold: signals only, the task row is never
touched. Without an id it renews every claim of this session, at most once per
lease.renew_every (safe to call from an agent tool hook). --daemon keeps renewing
until the claim ends or the parent process exits; if renewals keep failing it stops
at expires − grace with exit 12 (E_LOST_CLAIM).

Examples:
  lore task heartbeat tsk_31
  lore task heartbeat --daemon tsk_31 &`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in := opscoord.HeartbeatIn{Daemon: daemon}
			if len(args) == 1 {
				in.ID = args[0]
			}
			return runTaskHeartbeat(cmd.Context(), os.Stdout, in)
		},
	}
	cmd.Flags().BoolVar(&daemon, flagDaemon, false, "keep renewing until the claim ends or the parent exits")
	return cmd
}

// renewedRecently implements the hook form's rate limit: the last renewal
// (expires − ttl) is younger than renew_every.
func renewedRecently(w *coordWorld, id string) bool {
	rec, ok := w.held().Get(w.Session(), id)
	if !ok {
		return false
	}
	last := rec.Expires.Add(-w.Lease.TTL)
	return w.Clock.Now().Sub(last) < w.Lease.RenewEvery
}

// renewOne renews one claim on the ref its claim was taken on.
func renewOne(ctx context.Context, w *coordWorld, id string) (signals.Signal, error) {
	t, err := targetForHeld(ctx, w, id)
	if err != nil {
		return signals.Signal{}, err
	}
	defer t.Close()
	s, err := coord.Renew(ctx, w.env(t), id)
	return s, coordError(err)
}

// targetForHeld finds the signals ref of a claim this session took (from
// its held record), falling back to the task's own target.
func targetForHeld(ctx context.Context, w *coordWorld, id string) (coordTarget, error) {
	if rec, ok := w.held().Get(w.Session(), id); ok && rec.Ref == signals.CodeRepoRef {
		t := coordTarget{Signals: w.repoSignals(), Folder: rec.Folder, Rows: noRows{}}
		if full, err := openCoordTarget(ctx, w, id); err == nil {
			t.Rows, t.close = full.Rows, full.close
		}
		return t, nil
	}
	return openCoordTarget(ctx, w, id)
}

// noRows is the row store of a heartbeat that cannot read rows: renewing
// never touches the row, and a fencing failure is then reported as a lost
// claim (the row cannot say whether it was cancelled).
type noRows struct{}

func (noRows) Get(context.Context, string) (coord.Doc, bool, error) { return nil, false, nil }
func (noRows) Stage(context.Context, ...coord.RowWrite) ([]signals.Companion, error) {
	return nil, errRowsNotWired()
}

// heartbeat daemon timing.
const (
	// daemonParentPoll is how often the daemon checks that its parent (the
	// agent session) is still alive, so it never outlives the session.
	daemonParentPoll = 5 * time.Second
)

// heartbeatDaemon renews every renew_every until a claim is fenced out,
// the parent exits, or renewals fail until expires − grace (F7).
func heartbeatDaemon(ctx context.Context, w *coordWorld, ids []string) error {
	parent := os.Getppid()
	tick := time.NewTicker(daemonParentPoll)
	defer tick.Stop()
	next := time.Now()
	for {
		if time.Now().After(next) || time.Now().Equal(next) {
			for _, id := range ids {
				_, err := renewOne(ctx, w, id)
				var cli *errcodes.CLIError
				if err != nil && errors.As(err, &cli) && cli.Code == errcodes.Offline {
					rec, ok := w.held().Get(w.Session(), id)
					if ok && w.Clock.Now().After(rec.Expires.Add(-w.Lease.Grace)) {
						return errcodes.New(errcodes.LostClaim, fmt.Sprintf("%s: renewals kept failing; the lease ends at %s, stop work", id, rec.Expires.UTC().Format(time.Kitchen)))
					}
					continue
				}
				if err != nil {
					return err
				}
				pushWorkBranch(ctx, w)
			}
			next = time.Now().Add(w.Lease.RenewEvery)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if os.Getppid() != parent {
				return nil // the session that started us is gone
			}
		}
	}
}

// pushWorkBranch pushes the current work branch so a replacement agent can
// fetch it (§7.4 "Work branch"). Best effort: a failed push is reported by
// `mission status` as "work lost: never pushed", never fatal here.
func pushWorkBranch(ctx context.Context, w *coordWorld) bool {
	b := currentWorkBranch(ctx, w.Repo)
	if b == "" || w.Remote == nil || !w.Autopush {
		return false
	}
	_, err := w.Repo.Run(ctx, gitref.Invocation{Args: []string{"push", "--quiet", w.RemoteName, "refs/heads/" + b + ":refs/heads/" + b},
		Env: []string{envRideActive + "=1"}})
	return err == nil
}

func newTaskReleaseCommand() *cobra.Command {
	var from string
	cmd := &cobra.Command{
		Use:   "release <id>",
		Short: "Give back a claim you hold",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskRelease(cmd.Context(), os.Stdout, opscoord.ReleaseIn{ID: args[0], From: from})
		},
	}
	bindFromFlag(cmd, &from)
	return cmd
}

// requireHuman refuses agents for human overrides (§7.4 "Human overrides").
func requireHuman(w *coordWorld, what string) error {
	if w.Ident.Kind == identity.KindHuman {
		return nil
	}
	return errcodes.New(errcodes.InvalidInput, what+" is a human override; an agent actor may not do it").
		WithHint("run it as yourself (the identity in ~/.lore/config.yaml), not from an agent session")
}

func newTaskRevokeCommand() *cobra.Command {
	var from string
	cmd := &cobra.Command{
		Use:   "revoke <id>",
		Short: "Drop the current claim on a task (human override; the holder is fenced out)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskRevoke(cmd.Context(), os.Stdout, opscoord.RevokeIn{ID: args[0], From: from})
		},
	}
	bindFromFlag(cmd, &from)
	return cmd
}

func newTaskReassignCommand() *cobra.Command {
	var assign, from string
	cmd := &cobra.Command{
		Use:   "reassign [id]",
		Short: "Assign a task to someone else, dropping any claim (human override)",
		Long: `Reassign sets the assignee and raises claim_epoch, so a holder's next write fails
with exit 14 (E_REASSIGNED). With --from @handle instead of an id it reassigns every
open task assigned to that person (offboarding, cookbook 9).

Examples:
  lore task reassign tsk_31 --assign @bob
  lore task reassign --from @bob --assign @alice`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in := opscoord.ReassignIn{Assign: assign, From: from}
			if len(args) == 1 {
				in.ID = args[0]
			}
			return runTaskReassign(cmd.Context(), os.Stdout, in)
		},
	}
	cmd.Flags().StringVar(&assign, flagAssign, "", "the new assignee (@handle)")
	cmd.Flags().StringVar(&from, flagFromActor, "", "reassign every open task of this person")
	return cmd
}

// resolveHandleActor resolves @handle to an actor id on the org roster (the
// repo's own actors table when no org applies, F18), through the stores
// work's roster reader. A bare actor id or stable key is accepted as is.
func resolveHandleActor(ctx context.Context, handle string) (string, error) {
	h := strings.TrimSpace(handle)
	if strings.TrimPrefix(h, "@") == "" {
		return "", errcodes.New(errcodes.InvalidInput, "empty handle")
	}
	// A 0.1.18 org keeps its roster in the core (roster_core.go).
	cr, err := openCoreRoster(ctx)
	if err != nil {
		return "", err
	}
	if cr != nil {
		a, herr := cr.resolveHandle(ctx, h)
		if herr != nil {
			return "", herr
		}
		return a.ID, nil
	}
	r, err := openRoster(ctx, &commonFlags{})
	if err != nil {
		return "", err
	}
	defer func() { _ = r.client.Close() }() // a read: a close error changes nothing
	a, err := r.resolveHandle(ctx, h)
	if err != nil {
		return "", err
	}
	return a.ID, nil
}

// openTasksAssignedTo lists open task ids assigned to actor, read through
// the core (tasks are core-owned; every attached store).
func openTasksAssignedTo(ctx context.Context, actorID string) ([]string, error) {
	ts, err := coreDocs(ctx, tableTasks)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, t := range ts {
		if t.Str(fieldAssignedActor) == actorID && openStatus(t) {
			out = append(out, t.Str(docKeyID))
		}
	}
	return out, nil
}

// fieldAssignedActor is the task's assignee column.
const fieldAssignedActor = "assigned_to_actor_id"

func openStatus(t coord.Doc) bool {
	s := t.Status()
	return s == string(queue.StatusTodo) || s == string(queue.StatusInProgress)
}

// newTaskHookCommand is the hidden entry point of lore's git hooks for
// coordination: `lore task hook commit-msg <file>` adds the Lore-Task
// trailer while this session holds a claim (F9). It never fails a commit.
func newTaskHookCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "hook", Short: "Git hook entry points for coordination", Hidden: true}
	cmd.AddCommand(&cobra.Command{
		Use:   "commit-msg <message-file>",
		Short: "Add Lore-Task trailers for the claims this session holds",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := commitMsgHook(cmd.Context(), args[0]); err != nil {
				fmt.Fprintf(os.Stderr, "%s: commit-msg: %v (commit not changed)\n", BinaryName, err)
			}
			return nil
		},
	})
	return cmd
}

// commitMsgFileMode is used only if git's message file vanished meanwhile
// (WriteFile keeps an existing file's mode): owner-only, it is private text.
const commitMsgFileMode = 0o600

// commitMsgHook rewrites the message file. Offline by design: it reads only
// the session's held records in this checkout, never the network.
func commitMsgHook(ctx context.Context, file string) error {
	if os.Getenv(envRideActive) == "1" {
		return nil
	}
	session := strings.TrimSpace(os.Getenv(identity.EnvSession))
	if session == "" {
		session = humanSession(identityBestEffort(ctx).StableKey)
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	repo, err := gitref.Open(ctx, dir, nil)
	if err != nil {
		return err
	}
	gitDir, err := repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--absolute-git-dir"}})
	if err != nil {
		return err
	}
	held := coord.HeldStore{Path: strings.TrimSpace(string(gitDir)) + "/" + liveStateDirName + "/" + heldFileName}
	now := cliClock().Now()
	// The message file is git's (the hook's argument): read and rewrite it
	// through a root at its folder, so the path can name nothing else.
	root, err := os.OpenRoot(filepath.Dir(file))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }() // the rewrite below already returned its own error
	name := filepath.Base(file)
	msg, err := root.ReadFile(name)
	if err != nil {
		return err
	}
	out := msg
	for _, r := range held.List(session) {
		if now.After(r.Expires) || !r.InRepo {
			continue // expired, or a store-only task: its id never enters this repo (Q13)
		}
		out = mergewatch.AddTrailer(out, r.Task)
	}
	if string(out) == string(msg) {
		return nil
	}
	return root.WriteFile(name, out, commitMsgFileMode)
}

// historyEntryTag types the ids of history entries reassign writes (the_…,
// TASK 17.2); entries are not rows, the id is the list's merge key.
type historyEntryTag struct{}

// Prefix implements ids.Tag.
func (historyEntryTag) Prefix() ids.Prefix { return ids.PrefixHistoryEntry }

// appendReassignHistory adds reassign's entries to d's history (TASK 17.2,
// OV59): one for the lead, one for the assignees (sorted, comma-joined, as
// the core writes them), each only when it changes. The cap
// (tasks.history_max) is folded by the next core write of the task, which
// folds the whole list.
func appendReassignHistory(d coord.Doc, by string, now time.Time, prevLead string, prevAll []string, to string) error {
	prev := slices.Clone(prevAll)
	slices.Sort(prev)
	at := now.UTC().Format(time.RFC3339)
	var add []opstask.HistoryEntry
	for _, c := range [][3]string{{fieldTaskLead, prevLead, to}, {schema.FieldTaskAssignees, strings.Join(prev, ","), to}} {
		if c[1] == c[2] {
			continue
		}
		id, err := ids.New[historyEntryTag]()
		if err != nil {
			return err
		}
		add = append(add, opstask.HistoryEntry{ID: id.String(), At: at, By: by, Field: c[0], From: c[1], To: c[2]})
	}
	if len(add) == 0 {
		return nil
	}
	var list []opstask.HistoryEntry
	if raw, ok := d[schema.FieldTaskHistory]; ok && raw != nil {
		b, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("reassign: read history: %w", err)
		}
		if err = json.Unmarshal(b, &list); err != nil {
			return fmt.Errorf("reassign: read history: %w", err)
		}
	}
	b, err := json.Marshal(map[string][]opstask.HistoryEntry{schema.FieldTaskHistory: append(list, add...)})
	if err != nil {
		return fmt.Errorf("reassign: encode history: %w", err)
	}
	// Decoded back into the generic form the row codec keeps (maps, slices,
	// json.Number), like every other JSON column of a coord.Doc.
	g, err := canonjson.Decode(b, canonjson.DefaultMaxDepth)
	if err != nil {
		return fmt.Errorf("reassign: encode history: %w", err)
	}
	d[schema.FieldTaskHistory] = g[schema.FieldTaskHistory]
	return nil
}
