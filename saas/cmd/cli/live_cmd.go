// live_cmd.go — `lore live show|diff|log|restore` (§1 "Commands", §6.6,
// §9.4): inspect and repair the live ref.
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
)

func newLiveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "live",
		Short: "Inspect and maintain the live ref (refs/lore/live)",
		Long: `Live rows are shared with everyone at once on one hidden ref per repo,
refs/lore/live, one folder per lore project. These commands show and repair it.`,
	}
	cmd.AddCommand(newLiveShowCommand(), newLiveDiffCommand(), newLiveLogCommand(), newLiveRestoreCommand(),
		newLiveCompactCommand(), newLivePurgeCommand())
	return cmd
}

// liveHere returns the live session and folder of the cwd project.
func liveHere(cmd *cobra.Command) (*liveSession, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", errcodes.New(errcodes.Internal, "cwd").WithCause(err)
	}
	root := liveProjectRoot(cwd)
	if root == "" {
		return nil, "", errcodes.New(errcodes.NotProjectRoot, "not inside a lore project")
	}
	s := liveFor(cmd.Context(), root)
	if s == nil {
		return nil, "", errcodes.New(errcodes.Unsupported, "live rows are not available here (not a git repo, or LORE_LIVE=0)")
	}
	s.prepare(cmd.Context(), liveCmdState.fetchKind())
	return s, s.folderFor(root), nil
}

func newLiveShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show [id]",
		Short: "List this project's live rows, or print one live file",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, folder, err := liveHere(cmd)
			if err != nil {
				return err
			}
			_, tree, err := s.ref.LocalTree(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "read live ref").WithCause(err)
			}
			rows, _ := tree.Rows(folder)
			if len(args) == 1 {
				for k, f := range rows {
					if k.ID == args[0] {
						b, err := f.Encode()
						if err != nil {
							return errcodes.New(errcodes.Internal, "encode").WithCause(err)
						}
						fmt.Print(string(b))
						return nil
					}
				}
				return errcodes.New(errcodes.NotFound, args[0]+" is not live in "+folder)
			}
			keys := make([]live.RowKey, 0, len(rows))
			for k := range rows {
				keys = append(keys, k)
			}
			sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
			for _, k := range keys {
				f := rows[k]
				state := "live"
				if f.Deleted() {
					state = "tombstone"
				} else if f.Block.Conflict {
					state = "CONFLICT"
				}
				fmt.Printf("%-10s %-40s %-9s since %s\n", k.Table, k.ID, state, f.Block.LiveSince.Format("2006-01-02 15:04"))
			}
			fmt.Printf("%d live rows in %s (epoch %d)\n", len(keys), folder, tree.Epoch())
			return nil
		},
	}
}

func newLiveDiffCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "diff",
		Short: "Local, tracking and remote live ref, and the writes not pushed yet",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, _, err := liveHere(cmd)
			if err != nil {
				return err
			}
			snap, err := s.ref.Snapshot(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "read live ref").WithCause(err)
			}
			fmt.Printf("local     %s\ntracking  %s\nseen      %s\n", orNone(snap.Local), orNone(snap.Tracking), orNone(snap.Seen))
			pending, _ := s.ref.Pending()
			fmt.Printf("pending   %d write(s) not confirmed by the remote\n", len(pending))
			for _, p := range pending {
				fmt.Println("  " + p.Path)
			}
			return nil
		},
	}
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// liveLogLimit is how many commits `lore live log` prints by default.
const liveLogLimit = 20

func newLiveLogCommand() *cobra.Command {
	var n int
	cmd := &cobra.Command{
		Use:   "log",
		Short: "History of the live ref with its epoch",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, _, err := liveHere(cmd)
			if err != nil {
				return err
			}
			oid, tree, err := s.ref.LocalTree(cmd.Context())
			if err != nil || oid == "" {
				return errcodes.New(errcodes.NotFound, "no live ref yet")
			}
			out, err := s.repo.Run(cmd.Context(), gitref.Invocation{Args: []string{"log", "--format=%h %ad %an  %s", "--date=short", fmt.Sprintf("-n%d", n), oid}})
			if err != nil {
				return errcodes.New(errcodes.Internal, "git log").WithCause(err)
			}
			fmt.Printf("epoch %d\n%s", tree.Epoch(), out)
			return nil
		},
	}
	cmd.Flags().IntVarP(&n, "number", "n", liveLogLimit, "commits to show")
	return cmd
}

func newLiveRestoreCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "restore",
		Short: "Put the live rows back on the remote from the local copy after it was deleted or rewritten",
		Long: `When the remote live ref was deleted or replaced (a mirror push, a
mistaken delete), lore stops writing to it and keeps the local copy under
refs/lore-backup/live/. restore pushes that copy back, leaving out rows that
already landed on main and ids purged with ` + "`lore live purge`" + ` (§6.6).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, _, err := liveHere(cmd)
			if err != nil {
				return err
			}
			return s.restore(cmd.Context())
		},
	}
}

// restore pushes the newest local copy back to the remote.
func (s *liveSession) restore(ctx context.Context) error {
	src, err := s.restoreSource(ctx)
	if err != nil {
		return err
	}
	if src == "" {
		return errcodes.New(errcodes.NotFound, "no local copy of the live ref to restore")
	}
	tree, err := s.ref.ReadTree(ctx, src)
	if err != nil {
		return errcodes.New(errcodes.Internal, "read backup").WithCause(err)
	}
	keep, dropped := s.restorableTree(ctx, tree)
	ch := gitref.Changes{Put: map[string][]byte{}, Delete: dropped}
	c, err := s.repo.MakeCommit(ctx, gitref.CommitRequest{
		Base: src, Changes: ch, Message: fmt.Sprintf("lore: live restore (%d rows)", keep), Signature: s.ref.Signed(),
	})
	if err != nil {
		return errcodes.New(errcodes.Internal, "build restore commit").WithCause(err)
	}
	snap, err := s.ref.Snapshot(ctx)
	if err != nil {
		return errcodes.New(errcodes.Internal, "read live ref").WithCause(err)
	}
	if snap.Local != c.OID {
		if err := s.repo.UpdateRef(ctx, s.ref.Name, c.OID, snap.Local); err != nil {
			return errcodes.New(errcodes.Internal, "move local live ref").WithCause(err)
		}
	}
	if err := s.ref.ClearSeen(); err != nil {
		return errcodes.New(errcodes.Internal, "reset seen marker").WithCause(err)
	}
	out, err := s.ref.ForcePush(ctx, snap.Tracking)
	if err != nil {
		return errcodes.New(errcodes.Internal, "push restored live ref").WithCause(err)
	}
	if out.Status != live.PushDone && out.Status != live.PushNoRemote {
		return errcodes.New(errcodes.Offline, "the restored live ref was not confirmed by the remote ("+string(out.Status)+")").
			WithHint("the local copy is kept; run `lore live restore` again when the remote is reachable")
	}
	s.blocked = nil
	s.finished = true
	fmt.Printf("✓ restored refs/lore/live: %d rows (left out %d already landed or purged)\n", keep, len(dropped))
	return nil
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
// content already sits on origin/main (landed) and purged ids.
func (s *liveSession) restorableTree(ctx context.Context, tree live.Tree) (int, []string) {
	purged, _ := tree.Purged()
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
	return keep, dropped
}

func newLiveCompactCommand() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "compact",
		Short: "Rewrite the live ref to one commit with a higher epoch (keeps every row; a local backup is kept)",
		Long: `The live ref's history grows with every live write. compact replaces it with
one parentless commit holding the same rows, raises the epoch in _live/epoch
and keeps the old local copy under refs/lore-backup/live/ (never pushed).
Teammates' clones re-apply only their own unpushed writes on top (F8).
It also runs by itself once the ref has more than live.compact_after commits.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, _, err := liveHere(cmd)
			if err != nil {
				return err
			}
			if s.blocked != nil {
				return errcodes.New(errcodes.ReadOnly, "the remote live ref was deleted or rewritten").WithHint("run `lore live restore` first")
			}
			res, err := s.ref.Compact(cmd.Context(), dryRun)
			if err != nil {
				return errcodes.New(errcodes.Internal, "compact").WithCause(err)
			}
			p := res.Plan
			if dryRun {
				fmt.Printf("%s has %d commits (threshold %d); compacting would keep %d files, epoch %d → %d", s.ref.Name, p.Commits, s.cfg.CompactAfter, p.Files, p.FromEpoch, p.ToEpoch)
				if p.Purged > 0 {
					fmt.Printf(", dropping %d purged rows", p.Purged)
				}
				fmt.Println()
				return nil
			}
			switch res.Status {
			case live.PushDone, live.PushNoRemote:
				s.finished = true
				fmt.Printf("✓ compacted %s to 1 commit (epoch %d); backup kept locally as %s\n", s.ref.Name, p.ToEpoch, res.Backup)
				return nil
			case live.PushNothing:
				fmt.Println("nothing to compact: no live ref yet")
				return nil
			}
			return errcodes.New(errcodes.Offline, "compaction not confirmed by the remote ("+string(res.Status)+")").
				WithHint("nothing was lost; run it again when the remote is reachable")
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would happen, change nothing")
	return cmd
}

func newLivePurgeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "purge <id>...",
		Short: "Remove rows from live for good (leak cleanup): no clone can push them back",
		Long: `purge removes the rows from the live ref and lists their ids in
_live/purged.json, so a stale clone holding them never pushes them back and
every clone drops them. Their old versions stay in the live ref's history
until ` + "`lore live compact`" + ` rewrites it (§6.6). A copy that already landed on
main must be removed from main separately.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, _, err := liveHere(cmd)
			if err != nil {
				return err
			}
			if s.blocked != nil {
				return errcodes.New(errcodes.ReadOnly, "the remote live ref was deleted or rewritten").WithHint("run `lore live restore` first")
			}
			if _, err := s.ref.Purge(cmd.Context(), args, version); err != nil {
				return errcodes.New(errcodes.Internal, "purge").WithCause(err)
			}
			s.wrote += len(args)
			fmt.Printf("✓ purged %s from live; run `lore live compact` to drop them from the ref's history too\n", strings.Join(args, ", "))
			return nil
		},
	}
}
