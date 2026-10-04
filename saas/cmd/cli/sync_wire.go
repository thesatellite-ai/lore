// sync_wire.go — wires the lsync engine into every lore command.
//
// Every command that opens a Mode A project runs a sync pass BEFORE it
// touches the DB (so it sees what git just brought in: pull, checkout,
// merge, rebase) and AFTER it finishes (so its own writes reach
// .lore/data immediately). Design: LORE_SYNC_SPEC.md at the repo root.
//
// Opt-outs (environment):
//
//	LORE_SYNC=0                 disable sync entirely (cache-only lore.db)
//	LORE_SYNC_GIT=0             keep syncing files, but never touch git config,
//	                            .gitattributes or hooks
//	LORE_SYNC_QUIET=1           no sync chatter on stderr (errors still print)
//	LORE_SYNC_ALLOW_SECRETS=1   export rows even when a credential pattern
//	                            matches (one run; the value WILL be committed)
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"dbent"
	"saas/pkg/aicoder/audit"
	"saas/pkg/aicoder/gitsetup"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/security"
	"saas/pkg/aicoder/style"
)

// Environment switches (see file doc).
// ds:def id=sync-env-qmha7pjc owner=@khanakia stability=stable desc="LORE_SYNC* environment switches"
const (
	envSync            = "LORE_SYNC"
	envSyncGit         = "LORE_SYNC_GIT"
	envSyncQuiet       = "LORE_SYNC_QUIET"
	envSyncAllowSecret = "LORE_SYNC_ALLOW_SECRETS"
	envReadOnly        = "LORE_READ_ONLY"
	envValueOff        = "0"
	envValueOn         = "1"
)

// syncLockFile serialises sync passes across lore processes. Separate from
// processlock's general lock so a long-running command never blocks sync.
const syncLockFile = "sync.lock"

// loreMDRel is the generated knowledge file `lore render` writes; the post
// step re-renders it when teammates' changes were imported.
const loreMDRel = ".lore/LORE.md"

// syncLogPrefix starts every sync line on stderr.
const syncLogPrefix = "lore sync: "

// syncSession remembers a project whose DB a command opened, so the post
// step can run its pass after the command's own writes.
type syncSession struct {
	root    string
	dbPath  string
	dataDir string
	// wroteFiles is set when a pass of this command exported or removed
	// row files; only then is the "not committed" reminder worth a git call.
	wroteFiles bool
}

// syncSessions is per-process state: the CLI handles one command per
// process, so this is the command's list of opened projects.
var syncSessions = map[string]*syncSession{}

// syncDataDir returns <root>/.lore/data for a Mode A DB at
// <root>/.lore/lore.db. Any other DB location (an explicit --db elsewhere,
// Mode B shared DBs) has no sync dir: ok is false.
func syncDataDir(dbPath string) (string, bool) {
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return "", false
	}
	dir := filepath.Dir(abs)
	if filepath.Base(abs) != projresolve.ModeAFile || filepath.Base(dir) != projresolve.MarkerDir {
		return "", false
	}
	return filepath.Join(dir, lsync.DataDirName), true
}

// syncApplies reports whether the resolved project participates in sync.
func syncApplies(rctx *projresolve.Context) bool {
	if os.Getenv(envSync) == envValueOff || rctx == nil || rctx.Mode != projresolve.ModeA {
		return false
	}
	_, ok := syncDataDir(rctx.DBPath)
	return ok
}

// preCommandSync runs the start-of-command pass on the already-open DB.
// A failing pass never blocks the user's command: the cache is still a
// valid DB; the failure is printed and retried on the next command.
func preCommandSync(ctx context.Context, rctx *projresolve.Context, db *sql.DB) {
	if !syncApplies(rctx) {
		return
	}
	dataDir, _ := syncDataDir(rctx.DBPath)
	root := filepath.Dir(filepath.Dir(dataDir))
	s := syncSessions[rctx.DBPath]
	if s == nil {
		s = &syncSession{root: root, dbPath: rctx.DBPath, dataDir: dataDir}
		syncSessions[rctx.DBPath] = s
	}
	rep, err := runCatchUpPass(ctx, db, root, dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, style.Warn(syncLogPrefix+err.Error()))
		return
	}
	s.wroteFiles = s.wroteFiles || len(rep.Exported)+len(rep.Removed) > 0
	printSyncReport(rep)
}

// finishSyncSessions runs the end-of-command pass for every project the
// command opened, then wires git, re-renders LORE.md when teammates'
// changes arrived, and reminds the user about uncommitted lore files.
// Called from main after the command returns (success or failure: a
// failed command may still have written rows).
func finishSyncSessions(ctx context.Context) {
	for _, s := range syncSessions {
		db := dbent.InitDB(s.dbPath)
		if err := dbent.ApplyPragmas(db); err != nil {
			fmt.Fprintln(os.Stderr, style.Warn(syncLogPrefix+"open DB: "+err.Error()))
			_ = db.Close() // best-effort; the warning above is the signal
			continue
		}
		// Dirty-only: the start-of-command pass already walked the files;
		// this one just exports what the command wrote.
		o := syncOptions(db, s.root, s.dataDir, lsync.ChangeLocal)
		o.DirtyOnly = true
		rep, err := lsync.Reconcile(ctx, o)
		if err != nil {
			_ = db.Close() // the pass error is the one to report
			fmt.Fprintln(os.Stderr, style.Warn(syncLogPrefix+err.Error()))
			continue
		}
		printSyncReport(rep)
		s.wroteFiles = s.wroteFiles || len(rep.Exported)+len(rep.Removed) > 0
		rerenderIfDue(ctx, db, s.root)
		// Read-only commands never write: the wiring changes hooks, config,
		// .gitattributes, .gitignore and the workflow file.
		if os.Getenv(envSyncGit) != envValueOff && os.Getenv(envReadOnly) != envValueOn {
			wireGit(ctx, db, s.root)
			if s.wroteFiles {
				remindUncommitted(ctx, s.root)
			}
		}
		_ = db.Close() // finished with it; close errors are not actionable
	}
}

// runSyncPass runs one pass that exports the running command's own writes
// (audited as lore writes).
func runSyncPass(ctx context.Context, db *sql.DB, root, dataDir string) (lsync.Report, error) {
	return lsync.Reconcile(ctx, syncOptions(db, root, dataDir, lsync.ChangeLocal))
}

// runCatchUpPass runs the start-of-command pass: whatever changed in the DB
// since the last lore command was written by something else (sqlite3, a
// script) and is audited as an external write.
func runCatchUpPass(ctx context.Context, db *sql.DB, root, dataDir string) (lsync.Report, error) {
	return lsync.Reconcile(ctx, syncOptions(db, root, dataDir, lsync.ChangeExternal))
}

func syncOptions(db *sql.DB, root, dataDir string, kind lsync.ChangeKind) lsync.Options {
	o := lsync.Options{
		DB:           db,
		DataDir:      dataDir,
		LockPath:     filepath.Join(root, projresolve.MarkerDir, projresolve.StateDir, syncLockFile),
		ReadOnly:     os.Getenv(envReadOnly) == envValueOn,
		Backup:       backupBeforeSync(db, root),
		DBChangeKind: kind,
		OnChange:     auditChange,
	}
	o.CheckSecrets = secretCheck()
	return o
}

// secretCheck returns the credential scanner used for every file that may
// reach git (export and pre-commit), or nil when LORE_SYNC_ALLOW_SECRETS=1.
func secretCheck() func([]byte) string {
	if os.Getenv(envSyncAllowSecret) == envValueOn {
		return nil
	}
	scanner := security.NewScanner()
	return func(b []byte) string {
		if m := scanner.Scan(string(b)); len(m) > 0 {
			return "credential pattern " + m[0].PatternName + " found"
		}
		return ""
	}
}

// preSyncBackupSuffix names the snapshot taken before BOOTSTRAP / ADOPT.
const preSyncBackupSuffix = "-pre-sync.sqlite"

// preSyncBackupStamp names pre-sync backups: sortable, UTC, nanoseconds so
// two passes in the same second never overwrite each other's backup.
const preSyncBackupStamp = "20060102-150405.000000000"

// File modes for what the sync commands write into the work tree (.lore/
// directories, restored files, the generated workflow): git's work-tree
// defaults.
const (
	syncDirMode  = 0o755
	syncFileMode = 0o644
)

// backupBeforeSync snapshots lore.db with VACUUM INTO (safe on a live DB)
// into .lore/backups/, the same place `lore backup` writes.
func backupBeforeSync(db *sql.DB, root string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		dir := filepath.Join(root, projresolve.MarkerDir, projresolve.BackupDir)
		if err := os.MkdirAll(dir, syncDirMode); err != nil {
			return "", err
		}
		out := filepath.Join(dir, time.Now().UTC().Format(preSyncBackupStamp)+preSyncBackupSuffix)
		if _, err := db.ExecContext(ctx, "VACUUM INTO ?", out); err != nil {
			return "", err
		}
		return out, nil
	}
}

// printSyncReport writes a compact summary to stderr. Silent when nothing
// happened; errors and conflicts always print, even with LORE_SYNC_QUIET.
func printSyncReport(rep lsync.Report) {
	quiet := os.Getenv(envSyncQuiet) == envValueOn
	if !quiet {
		switch rep.Pass {
		case lsync.PassBootstrap:
			fmt.Fprintf(os.Stderr, "%s%s\n", syncLogPrefix, style.Success(fmt.Sprintf(
				"exported %d rows to .lore/data — review and commit it", len(rep.Exported))))
		case lsync.PassAdopt:
			fmt.Fprintf(os.Stderr, "%sadopted .lore/data: imported %d, exported %d, conflicts %d\n",
				syncLogPrefix, len(rep.Imported), len(rep.Exported), len(rep.Conflicts))
		default:
			var parts []string
			for _, c := range []struct {
				n    int
				what string
			}{
				{len(rep.Imported), "imported"},
				{len(rep.Exported), "exported"},
				{len(rep.Deleted), "trashed rows"},
				{len(rep.Removed), "removed files"},
			} {
				if c.n > 0 {
					parts = append(parts, fmt.Sprintf("%s %d", c.what, c.n))
				}
			}
			if len(parts) > 0 {
				fmt.Fprintf(os.Stderr, "%s%s\n", syncLogPrefix, strings.Join(parts, ", "))
			}
			if len(rep.Deleted) > 0 {
				fmt.Fprintf(os.Stderr, "%srows whose file disappeared are kept in `lore sync trash` (undo: `lore sync trash restore <id>`)\n", syncLogPrefix)
			}
		}
		if rep.BackupPath != "" {
			fmt.Fprintf(os.Stderr, "%sbackup of lore.db before %s: %s\n", syncLogPrefix, rep.Pass, rep.BackupPath)
		}
		for _, m := range rep.Merged {
			fmt.Fprintln(os.Stderr, syncLogPrefix+"merged duplicate "+m)
		}
		for _, w := range rep.Warnings {
			fmt.Fprintln(os.Stderr, style.Warn(syncLogPrefix+w))
		}
	}
	for _, c := range rep.Conflicts {
		fmt.Fprintln(os.Stderr, style.Warn(fmt.Sprintf("%sconflict %s: kept %s — %s", syncLogPrefix, c.Path, c.Kept, c.Why)))
	}
	for _, e := range rep.Errors {
		fmt.Fprintln(os.Stderr, style.Error(fmt.Sprintf("%s%s %s: %s", syncLogPrefix, e.Kind, e.Path, e.Detail)))
	}
}

// envDBPath is projresolve's explicit-DB environment override.
const envDBPath = "LORE_DB"

// needsMaterialize reports whether root is a checkout that carries shared
// lore data (.lore/data/_meta.json) but has no local cache yet: a fresh
// clone, or a deleted/corrupted-and-removed lore.db (E44).
func needsMaterialize(root string) bool {
	if os.Getenv(envSync) == envValueOff {
		return false
	}
	marker := filepath.Join(root, projresolve.MarkerDir)
	if !lsync.MetaExists(filepath.Join(marker, lsync.DataDirName)) {
		return false
	}
	for _, f := range []string{projresolve.ModeAFile, projresolve.ModeBFile} {
		if _, err := os.Lstat(filepath.Join(marker, f)); err == nil {
			return false
		}
	}
	return true
}

// materializeFromData builds lore.db for such a checkout: same schema as
// `lore init`, then an ADOPT pass imports every row file. No project row is
// minted — the project comes from the files (_meta.json pins it).
func materializeFromData(ctx context.Context, root string) (err error) {
	marker := filepath.Join(root, projresolve.MarkerDir)
	if err := os.MkdirAll(filepath.Join(marker, projresolve.StateDir), syncDirMode); err != nil {
		return fmt.Errorf("create .lore/state: %w", err)
	}
	dbPath := filepath.Join(marker, projresolve.ModeAFile)
	if err := createProjectDB(ctx, dbPath); err != nil {
		return err
	}
	db := dbent.InitDB(dbPath)
	defer func() {
		// This DB was just written: a failed close can mean a failed WAL
		// checkpoint, so it is reported unless an earlier error already is.
		if cerr := db.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close lore.db: %w", cerr)
		}
	}()
	if err := dbent.ApplyPragmas(db); err != nil {
		return fmt.Errorf("apply pragmas: %w", err)
	}
	rep, err := runSyncPass(ctx, db, root, filepath.Join(marker, lsync.DataDirName))
	if err != nil {
		return err
	}
	if os.Getenv(envSyncQuiet) != envValueOn {
		fmt.Fprintf(os.Stderr, "%sbuilt .lore/lore.db from .lore/data (%d rows)\n", syncLogPrefix, len(rep.Imported))
	}
	printSyncReport(rep)
	return nil
}

// maybeMaterialize runs materializeFromData for the cwd project when the
// command did not point at an explicit DB.
func maybeMaterialize(ctx context.Context, c *commonFlags) error {
	if c.flagDB != "" || os.Getenv(envDBPath) != "" {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil || !needsMaterialize(cwd) {
		return nil
	}
	return materializeFromData(ctx, cwd)
}

// rerenderIfDue re-renders LORE.md when rows were imported since its last
// render — by this command, an earlier one, or a git hook (E35).
func rerenderIfDue(ctx context.Context, db *sql.DB, root string) {
	due, err := lsync.RerenderDue(ctx, db)
	if err != nil || !due {
		return
	}
	if rerenderLoreMD(ctx, root) {
		if err := lsync.MarkRerendered(ctx, db); err != nil {
			fmt.Fprintln(os.Stderr, style.Warn(syncLogPrefix+"record re-render: "+err.Error()))
		}
	}
}

// rerenderLoreMD refreshes .lore/LORE.md after teammates' knowledge was
// imported, so the agent context file matches the merged data. Only when
// the project already renders (the file exists); quiet, never stitches
// CLAUDE.md (that is `lore render`'s explicit job).
//
// Returns true when the file is current afterwards (rendered, or the project
// does not render at all), false when it should be retried later.
func rerenderLoreMD(ctx context.Context, root string) bool {
	out := filepath.Join(root, filepath.FromSlash(loreMDRel))
	if _, err := os.Stat(out); err != nil {
		return true // project does not use render: nothing is stale
	}
	cwd, err := os.Getwd()
	if err != nil || filepath.Clean(cwd) != filepath.Clean(root) {
		return false // render resolves the project from cwd (no walk-up)
	}
	f := &renderFlags{outPath: out, noPointer: true, quiet: true}
	if err := runRender(ctx, f); err != nil {
		fmt.Fprintln(os.Stderr, style.Warn(syncLogPrefix+"re-render "+loreMDRel+": "+err.Error()))
		return false
	}
	return true
}

// Git wiring values (the decisions; gitsetup holds the mechanism).
const (
	gitBlockMarker      = "lore-sync"
	mergeDriverLore     = "lore"
	mergeDriverKeepOurs = "lore-keep-ours"
)

// gitAttributeLines are the .gitattributes rules lore needs, relative to
// the project root (attributes apply to the directory they live in):
//   - row files: LF everywhere (E26), lore's field-level merge driver,
//     collapsed in GitHub PR diffs (linguist-generated)
//   - LORE.md: generated; keep ours on merge and let lore re-render it
//     from the merged data (E35)
//
// ds:def id=sync-gitattributes-xzr8sqyk owner=@khanakia stability=stable desc=".gitattributes rules lore installs"
var gitAttributeLines = []string{
	".lore/data/**/*.json text eol=lf merge=" + mergeDriverLore + " linguist-generated=true",
	".lore/data/*.json text eol=lf merge=" + mergeDriverLore + " linguist-generated=true",
	loreMDRel + " text eol=lf merge=" + mergeDriverKeepOurs + " linguist-generated=true",
}

// mergeDriverConfig is the per-clone git config the attributes refer to.
// git never runs a driver named only in .gitattributes; each clone must
// define it, which wireGit does on every command (idempotent).
// ds:def id=sync-mergedriver-config-bur8yta8 owner=@khanakia stability=stable desc="per-clone merge driver config"
func mergeDriverConfig() [][2]string {
	return [][2]string{
		{"merge." + mergeDriverLore + ".name", "lore field-level row merge"},
		{"merge." + mergeDriverLore + ".driver", BinaryName + " merge-driver %O %A %B %P"},
		{"merge." + mergeDriverKeepOurs + ".name", "lore: keep ours, re-render after merge"},
		{"merge." + mergeDriverKeepOurs + ".driver", BinaryName + " merge-driver --keep-ours %O %A %B %P"},
	}
}

// hookLines returns the block chained into each git hook. Every block is a
// no-op when lore is not installed, and only pre-commit may fail the git
// operation (on invalid / conflicted lore files).
// ds:def id=sync-hooklines-3282rwuw owner=@khanakia stability=stable desc="hook blocks chained into git hooks"
func hookLines(projectRel string) map[string][]string {
	cd := ""
	if !isRepoRoot(projectRel) {
		cd = `cd "` + filepath.ToSlash(projectRel) + `" && `
	}
	guard := `command -v ` + BinaryName + ` >/dev/null 2>&1 && `
	refresh := []string{"( " + cd + guard + BinaryName + " sync hook refresh >/dev/null 2>&1 ) || true"}
	// The hook scripts may be committed (a repo's .githooks), so teammates
	// who still run a lore without `sync hook` execute them too. Probing
	// for the command keeps their commits working; they get a note instead.
	probe := BinaryName + " sync hook --help >/dev/null 2>&1"
	tooOld := `echo "` + BinaryName + `: installed ` + BinaryName + ` cannot check .lore/data (no 'sync hook'); upgrade it" >&2`
	return map[string][]string{
		"pre-commit":    {"( " + cd + "if command -v " + BinaryName + " >/dev/null 2>&1; then if " + probe + "; then " + BinaryName + " sync hook pre-commit; else " + tooOld + "; fi; fi ) || exit $?"},
		"post-checkout": refresh,
		"post-merge":    refresh,
		"post-rewrite":  refresh,
	}
}

// projectRelPath returns dir relative to the repository's top level, slash
// separated. Symlinks are resolved first (git reports the resolved top level;
// macOS temp dirs live behind /var → /private/var). It returns "" — the
// repository root — when dir cannot be placed inside toplevel; callers that
// match paths against it then match nothing, which is the safe failure.
func projectRelPath(toplevel, dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	rel, err := filepath.Rel(toplevel, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

// isRepoRoot reports whether a project path relative to the repository's
// top level names the top level itself ("" from a failed Rel, "." from a
// successful one); such a project needs no `cd` and keeps the plain marker.
func isRepoRoot(projectRel string) bool {
	return projectRel == "" || projectRel == repoRootRel
}

// repoRootRel is what filepath.Rel returns for a path relative to itself.
const repoRootRel = "."

// hookMarker names a project's block inside the shared hook scripts. Each
// lore project in a repo (a monorepo may hold several) needs its own block,
// or the last project to run would overwrite the others' `cd <project>`
// line and only it would be checked on commit.
func hookMarker(projectRel string) string {
	if isRepoRoot(projectRel) {
		return gitBlockMarker
	}
	return gitBlockMarker + ":" + filepath.ToSlash(projectRel)
}

// gitWiringResult lists what wireGit changed (for `lore sync install-git`).
type gitWiringResult struct {
	Toplevel   string `json:"toplevel"`
	HooksDir   string `json:"hooks_dir"`
	ConfigPath string `json:"config_path"`
	Attributes bool   `json:"attributes_changed"`
	// Gitignore is true when lore re-included .lore/data in .gitignore
	// because a rule there hid lore files.
	Gitignore bool `json:"gitignore_changed"`
	// IgnoredData lists lore paths git still ignores after that, each with
	// the rule responsible. Non-empty means teammates will not receive them.
	IgnoredData []string `json:"ignored_data,omitempty"`
	// Workflow reports the automatic refresh of an installed
	// lore-sync-merge workflow's lore version (empty when nothing to do).
	Workflow syncActionRefresh `json:"workflow"`
	Config   []string          `json:"config_changed,omitempty"`
	Hooks    []string          `json:"hooks_changed,omitempty"`
}

// ensureGitWiring installs the merge drivers, attributes and hook blocks.
// It never repoints core.hooksPath: blocks are chained into whatever hook
// directory git already uses (husky, lefthook, a repo's .githooks), E46.
func ensureGitWiring(ctx context.Context, root string) (gitWiringResult, error) {
	repo, err := gitsetup.Open(ctx, root)
	if err != nil {
		return gitWiringResult{}, err
	}
	res := gitWiringResult{Toplevel: repo.Toplevel, HooksDir: repo.HooksDir, ConfigPath: repo.ConfigPath}
	changed, err := gitsetup.EnsureBlock(filepath.Join(root, ".gitattributes"), gitBlockMarker, gitAttributeLines)
	if err != nil {
		return res, err
	}
	res.Attributes = changed
	if res.Config, err = gitsetup.EnsureConfig(ctx, root, mergeDriverConfig()); err != nil {
		return res, err
	}
	if res.Gitignore, res.IgnoredData, err = ensureDataNotIgnored(ctx, root); err != nil {
		return res, err
	}
	if res.Workflow, err = refreshSyncActionPin(ctx, repo, root, version); err != nil {
		// Never fatal: the workflow only matters in CI, and the rest of the
		// wiring must still happen.
		res.Workflow = syncActionRefresh{Note: "could not check the lore-sync-merge workflow: " + err.Error()}
	}
	rel := projectRelPath(repo.Toplevel, root)
	for hook, lines := range hookLines(rel) {
		ch, err := gitsetup.EnsureHookBlock(repo.HooksDir, hook, hookMarker(rel), lines)
		if err != nil {
			return res, err
		}
		if ch {
			res.Hooks = append(res.Hooks, hook)
		}
	}
	return res, nil
}

// dataRel is the committed data folder, relative to the project root.
var dataRel = path.Join(projresolve.MarkerDir, lsync.DataDirName)

// dataUnignoreLines re-include lore's data folder when a repo's own ignore
// rules happen to match lore files (a `_*` rule hides _meta.json and a fresh
// clone then does not recognise the folder; `*.json` or `snapshots/` hide
// rows). A negation cannot re-include a file whose parent folder is
// excluded (`.lore/`): ensureDataNotIgnored reports that case instead.
//
// The negation re-includes EVERYTHING under the folder, including the
// .DS_Store / Thumbs.db a file manager drops there, which the repo's own
// rules may have excluded; the junk names are ignored again right after it
// (in a .gitignore the later rule wins).
var dataUnignoreLines = append([]string{"!" + dataRel + "/", "!" + dataRel + "/**"}, dataJunkIgnoreLines()...)

// dataJunkIgnoreLines ignores operating-system clutter at any depth of the
// data folder.
func dataJunkIgnoreLines() []string {
	var out []string
	for _, p := range lsync.OSJunkGitPatterns() {
		out = append(out, dataRel+"/**/"+p)
	}
	return out
}

// ensureDataNotIgnored makes sure `git add` picks up every kind of file sync
// writes. When a rule hides one, it appends dataUnignoreLines to the
// project's .gitignore and checks again. If that re-included nothing (the
// whole .lore/ folder is excluded, usually on purpose to keep lore private)
// the edit is undone, so .gitignore only ever changes when it helps.
// Whatever is still ignored is returned as "path (rule)" so the caller can
// name the rule.
func ensureDataNotIgnored(ctx context.Context, root string) (bool, []string, error) {
	reg, _ := lsync.NewRegistry() // unclassified tables are a build-time failure (registry_test)
	probes := make([]string, 0, len(reg.Names())+2)
	for _, p := range lsync.SamplePaths(reg) {
		probes = append(probes, path.Join(dataRel, p))
	}
	ignored, err := gitsetup.IgnoredPaths(ctx, root, probes)
	if err != nil || len(ignored) == 0 {
		return false, nil, err
	}
	gitignore := filepath.Join(root, ".gitignore")
	original, readErr := os.ReadFile(gitignore)
	existed := readErr == nil
	before := len(ignored)
	changed, err := gitsetup.EnsureBlock(gitignore, gitBlockMarker, dataUnignoreLines)
	if err != nil {
		return false, nil, err
	}
	if ignored, err = gitsetup.IgnoredPaths(ctx, root, probes); err != nil {
		return changed, nil, err
	}
	if changed && len(ignored) >= before {
		if err := restoreFile(gitignore, original, existed); err != nil {
			return true, nil, err
		}
		changed = false
	}
	still := make([]string, 0, len(ignored))
	for p, rule := range ignored {
		still = append(still, p+" (ignored by "+rule+")")
	}
	sort.Strings(still)
	return changed, still, nil
}

// restoreFile puts back a file's earlier content, or removes it when it did
// not exist before.
func restoreFile(path string, content []byte, existed bool) error {
	if !existed {
		return os.Remove(path)
	}
	return os.WriteFile(path, content, syncFileMode)
}

// State keys (lsync caller state) caching the git wiring check.
const (
	stateGitPaths       = "git_wiring_paths"
	stateGitFingerprint = "git_wiring_fingerprint"
)

// wiringVersion changes whenever what wireGit installs changes, forcing
// every clone to re-check once after an upgrade.
func wiringVersion() string {
	h := sha256.New()
	// The running lore's version: an upgrade must re-run the check once, or
	// an installed workflow would keep its old pin until something else
	// touched the wiring.
	h.Write([]byte(version + "\n"))
	for _, l := range append(append([]string(nil), gitAttributeLines...), dataUnignoreLines...) {
		h.Write([]byte(l + "\n"))
	}
	for _, kv := range mergeDriverConfig() {
		h.Write([]byte(kv[0] + "=" + kv[1] + "\n"))
	}
	hooks := hookLines("")
	names := make([]string, 0, len(hooks))
	for n := range hooks {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		h.Write([]byte(n + ":" + strings.Join(hooks[n], "\n") + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// wiringFingerprint stats every file the wiring lives in. Equal
// fingerprints mean nobody touched the git config, the hooks or
// .gitattributes since lore last verified them — so no git process needs
// to be spawned (each costs tens of milliseconds).
func wiringFingerprint(paths []string) string {
	h := sha256.New()
	h.Write([]byte(wiringVersion()))
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			h.Write([]byte(p + ":missing\n"))
			continue
		}
		fmt.Fprintf(h, "%s:%d:%d\n", p, info.Size(), info.ModTime().UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil))
}

// wiredPaths lists the files whose change invalidates the wiring cache.
func wiredPaths(root string, res gitWiringResult) []string {
	paths := []string{res.ConfigPath, filepath.Join(root, ".gitattributes")}
	// The project's workflow file: a hand edit, an install or an uninstall
	// re-runs the check.
	if res.Toplevel != "" {
		paths = append(paths, filepath.Join(res.Toplevel, filepath.FromSlash(syncActionFileRel(lorePathPrefix(res.Toplevel, root)))))
	}
	// Ignore files that can hide .lore/data: a new rule there re-runs the check.
	for _, dir := range []string{res.Toplevel, root, filepath.Join(root, projresolve.MarkerDir), filepath.Join(root, filepath.FromSlash(dataRel))} {
		if dir != "" {
			paths = append(paths, filepath.Join(dir, ".gitignore"))
		}
	}
	for hook := range hookLines("") {
		paths = append(paths, filepath.Join(res.HooksDir, hook))
	}
	sort.Strings(paths)
	return paths
}

// wireGit is the silent per-command form of ensureGitWiring: not a git repo
// or no git installed is fine (sync still works on files), and any change
// it made is mentioned once. A stat-only fingerprint skips the git calls
// when nothing relevant changed since the last verification.
func wireGit(ctx context.Context, db *sql.DB, root string) {
	if cached, err := lsync.GetState(ctx, db, stateGitPaths); err == nil && cached != "" {
		paths := strings.Split(cached, "\n")
		fp, _ := lsync.GetState(ctx, db, stateGitFingerprint)
		if fp != "" && fp == wiringFingerprint(paths) {
			return
		}
	}
	res, err := ensureGitWiring(ctx, root)
	if err != nil {
		if err != gitsetup.ErrNotRepo && err != gitsetup.ErrNoGit {
			fmt.Fprintln(os.Stderr, style.Warn(syncLogPrefix+"git wiring: "+err.Error()))
		}
		return
	}
	if (res.Attributes || len(res.Config) > 0 || len(res.Hooks) > 0) && os.Getenv(envSyncQuiet) != envValueOn {
		fmt.Fprintf(os.Stderr, "%sconfigured git for lore data (merge driver, .gitattributes, hooks in %s)\n", syncLogPrefix, res.HooksDir)
	}
	if res.Gitignore && os.Getenv(envSyncQuiet) != envValueOn {
		fmt.Fprintf(os.Stderr, "%sa .gitignore rule hid lore files; re-included %s in .gitignore — commit it\n", syncLogPrefix, dataRel)
	}
	if w := res.Workflow; w.To != "" && os.Getenv(envSyncQuiet) != envValueOn {
		fmt.Fprintf(os.Stderr, "%supdated %s to lore %s (was %s) — commit it\n", syncLogPrefix, displayPath(root, w.Path), w.To, w.From)
	}
	if w := res.Workflow; w.Note != "" {
		// Shown once per state of the file (it is a watched path).
		fmt.Fprintln(os.Stderr, style.Warn(syncLogPrefix+displayPath(root, w.Path)+": "+w.Note))
	}
	if len(res.IgnoredData) > 0 {
		// Never quiet. Shown once per state of the ignore files: what is
		// left is a whole excluded folder, often a deliberate choice to keep
		// lore private, so repeating it on every command would be noise.
		// Editing any .gitignore in wiredPaths re-runs the check.
		fmt.Fprintln(os.Stderr, style.Warn(ignoredDataMessage(res.IgnoredData)))
	}
	paths := wiredPaths(root, res)
	if err := lsync.SetState(ctx, db, stateGitPaths, strings.Join(paths, "\n")); err == nil {
		_ = lsync.SetState(ctx, db, stateGitFingerprint, wiringFingerprint(paths)) // cache only; recomputed next time if lost
	}
}

// displayPath shows path relative to root when it is inside it.
func displayPath(root, path string) string {
	if path == "" {
		return "lore-sync-merge workflow"
	}
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}

// ignoredDataMessage explains which lore files git will not commit and why.
func ignoredDataMessage(ignored []string) string {
	return fmt.Sprintf("%sgit ignores lore data, so teammates will not receive it:\n  %s\nchange that rule so %s/ is not excluded (a negation cannot re-include files inside an ignored folder)",
		syncLogPrefix, strings.Join(ignored, "\n  "), dataRel)
}

// remindUncommitted prints how many lore files are not committed (E37).
func remindUncommitted(ctx context.Context, root string) {
	if os.Getenv(envSyncQuiet) == envValueOn {
		return
	}
	paths, err := gitsetup.Uncommitted(ctx, root, filepath.Join(projresolve.MarkerDir, lsync.DataDirName))
	if err != nil || len(paths) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "%s%d file(s) in .lore/data not committed — commit them with your change\n", syncLogPrefix, len(paths))
}

// auditChange appends one hash-chained audit entry per row a sync pass
// changed, inside the pass's transaction (saas/pkg/aicoder/audit).
// Action is "<table>.<kind>", e.g. "rules.write", "memories.import",
// "tasks.external-write".
func auditChange(ctx context.Context, q lsync.Execer, c lsync.Change) error {
	return audit.Append(ctx, q, audit.Entry{
		ActorID:     currentAuditActor(ctx, q),
		Action:      c.Table + "." + string(c.Kind),
		TargetTable: c.Table,
		TargetID:    c.ID,
		BeforeHash:  c.BeforeHash,
		AfterHash:   c.AfterHash,
	})
}

// auditActor caches the resolved actor for this process (one CLI command).
var auditActor string

// currentAuditActor returns the actor id of the person running lore, or
// their stable key when no actor row exists yet (e.g. the first command
// in a fresh clone). Never fails: an audit entry without a resolvable
// actor still records the change.
func currentAuditActor(ctx context.Context, q lsync.Execer) string {
	if auditActor != "" {
		return auditActor
	}
	key := identity.Resolve(identity.Inputs{}).StableKey
	var id string
	if err := q.QueryRowContext(ctx, `SELECT id FROM actors WHERE stable_key = ?`, key).Scan(&id); err == nil && id != "" {
		auditActor = id
		return id
	}
	if key == "" {
		key = auditUnknownActor
	}
	return key
}

// auditUnknownActor marks entries whose actor could not be resolved.
const auditUnknownActor = "unknown"
