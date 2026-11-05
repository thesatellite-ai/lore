// coord_ops_run.go — the run functions behind the ops/coord operations of
// `lore task` (next, heartbeat, release, revoke, reassign): v0.1.17's
// command bodies, writing their lines to out instead of the terminal so
// the operation returns them as typed output (PASS3 ONE-PATH, P3-COORD-1).
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/queue"
	"saas/pkg/aicoder/signals/coord"
	"saas/pkg/aicoder/style"

	"github.com/thesatellite-ai/lore/logic/urgency"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	"github.com/thesatellite-ai/lore/schema"
)

// runTaskNext is task.next: resume the claim held, or claim the most
// urgent ready leaf; --dry-run ranks and explains, writing nothing.
// Poisoned tasks are announced even when the claim then fails, so the
// lines gathered so far are in the output either way.
func runTaskNext(ctx context.Context, in opscoord.NextIn) (opscoord.NextOut, error) {
	var b bytes.Buffer
	res, err := taskNext(ctx, &b, in)
	res.Lines = plainLines(b.String())
	return res, err
}

// taskNext writes task next's lines to out.
func taskNext(ctx context.Context, out io.Writer, in opscoord.NextIn) (opscoord.NextOut, error) {
	w, t, err := openTaskCoord(ctx, "", "")
	if err != nil {
		return opscoord.NextOut{}, err
	}
	defer t.Close()
	list, ok := t.Rows.(coordLister)
	if !ok {
		return opscoord.NextOut{}, errRowsNotWired()
	}
	if mc, ok := t.Rows.(interface{ MergedCheck(context.Context) }); ok {
		mc.MergedCheck(ctx)
	}
	f := queue.Filter{Mission: in.Mission, Role: in.Role, Repo: in.Repo, Here: t.Repo}
	req := coord.NextRequest{
		Filter: f, Opts: coord.ClaimOptions{Owns: in.Owns, WorkBranch: currentWorkBranch(ctx, w.Repo), InRepo: !isStoreTarget(t)},
		List: list.List, RepoName: list.RepoName, Pick: cryptoPick, Urgency: urgencyContext(ctx),
	}
	if in.DryRun {
		// TASK 13.3: rank and explain, write nothing.
		v, readErr := w.env(t).Signals.Read(ctx)
		if readErr != nil {
			return opscoord.NextOut{}, coordError(readErr)
		}
		res, readErr := coord.Queue(ctx, w.env(t), v, req)
		if readErr != nil {
			return opscoord.NextOut{}, coordError(readErr)
		}
		printNextDryRun(out, res, in.Why)
		return nextRanking(res), nil
	}
	res, err := coord.Next(ctx, w.env(t), req)
	for _, p := range res.Poisoned {
		_, _ = fmt.Fprintf(out, "%s %s failed %d times and is now blocked (owner's inbox)\n", style.Warn("!"), p, w.Agents.MaxAttempts)
	}
	if err != nil {
		return opscoord.NextOut{}, coordError(err)
	}
	if res.Resumed {
		_, _ = fmt.Fprintf(out, "you hold %s; resume, or `lore task release %s`\n", res.Task, res.Task)
		return opscoord.NextOut{Claimed: res.Task}, nil
	}
	printClaim(out, res.Task, res.Claim, w, t.Signals.Ref)
	if in.Why {
		if s, ok := res.Scores[res.Task]; ok {
			_, _ = fmt.Fprintf(out, "  urgency %s\n", urgency.Format(s.Total))
			for _, l := range s.Lines() {
				_, _ = fmt.Fprintln(out, "    "+l)
			}
		}
	}
	_, _ = fmt.Fprintf(out, "  read its notes: lore task show %s\n", res.Task)
	return opscoord.NextOut{Claimed: res.Task}, nil
}

// nextRanking is a --dry-run result as the v0.1.17 --dry-run --json shape
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

// splitLines is printed text as lines, without the final newline.
func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// runTaskHeartbeat is task.heartbeat: renew one claim, or every claim of
// this session (rate limited to renew_every), or keep renewing (--daemon).
func runTaskHeartbeat(ctx context.Context, out io.Writer, in opscoord.HeartbeatIn) error {
	w, err := openCoordWorld(ctx)
	if err != nil {
		return err
	}
	if err := w.requireActor(); err != nil {
		return err
	}
	var ids []string
	if in.ID != "" {
		ids = []string{in.ID}
	} else {
		for _, r := range w.held().List(w.Session()) {
			ids = append(ids, r.Task)
		}
		if len(ids) == 0 {
			_, _ = fmt.Fprintln(out, "no claims held by this session")
			return nil
		}
	}
	if in.Daemon {
		return heartbeatDaemon(ctx, w, ids)
	}
	for _, id := range ids {
		if in.ID == "" && renewedRecently(w, id) {
			continue
		}
		s, err := renewOne(ctx, w, id)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "%s %s renewed until %s (seq %d)\n", style.Success("✓"), id, s.Expires.UTC().Format(time.RFC3339), s.RenewedSeq)
	}
	return nil
}

// runTaskRelease is task.release: give back a claim you hold.
func runTaskRelease(ctx context.Context, out io.Writer, in opscoord.ReleaseIn) error {
	w, t, err := openTaskCoord(ctx, in.ID, in.From)
	if err != nil {
		return err
	}
	defer t.Close()
	if err := coord.Release(ctx, w.env(t), in.ID); err != nil {
		return coordError(err)
	}
	_, _ = fmt.Fprintf(out, "%s released %s\n", style.Success("✓"), in.ID)
	return nil
}

// runTaskRevoke is task.revoke: a person drops the current claim; the
// holder is fenced out.
func runTaskRevoke(ctx context.Context, out io.Writer, in opscoord.RevokeIn) error {
	w, t, err := openTaskCoord(ctx, in.ID, in.From)
	if err != nil {
		return err
	}
	defer t.Close()
	if err = requireHuman(w, "revoke"); err != nil {
		return err
	}
	removed, err := coord.Override(ctx, w.env(t), in.ID, coord.OverrideRevoke, nil)
	if err != nil {
		return coordError(err)
	}
	who := "no one held it"
	if removed != nil {
		who = "was held by " + holderLabel(*removed)
	}
	_, _ = fmt.Fprintf(out, "%s revoked the claim on %s (%s)\n", style.Success("✓"), in.ID, who)
	return nil
}

// runTaskReassign is task.reassign: one task, or every open task of
// --from @handle, to --assign @handle (TASK 14.4 lead + assignees, 17.2
// history, claim_epoch raised so the holder is fenced out).
func runTaskReassign(ctx context.Context, out io.Writer, in opscoord.ReassignIn) error {
	if in.Assign == "" {
		return errcodes.New(errcodes.InvalidInput, "--assign @handle is required")
	}
	w, err := openCoordWorld(ctx)
	if err != nil {
		return err
	}
	if err = requireHuman(w, "reassign"); err != nil {
		return err
	}
	to, err := resolveHandleActor(ctx, in.Assign)
	if err != nil {
		return err
	}
	var ids []string
	switch {
	case in.ID != "":
		ids = []string{in.ID}
	case in.From != "":
		who, err := resolveHandleActor(ctx, in.From)
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
	by := w.Caller().ActorID
	for _, id := range ids {
		if err := reassignOne(ctx, w, id, to, by); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "%s %s reassigned to %s\n", style.Success("✓"), id, in.Assign)
	}
	if len(ids) == 0 {
		_, _ = fmt.Fprintf(out, "no open tasks assigned to %s\n", in.From)
	}
	return nil
}

// reassignOne reassigns task id to actor to, closing its target before
// the next task opens.
func reassignOne(ctx context.Context, w *coordWorld, id, to, by string) error {
	t, err := openCoordTarget(ctx, w, id)
	if err != nil {
		return err
	}
	defer t.Close()
	if _, err := coord.OverrideChecked(ctx, w.env(t), id, coord.OverrideReassign, func(d coord.Doc, now time.Time) error {
		prevLead, prevAll := d.Str(fieldTaskLead), d.Strs(schema.FieldTaskAssignees)
		d.SetStr(fieldTaskLead, to)
		d[schema.FieldTaskAssignees] = []string{to}
		return appendReassignHistory(d, by, now, prevLead, prevAll, to)
	}); err != nil {
		return coordError(err)
	}
	return nil
}
