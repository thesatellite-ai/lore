// live_cmd.go — the live ref behind `lore live show|diff|log|restore|
// compact|purge` (§1 "Commands", §6.6, §9.4, F8). The commands themselves
// are operations of the core registry (ops/live, PASS3 ONE-PATH); this file
// is the release binary's opslive.Ref (wire.Options.LiveRef: coreLiveRef),
// built on the command's live session (live_wire.go), so the pre-run fetch
// and integrate and the end-of-command push work as in v0.1.17.
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/live"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/model/errs"
	opslive "github.com/thesatellite-ai/lore/ops/live"
)

// liveHere returns the live session and folder of the cwd project (the
// v0.1.17 commands that are still cobra commands: land, explain, doctor).
func liveHere(cmd *cobra.Command) (*liveSession, string, error) {
	return liveHereCtx(cmd.Context())
}

// liveHereCtx is liveHere for a context: E_NOT_PROJECT_ROOT outside a lore
// project, E_UNSUPPORTED where live rows do not apply.
func liveHereCtx(ctx context.Context) (*liveSession, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", errcodes.New(errcodes.Internal, "cwd").WithCause(err)
	}
	root := liveProjectRoot(cwd)
	if root == "" {
		return nil, "", errcodes.New(errcodes.NotProjectRoot, "not inside a lore project")
	}
	s := liveFor(ctx, root)
	if s == nil {
		return nil, "", errcodes.New(errcodes.Unsupported, "live rows are not available here (not a git repo, or LORE_LIVE=0)")
	}
	s.prepare(ctx, liveCmdState.fetchKind())
	return s, s.folderFor(root), nil
}

// coreLiveRef is wire.Options.LiveRef: the cwd project's live ref, with
// v0.1.17's errors carried into the core's codes (coreErr).
func coreLiveRef(ctx context.Context) (opslive.Ref, error) {
	s, folder, err := liveHereCtx(ctx)
	if err != nil {
		return nil, coreErr(err)
	}
	return &liveRefAdapter{s: s, folder: folder}, nil
}

// liveRefAdapter implements opslive.Ref over one live session and the
// project's live folder.
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

// File implements opslive.Ref: the row file as stored on the live ref.
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
	return nil, errs.New(errs.NotFound, id+" is not live in "+a.folder)
}

// Refs implements opslive.Ref.
func (a *liveRefAdapter) Refs(ctx context.Context) (opslive.Refs, error) {
	snap, err := a.s.ref.Snapshot(ctx)
	if err != nil {
		return opslive.Refs{}, errs.Wrap(errs.Internal, "read live ref", err)
	}
	out := opslive.Refs{Local: snap.Local, Tracking: snap.Tracking, Seen: snap.Seen, Pending: []string{}}
	pending, _ := a.s.ref.Pending() // display only: an unreadable list shows as 0 pending
	for _, p := range pending {
		out.Pending = append(out.Pending, p.Path)
	}
	return out, nil
}

// Log implements opslive.Ref.
func (a *liveRefAdapter) Log(ctx context.Context, n int) (opslive.History, error) {
	oid, tree, err := a.s.ref.LocalTree(ctx)
	if err != nil || oid == "" {
		return opslive.History{}, errs.New(errs.NotFound, "no live ref yet")
	}
	out, err := a.s.repo.Run(ctx, gitref.Invocation{Args: []string{"log", "--format=%h %ad %an  %s", "--date=short", fmt.Sprintf("-n%d", n), oid}})
	if err != nil {
		return opslive.History{}, errs.Wrap(errs.Internal, "git log", err)
	}
	return opslive.History{Epoch: tree.Epoch(), Lines: string(out)}, nil
}

// Restore implements opslive.Ref (§6.6). It runs while the ref is blocked:
// it is the repair.
func (a *liveRefAdapter) Restore(ctx context.Context) (opslive.Restored, error) {
	keep, left, err := a.s.restore(ctx)
	if err != nil {
		return opslive.Restored{}, coreErr(err)
	}
	return opslive.Restored{Rows: keep, LeftOut: left}, nil
}

// errLiveBlocked is the refusal of a ref write after the remote live ref
// was deleted or rewritten.
func errLiveBlocked() error {
	return errs.New(errs.ReadOnly, "the remote live ref was deleted or rewritten").WithHint("run `lore live restore` first")
}

// Compact implements opslive.Ref (F8).
func (a *liveRefAdapter) Compact(ctx context.Context, dryRun bool) (opslive.Compacted, error) {
	s := a.s
	if s.blocked != nil {
		return opslive.Compacted{}, errLiveBlocked()
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
	if dryRun {
		return out, nil
	}
	switch res.Status {
	case live.PushDone, live.PushNoRemote:
		s.finished = true
		return out, nil
	case live.PushNothing:
		out.Nothing = true
		return out, nil
	case live.PushOffline, live.PushRefused, live.PushRetriesOut:
		// not confirmed: the error below
	}
	return opslive.Compacted{}, errs.New(errs.Offline, "compaction not confirmed by the remote ("+string(res.Status)+")").
		WithHint("nothing was lost; run it again when the remote is reachable")
}

// Purge implements opslive.Ref (§6.6); the end-of-command push sends it.
func (a *liveRefAdapter) Purge(ctx context.Context, ids []string) error {
	s := a.s
	if s.blocked != nil {
		return errLiveBlocked()
	}
	if _, err := s.ref.Purge(ctx, ids, version); err != nil {
		return errs.Wrap(errs.Internal, "purge", err)
	}
	s.wrote += len(ids)
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// restore pushes the newest local copy back to the remote and returns the
// rows kept and left out (landed or purged).
func (s *liveSession) restore(ctx context.Context) (int, int, error) {
	src, err := s.restoreSource(ctx)
	if err != nil {
		return 0, 0, err
	}
	if src == "" {
		return 0, 0, errcodes.New(errcodes.NotFound, "no local copy of the live ref to restore")
	}
	tree, err := s.ref.ReadTree(ctx, src)
	if err != nil {
		return 0, 0, errcodes.New(errcodes.Internal, "read backup").WithCause(err)
	}
	keep, dropped, err := s.restorableTree(ctx, tree)
	if err != nil {
		return 0, 0, err
	}
	ch := gitref.Changes{Put: map[string][]byte{}, Delete: dropped}
	c, err := s.repo.MakeCommit(ctx, gitref.CommitRequest{
		Base: src, Changes: ch, Message: fmt.Sprintf("lore: live restore (%d rows)", keep), Signature: s.ref.Signed(),
	})
	if err != nil {
		return 0, 0, errcodes.New(errcodes.Internal, "build restore commit").WithCause(err)
	}
	snap, err := s.ref.Snapshot(ctx)
	if err != nil {
		return 0, 0, errcodes.New(errcodes.Internal, "read live ref").WithCause(err)
	}
	if snap.Local != c.OID {
		if updateErr := s.repo.UpdateRef(ctx, s.ref.Name, c.OID, snap.Local); updateErr != nil {
			return 0, 0, errcodes.New(errcodes.Internal, "move local live ref").WithCause(updateErr)
		}
	}
	if clearErr := s.ref.ClearSeen(); clearErr != nil {
		return 0, 0, errcodes.New(errcodes.Internal, "reset seen marker").WithCause(clearErr)
	}
	out, err := s.ref.ForcePush(ctx, snap.Tracking)
	if err != nil {
		return 0, 0, errcodes.New(errcodes.Internal, "push restored live ref").WithCause(err)
	}
	if out.Status != live.PushDone && out.Status != live.PushNoRemote {
		return 0, 0, errcodes.New(errcodes.Offline, "the restored live ref was not confirmed by the remote ("+string(out.Status)+")").
			WithHint("the local copy is kept; run `lore live restore` again when the remote is reachable")
	}
	s.blocked = nil
	s.finished = true
	return keep, len(dropped), nil
}

// restoreSource is the commit to restore: the local live ref when it holds
// rows, else the newest backup.
func (s *liveSession) restoreSource(ctx context.Context) (string, error) {
	names, refs, err := s.ref.Backups(ctx)
	if err != nil {
		return "", errcodes.New(errcodes.Internal, "list backups").WithCause(err)
	}
	snap, err := s.ref.Snapshot(ctx)
	if err != nil {
		return "", errcodes.New(errcodes.Internal, "read live ref").WithCause(err)
	}
	if snap.Local != "" {
		return snap.Local, nil
	}
	if len(names) > 0 {
		return refs[names[0]], nil
	}
	return "", nil
}

// restorableTree lists row paths to leave out of a restore: rows whose
// content already sits on origin/main (landed) and purged ids. An
// unreadable _live/purged.json fails the restore: pushing the backup without
// knowing which ids were purged would bring purged rows back (fail closed).
func (s *liveSession) restorableTree(ctx context.Context, tree live.Tree) (int, []string, error) {
	purged, err := tree.Purged()
	if err != nil {
		return 0, nil, errcodes.New(errcodes.Internal, "read the purged ids of the live copy; nothing was restored").WithCause(err)
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
				body, err := f.EncodeBody()
				if err == nil {
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
