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
	"github.com/thesatellite-ai/lore/wire"
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
	// A fresh clone where only core commands ran has .lore/data and no
	// lore.db (it is gitignored): build it first, as resolveContext does,
	// instead of answering "not a lore project".
	if err := maybeMaterialize(context.Background(), c); err != nil {
		return nil, materializeError(err)
	}
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

func indent(s, pad string) string {
	return pad + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n"+pad)
}

func newSyncTrashCommand() *cobra.Command {
	var f commonFlags
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "trash",
		Short: "Moved: lore trash --local (this spelling goes away in the next release)",
		Long: `Rows deleted by sync (their file was deleted, reverted, or purged) are
copied here first. Restore one with ` + "`lore trash restore --local <id>`" + `; the
restored row is exported again like any edit.`,
		Example: "  lore trash --local\n  lore trash restore --local 12",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			syncTrashPointer(cmd, "lore trash --local")
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
		Short:   "Moved: lore trash restore --local <n>",
		Example: "  lore trash restore --local 12",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			syncTrashPointer(cmd, "lore trash restore --local "+args[0])
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "trash id must be a number").WithCause(err)
			}
			t, err := openSyncTarget(&f)
			if err != nil {
				return err
			}
			defer t.close()
			if catchErr := t.catchUp(cmd.Context()); catchErr != nil {
				return catchErr
			}
			if restoreErr := lsync.RestoreTrash(cmd.Context(), t.db, id); restoreErr != nil {
				if errors.Is(restoreErr, lsync.ErrCoreOwned) {
					// a63c6f8: the message names the way that works for a core row.
					return errcodes.New(errcodes.InvalidInput, restoreErr.Error())
				}
				if errors.Is(restoreErr, lsync.ErrNotFound) {
					return errcodes.New(errcodes.NotFound, restoreErr.Error()).WithHint("see `lore trash --local`")
				}
				return errcodes.New(errcodes.Internal, "restore from trash").WithCause(restoreErr)
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

// syncTrashCommandPath is the old spelling (DQ1, CLI_REFERENCE 2.15).
const syncTrashCommandPath = "lore sync trash"

// syncTrashPointer prints the one-release note of the old spelling (DQ1,
// DELETION 502: `lore sync trash` keeps working with a line pointing to
// `lore trash --local`). lore trash --local runs these same commands
// (trash_local_cmd.go) and prints no note: cmd's path tells them apart.
func syncTrashPointer(cmd *cobra.Command, now string) {
	if strings.HasPrefix(cmd.CommandPath(), syncTrashCommandPath) {
		fmt.Fprintf(os.Stderr, "note: %s is now %s; this spelling goes away in the next release\n", cmd.CommandPath(), now)
	}
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

// Hook names `lore sync hook` accepts.
const (
	hookPreCommit  = "pre-commit"
	hookRefresh    = "refresh"
	hookPostCommit = "post-commit"
	hookPrePush    = "pre-push"
)

// newSyncHookCommand is what the git hook blocks call. Hidden: it is an
// implementation detail of the installed hooks, not a user command.
func newSyncHookCommand() *cobra.Command {
	var f commonFlags
	return &cobra.Command{
		Use:    "hook <pre-commit|refresh|post-commit|pre-push>",
		Short:  "Entry point for lore's git hook blocks",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// lore's own ride-along commit runs git; its hooks must not
			// re-enter lore (F6 re-entry guard).
			if os.Getenv(envRideActive) != "" {
				return nil
			}
			switch args[0] {
			case hookPostCommit:
				return livePostCommitHook(cmd.Context())
			case hookPrePush:
				return livePrePushHook(cmd.Context(), cmd.InOrStdin(), args[1:])
			}
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
		if e.Kind == lsync.ErrKindSecret {
			continue
		}
		// A broken branch file of a live row is shadowed by live (fixed
		// rule 4, same as below): it is never staged, so it must not block
		// a commit that does not carry it (F6 "dirty landed path").
		if kept, _ := withoutLiveRows(ctx, t.db, []string{e.Path}); len(kept) == 0 {
			continue
		}
		bad = append(bad, e.Path+" ("+e.Kind+")")
	}
	rel := filepath.Join(projresolve.MarkerDir, lsync.DataDirName)
	changed, err := gitsetup.Uncommitted(ctx, t.root, rel)
	if err != nil {
		return nil // not a git repo
	}
	// A branch file of a live row is shadowed by live (fixed rule 4): it is
	// neither checked nor staged here, so a stale or hand-edited copy never
	// rides into the user's commit (F6 "dirty landed path").
	changed, shadowed := withoutLiveRows(ctx, t.db, changed)
	scan := secretCheck()
	for _, p := range changed {
		b, rerr := os.ReadFile(filepath.Clean(filepath.Join(t.root, p)))
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
			WithHint(brokenFilesHint(bad))
	}
	if len(changed) > 0 {
		specs := []string{rel}
		if shadowed {
			specs = changed
		}
		for _, p := range specs {
			if err := gitsetup.Add(ctx, t.root, p); err != nil {
				fmt.Fprintln(os.Stderr, style.Warn(syncLogPrefix+"could not stage .lore/data: "+err.Error()))
				break
			}
		}
	}
	// RX7 (OV64): a staged key row that drops internal, sensitive or
	// strict without a new changed_by record refuses the commit. No HEAD
	// yet (a first commit) means nothing to compare.
	found, lerr := wire.KeyLoosenings(ctx, t.root, "HEAD", "")
	if lerr != nil {
		return nil
	}
	return keyLooseningError(found)
}

// Reasons brokenLoreFile gives; printed after each refused path and read
// back by brokenFilesHint, so the hint names what is actually wrong.
const (
	brokenConflictMarkers = "conflict markers"
	brokenInvalidJSON     = "invalid JSON"
)

// brokenFilesHint is the next step for a refused pre-commit. Each entry of
// bad is "<path> (<reason>)"; a reason that is neither a broken-file kind
// nor a sync-pass error kind (lsync.ErrKind*) is the secret scan's.
func brokenFilesHint(bad []string) string {
	var markers, invalid, rowFile, newer, secret bool
	for _, b := range bad {
		switch {
		case strings.Contains(b, "("+brokenConflictMarkers), strings.Contains(b, "("+lsync.ErrKindConflict+")"):
			markers = true
		case strings.Contains(b, "("+brokenInvalidJSON+")"):
			invalid = true
		// Kinds reported by the sync pass (lsync.Report.Errors).
		case strings.Contains(b, "("+lsync.ErrKindInvalid+")"), strings.Contains(b, "("+lsync.ErrKindImport+")"):
			rowFile = true
		case strings.Contains(b, "("+lsync.ErrKindNewer+")"):
			newer = true
		default:
			secret = true
		}
	}
	var steps []string
	if invalid {
		steps = append(steps, "fix the JSON syntax of the files marked invalid JSON")
	}
	if rowFile {
		steps = append(steps, "fix or restore the files marked "+lsync.ErrKindInvalid+" or "+lsync.ErrKindImport+" (`git checkout -- <file>` drops a hand edit)")
	}
	if newer {
		steps = append(steps, "upgrade "+BinaryName+" to read the files marked "+lsync.ErrKindNewer)
	}
	if markers {
		steps = append(steps, "resolve the conflict markers (or `"+BinaryName+" sync resolve`)")
	}
	if secret {
		steps = append(steps, "remove the credential from the files that hold one")
	}
	return strings.Join(steps, "; ") + ", then commit again"
}

// brokenLoreFile explains why b is not committable ("" when fine): raw git
// conflict hunks, invalid JSON, or merge markers inside a string value.
func brokenLoreFile(b []byte) string {
	if merge3.ContainsConflictMarkers(string(b)) {
		return brokenConflictMarkers
	}
	doc, err := canonjson.Decode(b, canonjson.DefaultMaxDepth)
	if err != nil {
		return brokenInvalidJSON
	}
	for k, v := range doc {
		if s, ok := v.(string); ok && merge3.ContainsConflictMarkers(s) {
			return brokenConflictMarkers + " in " + k
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
			if keys := out.NewerWins(); len(keys) > 0 {
				// git shows a driver's stderr during the merge: say which
				// changes lost, so a teammate's edit never vanishes silently.
				fmt.Fprintf(os.Stderr, "lore merge-driver: %s: both sides changed %s; kept the newer edit\n", repoPath, strings.Join(keys, ", "))
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
		b, err := os.ReadFile(filepath.Clean(filepath.Join(dataDir, rel)))
		if err == nil {
			return rel, b, nil
		}
	}
	return "", nil, errcodes.New(errcodes.NotFound, "no .lore/data file for "+id).
		WithHint("only synced rows can be promoted; run `lore sync status`")
}

// unchangedWord is printed by `lore sync install-git` for a list with no
// changes, instead of an empty value.
const unchangedWord = "unchanged"

// joinOrUnchanged lists what changed, or says nothing did.
func joinOrUnchanged(items []string) string {
	if len(items) == 0 {
		return unchangedWord
	}
	return strings.Join(items, ", ")
}
