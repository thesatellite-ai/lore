package host

// live_ref.go — opslive.Ref (`lore live show|diff|log|restore|compact|
// purge`, §1 "Commands", §6.6, F8) over the run's live session: wire's
// Options.LiveRef. Ported from saas/cmd/cli live_cmd.go.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/errs"
	opslive "github.com/thesatellite-ai/lore/ops/live"
)

// liveUnsupported is v0.1.17's refusal where live rows do not apply.
const liveUnsupported = "live rows are not available here (not a git repo, or LORE_LIVE=0)"

// liveUnsupportedHint is the next step of that refusal.
const liveUnsupportedHint = "live rows need a git checkout and LORE_LIVE unset (or 1); `lore sync status` shows whether they apply here"

// liveRef is wire.Options.LiveRef: the cwd project's live ref.
func (h *liveHost) liveRef(ctx context.Context) (opslive.Ref, error) {
	root := liveProjectRoot(h.cfg.WorkDir)
	if root == "" {
		return nil, errs.New(errs.NotProjectRoot, "not inside a lore project")
	}
	r := runFrom(ctx)
	if r == nil || liveDisabled() {
		return nil, errs.New(errs.Unsupported, liveUnsupported).WithHint(liveUnsupportedHint)
	}
	top, folder := livePlace(root)
	s := r.session(ctx, top)
	if s == nil {
		return nil, errs.New(errs.Unsupported, liveUnsupported).WithHint(liveUnsupportedHint)
	}
	return &liveRefAdapter{s: s, folder: folder}, nil
}

// liveRefAdapter implements opslive.Ref over one session and folder.
type liveRefAdapter struct {
	s      *liveSession
	folder string
}

var _ opslive.Ref = (*liveRefAdapter)(nil)

// List implements opslive.Ref: the folder's rows, sorted by "table/id".
func (a *liveRefAdapter) List(ctx context.Context) (opslive.Listing, error) {
	_, tree, err := a.s.ref.LocalTree(ctx)
	if err != nil {
		return opslive.Listing{}, errs.Wrap(errs.Internal, "read live ref", err)
	}
	rows, _ := tree.Rows(a.folder) // v0.1.17: an unreadable folder lists as empty
	keys := make([]live.RowKey, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	out := opslive.Listing{Folder: a.folder, Epoch: tree.Epoch(), Rows: make([]opslive.Row, 0, len(keys))}
	for _, k := range keys {
		f := rows[k]
		state := opslive.StateLive
		if f.Deleted() {
			state = opslive.StateTombstone
		} else if f.Block.Conflict {
			state = opslive.StateConflict
		}
		out.Rows = append(out.Rows, opslive.Row{Table: k.Table, ID: k.ID, State: state, Since: f.Block.LiveSince})
	}
	return out, nil
}

// File implements opslive.Ref: the row file as stored.
func (a *liveRefAdapter) File(ctx context.Context, id string) ([]byte, error) {
	_, tree, err := a.s.ref.LocalTree(ctx)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "read live ref", err)
	}
	rows, _ := tree.Rows(a.folder)
	for k, f := range rows {
		if k.ID == id {
			b, err := f.Encode()
			if err != nil {
				return nil, errs.Wrap(errs.Internal, "encode", err)
			}
			return b, nil
		}
	}
	return nil, errs.New(errs.NotFound, id+" is not live in "+a.folder).
		WithHint("`lore live show` lists the live rows; a row that is not live reads with `lore row show --id <id>`")
}

// Refs implements opslive.Ref.
func (a *liveRefAdapter) Refs(ctx context.Context) (opslive.Refs, error) {
	snap, err := a.s.ref.Snapshot(ctx)
	if err != nil {
		return opslive.Refs{}, errs.Wrap(errs.Internal, "read live ref", err)
	}
	out := opslive.Refs{Local: snap.Local, Tracking: snap.Tracking, Seen: snap.Seen, Pending: []string{}}
	pending, _ := a.s.ref.Pending() // display only: unreadable shows as 0 pending
	for _, p := range pending {
		out.Pending = append(out.Pending, p.Path)
	}
	return out, nil
}

// Log implements opslive.Ref.
func (a *liveRefAdapter) Log(ctx context.Context, n int) (opslive.History, error) {
	oid, tree, err := a.s.ref.LocalTree(ctx)
	if err != nil || oid == "" {
		return opslive.History{}, errs.New(errs.NotFound, "no live ref yet").
			WithHint("the first live write (e.g. `lore task add`) creates it; `lore live show` lists live rows")
	}
	out, err := a.s.repo.Run(ctx, gitref.Invocation{Args: []string{"log", "--format=%h %ad %an  %s", "--date=short", fmt.Sprintf("-n%d", n), oid}})
	if err != nil {
		return opslive.History{}, errs.Wrap(errs.Internal, "git log", err)
	}
	return opslive.History{Epoch: tree.Epoch(), Lines: string(out)}, nil
}

// errLiveBlocked refuses a ref write after the remote ref was rewritten.
func errLiveBlocked(blocked error) error {
	if errors.Is(blocked, live.ErrRemoteGoneURLUnknown) {
		return errs.New(errs.ReadOnly, liveGoneURLUnknownText).WithHint(liveGoneURLUnknownHint)
	}
	return errs.New(errs.ReadOnly, "the remote live ref was deleted or rewritten").WithHint("run `lore live restore` first")
}

// Compact implements opslive.Ref (F8).
func (a *liveRefAdapter) Compact(ctx context.Context, dryRun bool) (opslive.Compacted, error) {
	s := a.s
	if s.blocked != nil {
		return opslive.Compacted{}, errLiveBlocked(s.blocked)
	}
	res, err := s.ref.Compact(ctx, dryRun)
	if err != nil {
		return opslive.Compacted{}, errs.Wrap(errs.Internal, "compact", err)
	}
	p := res.Plan
	out := opslive.Compacted{Backup: res.Backup, Plan: opslive.CompactPlan{
		Ref: s.ref.Name, Commits: p.Commits, Threshold: s.cfg.CompactAfter, Files: p.Files,
		FromEpoch: p.FromEpoch, ToEpoch: p.ToEpoch, Purged: p.Purged,
	}}
	if res.Status == live.PushNothing {
		// No live ref yet, or already one commit with nothing purged
		// (live.CompactNeeded): the same answer with and without --dry-run.
		out.Nothing = true
		return out, nil
	}
	if dryRun {
		return out, nil
	}
	switch res.Status {
	case live.PushDone, live.PushNoRemote:
		s.finished = true
		return out, nil
	default: // offline, refused, retries out (PushNothing answered above)
	}
	return opslive.Compacted{}, errs.New(errs.Offline, "compaction not confirmed by the remote ("+string(res.Status)+")").
		WithHint("nothing was lost; run it again when the remote is reachable")
}

// Purge implements opslive.Ref (§6.6); the run's finish pushes it.
func (a *liveRefAdapter) Purge(ctx context.Context, ids []string) error {
	s := a.s
	if s.blocked != nil {
		return errLiveBlocked(s.blocked)
	}
	if _, err := s.ref.Purge(ctx, ids, s.h.cfg.Version); err != nil {
		return errs.Wrap(errs.Internal, "purge", err)
	}
	s.wrote += len(ids)
	return nil
}

// Restore implements opslive.Ref (§6.6): it runs while the ref is blocked,
// it is the repair. The newest local copy (or backup) goes back to the
// remote without landed and purged rows.
func (a *liveRefAdapter) Restore(ctx context.Context) (opslive.Restored, error) {
	s := a.s
	src, err := s.restoreSource(ctx)
	if err != nil {
		return opslive.Restored{}, err
	}
	if src == "" {
		return opslive.Restored{}, errs.New(errs.NotFound, "no local copy of the live ref to restore")
	}
	tree, err := s.ref.ReadTree(ctx, src)
	if err != nil {
		return opslive.Restored{}, errs.Wrap(errs.Internal, "read backup", err)
	}
	keep, dropped, err := s.restorableTree(ctx, tree)
	if err != nil {
		return opslive.Restored{}, err
	}
	c, err := s.repo.MakeCommit(ctx, gitref.CommitRequest{
		Base: src, Changes: gitref.Changes{Put: map[string][]byte{}, Delete: dropped},
		Message: fmt.Sprintf("lore: live restore (%d rows)", keep), Signature: s.ref.Signed(),
	})
	if err != nil {
		return opslive.Restored{}, errs.Wrap(errs.Internal, "build restore commit", err)
	}
	snap, err := s.ref.Snapshot(ctx)
	if err != nil {
		return opslive.Restored{}, errs.Wrap(errs.Internal, "read live ref", err)
	}
	if snap.Local != c.OID {
		if err = s.repo.UpdateRef(ctx, s.ref.Name, c.OID, snap.Local); err != nil {
			return opslive.Restored{}, errs.Wrap(errs.Internal, "move local live ref", err)
		}
	}
	if err = s.ref.ClearSeen(); err != nil {
		return opslive.Restored{}, errs.Wrap(errs.Internal, "reset seen marker", err)
	}
	out, err := s.ref.ForcePush(ctx, snap.Tracking)
	if err != nil {
		return opslive.Restored{}, errs.Wrap(errs.Internal, "push restored live ref", err)
	}
	if out.Status != live.PushDone && out.Status != live.PushNoRemote {
		return opslive.Restored{}, errs.New(errs.Offline, "the restored live ref was not confirmed by the remote ("+string(out.Status)+")").
			WithHint("the local copy is kept; run `lore live restore` again when the remote is reachable")
	}
	s.blocked = nil
	s.finished = true
	return opslive.Restored{Rows: keep, LeftOut: len(dropped)}, nil
}

// restoreSource is the local live ref when it exists, else the newest backup.
func (s *liveSession) restoreSource(ctx context.Context) (string, error) {
	names, refs, err := s.ref.Backups(ctx)
	if err != nil {
		return "", errs.Wrap(errs.Internal, "list backups", err)
	}
	snap, err := s.ref.Snapshot(ctx)
	if err != nil {
		return "", errs.Wrap(errs.Internal, "read live ref", err)
	}
	if snap.Local != "" {
		return snap.Local, nil
	}
	if len(names) > 0 {
		return refs[names[0]], nil
	}
	return "", nil
}

// restorableTree counts the rows a restore keeps and lists the paths it
// leaves out: purged ids and rows already on origin/<default> (landed). An
// unreadable purged list fails the restore (fail closed).
func (s *liveSession) restorableTree(ctx context.Context, tree live.Tree) (int, []string, error) {
	purged, err := tree.Purged()
	if err != nil {
		return 0, nil, errs.Wrap(errs.Internal, "read the purged ids of the live copy; nothing was restored", err)
	}
	mainRef := "refs/remotes/" + s.ref.RemoteName + "/" + s.defaultBranch(ctx)
	var dropped []string
	keep := 0
	for _, folder := range tree.Folders() {
		rows, _ := tree.Rows(folder)
		for k, f := range rows {
			p := live.RowPath(folder, k.Table, k.ID)
			if purged[k.ID] {
				dropped = append(dropped, p)
				continue
			}
			if !f.Deleted() {
				if body, err := f.EncodeBody(); err == nil {
					onMain, found, rerr := s.repo.ReadFile(ctx, mainRef, mainDataPath(folder, k.Table+"/"+k.ID+".json"))
					if rerr == nil && found && strings.TrimSpace(string(onMain)) == strings.TrimSpace(string(body)) {
						dropped = append(dropped, p)
						continue
					}
				}
			}
			keep++
		}
	}
	sort.Strings(dropped)
	return keep, dropped, nil
}
