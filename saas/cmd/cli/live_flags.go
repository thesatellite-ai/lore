// live_flags.go — the command surface of live rows: --live / --no-live on
// every entity's add and edit (§1 "Flags", fixed rule 2), the pre-run hook
// that prepares the live ref, and the explanation of ids that are not found
// because they were deleted in live or landed on main (F1).
//
// The flags are attached by walking the command tree once (wireLive), so
// entity command files do not each repeat the wiring and a new entity gets
// the flags for free.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/live"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/rowroute"

	"github.com/spf13/cobra"
)

// Flag names (§1 "Flags").
const (
	flagLive   = "live"
	flagNoLive = "no-live"
	flagTrace  = "trace"
)

// liveVerbs are the subcommands that write one row of their group's kind
// and take --live / --no-live.
var liveVerbs = map[string]bool{"add": true, "edit": true}

// liveGroupTables maps entity command groups whose table name is not the
// plain plural of the group name.
var liveGroupTables = map[string]string{
	"memory":            "memories",
	"hotfix":            "hotfixes",
	"tasklist":          "task_lists",
	"architecture-note": "architecture_notes",
	"cookbook-recipe":   "cookbook_recipes",
	"taste-pref":        "taste_prefs",
}

// liveTableForGroup returns the synced table a command group writes ("" if
// none).
func liveTableForGroup(group string) string {
	if t, ok := liveGroupTables[group]; ok && lsync.TableSynced(t) {
		return t
	}
	t := strings.ReplaceAll(group, "-", "_") + "s"
	if lsync.TableSynced(t) {
		return t
	}
	return ""
}

// liveCommand is what the running command asked of live rows. One command
// runs per process, so this is per-process state like syncSessions.
type liveCommand struct {
	// path is the cobra command path ("lore task add").
	path string
	// table is the kind the command's --live/--no-live applies to.
	table  string
	choice rowroute.Choice
}

var liveCmdState liveCommand

// fetchKind is FetchAlways for `lore sync` (and `lore sync status`), which
// always fetch (TEAM-025), FetchCached for everything else.
func (c liveCommand) fetchKind() live.FetchKind {
	switch c.path {
	case BinaryName + " sync", BinaryName + " sync status", BinaryName + " land", BinaryName + " live restore", BinaryName + " live compact":
		return live.FetchAlways
	}
	return live.FetchCached
}

// liveSkipPrefixes are commands that never touch the live ref: git's merge
// driver and hooks (they run inside git operations and must stay fast and
// offline), and commands that do not open a project.
var liveSkipPrefixes = []string{
	BinaryName + " merge-driver",
	BinaryName + " sync hook",
	BinaryName + " version",
	BinaryName + " completion",
	BinaryName + " help",
	BinaryName + " init",
	BinaryName + " config",
	BinaryName + " identity",
	BinaryName + " whoami",
	BinaryName + " org",
	// The 0.1.18 core's commands (core_mount.go): they write row files
	// through the new engine, never lore.db or the live ref.
	BinaryName + " memory",
	BinaryName + " migrate",
	BinaryName + " api",
	BinaryName + " spec",
	BinaryName + " stores",
}

// liveReadVerbs never write rows, so they run even when the live ref is
// read-only for this binary or stopped after a remote rewrite.
var liveReadVerbs = map[string]bool{
	"list": true, "show": true, "search": true, "status": true, "log": true, "diff": true,
	"doctor": true, "explain": true, "conflicts": true, "peek": true, "tables": true,
	"why-context": true, "render": true, "restore": true, "sync": true, "trash": true,
	"deferred": true, "someday": true, "triage": true,
}

// wireLive attaches the live surface to the command tree: the land and
// live commands, --live/--no-live on every entity add/edit, the pre-run
// hook and the not-found explanation. Called once from main's init after
// every command is registered.
func wireLive(root *cobra.Command) {
	root.AddCommand(newLandCommand(), newLiveCommand(), newExplainCommand())
	root.PersistentFlags().Bool(flagTrace, false, "print every git command lore runs for live rows, with timing (same as LORE_TRACE=1)")
	for _, group := range root.Commands() {
		table := liveTableForGroup(group.Name())
		if table == "" {
			continue
		}
		for _, sub := range group.Commands() {
			if !liveVerbs[sub.Name()] || sub.Flags().Lookup(flagLive) != nil {
				continue
			}
			sub.Flags().Bool(flagLive, false, "share this row with everyone at once on the live ref (refs/lore/live)")
			sub.Flags().Bool(flagNoLive, false, "keep this row with the code (this branch's .lore/data)")
			sub.MarkFlagsMutuallyExclusive(flagLive, flagNoLive)
		}
	}
	wrapLeaves(root)
	prev := root.PersistentPreRunE
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if prev != nil {
			if err := prev(cmd, args); err != nil {
				return err
			}
		}
		return livePreRun(cmd)
	}
}

// wrapLeaves wraps every runnable command so a not-found id error is
// explained with what live knows (F1 step 3).
func wrapLeaves(c *cobra.Command) {
	for _, sub := range c.Commands() {
		wrapLeaves(sub)
	}
	if c.RunE == nil {
		return
	}
	run := c.RunE
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return explainLiveNotFound(cmd.Context(), cmd, args, run(cmd, args))
	}
}

// livePreRun records the command's live choice and prepares the live ref.
func livePreRun(cmd *cobra.Command) error {
	if f := cmd.Flags().Lookup(flagTrace); f != nil && f.Changed && f.Value.String() == "true" {
		_ = os.Setenv(envTrace, envValueOn) // this process only; read by liveGit
	}
	liveCmdState = liveCommand{path: cmd.CommandPath()}
	// Every verb of an entity group has that kind as its primary table (a
	// `task done` decides like a task write); only add and edit take the
	// flags.
	if p := cmd.Parent(); p != nil {
		liveCmdState.table = liveTableForGroup(p.Name())
	}
	if f := cmd.Flags().Lookup(flagLive); f != nil && f.Changed && f.Value.String() == "true" {
		liveCmdState.choice = rowroute.ChoiceLive
	}
	if f := cmd.Flags().Lookup(flagNoLive); f != nil && f.Changed && f.Value.String() == "true" {
		liveCmdState.choice = rowroute.ChoiceNoLive
	}
	for _, p := range liveSkipPrefixes {
		if liveCmdState.path == p || strings.HasPrefix(liveCmdState.path, p+" ") {
			return nil
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil // no working directory: no project, so nothing to wire live for
	}
	root := liveProjectRoot(cwd)
	if root == "" {
		return nil
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	s := liveFor(ctx, root)
	if s == nil {
		return nil
	}
	s.prepare(ctx, liveCmdState.fetchKind())
	if s.fetchErr != nil {
		if age := liveFetchAge(s); age != "" {
			fmt.Fprintf(os.Stderr, "%soffline: using live rows as fetched %s ago\n", liveLogPrefix, age)
		}
	}
	if liveReadVerbs[cmd.Name()] {
		return nil
	}
	if s.blocked != nil {
		return errcodes.New(errcodes.ReadOnly, "the remote live ref was deleted or rewritten; lore stopped writing to it").
			WithCause(s.blocked).
			WithHint("your local copy is kept under " + live.RefBackupPrefix + "; run `" + BinaryName + " live restore` to put it back")
	}
	if err := s.ref.CheckFloor(ctx, version); err != nil {
		if errors.Is(err, live.ErrBelowFloor) {
			return errcodes.New(errcodes.OldLore, "this lore is too old to write live rows here (read-only)").
				WithCause(err).WithHint("upgrade lore; reading still works")
		}
	}
	return nil
}

// liveFetchAge is how old the last successful fetch is, for the offline
// note ("" when there never was one).
func liveFetchAge(s *liveSession) string {
	at := live.FetchStamp(s.stateDir)
	if at.IsZero() {
		return ""
	}
	return s.clk.Now().Sub(at).Round(time.Second).String()
}

// explainLiveNotFound turns E_NOT_FOUND for an id into the F1 answer: a
// tombstone says "deleted (live, <date>)", an id purged on origin/main says
// "deleted on purpose", anything else gets the peek hint (it may have
// landed on main while this branch is older).
func explainLiveNotFound(ctx context.Context, cmd *cobra.Command, args []string, err error) error {
	var cli *errcodes.CLIError
	if err == nil || !errors.As(err, &cli) || cli.Code != errcodes.NotFound || len(args) == 0 {
		return err
	}
	var miss *handleMissError
	if errors.As(err, &miss) {
		return err // a roster miss, not a missing row: its own hint stands
	}
	id := args[0]
	cwd, werr := os.Getwd()
	if werr != nil {
		return err
	}
	root := liveProjectRoot(cwd)
	s := liveFor(ctxOrBackground(ctx), root)
	table := ""
	if p := cmd.Parent(); p != nil {
		table = liveTableForGroup(p.Name())
	}
	if s != nil {
		if why := s.whyMissing(ctxOrBackground(ctx), s.folderFor(root), id); why != "" {
			return errcodes.New(errcodes.NotFound, why)
		}
	}
	remote, branch := "origin", defaultBranchFallback
	if s != nil {
		remote, branch = s.ref.RemoteName, s.defaultBranch(ctxOrBackground(ctx))
	}
	return cli.WithHint(livePeekHint(remote, branch, table))
}

// livePeekHint points at `lore sync peek <remote>/<branch> [table]` for an id
// that may have landed on the default branch while this branch is older. It
// names the real default branch (liveSession.defaultBranch), never a
// hardcoded "main".
func livePeekHint(remote, branch, table string) string {
	peek := BinaryName + " sync peek " + remote + "/" + branch
	if table != "" {
		peek += " " + table
	}
	return "it may have landed on " + branch + " while this branch is older: `" + peek + "`"
}

func ctxOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// whyMissingOnLive explains an id the live tree knows about: a tombstone,
// or an id in _live/purged.json. "" when the tree says nothing about it. An
// unreadable purge list is reported, never read as "not purged".
func whyMissingOnLive(tree live.Tree, folder, id string) string {
	rows, _ := tree.Rows(folder) // undecodable files say nothing about id; they are reported by apply
	for k, f := range rows {
		if k.ID == id && f.Deleted() {
			when := ""
			if !f.Block.HLC.Wall.IsZero() {
				when = ", " + f.Block.HLC.Wall.Format(time.DateOnly)
			}
			return fmt.Sprintf("%s was deleted (live%s)", id, when)
		}
	}
	purged, err := tree.Purged()
	if err != nil {
		return fmt.Sprintf("%s: the live purge list could not be read (%v)", id, err)
	}
	if purged[id] {
		return id + " was deleted on purpose (`lore live purge`)"
	}
	return ""
}

// whyMissing explains an id lore.db does not hold: a live tombstone, or
// an id in origin/main's _purged.json. "" when live knows nothing.
func (s *liveSession) whyMissing(ctx context.Context, folder, id string) string {
	if _, tree, err := s.ref.LocalTree(ctx); err == nil {
		if why := whyMissingOnLive(tree, folder, id); why != "" {
			return why
		}
	}
	if s.purgedOnMain(ctx, folder, id) {
		return id + " was deleted on purpose (listed in main's _purged.json)"
	}
	return ""
}
