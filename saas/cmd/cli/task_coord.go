// task_coord.go — `lore task claim|next|heartbeat|release|revoke|reassign`
// and the hidden `lore task hook commit-msg` (LORE_TEAM_SPEC F7, F9, §7.3,
// §7.4). The decisions live in saas/pkg/aicoder/signals (claims on the
// signals ref), .../signals/coord (the fenced task row) and .../queue (the
// ready queue); this file parses flags, opens the world and prints.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"dbent/gen/ent"
	entTeam "dbent/gen/ent/team"
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
func taskCoordCommands() []*cobra.Command {
	return []*cobra.Command{
		newTaskClaimCommand(), newTaskNextCommand(), newTaskHeartbeatCommand(), newTaskReleaseCommand(),
		newTaskRevokeCommand(), newTaskReassignCommand(), newTaskHookCommand(), newTaskMergedCommand(), newTaskNoteCommand(),
	}
}

// openTaskCoord opens the world and the target of one task, refusing
// callers that are not an agent or a person.
func openTaskCoord(ctx context.Context, id, from string) (*coordWorld, coordTarget, error) {
	w, err := openCoordWorld(ctx)
	if err != nil {
		return nil, coordTarget{}, err
	}
	if err := w.requireActor(); err != nil {
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

func printClaim(id string, cr coord.ClaimResult, w *coordWorld, ref string) {
	s := cr.Plan.Signal
	if cr.Plan.Resumed {
		fmt.Printf("you hold %s already (until %s); resume, or `lore task release %s`\n", id, s.Expires.UTC().Format(time.Kitchen), id)
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
	fmt.Printf("%s claimed %s%s (lease %s, token %s…) on %s%s\n", style.Success("✓"), id, title,
		shortDuration(w.Lease.TTL), s.ClaimToken[:min(len(s.ClaimToken), coord.TokenPrefixLen)], ref, extra)
	if s.WorkBranch != "" {
		fmt.Printf("  work branch %s\n", s.WorkBranch)
	}
	for _, o := range cr.Overlaps {
		fmt.Printf("%s file ownership overlaps: %s\n", style.Warn("!"), o)
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

func newTaskClaimCommand() *cobra.Command {
	var owns []string
	var strict bool
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
			ctx := cmd.Context()
			w, t, err := openTaskCoord(ctx, args[0], from)
			if err != nil {
				return err
			}
			defer t.Close()
			cr, err := coord.Claim(ctx, w.env(t), args[0], coord.ClaimOptions{Owns: owns, WorkBranch: currentWorkBranch(ctx, w.Repo), Repo: t.Repo, InRepo: !isStoreTarget(t)})
			if err != nil {
				return coordError(err)
			}
			if strict && len(cr.Overlaps) > 0 && !cr.Plan.Resumed {
				// The claim is taken; --strict gives it back at once.
				if rerr := coord.Release(ctx, w.env(t), args[0]); rerr != nil {
					return coordError(rerr)
				}
				return errcodes.New(errcodes.InvalidInput, "file ownership overlaps: "+strings.Join(cr.Overlaps, "; ")).
					WithHint("drop --strict to claim with a warning")
			}
			printClaim(args[0], cr, w, t.Signals.Ref)
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&owns, flagOwns, nil, "paths this claim owns (directory prefixes ending in / or exact files)")
	cmd.Flags().BoolVar(&strict, flagStrict, false, "refuse instead of warning when --owns overlaps another claim")
	bindFromFlag(cmd, &from)
	return cmd
}

func newTaskNextCommand() *cobra.Command {
	var f queue.Filter
	var owns []string
	cmd := &cobra.Command{
		Use:   "next",
		Short: "Claim the next ready task (resume-first, dependencies, gates, priority)",
		Long: `Next returns the task you already hold, or claims the first ready task: open,
not gated, no live claim, every --after dependency satisfied. Exit 10 (E_NO_WORK)
lists why each task waits; exit 11 (E_CONTENDED) means every claim lost a race.

Examples:
  lore task next --mission mis_12 --repo backend
  lore task next --mission mis_12 --role coordinator`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			w, t, err := openTaskCoord(ctx, "", "")
			if err != nil {
				return err
			}
			defer t.Close()
			list, ok := t.Rows.(coordLister)
			if !ok {
				return errRowsNotWired()
			}
			if mc, ok := t.Rows.(interface{ MergedCheck(context.Context) }); ok {
				mc.MergedCheck(ctx)
			}
			repo, _ := cmd.Flags().GetString(constants.FlagRepo) // registered below: GetString errors only for an unknown flag name
			f.Repo = repo
			f.Here = t.Repo
			res, err := coord.Next(ctx, w.env(t), coord.NextRequest{
				Filter: f, Opts: coord.ClaimOptions{Owns: owns, WorkBranch: currentWorkBranch(ctx, w.Repo), InRepo: !isStoreTarget(t)},
				List: list.List, RepoName: list.RepoName, Pick: cryptoPick,
			})
			for _, p := range res.Poisoned {
				fmt.Printf("%s %s failed %d times and is now blocked (owner's inbox)\n", style.Warn("!"), p, w.Agents.MaxAttempts)
			}
			if err != nil {
				return coordError(err)
			}
			if res.Resumed {
				fmt.Printf("you hold %s; resume, or `lore task release %s`\n", res.Task, res.Task)
				return nil
			}
			printClaim(res.Task, res.Claim, w, t.Signals.Ref)
			fmt.Printf("  read its notes: lore task show %s\n", res.Task)
			return nil
		},
	}
	cmd.Flags().StringVar(&f.Mission, flagMission, "", "only tasks of this mission")
	cmd.Flags().StringVar(&f.Role, flagRole, "", "plan role (e.g. coordinator)")
	cmd.Flags().String(constants.FlagRepo, "", "only tasks of this repo (a repo name of the group)")
	cmd.Flags().StringSliceVar(&owns, flagOwns, nil, "paths the claim owns")
	return cmd
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
			ctx := cmd.Context()
			w, err := openCoordWorld(ctx)
			if err != nil {
				return err
			}
			if err := w.requireActor(); err != nil {
				return err
			}
			ids := args
			if len(ids) == 0 {
				for _, r := range w.held().List(w.Session()) {
					ids = append(ids, r.Task)
				}
				if len(ids) == 0 {
					fmt.Println("no claims held by this session")
					return nil
				}
			}
			if daemon {
				return heartbeatDaemon(ctx, w, ids)
			}
			for _, id := range ids {
				if len(args) == 0 && renewedRecently(w, id) {
					continue
				}
				s, err := renewOne(ctx, w, id)
				if err != nil {
					return err
				}
				fmt.Printf("%s %s renewed until %s (seq %d)\n", style.Success("✓"), id, s.Expires.UTC().Format(time.RFC3339), s.RenewedSeq)
			}
			return nil
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
			ctx := cmd.Context()
			w, t, err := openTaskCoord(ctx, args[0], from)
			if err != nil {
				return err
			}
			defer t.Close()
			if err := coord.Release(ctx, w.env(t), args[0]); err != nil {
				return coordError(err)
			}
			fmt.Printf("%s released %s\n", style.Success("✓"), args[0])
			return nil
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
			ctx := cmd.Context()
			w, t, err := openTaskCoord(ctx, args[0], from)
			if err != nil {
				return err
			}
			defer t.Close()
			if err := requireHuman(w, "revoke"); err != nil {
				return err
			}
			removed, err := coord.Override(ctx, w.env(t), args[0], coord.OverrideRevoke, nil)
			if err != nil {
				return coordError(err)
			}
			who := "no one held it"
			if removed != nil {
				who = "was held by " + holderLabel(*removed)
			}
			fmt.Printf("%s revoked the claim on %s (%s)\n", style.Success("✓"), args[0], who)
			return nil
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
			ctx := cmd.Context()
			if assign == "" {
				return errcodes.New(errcodes.InvalidInput, "--assign @handle is required")
			}
			w, err := openCoordWorld(ctx)
			if err != nil {
				return err
			}
			if err := requireHuman(w, "reassign"); err != nil {
				return err
			}
			to, err := resolveHandleActor(ctx, assign)
			if err != nil {
				return err
			}
			var ids []string
			switch {
			case len(args) == 1:
				ids = args
			case from != "":
				who, err := resolveHandleActor(ctx, from)
				if err != nil {
					return err
				}
				ids, err = openTasksAssignedTo(ctx, who)
				if err != nil {
					return err
				}
			default:
				return errcodes.New(errcodes.InvalidInput, "give a task id or --from @handle")
			}
			for _, id := range ids {
				t, err := openCoordTarget(ctx, w, id)
				if err != nil {
					return err
				}
				defer t.Close()
				if _, err := coord.Override(ctx, w.env(t), id, coord.OverrideReassign, func(d coord.Doc) { d.SetStr("assigned_to_actor_id", to) }); err != nil {
					return coordError(err)
				}
				fmt.Printf("%s %s reassigned to %s\n", style.Success("✓"), id, assign)
			}
			if len(ids) == 0 {
				fmt.Printf("no open tasks assigned to %s\n", from)
			}
			return nil
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
	r, err := openRoster(ctx, &commonFlags{})
	if err != nil {
		return "", err
	}
	defer r.client.Close()
	a, err := r.resolveHandle(ctx, h)
	if err != nil {
		return "", err
	}
	return a.ID, nil
}

// resolveHandleTeam returns the id of the active team @handle on the org
// roster (the store's org folder, where `lore team add` writes it), or ""
// when no such team exists. It is the roster read a linked client repo
// needs: its own lore.db holds no teams (team names are internal), so a
// lookup there alone could never see the team and the TEAM-030 refusal
// would be unreachable. A roster that cannot be read is an error, never
// "no team".
func resolveHandleTeam(ctx context.Context, handle string) (string, error) {
	h := strings.TrimPrefix(strings.TrimSpace(handle), handlePrefix)
	if h == "" {
		return "", errcodes.New(errcodes.InvalidInput, "empty handle")
	}
	r, err := openRoster(ctx, &commonFlags{})
	if err != nil {
		return "", err
	}
	defer r.client.Close()
	tm, err := r.client.Team.Query().Where(entTeam.ProjectID(r.project), entTeam.Handle(h), entTeam.Active(true)).Only(ctx)
	if ent.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", errcodes.New(errcodes.Internal, "read the roster's teams").WithCause(err)
	}
	return tm.ID, nil
}

// openTasksAssignedTo lists open task ids assigned to actor in this project.
func openTasksAssignedTo(ctx context.Context, actorID string) ([]string, error) {
	var f commonFlags
	_, client, err := resolveContext(&f)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	ts, err := client.Task.Query().All(ctx)
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "list tasks").WithCause(err)
	}
	var out []string
	for _, t := range ts {
		if t.AssignedToActorID != nil && *t.AssignedToActorID == actorID && openStatus(t) {
			out = append(out, t.ID)
		}
	}
	return out, nil
}

func openStatus(t *ent.Task) bool {
	s := string(t.Status)
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
	msg, err := os.ReadFile(file)
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
	return os.WriteFile(file, out, 0o644)
}
