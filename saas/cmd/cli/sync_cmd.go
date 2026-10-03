// sync_cmd.go — `lore sync …` and `lore merge-driver`.
//
// Sync runs automatically around every command (sync_wire.go); these
// subcommands expose it explicitly: run a pass, inspect state, resolve
// conflicts, restore trashed rows, purge, fix parallel bootstraps, install
// git wiring, and the hidden entry points git hooks / the merge driver call.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"dbent"
	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitsetup"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/merge3"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/style"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
)

// syncTarget is an opened Mode A project for a sync subcommand.
type syncTarget struct {
	db      *sql.DB
	root    string
	dataDir string
}

// openSyncTarget resolves the project and opens its DB WITHOUT the
// automatic pass (the subcommand decides what to run).
func openSyncTarget(c *commonFlags) (*syncTarget, error) {
	rctx, err := projresolve.Resolve(projresolve.Inputs{FlagDB: c.flagDB, FlagProject: c.flagProject})
	if err != nil {
		return nil, mapResolveError(err)
	}
	dataDir, ok := syncDataDir(rctx.DBPath)
	if !ok || rctx.Mode != projresolve.ModeA {
		return nil, errcodes.New(errcodes.InvalidInput, "sync applies to Mode A projects (.lore/lore.db) only").
			WithHint("Mode B shared DBs and custom --db paths are not synced through git")
	}
	db := dbent.InitDB(rctx.DBPath)
	if err := dbent.ApplyPragmas(db); err != nil {
		_ = db.Close() // the pragma error is the one to report
		return nil, errcodes.New(errcodes.Internal, "apply pragmas").WithCause(err)
	}
	t := &syncTarget{db: db, root: filepath.Dir(filepath.Dir(dataDir)), dataDir: dataDir}
	// Register for the end-of-command step too, so git wiring and the
	// uncommitted-files reminder run after explicit sync commands as well.
	if syncSessions[rctx.DBPath] == nil {
		syncSessions[rctx.DBPath] = &syncSession{root: t.root, dbPath: rctx.DBPath, dataDir: dataDir}
	}
	return t, nil
}

func (t *syncTarget) close() { _ = t.db.Close() } // CLI exit follows; nothing to recover

// catchUp runs the start-of-command pass for subcommands that are about to
// change rows, so changes made outside lore since the last command are
// audited as external writes and not attributed to this command.
func (t *syncTarget) catchUp(ctx context.Context) error {
	rep, err := runCatchUpPass(ctx, t.db, t.root, t.dataDir)
	if err != nil {
		return errcodes.New(errcodes.Internal, "sync pass").WithCause(err)
	}
	printSyncReport(rep)
	return nil
}

// JSON envelope kinds for the sync commands (json_helper.go contract).
const (
	jsonKindSyncReport    = "sync.report"
	jsonKindSyncStatus    = "sync.status"
	jsonKindSyncConflicts = "sync.conflicts"
	jsonKindSyncTrash     = "sync.trash"
	jsonKindSyncGit       = "sync.git"
	jsonKindSyncPeek      = "sync.peek"
	jsonKindSyncDupes     = "sync.dupes"
)

func newSyncCommand() *cobra.Command {
	var f commonFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync lore.db with the git-tracked .lore/data files",
		Long: `sync reconciles .lore/lore.db with .lore/data/ — one JSON file per shared row,
committed to git, so lore knowledge follows branches and reaches main when a
PR merges.

A pass runs automatically before and after every lore command; run it by
hand after editing files yourself, or to see what it does:

  lore sync                 run a pass now and print what changed
  lore sync status          what is pending, conflicted, or broken
  lore sync conflicts       versions kept aside when both sides edited a row
  lore sync trash           rows removed because their file disappeared
  lore sync export --all    rewrite every file from lore.db

Environment: LORE_SYNC=0 disables sync; LORE_SYNC_GIT=0 leaves git config,
.gitattributes and hooks alone; LORE_SYNC_QUIET=1 silences progress lines.`,
		Example: `  lore sync
  lore sync --json
  lore sync status
  lore sync resolve 3 --take other`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := openSyncTarget(&f)
			if err != nil {
				return err
			}
			defer t.close()
			rep, err := runCatchUpPass(cmd.Context(), t.db, t.root, t.dataDir)
			if err != nil {
				return errcodes.New(errcodes.Internal, "sync pass").WithCause(err)
			}
			if asJSON {
				printJSON(jsonKindSyncReport, rep, 0)
			} else {
				printSyncReport(rep)
				if !rep.Changed() && len(rep.Errors) == 0 && len(rep.Conflicts) == 0 {
					fmt.Println(style.Muted("· already in sync"))
				}
			}
			if len(rep.Errors) > 0 {
				return errcodes.New(errcodes.InvalidInput, fmt.Sprintf("%d file(s) could not be synced", len(rep.Errors))).
					WithHint("fix the files listed above (see `lore sync status`)")
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&asJSON, constants.FlagJSON, false, "JSON report")
	cmd.AddCommand(newSyncStatusCommand(), newSyncExportCommand(), newSyncConflictsCommand(),
		newSyncResolveCommand(), newSyncTrashCommand(), newSyncPurgeCommand(), newSyncFixProjectsCommand(),
		newSyncInstallGitCommand(), newSyncHookCommand(), newSyncPeekCommand(), newSyncPromoteCommand(),
		newSyncDupesCommand(), newSyncMergeRowsCommand())
	return cmd
}

func newSyncStatusCommand() *cobra.Command {
	var f commonFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "status",
		Short:   "Show sync state: baseline, pending exports, conflicts, broken files",
		Example: "  lore sync status\n  lore sync status --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := openSyncTarget(&f)
			if err != nil {
				return err
			}
			defer t.close()
			st, err := lsync.ReadStatus(cmd.Context(), t.db, t.dataDir)
			if err != nil {
				return errcodes.New(errcodes.Internal, "read sync status").WithCause(err)
			}
			uncommitted, gitErr := gitsetup.Uncommitted(cmd.Context(), t.root, filepath.Join(projresolve.MarkerDir, lsync.DataDirName))
			out := struct {
				lsync.Status
				Uncommitted []string `json:"uncommitted,omitempty"`
				GitError    string   `json:"git_error,omitempty"`
			}{Status: st, Uncommitted: uncommitted}
			if gitErr != nil && !errors.Is(gitErr, gitsetup.ErrNotRepo) {
				out.GitError = gitErr.Error()
			}
			if asJSON {
				printJSON(jsonKindSyncStatus, out, 0)
				return nil
			}
			fmt.Printf("baselined:        %v\n", st.Baselined)
			fmt.Printf("data dir present: %v\n", st.DataDirPresent)
			fmt.Printf("tracked files:    %d\n", st.TrackedFiles)
			fmt.Printf("pending exports:  %d\n", st.PendingExports)
			fmt.Printf("open conflicts:   %d\n", st.OpenConflicts)
			fmt.Printf("trash rows:       %d\n", st.TrashRows)
			fmt.Printf("uncommitted:      %d\n", len(uncommitted))
			for _, e := range st.Errors {
				fmt.Println(style.Error(fmt.Sprintf("  %s %s: %s", e.Kind, e.Path, e.Detail)))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&asJSON, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newSyncExportCommand() *cobra.Command {
	var f commonFlags
	var all, asJSON bool
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Rewrite .lore/data files from lore.db",
		Long: `export --all forgets the sync baseline and rewrites every row file from
lore.db. Use it to recreate a deleted .lore/data directory, or after editing
the DB with tools that bypassed lore (its triggers normally catch those).`,
		Example: "  lore sync export --all",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !all {
				return errcodes.New(errcodes.InvalidInput, "pass --all to rewrite every file").
					WithHint("a normal `lore sync` already exports pending changes")
			}
			t, err := openSyncTarget(&f)
			if err != nil {
				return err
			}
			defer t.close()
			if err := t.catchUp(cmd.Context()); err != nil {
				return err
			}
			if err := lsync.MarkAllForExport(cmd.Context(), t.db, t.dataDir); err != nil {
				return errcodes.New(errcodes.Internal, "mark rows for export").WithCause(err)
			}
			// Rewriting files changes no data: no audit entries.
			o := syncOptions(t.db, t.root, t.dataDir, lsync.ChangeLocal)
			o.OnChange = nil
			rep, err := lsync.Reconcile(cmd.Context(), o)
			if err != nil {
				return errcodes.New(errcodes.Internal, "sync pass").WithCause(err)
			}
			if asJSON {
				printJSON(jsonKindSyncReport, rep, 0)
				return nil
			}
			printSyncReport(rep)
			fmt.Printf("%s %d file(s) written\n", style.Success("✓"), len(rep.Exported))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&all, constants.FlagAll, false, "rewrite every row file")
	cmd.Flags().BoolVar(&asJSON, constants.FlagJSON, false, "JSON report")
	return cmd
}

func newSyncConflictsCommand() *cobra.Command {
	var f commonFlags
	var all, asJSON bool
	cmd := &cobra.Command{
		Use:   "conflicts",
		Short: "List versions kept aside when both sides edited the same row",
		Long: `When a row changed both in lore.db and in its file (or two versions met
during adopt), one version is kept and the other is saved here. Inspect
them, then run ` + "`lore sync resolve <id> --take kept|other`" + `.`,
		Example: "  lore sync conflicts\n  lore sync conflicts --all --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := openSyncTarget(&f)
			if err != nil {
				return err
			}
			defer t.close()
			cs, err := lsync.ListConflicts(cmd.Context(), t.db, all)
			if err != nil {
				return errcodes.New(errcodes.Internal, "list conflicts").WithCause(err)
			}
			if asJSON {
				printJSON(jsonKindSyncConflicts, cs, len(cs))
				return nil
			}
			if len(cs) == 0 {
				fmt.Println(style.Muted("· no conflicts"))
				return nil
			}
			for _, c := range cs {
				state := "open"
				if c.Resolved {
					state = "resolved"
				}
				fmt.Printf("#%d %s/%s kept=%s %s at %s\n", c.ID, c.Table, c.RowID, c.Kept, state, c.At)
				fmt.Println(indent(c.OtherDoc, "    "))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&all, constants.FlagAll, false, "include resolved conflicts")
	cmd.Flags().BoolVar(&asJSON, constants.FlagJSON, false, "JSON output")
	return cmd
}

func indent(s, pad string) string {
	return pad + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n"+pad)
}

func newSyncResolveCommand() *cobra.Command {
	var f commonFlags
	var take string
	cmd := &cobra.Command{
		Use:   "resolve <conflict-id>",
		Short: "Close a conflict, keeping the current version or the saved other one",
		Example: `  lore sync resolve 3 --take kept    # accept what sync kept
  lore sync resolve 3 --take other   # put the saved version back`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "conflict id must be a number").WithCause(err)
			}
			choice := lsync.ResolveTake(take)
			if choice != lsync.TakeKept && choice != lsync.TakeOther {
				return errcodes.New(errcodes.InvalidInput, "--take must be kept or other")
			}
			t, err := openSyncTarget(&f)
			if err != nil {
				return err
			}
			defer t.close()
			if err := t.catchUp(cmd.Context()); err != nil {
				return err
			}
			if err := lsync.ResolveConflict(cmd.Context(), t.db, id, choice); err != nil {
				if errors.Is(err, lsync.ErrNotFound) {
					return errcodes.New(errcodes.NotFound, err.Error()).WithHint("see `lore sync conflicts`")
				}
				return errcodes.New(errcodes.Internal, "resolve conflict").WithCause(err)
			}
			rep, err := runSyncPass(cmd.Context(), t.db, t.root, t.dataDir)
			if err != nil {
				return errcodes.New(errcodes.Internal, "sync pass").WithCause(err)
			}
			printSyncReport(rep)
			fmt.Printf("%s conflict #%d resolved (%s)\n", style.Success("✓"), id, choice)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&take, constants.FlagTake, "", "kept | other (required)")
	return cmd
}

func newSyncTrashCommand() *cobra.Command {
	var f commonFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "trash",
		Short: "List rows removed because their file disappeared",
		Long: `Rows deleted by sync (their file was deleted, reverted, or purged) are
copied here first. Restore one with ` + "`lore sync trash restore <id>`" + `; the
restored row is exported again like any edit.`,
		Example: "  lore sync trash\n  lore sync trash restore 12",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := openSyncTarget(&f)
			if err != nil {
				return err
			}
			defer t.close()
			rows, err := lsync.ListTrash(cmd.Context(), t.db)
			if err != nil {
				return errcodes.New(errcodes.Internal, "list trash").WithCause(err)
			}
			if asJSON {
				printJSON(jsonKindSyncTrash, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("· trash is empty"))
			}
			for _, r := range rows {
				fmt.Printf("#%d %s/%s  %s  (%s)\n", r.ID, r.Table, r.RowID, r.At, r.Reason)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&asJSON, constants.FlagJSON, false, "JSON output")
	restore := &cobra.Command{
		Use:     "restore <trash-id>",
		Short:   "Put a trashed row back (it is re-exported on the next pass)",
		Example: "  lore sync trash restore 12",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "trash id must be a number").WithCause(err)
			}
			t, err := openSyncTarget(&f)
			if err != nil {
				return err
			}
			defer t.close()
			if err := t.catchUp(cmd.Context()); err != nil {
				return err
			}
			if err := lsync.RestoreTrash(cmd.Context(), t.db, id); err != nil {
				if errors.Is(err, lsync.ErrNotFound) {
					return errcodes.New(errcodes.NotFound, err.Error()).WithHint("see `lore sync trash`")
				}
				return errcodes.New(errcodes.Internal, "restore from trash").WithCause(err)
			}
			rep, err := runSyncPass(cmd.Context(), t.db, t.root, t.dataDir)
			if err != nil {
				return errcodes.New(errcodes.Internal, "sync pass").WithCause(err)
			}
			printSyncReport(rep)
			fmt.Printf("%s restored trash entry #%d\n", style.Success("✓"), id)
			return nil
		},
	}
	cmd.AddCommand(restore)
	return cmd
}

func newSyncPurgeCommand() *cobra.Command {
	var f commonFlags
	var before string
	var confirm, dryRun bool
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Permanently remove rows archived before a cutoff",
		Long: `purge deletes archived rows for good: their files are removed, and their ids
are recorded in .lore/data/_purged.json so an old clone never brings them
back. Run it on main and commit the result. Rows go to the local trash first.

--archived-before accepts a date (2026-01-31) or a duration back from now (90d, 720h).`,
		Example: `  lore sync purge --archived-before 90d --dry-run
  lore sync purge --archived-before 2026-01-01 --confirm`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cutoff, err := parseCutoff(before, time.Now())
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "bad --archived-before").WithCause(err)
			}
			if !confirm && !dryRun {
				return errcodes.New(errcodes.InvalidInput, "purge is permanent; pass --confirm (or --dry-run to preview)")
			}
			t, err := openSyncTarget(&f)
			if err != nil {
				return err
			}
			defer t.close()
			if dryRun {
				n, err := lsync.CountArchivedBefore(cmd.Context(), t.db, cutoff)
				if err != nil {
					return errcodes.New(errcodes.Internal, "count archived rows").WithCause(err)
				}
				fmt.Printf("would purge %d archived row(s) archived before %s\n", n, cutoff.Format(time.RFC3339))
				return nil
			}
			if err := t.catchUp(cmd.Context()); err != nil {
				return err
			}
			purged, err := lsync.PurgeArchived(cmd.Context(), t.db, t.dataDir, cutoff)
			if err != nil {
				return errcodes.New(errcodes.Internal, "purge").WithCause(err)
			}
			rep, err := runSyncPass(cmd.Context(), t.db, t.root, t.dataDir)
			if err != nil {
				return errcodes.New(errcodes.Internal, "sync pass").WithCause(err)
			}
			printSyncReport(rep)
			fmt.Printf("%s purged %d row(s)\n", style.Success("✓"), len(purged))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&before, constants.FlagArchivedBefore, "", "cutoff: date (2006-01-02) or age (90d, 720h)")
	cmd.Flags().BoolVar(&confirm, constants.FlagConfirm, false, "required: purge is permanent")
	cmd.Flags().BoolVar(&dryRun, constants.FlagDryRun, false, "count what would be purged")
	return cmd
}

// dayUnit is the "d" suffix parseCutoff accepts on top of time.ParseDuration.
const dayUnit = 24 * time.Hour

// parseCutoff turns "2026-01-31", "90d" or "720h" into an instant.
func parseCutoff(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("required")
	}
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t.UTC(), nil
	}
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days < 0 {
			return time.Time{}, fmt.Errorf("bad day count %q", s)
		}
		return now.Add(-time.Duration(days) * dayUnit), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return time.Time{}, fmt.Errorf("want a date or a duration like 90d, got %q", s)
	}
	return now.Add(-d), nil
}

func newSyncFixProjectsCommand() *cobra.Command {
	var f commonFlags
	var keep string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "fix-projects",
		Short: "Collapse several project rows into one (two parallel bootstraps met in a merge)",
		Long: `When two people bootstrapped lore data on separate branches, both branches carry
a different project id. After the merge, run this once (on main) to keep one
project — the one .lore/data/_meta.json names, unless --keep says otherwise —
and re-point every row at it. Commit the result.`,
		Example: "  lore sync fix-projects --dry-run\n  lore sync fix-projects\n  lore sync fix-projects --keep prj_…",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := openSyncTarget(&f)
			if err != nil {
				return err
			}
			defer t.close()
			if dryRun {
				var n int
				if err := t.db.QueryRowContext(cmd.Context(), `SELECT COUNT(*) FROM projects`).Scan(&n); err != nil {
					return errcodes.New(errcodes.Internal, "count projects").WithCause(err)
				}
				fmt.Printf("%d project row(s); %d would be merged\n", n, max(n-1, 0))
				return nil
			}
			if err := t.catchUp(cmd.Context()); err != nil {
				return err
			}
			kept, merged, err := lsync.FixProjects(cmd.Context(), t.db, t.dataDir, keep)
			if err != nil {
				if errors.Is(err, lsync.ErrNotFound) {
					return errcodes.New(errcodes.NotFound, err.Error()).WithHint("see `lore project list`")
				}
				return errcodes.New(errcodes.Internal, "fix projects").WithCause(err)
			}
			if len(merged) == 0 {
				fmt.Println(style.Muted("· only one project; nothing to fix"))
				return nil
			}
			rep, err := runSyncPass(cmd.Context(), t.db, t.root, t.dataDir)
			if err != nil {
				return errcodes.New(errcodes.Internal, "sync pass").WithCause(err)
			}
			printSyncReport(rep)
			fmt.Printf("%s kept %s, merged %s\n", style.Success("✓"), kept, strings.Join(merged, ", "))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&keep, constants.FlagKeep, "", "project id to keep (default: the one _meta.json names)")
	cmd.Flags().BoolVar(&dryRun, constants.FlagDryRun, false, "show what would change")
	return cmd
}

func newSyncInstallGitCommand() *cobra.Command {
	var f commonFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "install-git",
		Short: "Install lore's merge driver, .gitattributes rules and hook blocks now",
		Long: `Lore does this automatically after every command; run it explicitly to see
what it changes. It never repoints core.hooksPath: when a hook manager owns
the hooks directory (husky, lefthook, a committed .githooks), lore's block is
chained into that directory's scripts.`,
		Example: "  lore sync install-git\n  lore sync install-git --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := openSyncTarget(&f)
			if err != nil {
				return err
			}
			defer t.close()
			res, err := ensureGitWiring(cmd.Context(), t.root)
			if err != nil {
				return errcodes.New(errcodes.Internal, "git wiring").WithCause(err)
			}
			if len(res.IgnoredData) > 0 {
				return errcodes.New(errcodes.SyncDataIgnored, ignoredDataMessage(res.IgnoredData))
			}
			if asJSON {
				printJSON(jsonKindSyncGit, res, 0)
				return nil
			}
			fmt.Printf("%s git wired for lore data\n", style.Success("✓"))
			fmt.Printf("  hooks dir:   %s\n", res.HooksDir)
			fmt.Printf("  attributes:  changed=%v\n", res.Attributes)
			fmt.Printf("  gitignore:   changed=%v\n", res.Gitignore)
			fmt.Printf("  config:      %s\n", strings.Join(res.Config, ", "))
			fmt.Printf("  hooks:       %s\n", strings.Join(res.Hooks, ", "))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&asJSON, constants.FlagJSON, false, "JSON output")
	return cmd
}

// Hook names `lore sync hook` accepts.
const (
	hookPreCommit = "pre-commit"
	hookRefresh   = "refresh"
)

// newSyncHookCommand is what the git hook blocks call. Hidden: it is an
// implementation detail of the installed hooks, not a user command.
func newSyncHookCommand() *cobra.Command {
	var f commonFlags
	return &cobra.Command{
		Use:    "hook <pre-commit|refresh>",
		Short:  "Entry point for lore's git hook blocks",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := openSyncTarget(&f)
			if err != nil {
				// Not a lore project (e.g. a hook copied around): never
				// block git over it.
				return nil
			}
			defer t.close()
			// Inside a git operation: no end-of-command git wiring or
			// "not committed" reminder (the commit is happening now).
			syncSessions = map[string]*syncSession{}
			rep, err := runSyncPass(cmd.Context(), t.db, t.root, t.dataDir)
			if err != nil {
				fmt.Fprintln(os.Stderr, style.Warn(syncLogPrefix+err.Error()))
				return nil
			}
			printSyncReport(rep)
			switch args[0] {
			case hookRefresh:
				// A pull/checkout/merge just changed the data: refresh the
				// generated LORE.md now (cwd is the project root here).
				rerenderIfDue(cmd.Context(), t.db, t.root)
				syncSessions = map[string]*syncSession{} // render re-registered it
				return nil
			case hookPreCommit:
				return preCommitCheck(cmd.Context(), t, rep)
			default:
				return errcodes.New(errcodes.InvalidInput, "unknown hook "+args[0])
			}
		},
	}
}

// preCommitCheck stages lore's own data changes (so knowledge cannot be
// left behind by a partial `git add`, E37/E38) and refuses the commit when a
// lore file is unparseable or still carries conflict markers (E12) — either
// reported by the sync pass that just ran, or found in a changed file.
//
// Markers written by lore's merge driver live INSIDE JSON string values
// (escaped newlines), so files are decoded and every string checked; a raw
// line scan would miss them.
func preCommitCheck(ctx context.Context, t *syncTarget, rep lsync.Report) error {
	var bad []string
	for _, e := range rep.Errors {
		if e.Kind != lsync.ErrKindSecret {
			bad = append(bad, e.Path+" ("+e.Kind+")")
		}
	}
	rel := filepath.Join(projresolve.MarkerDir, lsync.DataDirName)
	changed, err := gitsetup.Uncommitted(ctx, t.root, rel)
	if err != nil {
		return nil // not a git repo
	}
	scan := secretCheck()
	for _, p := range changed {
		b, rerr := os.ReadFile(filepath.Join(t.root, p))
		if rerr != nil {
			continue // deleted file: staging records the deletion
		}
		if why := brokenLoreFile(b); why != "" {
			bad = append(bad, p+" ("+why+")")
			continue
		}
		// Files edited by hand never went through the exporter's scan.
		if scan != nil {
			if why := scan(b); why != "" {
				bad = append(bad, p+" ("+why+")")
			}
		}
	}
	if len(bad) > 0 {
		return errcodes.New(errcodes.InvalidInput, "refusing to commit broken lore files: "+strings.Join(bad, ", ")).
			WithHint("resolve the markers inside the JSON values (or `lore sync resolve`), then commit again")
	}
	if len(changed) > 0 {
		if err := gitsetup.Add(ctx, t.root, rel); err != nil {
			fmt.Fprintln(os.Stderr, style.Warn(syncLogPrefix+"could not stage .lore/data: "+err.Error()))
		}
	}
	return nil
}

// brokenLoreFile explains why b is not committable ("" when fine): raw git
// conflict hunks, invalid JSON, or merge markers inside a string value.
func brokenLoreFile(b []byte) string {
	if merge3.ContainsConflictMarkers(string(b)) {
		return "conflict markers"
	}
	doc, err := canonjson.Decode(b, canonjson.DefaultMaxDepth)
	if err != nil {
		return "invalid JSON"
	}
	for k, v := range doc {
		if s, ok := v.(string); ok && merge3.ContainsConflictMarkers(s) {
			return "conflict markers in " + k
		}
	}
	return ""
}

// newMergeDriverCommand is the git merge driver registered as
// `merge.lore.driver = lore merge-driver %O %A %B %P`. Hidden.
//
// lore configures `%O %A %B %P`. The repo path (%P) is what identifies
// _purged.json (merged as a union of ids); it is accepted as optional so a
// hand-written driver line without it still merges row files, but such a
// line would merge _purged.json field by field instead.
// Argument counts of `lore merge-driver`: %O %A %B, plus the optional %P.
const (
	mergeDriverMinArgs = 3
	mergeDriverMaxArgs = 4
)

func newMergeDriverCommand() *cobra.Command {
	var keepOurs bool
	cmd := &cobra.Command{
		Use:    "merge-driver <base> <ours> <theirs> [path]",
		Short:  "git merge driver for .lore/data row files",
		Hidden: true,
		Args:   cobra.RangeArgs(mergeDriverMinArgs, mergeDriverMaxArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if keepOurs {
				// LORE.md: keep ours; the next lore command re-renders it
				// from the merged data (E35).
				return nil
			}
			repoPath := ""
			if len(args) == mergeDriverMaxArgs {
				repoPath = args[mergeDriverMaxArgs-1]
			}
			out, err := lsync.MergeFiles(cmd.Context(), args[0], args[1], args[2], repoPath)
			if err != nil {
				return errcodes.New(errcodes.Internal, "merge driver").WithCause(err)
			}
			if out.Conflicted {
				// git's merge-driver contract: non-zero exit = conflict left
				// in the file for a human. Exit directly: returning an error
				// would also print an ERROR line that reads like a crash.
				fmt.Fprintf(os.Stderr, "lore merge-driver: %s needs a manual merge (markers are inside the JSON values)\n", repoPath)
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&keepOurs, constants.FlagKeepOurs, false, "keep our side unchanged (generated files)")
	return cmd
}

// peekRow is one row read from another git ref.
type peekRow struct {
	Table   string `json:"table"`
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

// summaryKeys are the fields peek shows for a row, first non-empty wins.
var summaryKeys = []string{"title", "name", "body", "stable_key", "key"}

// peekSummaryLen bounds the one-line summary peek prints per row.
const peekSummaryLen = 100

func rowSummary(doc map[string]any) string {
	for _, k := range summaryKeys {
		if s, ok := doc[k].(string); ok && strings.TrimSpace(s) != "" {
			line := strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
			if len(line) > peekSummaryLen {
				line = line[:peekSummaryLen] + "…"
			}
			return line
		}
	}
	return ""
}

func newSyncPeekCommand() *cobra.Command {
	var f commonFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "peek <git-ref> [table]",
		Short: "List the lore rows another branch carries, without checking it out",
		Long: `peek reads .lore/data straight from a git ref (a branch, tag or commit) and
lists its rows, so you can see a teammate's unmerged knowledge or tasks
without switching branches. Nothing is imported. Fetch first to see remote
branches (` + "`git fetch`" + `, then use origin/<branch>).`,
		Example: `  lore sync peek origin/feature/auth
  lore sync peek origin/feature/auth tasks
  lore sync peek main memories --json`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := openSyncTarget(&f)
			if err != nil {
				return err
			}
			defer t.close()
			prefix := filepath.ToSlash(filepath.Join(projresolve.MarkerDir, lsync.DataDirName))
			if len(args) == 2 {
				prefix += "/" + args[1]
			}
			files, err := gitsetup.ReadTree(cmd.Context(), t.root, args[0], prefix)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "read "+args[0]).WithCause(err).
					WithHint("is it a valid ref? try `git fetch` first for remote branches")
			}
			var rows []peekRow
			for p, b := range files {
				rel := strings.TrimPrefix(p, filepath.ToSlash(filepath.Join(projresolve.MarkerDir, lsync.DataDirName))+"/")
				table, file := filepath.Split(rel)
				if table == "" {
					continue // _meta.json / _purged.json
				}
				doc, derr := lsync.DecodeRowFile(b)
				if derr != nil {
					rows = append(rows, peekRow{Table: strings.TrimSuffix(table, "/"), ID: strings.TrimSuffix(file, ".json"), Summary: "(unreadable: " + derr.Error() + ")"})
					continue
				}
				id, _ := doc["id"].(string)
				rows = append(rows, peekRow{Table: strings.TrimSuffix(table, "/"), ID: id, Summary: rowSummary(doc)})
			}
			sort.Slice(rows, func(i, j int) bool {
				if rows[i].Table != rows[j].Table {
					return rows[i].Table < rows[j].Table
				}
				return rows[i].ID < rows[j].ID
			})
			if asJSON {
				printJSON(jsonKindSyncPeek, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("· no lore rows at " + args[0]))
			}
			for _, r := range rows {
				fmt.Printf("%-18s %s  %s\n", r.Table, r.ID, r.Summary)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&asJSON, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newSyncPromoteCommand() *cobra.Command {
	var f commonFlags
	var to string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "promote <id>...",
		Short: "Commit rows onto another local branch (e.g. an urgent rule onto main)",
		Long: `promote copies the current .lore/data file of each row into a new commit on
the target branch — without checking it out and without pushing. Use it when
a rule learned on a feature branch must apply everywhere now: promote it to
main, then push main (or open a PR) as usual. The target cannot be the
branch you are on.`,
		Example: `  lore sync promote rul_… --to main --dry-run
  lore sync promote rul_… --to main`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if to == "" {
				return errcodes.New(errcodes.InvalidInput, "--to <branch> is required")
			}
			t, err := openSyncTarget(&f)
			if err != nil {
				return err
			}
			defer t.close()
			// Export pending changes first so the files are current.
			if _, err := runSyncPass(cmd.Context(), t.db, t.root, t.dataDir); err != nil {
				return errcodes.New(errcodes.Internal, "sync pass").WithCause(err)
			}
			files := map[string][]byte{}
			for _, id := range args {
				rel, b, err := findRowFile(t.dataDir, id)
				if err != nil {
					return err
				}
				files[filepath.ToSlash(filepath.Join(projresolve.MarkerDir, lsync.DataDirName, rel))] = b
			}
			if dryRun {
				for p := range files {
					fmt.Printf("would commit %s onto %s\n", p, to)
				}
				return nil
			}
			msg := fmt.Sprintf("lore: promote %s", strings.Join(args, ", "))
			sha, err := gitsetup.CommitFiles(cmd.Context(), t.root, to, msg, files)
			if err != nil {
				if errors.Is(err, gitsetup.ErrCurrentBranch) {
					return errcodes.New(errcodes.InvalidInput, err.Error())
				}
				return errcodes.New(errcodes.Internal, "promote").WithCause(err)
			}
			fmt.Printf("%s committed %d file(s) onto %s (%s) — not pushed\n", style.Success("✓"), len(files), to, sha[:12])
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&to, constants.FlagTo, "", "target branch (required)")
	cmd.Flags().BoolVar(&dryRun, constants.FlagDryRun, false, "show what would be committed")
	return cmd
}

// findRowFile locates <table>/<id>.json in the data dir.
func findRowFile(dataDir, id string) (string, []byte, error) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return "", nil, errcodes.New(errcodes.Internal, "read .lore/data").WithCause(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rel := filepath.Join(e.Name(), id+".json")
		b, err := os.ReadFile(filepath.Join(dataDir, rel))
		if err == nil {
			return rel, b, nil
		}
	}
	return "", nil, errcodes.New(errcodes.NotFound, "no .lore/data file for "+id).
		WithHint("only synced rows can be promoted; run `lore sync status`")
}

func newSyncDupesCommand() *cobra.Command {
	var f commonFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "dupes",
		Short: "List rows recorded twice (same text, different ids)",
		Long: `After teammates' independent databases are adopted, the same fact is often
recorded twice under different ids. dupes lists exact matches (ignoring case
and whitespace); fold one into the other with ` + "`lore sync merge-rows`" + `.`,
		Example: "  lore sync dupes\n  lore sync dupes --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := openSyncTarget(&f)
			if err != nil {
				return err
			}
			defer t.close()
			groups, err := lsync.FindDuplicates(cmd.Context(), t.db)
			if err != nil {
				return errcodes.New(errcodes.Internal, "find duplicates").WithCause(err)
			}
			if asJSON {
				printJSON(jsonKindSyncDupes, groups, len(groups))
				return nil
			}
			if len(groups) == 0 {
				fmt.Println(style.Muted("· no duplicates"))
			}
			for _, g := range groups {
				fmt.Printf("%s: %s\n    %s\n", g.Table, strings.Join(g.IDs, ", "), rowSummary(map[string]any{"body": g.Text}))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&asJSON, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newSyncMergeRowsCommand() *cobra.Command {
	var f commonFlags
	var keep, drop string
	cmd := &cobra.Command{
		Use:     "merge-rows <table> --keep <id> --drop <id>",
		Short:   "Fold a duplicate row into another (references move, the dropped row is trashed)",
		Example: "  lore sync merge-rows memories --keep mem_A --drop mem_B",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if keep == "" || drop == "" {
				return errcodes.New(errcodes.InvalidInput, "--keep and --drop are required")
			}
			t, err := openSyncTarget(&f)
			if err != nil {
				return err
			}
			defer t.close()
			if err := t.catchUp(cmd.Context()); err != nil {
				return err
			}
			if err := lsync.MergeRows(cmd.Context(), t.db, args[0], keep, drop); err != nil {
				if errors.Is(err, lsync.ErrNotFound) {
					return errcodes.New(errcodes.NotFound, err.Error())
				}
				return errcodes.New(errcodes.InvalidInput, "merge rows").WithCause(err)
			}
			rep, err := runSyncPass(cmd.Context(), t.db, t.root, t.dataDir)
			if err != nil {
				return errcodes.New(errcodes.Internal, "sync pass").WithCause(err)
			}
			printSyncReport(rep)
			fmt.Printf("%s merged %s into %s\n", style.Success("✓"), drop, keep)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&keep, constants.FlagKeep, "", "id of the row to keep")
	cmd.Flags().StringVar(&drop, constants.FlagDrop, "", "id of the duplicate to remove")
	return cmd
}
