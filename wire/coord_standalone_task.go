package wire

// coord_standalone_task.go — task claim, heartbeat, release, revoke and
// next for a host without its own coordination machinery (cmd/lore,
// P3-SIGCOORD-2): v0.1.17's command bodies (saas task_coord.go,
// coord_ops_run.go) over the root signals and coord packages, returning
// the lines they printed.

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/logic/queue"
	"github.com/thesatellite-ai/lore/logic/urgency"
	"github.com/thesatellite-ai/lore/model/errs"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	"github.com/thesatellite-ai/lore/schema"
)

// refuseFrom keeps --from on the repo itself: another source is a store
// project, which this host does not reach (FA-OLDCODE-1).
func refuseFrom(name, from string) error {
	if from != "" && from != fromRepo {
		return coordUnsupported(name)
	}
	return nil
}

// TaskClaim implements opscoord.Host: claim a task (or coordinate a
// parent with open sub-tasks) on this repo's signals ref.
func (h standaloneCoord) TaskClaim(ctx context.Context, in opscoord.ClaimIn) (opscoord.Report, error) {
	if err := refuseFrom(opscoord.OpTaskClaim, in.From); err != nil {
		return opscoord.Report{}, err
	}
	w, err := h.openWorldFor(ctx, in.ID)
	if err != nil {
		return opscoord.Report{}, err
	}
	return h.claimOn(ctx, w, in)
}

// claimOn is the claim of in.ID on world w: task claim's, and the store
// task queue's claim of the task it ranked first (devco:D-666).
func (h standaloneCoord) claimOn(ctx context.Context, w *coordWorld, in opscoord.ClaimIn) (opscoord.Report, error) {
	if err := w.requirePushAsked(in.Push, claimAsk("a claim", claimRerun(in), in.ID)); err != nil {
		return opscoord.Report{}, err
	}
	env := w.env(ctx)
	opts := coord.ClaimOptions{Owns: in.Owns, WorkBranch: w.workBranch(ctx), Repo: h.hereRepo(ctx, w), InRepo: w.taskInRepo(), Coordinate: in.Coordinate}
	// TASK 6.9: a parent with open sub-tasks is refused unless --coordinate.
	docs, err := engineRows{a: h.a}.List(ctx)
	if err != nil {
		return opscoord.Report{}, err
	}
	opts.OpenChildren = coord.OpenChildrenOf(docs)
	cr, err := coord.Claim(ctx, env, in.ID, opts)
	if err != nil {
		return opscoord.Report{}, w.coordErr(w.namedDoneBy(ctx, err))
	}
	var lines []string
	if in.Coordinate && len(opts.OpenChildren(in.ID)) > 0 {
		lines = append(lines, fmt.Sprintf("you now coordinate %s; claim a child to work: lore task claim --id %s%s", in.ID, opts.OpenChildren(in.ID)[0], w.claimPushFlag()))
	}
	if in.Strict && len(cr.Overlaps) > 0 && !cr.Plan.Resumed {
		// The claim is taken; --strict gives it back at once.
		if rerr := coord.Release(ctx, env, in.ID); rerr != nil {
			return opscoord.Report{}, w.coordErr(rerr)
		}
		return opscoord.Report{}, errs.New(errs.InvalidInput, "file ownership overlaps: "+strings.Join(cr.Overlaps, "; ")).
			WithHint("drop --strict to claim with a warning")
	}
	return opscoord.Report{Lines: append(lines, claimLines(in.ID, cr, w.lease.TTL, w.claimPlace(env.Signals.Ref))...)}, nil
}

// claimLines is v0.1.17 printClaim.
func claimLines(id string, cr coord.ClaimResult, ttl time.Duration, ref string) []string {
	s := cr.Plan.Signal
	if cr.Plan.Resumed {
		return []string{fmt.Sprintf("you hold %s already (until %s); resume, or `lore task release --id %s`", id, s.Expires.UTC().Format(time.Kitchen), id)}
	}
	extra := ""
	if cr.Plan.Reclaim && cr.Plan.Prev != nil {
		extra = fmt.Sprintf("; reclaim, attempt %d; previous holder %s timed out", cr.Plan.Attempts, holderName(*cr.Plan.Prev))
	}
	title := ""
	if cr.Doc != nil {
		title = " " + cr.Doc.Str(fieldTitle)
	}
	out := []string{fmt.Sprintf("%s claimed %s%s (lease %s, token %s…) on %s%s", markOK, id, title,
		shortDuration(ttl), s.ClaimToken[:min(len(s.ClaimToken), coord.TokenPrefixLen)], ref, extra)}
	if s.WorkBranch != "" {
		out = append(out, "  work branch "+s.WorkBranch)
	}
	for _, o := range cr.Overlaps {
		out = append(out, "! file ownership overlaps: "+o)
	}
	return out
}

// claimPlace is where a claim line says the claim went: the signals ref,
// and for a repo with no remote that it stays local (devco:D-362: there is
// nothing to push to, so --push changes nothing and nobody else sees it).
//
// devco:D-560: a linked repo's claim is no ref of the code repo nor a branch
// of the store clone: it is the store's signals ref, written on the store's
// remote (the clone keeps only a tracking copy under refs/remotes), while
// the task row's status is a row commit in the store folder that the
// "committed locally" line names. Saying only "on refs/lore/signals/main"
// sent people looking for a ref that exists in neither place.
//
// devco:D-608: it is the one label of every signals write (claim, next
// --claim, release, hold, hold release, revoke, reassign); a code repo's
// write names the remote it was pushed to, as the revoke line did.
func (w *coordWorld) claimPlace(ref string) string {
	if w.spot.store {
		// devco:D-580: a signals write is pushed in the same command or not
		// made at all (signals.Store fails closed with autopush off), so the
		// "kept in the local store clone" wording this had was never true.
		return "the signals ref " + ref + " of store " + w.spot.label + " (pushed to the store's remote now: teammates see it on their next fetch)"
	}
	if _, local := localSignalsRemote(w.spot, w.commonDir); local {
		return ref + " (local: this repo has no remote, so the claim stays in this repository)"
	}
	if w.spot.remote != nil {
		return ref + " of this repo's remote " + cmp.Or(w.spot.remoteName, w.remoteName) + " (pushed now: teammates see it on their next fetch)"
	}
	return ref
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

// hereRepo is this checkout's repo mount name ("" when the repo has no
// identity yet): the default --repo of task next and the claim's repo.
func (h standaloneCoord) hereRepo(ctx context.Context, w *coordWorld) string {
	if w.spot.store {
		return w.remoteRepoName(ctx)
	}
	id, ok := h.a.RepoIdentity(ctx)
	if !ok {
		return ""
	}
	return engineRows{a: h.a}.RepoName(id)
}

// TaskHeartbeat implements opscoord.Host: renew one claim, or every claim
// of this session (rate limited to renew_every), or keep renewing them
// (--daemon, heartbeatDaemon).
func (h standaloneCoord) TaskHeartbeat(ctx context.Context, in opscoord.HeartbeatIn) (opscoord.Report, error) {
	if !inCodeRepo(ctx, h.a.opts.RepoDir) {
		// the home folder, a plain folder, a store clone: the claims held
		// on the org stores' signals refs (devco:D-666), kept alive by
		// --daemon too (devco:D-671)
		if in.Daemon {
			return h.storeHeartbeatDaemon(ctx, in.ID)
		}
		return h.storeHeartbeat(ctx, in.ID, nil)
	}
	w, err := h.openWorld(ctx)
	if err != nil {
		return opscoord.Report{}, err
	}
	if in.ID == "" && !in.Daemon {
		// a code repo renews its own held claims, then the store tasks
		// held from home (their held record is the store clone's)
		var repoIDs []string
		for _, r := range w.held().List(w.session()) {
			repoIDs = append(repoIDs, r.Task)
		}
		rep, rerr := h.renewHeld(ctx, w, repoIDs, true)
		if rerr != nil {
			return rep, rerr
		}
		srep, serr := h.storeHeartbeat(ctx, "", repoIDs)
		rep.Lines = append(rep.Lines, srep.Lines...)
		if serr != nil {
			return rep, serr
		}
		if len(rep.Lines) == 0 && len(repoIDs) == 0 {
			rep.Lines = []string{"no claims held by this session"}
		}
		return rep, nil
	}
	var ids []string
	if in.ID != "" {
		ids = []string{in.ID}
	} else {
		for _, r := range w.held().List(w.session()) {
			ids = append(ids, r.Task)
		}
	}
	if in.Daemon {
		// devco:D-673: the store claims this session took from home are
		// held on the store clones, not this repo (repoDaemonScope)
		if store, uncovered := h.repoDaemonScope(ctx, w, in.ID, ids); store {
			return h.storeHeartbeatDaemon(ctx, in.ID)
		} else if len(uncovered) > 0 {
			fmt.Fprintln(os.Stderr, uncoveredStoreClaims(uncovered))
		}
	}
	if len(ids) == 0 {
		return opscoord.Report{Lines: []string{"no claims held by this session"}}, nil
	}
	if in.Daemon {
		return w.heartbeatDaemon(ctx, ids)
	}
	return h.renewHeld(ctx, w, ids, in.ID == "")
}

// renewHeld renews each claim of ids on world w; limited skips one renewed
// within lease.renew_every (a heartbeat with no --id).
func (h standaloneCoord) renewHeld(ctx context.Context, w *coordWorld, ids []string, limited bool) (opscoord.Report, error) {
	var lines []string
	for _, id := range ids {
		if limited && w.renewedRecently(id) {
			continue
		}
		if ferr := w.forHeld(ctx, id); ferr != nil {
			return opscoord.Report{Lines: lines}, ferr
		}
		s, rerr := coord.Renew(ctx, w.env(ctx), id)
		if rerr != nil {
			return opscoord.Report{Lines: lines}, w.coordErr(rerr)
		}
		lines = append(lines, fmt.Sprintf("%s %s renewed until %s (seq %d)", markOK, id, s.Expires.UTC().Format(time.RFC3339), s.RenewedSeq))
	}
	return opscoord.Report{Lines: lines}, nil
}

// daemonParentPoll is how often the heartbeat daemon checks that the
// process that started it (the agent session) is still alive, so the
// daemon never outlives the session (v0.1.17 daemonParentPoll).
const daemonParentPoll = 5 * time.Second

// heartbeatDaemon is `task heartbeat --daemon` (F7, v0.1.17
// heartbeatDaemon): it renews every claim in ids each lease.renew_every and
// pushes the work branch after a renewal (§7.4), until
//   - ctx ends or the parent process exits: stops quietly (exit 0);
//   - a claim is fenced out or gone (any refusal other than offline or
//     contended): that error, so the session stops work on it;
//   - renewals of a claim kept failing offline/contended until its held
//     expiry minus the lease grace: E_LOST_CLAIM ("stop work").
//
// Renewals write the signals ref only (coord.Renew stages no rows).
func (w *coordWorld) heartbeatDaemon(ctx context.Context, ids []string) (opscoord.Report, error) {
	parent := os.Getppid()
	tick := time.NewTicker(daemonParentPoll)
	defer tick.Stop()
	next := w.a.Now()
	renewals := 0
	for {
		if !w.a.Now().Before(next) {
			for _, id := range ids {
				daemonHook(daemonRenewing)
				if err := w.renewOnce(ctx, id); err != nil {
					// ctx ended during the renewal (the session stopped
					// it, Ctrl-C): a quiet stop, as between renewals,
					// never the cut-off git command's error.
					if ctx.Err() != nil {
						return w.daemonStopped(renewals), nil
					}
					if !errs.Has(err, errs.Offline) && !errs.Has(err, errs.Contended) {
						return opscoord.Report{}, err
					}
					if rec, ok := w.held().Get(w.session(), id); ok && w.a.Now().After(rec.Expires.Add(-w.lease.Grace)) {
						return opscoord.Report{}, errs.Wrap(errs.LostClaim, fmt.Sprintf("%s: renewals kept failing; the lease ends at %s, stop work", id, rec.Expires.UTC().Format(time.Kitchen)), err)
					}
					continue
				}
				renewals++
				if b := w.workBranch(ctx); b != "" {
					w.pushWorkBranch(ctx, b) // best effort; inbox and mission status say "not pushed" from the remote-tracking ref (D-619)
				}
			}
			next = w.a.Now().Add(w.lease.RenewEvery)
			daemonHook(daemonRound)
		}
		select {
		case <-ctx.Done():
			return w.daemonStopped(renewals), nil
		case <-tick.C:
			if os.Getppid() != parent {
				return w.daemonStopped(renewals), nil // the session that started us is gone
			}
		}
	}
}

// Heartbeat daemon events daemonHook reports, in order: daemonRenewing
// before each claim's renewal, daemonRound after each round of renewals.
const (
	daemonRenewing = "renewing"
	daemonRound    = "round"
)

// daemonHook is called at each daemon event; a no-op in the binary. Why: a
// test stops the daemon after (or during) a renewal by the event itself,
// not by a context timeout that a loaded machine's slow git outruns.
var daemonHook = func(string) {}

// renewOnce renews one claim on the ref it was taken on.
func (w *coordWorld) renewOnce(ctx context.Context, id string) error {
	if err := w.forHeld(ctx, id); err != nil {
		return err
	}
	if _, err := coord.Renew(ctx, w.env(ctx), id); err != nil {
		return w.coordErr(err)
	}
	return nil
}

// daemonStopped is the daemon's closing line.
func (w *coordWorld) daemonStopped(renewals int) opscoord.Report {
	return opscoord.Report{Lines: []string{fmt.Sprintf("heartbeat daemon stopped after %d renewal(s)", renewals)}}
}

// renewedRecently is the hook form's rate limit: the last renewal
// (expires − ttl) is younger than renew_every.
func (w *coordWorld) renewedRecently(id string) bool {
	rec, ok := w.held().Get(w.session(), id)
	if !ok {
		return false
	}
	return w.a.Now().Sub(rec.Expires.Add(-w.lease.TTL)) < w.lease.RenewEvery
}

// TaskRelease implements opscoord.Host: give back a claim you hold.
func (h standaloneCoord) TaskRelease(ctx context.Context, in opscoord.ReleaseIn) (opscoord.Report, error) {
	if err := refuseFrom(opscoord.OpTaskRelease, in.From); err != nil {
		return opscoord.Report{}, err
	}
	w, err := h.openWorldFor(ctx, in.ID)
	if err != nil {
		return opscoord.Report{}, err
	}
	env := w.env(ctx)
	if err = coord.Release(ctx, env, in.ID); err != nil {
		return opscoord.Report{}, w.coordErr(err)
	}
	// devco:D-582: say where the release went, the same place the claim
	// line named (the store's remote, the code repo's remote, or local).
	return opscoord.Report{Lines: []string{markOK + " released " + in.ID + ": the claim is off " + w.claimPlace(env.Signals.Ref)}}, nil
}

// TaskRevoke implements opscoord.Host: a person drops the current claim;
// the holder is fenced out.
func (h standaloneCoord) TaskRevoke(ctx context.Context, in opscoord.RevokeIn) (opscoord.Report, error) {
	if err := refuseFrom(opscoord.OpTaskRevoke, in.From); err != nil {
		return opscoord.Report{}, err
	}
	w, err := h.openWorldFor(ctx, in.ID)
	if err != nil {
		return opscoord.Report{}, err
	}
	if err = w.requireHuman(ctx, "revoke"); err != nil {
		return opscoord.Report{}, err
	}
	env := w.env(ctx)
	if err = w.requireOverridePush(ctx, env, in.ID, in.Push, pushAsk{what: "a revoke", rerun: withFlag([]string{"task", "revoke", "--id", in.ID}, "--from", in.From)}); err != nil {
		return opscoord.Report{}, err
	}
	removed, err := coord.Override(ctx, env, in.ID, coord.OverrideRevoke, nil)
	if err != nil {
		return opscoord.Report{}, w.coordErr(err)
	}
	if removed == nil {
		return opscoord.Report{Lines: []string{fmt.Sprintf("%s revoked the claim on %s (no one held it)", markOK, in.ID)}}, nil
	}
	lines := []string{fmt.Sprintf("%s revoked the claim on %s (was held by %s)", markOK, in.ID, holderName(*removed))}
	return opscoord.Report{Lines: append(lines, w.overridePushedLine(removed.Holder != w.actorID))}, nil
}

// claimRerun is the `task claim` the person ran (every flag they gave,
// named), without --push: the command a push refusal prints with --push
// appended (devco:D-669).
func claimRerun(in opscoord.ClaimIn) []string {
	argv := []string{"task", "claim", "--id", in.ID}
	for _, o := range in.Owns {
		argv = append(argv, "--owns", o)
	}
	argv = withSwitch(argv, "--strict", in.Strict)
	argv = withSwitch(argv, "--coordinate", in.Coordinate)
	return withFlag(argv, "--from", in.From)
}

// nextRerun is the `task next --claim` the person ran (its filters and
// --owns), without --push (devco:D-669). It names no task: task next
// picks it, so there is no claim-free command with a known id to offer.
func nextRerun(in opscoord.NextIn) []string {
	argv := []string{"task", "next", "--claim"}
	argv = withFlag(argv, "--mission", in.Mission)
	argv = withFlag(argv, "--repo", in.Repo)
	argv = withFlag(argv, "--role", in.Role)
	argv = withFlag(argv, "--store", in.Store) // devco:D-666: a queue of an org store from home
	for _, o := range in.Owns {
		argv = append(argv, "--owns", o)
	}
	return withSwitch(argv, "--why", in.Why)
}

// withFlag appends "flag value" to argv when value is set.
func withFlag(argv []string, flag, value string) []string {
	if value == "" {
		return argv
	}
	return append(argv, flag, value)
}

// withSwitch appends a boolean flag to argv when on.
func withSwitch(argv []string, flag string, on bool) []string {
	if !on {
		return argv
	}
	return append(argv, flag)
}

// requireOverridePush is requirePushAsked for a person's override
// (revoke, reassign; devco:S-17): it asks only when the override changes
// the signals ref, that is when the task has a claim or a revoke marker on
// it. An override of a task nobody holds writes its row only, which waits
// for lore sync like every other row.
func (w *coordWorld) requireOverridePush(ctx context.Context, env coord.Env, id string, asked bool, ask pushAsk) error {
	if asked || !w.pushNeeded() {
		return nil
	}
	v, err := env.Signals.Read(ctx)
	if err != nil {
		return w.coordErr(err)
	}
	_, claimed := v.Board[signals.ClaimPath(env.Folder, id)]
	_, marked := v.Board[signals.RevokedPath(env.Folder, id)]
	if !claimed && !marked {
		return nil
	}
	return w.requirePushAsked(false, ask)
}

// overridePushedLine says what a revoke sent and to where (devco:S-17: the
// row line alone read "not pushed yet" although the holder was already
// fenced out). marked: the holder is kept off the task until a reassign.
// devco:D-608: the place is claimPlace, the label a claim and a release
// print, not "pushed <ref> to <remote name>".
func (w *coordWorld) overridePushedLine(marked bool) string {
	line := "  on " + w.claimPlace(w.spot.ref) + ": the holder is fenced out now"
	if marked {
		line += " and lore task next will not hand it this task again until a person runs `lore task reassign`"
	}
	return line + "; the task row itself waits for your next lore sync"
}

// nextTagName is the tag whose tasks get the next_tag term (TASK 13.2).
const nextTagName = "next"

// TaskNext implements opscoord.Host. By default it reads (devco:D-426:
// the name reads like a read, and a claim commits): it shows the task
// --claim would take, why (its urgency terms), the ranking and what waits,
// writing nothing. --claim resumes the claim held or claims the most urgent
// ready leaf, as before; --dry-run prints the old ranking form and never
// claims. Poisoned tasks are announced (and blocked) only by a claim.
func (h standaloneCoord) TaskNext(ctx context.Context, in opscoord.NextIn) (opscoord.NextOut, error) {
	if out, ok, serr := h.storeQueue(ctx, in); ok {
		return out, serr // an org store's tasks with no code repo (devco:D-666)
	}
	w, err := h.openWorldFor(ctx, "")
	if err != nil {
		return opscoord.NextOut{}, err
	}
	claim := in.Claim && !in.DryRun
	if claim {
		if err = w.requirePushAsked(in.Push, pushAsk{what: "the claim task next takes", rerun: nextRerun(in)}); err != nil {
			return opscoord.NextOut{}, err
		}
	}
	env := w.env(ctx)
	rows := engineRows{a: h.a}
	if er, ok := env.Rows.(engineRows); ok {
		rows = er // its List rolls a lost attempt back before the re-run picks (P3-SIGCOORD-6)
	}
	req := coord.NextRequest{
		Filter: queue.Filter{Mission: in.Mission, Role: in.Role, Repo: in.Repo, Here: h.hereRepo(ctx, w)},
		Opts:   coord.ClaimOptions{Owns: in.Owns, WorkBranch: w.workBranch(ctx), InRepo: w.taskInRepo()},
		List:   rows.List, RepoName: rows.RepoName, Pick: cryptoPick, Urgency: h.urgencyContext(ctx),
	}
	if !claim {
		pk, rerr := coord.PeekNext(ctx, env, req)
		if rerr != nil {
			return opscoord.NextOut{}, w.coordErr(rerr)
		}
		out := nextRanking(pk.Queue)
		if in.DryRun {
			out.Lines = dryRunLines(pk.Queue, in.Why)
			return out, nil
		}
		out.Holding = pk.Holding
		out.Lines = readLines(pk, in.Why, w.claimPushFlag())
		return out, nil
	}
	res, err := coord.Next(ctx, env, req)
	var lines []string
	for _, p := range res.Poisoned {
		lines = append(lines, fmt.Sprintf("! %s failed %d times and is now blocked (owner's inbox)", p, w.agents.MaxAttempts))
	}
	if err != nil {
		return opscoord.NextOut{Lines: lines}, w.coordErr(w.namedDoneBy(ctx, err))
	}
	if res.Resumed {
		lines = append(lines, fmt.Sprintf("you hold %s; resume, or `lore task release --id %s`", res.Task, res.Task))
		return opscoord.NextOut{Claimed: res.Task, Lines: lines}, nil
	}
	lines = append(lines, claimLines(res.Task, res.Claim, w.lease.TTL, w.claimPlace(env.Signals.Ref))...)
	if s, ok := res.Scores[res.Task]; ok && in.Why {
		lines = append(lines, "  urgency "+urgency.Format(s.Total))
		for _, l := range s.Lines() {
			lines = append(lines, "    "+l)
		}
	}
	lines = append(lines, "  read its notes: lore task show --id "+res.Task)
	return opscoord.NextOut{Claimed: res.Task, Lines: lines}, nil
}

// readLines is the text of a plain `task next` (devco:D-426): the task
// --claim would take and why it is next, the rest of the ranking, what
// waits, and how to take it. why breaks the reason out term by term; push
// is claimPushFlag, so the printed claim is one that works here (D-607).
func readLines(pk coord.Peek, why bool, push string) []string {
	return readLinesAt(pk, why, push, "", true)
}

// readLinesAt is readLines with scope, the flag that names the store
// task queue again (" --store <org>", " --setup <setup>", devco:D-666), on
// every printed task next; claimByID keeps the "or task claim --id"
// alternative (false where a claim by id would not reach that store).
func readLinesAt(pk coord.Peek, why bool, push, scope string, claimByID bool) []string {
	res := pk.Queue
	var out []string
	switch {
	case pk.Holding != "":
		out = append(out, fmt.Sprintf("you hold %s; resume it (%s resumes it), or `lore task release --id %s`", pk.Holding, "lore task next --claim"+push+scope, pk.Holding))
	case len(res.Ready) == 0:
		out = append(out, "nothing ready")
	default:
		top := res.Ready[0]
		s := res.Scores[top.ID]
		out = append(out, fmt.Sprintf("next %s %s   urgency %s", top.ID, top.Title, urgency.Format(s.Total)))
		out = append(out, nextReason(res, s, why)...)
	}
	if pk.Holding == "" && len(res.Ready) > 1 {
		out = append(out, "ranked:")
		for i, t := range res.Ready {
			out = append(out, fmt.Sprintf("  %d  %s  %s", i+1, t.ID, urgency.Format(res.Scores[t.ID].Total)))
		}
	}
	var waiting []string
	for _, wt := range res.Waiting {
		if wt.Task.ID != pk.Holding { // your own claim is said above, not as work that waits
			waiting = append(waiting, fmt.Sprintf("  %s  %s", wt.Task.ID, wt.Reason))
		}
	}
	if len(waiting) > 0 {
		out = append(append(out, "waiting:"), waiting...)
	}
	if pk.Holding == "" && len(res.Ready) > 0 {
		if !claimByID {
			return append(out, "nothing written; take it: lore task next --claim"+push+scope)
		}
		return append(out, fmt.Sprintf("nothing written; take it: %s (or %s)", "lore task next --claim"+push+scope, "lore task claim --id "+res.Ready[0].ID+push))
	}
	return append(out, "nothing written")
}

// nextReason is why the top task is next: its urgency terms on one line
// ("why: +6.00 priority high; +2.00 age 3d"), or one per line with why,
// and the lead over the runner-up when there is one.
func nextReason(res queue.Result, s urgency.Score, why bool) []string {
	terms := s.Lines()
	var out []string
	switch {
	case len(terms) == 0:
		out = append(out, "  why: the only ready task")
		if len(res.Ready) > 1 {
			out[0] = "  why: first of the ready tasks at equal urgency"
		}
	case why:
		out = append(out, "  why:")
		for _, l := range terms {
			out = append(out, "    "+l)
		}
	default:
		out = append(out, "  why: "+strings.Join(terms, "; "))
	}
	if len(res.Ready) > 1 {
		second := res.Ready[1]
		out = append(out, fmt.Sprintf("  ahead of %s (urgency %s)", second.ID, urgency.Format(res.Scores[second.ID].Total)))
	}
	return out
}

// cryptoPick is a uniformly random number in [0, n).
func cryptoPick(n int) int { return int(signals.CryptoJitter{}.Int63n(int64(n))) }

// urgencyContext resolves the urgency inputs on other rows: the `next` tag
// and mission target dates. A read failure degrades to no such term:
// ordering is a preference, not a safety rule.
func (h standaloneCoord) urgencyContext(ctx context.Context) coord.UrgencyContext {
	var u coord.UrgencyContext
	next := map[string]bool{}
	target := map[string]time.Time{}
	for _, src := range h.a.ReadStores() {
		if tags, err := h.a.List(ctx, src, schema.TableTags); err == nil {
			for _, t := range tags {
				if d, derr := docOfRow(t); derr == nil && !t.Deleted && strings.EqualFold(d.Str(fieldTagName), nextTagName) {
					next[t.ID.String()] = true
				}
			}
		}
		if ms, err := h.a.List(ctx, src, schema.TableMissions); err == nil {
			for _, m := range ms {
				if d, derr := docOfRow(m); derr == nil && !m.Deleted {
					target[m.ID.String()] = d.Time(fieldTargetDate)
				}
			}
		}
	}
	u.NextTag = func(id string) bool { return next[id] }
	u.MissionTarget = func(id string) time.Time { return target[id] }
	return u
}

// fieldTargetDate is a mission's target date.
const fieldTargetDate = "target_date"

// nextRanking is a --dry-run result in the v0.1.17 --dry-run --json shape
// (ready and waiting are [] when empty, never null).
func nextRanking(res queue.Result) opscoord.NextOut {
	out := opscoord.NextOut{DryRun: true, Ready: []opscoord.Ranked{}, Waiting: []opscoord.Waiting{}}
	for _, t := range res.Ready {
		s := res.Scores[t.ID]
		out.Ready = append(out.Ready, opscoord.Ranked{ID: t.ID, Title: t.Title, Urgency: urgency.Format(s.Total), Terms: s.Terms})
	}
	for _, wt := range res.Waiting {
		out.Waiting = append(out.Waiting, opscoord.Waiting{ID: wt.Task.ID, Kind: string(wt.Kind), Reason: wt.Reason})
	}
	return out
}

// dryRunLines is TASK 13.3's ranking text (v0.1.17 printNextDryRun).
func dryRunLines(res queue.Result, why bool) []string {
	var out []string
	if len(res.Ready) == 0 {
		out = append(out, "nothing ready")
	} else {
		top := res.Ready[0]
		s := res.Scores[top.ID]
		out = append(out, fmt.Sprintf("would claim %s %s   urgency %s", top.ID, top.Title, urgency.Format(s.Total)))
		if why {
			for _, l := range s.Lines() {
				out = append(out, "  "+l)
			}
		}
		out = append(out, "ranked:")
		for i, t := range res.Ready {
			out = append(out, fmt.Sprintf("  %d  %s  %s", i+1, t.ID, urgency.Format(res.Scores[t.ID].Total)))
		}
	}
	if len(res.Waiting) > 0 {
		out = append(out, "waiting:")
		for _, wt := range res.Waiting {
			out = append(out, fmt.Sprintf("  %s  %s", wt.Task.ID, wt.Reason))
		}
	}
	return append(out, "nothing written (--dry-run)")
}

// Fields read from task and tag documents.
const (
	fieldTitle   = "title"
	fieldTagName = "name"
)
