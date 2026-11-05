// doctor.go — `lore doctor` (S2.5)
//
// Health check command. Exit codes (R29 #49 stable contract):
//
//	0 healthy
//	1 degraded (warnings)
//	2 broken (refuse to use)
//
// JSON schema versioned per R25 #22
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"saas/pkg/constants"
	"strings"

	"dbent"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitsetup"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/migrationlog"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/style"

	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
	"github.com/thesatellite-ai/lore/wire"

	"github.com/spf13/cobra"
)

// ds:def id=doctor-statuses-y6sro1m4 owner=@khanakia stability=stable desc="lore doctor statuses and their exit codes (healthy 0, degraded 1, broken 2)"
// Doctor statuses — a stable contract for monitoring (exit 0 / 1 / 2).
const (
	doctorHealthy  = "healthy"
	doctorDegraded = "degraded"
	doctorBroken   = "broken"
)

type doctorReport struct {
	SchemaVersion int                `json:"schema_version"`
	OK            bool               `json:"ok"`
	Status        string             `json:"status"` // healthy | degraded | broken
	DBOK          bool               `json:"db_ok"`
	WALSizeBytes  int64              `json:"wal_size_bytes"`
	IdentityInfo  doctorIdentityInfo `json:"identity"`
	PathsConflict []string           `json:"path_conflicts,omitempty"`
	Warnings      []string           `json:"warnings,omitempty"`
	Notes         []string           `json:"notes,omitempty"`
	Errors        []string           `json:"errors,omitempty"`
	BinaryVersion string             `json:"binary_version"`
	// Sync is present only for Mode A projects that sync through git.
	Sync *doctorSyncInfo `json:"sync,omitempty"`
	// RepoAddresses is the "Repo addresses" check (PROV2, Q5); present
	// once the checkout has a repo id.
	RepoAddresses *opsinfra.AddressReport `json:"repo_addresses,omitempty"`
}

// doctorSyncInfo is the [sync] section of the doctor report.
type doctorSyncInfo struct {
	lsync.Status
	ProjectFiles    int `json:"project_files"`
	DanglingRefs    int `json:"dangling_references"`
	DuplicateGroups int `json:"duplicate_groups"`
	// FileErrors are the broken / refused files doctor actually reports:
	// live CheckFiles results plus recorded secret refusals (stale shape
	// errors from an earlier pass are not repeated).
	FileErrors []lsync.FileError `json:"file_errors,omitempty"`
	// Uncommitted counts .lore/data files not yet committed (informational).
	Uncommitted   int  `json:"uncommitted_files"`
	DataIgnored   bool `json:"data_dir_gitignored"`
	MergeDriverOK bool `json:"merge_driver_configured"`
	InGitRepo     bool `json:"in_git_repo"`
}

type doctorIdentityInfo struct {
	Resolved string `json:"resolved"`
	Source   string `json:"source"`
	Stable   bool   `json:"stable"`
}

type doctorFlags struct {
	commonFlags
	jsonOutput bool
	// fix applies the safe repairs doctor knows (today: reversed
	// --supersedes links written by older versions; supersede_repair.go).
	fix bool
}

func newDoctorCommand() *cobra.Command {
	f := &doctorFlags{}
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run health checks; exit 0 healthy, 1 degraded, 2 broken",
		Long: `doctor inspects the project DB and identity resolution state

Checks:
  • DB integrity (PRAGMA quick_check)
  • WAL file size (warn if > 100MB)
  • Identity resolution + stability
  • PATH conflicts (` + "`which -a aicoder`" + `)
  • Sync: pending exports, conflicts, broken .lore/data files, duplicate
    project files, dangling references, duplicate rows, .lore/data
    gitignored, merge driver missing (uncommitted files are reported,
    not counted as unhealthy)
  • Schema version (TODO: validate against expected)
  • --supersedes links written the old way (on the new row instead of
    the old one); --fix repairs them (and writes a task lead missing from its assignees back), and a second --fix changes nothing

Exit codes are stable across versions for monitoring integration.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := runDoctor(cmd.Context(), f)
			if err != nil {
				return err
			}
			if f.jsonOutput {
				out, _ := json.MarshalIndent(report, "", "  ")
				fmt.Println(string(out))
			} else {
				printDoctor(report)
			}
			code := 0
			switch report.Status {
			case doctorBroken:
				code = 2
			case doctorDegraded:
				code = 1
			}
			if code == 0 {
				return nil
			}
			// os.Exit skips main's end-of-command sync pass, which is what
			// writes the rows --fix repaired to .lore/data; run it first.
			if f.fix {
				if err := finishSyncSessions(cmd.Context()); err != nil {
					fmt.Fprintln(os.Stderr, style.Warn("lore doctor --fix: "+err.Error()))
				}
			}
			os.Exit(code)
			return nil
		},
	}
	bindCommonFlags(cmd, &f.commonFlags)
	cmd.Flags().BoolVar(&f.jsonOutput, constants.FlagJSON, false, "JSON output (stable schema)")
	cmd.Flags().BoolVar(&f.fix, doctorFlagFix, false, "repair what doctor can fix safely (reversed --supersedes links, a task lead missing from assignees)")
	return cmd
}

func runDoctor(ctx context.Context, f *doctorFlags) (_ *doctorReport, err error) {
	r := &doctorReport{
		SchemaVersion: 1,
		BinaryVersion: version,
		Status:        doctorHealthy,
	}

	// 1. Resolve project context (errors don't fail the doctor — they go in
	// the report so JSON consumers see structured status)
	rctx, err := projresolve.Resolve(projresolve.Inputs{
		Dir:         oldProjectDir(),
		FlagDB:      f.flagDB,
		FlagProject: f.flagProject,
		FlagRepo:    f.flagRepo,
	})
	if err != nil {
		r.Errors = append(r.Errors, "resolve: "+err.Error())
		r.Status = doctorBroken
		// Continue with identity check even if project not resolvable
	}

	// 2. DB integrity
	if rctx != nil {
		db := dbent.InitDB(rctx.DBPath)
		defer joinClose(&err, db, "the DB")
		if err := dbent.ApplyPragmas(db); err != nil {
			r.Errors = append(r.Errors, dbOpenError("pragmas", err))
			r.Status = doctorBroken
		} else if err := dbent.QuickCheck(db); err != nil {
			r.Errors = append(r.Errors, dbOpenError("quick_check", err))
			r.Status = doctorBroken
		} else {
			r.DBOK = true
		}
		if r.DBOK {
			doctorMigrations(ctx, db, r)
			doctorSchemaDrift(ctx, db, r) // FORMAT_EVOLUTION 8.11
		}
		if r.DBOK && syncApplies(rctx) {
			doctorSync(ctx, rctx, db, r)
			// Live rows (LORE_TEAM_SPEC §9.4): refspec, hooks, rows only in
			// live, waiting landings, compaction (live_doctor.go).
			dataDir, _ := syncDataDir(rctx.DBPath)
			doctorLive(ctx, filepath.Dir(filepath.Dir(dataDir)), r)
			// DELETION 3.10: safety copies of rows purged elsewhere (doctor_local.go).
			doctorPurgedCopies(ctx, db, dataDir, r)
		}
		// After the sync checks, so they describe the state doctor found;
		// --fix writes are exported by the end-of-command sync pass.
		if r.DBOK {
			doctorSupersedes(ctx, f, dbent.New(db).Client(), r)
			// 6.7: a tool seen here without a render target (doctor_targets.go).
			doctorTargetSuggest(ctx, dbent.New(db).Client(), filepath.Dir(filepath.Dir(rctx.DBPath)), r)
		}
		r.WALSizeBytes = walSize(rctx.DBPath)
		if r.WALSizeBytes > 100*1024*1024 {
			r.Warnings = append(r.Warnings,
				fmt.Sprintf("WAL is large (%d bytes); consider `lore maintain`", r.WALSizeBytes))
			if r.Status == doctorHealthy {
				r.Status = doctorDegraded
			}
		}
		_ = sql.ErrNoRows // silence unused import warning
	}

	// 3. Identity
	resolved := identity.Resolve(identity.Inputs{})
	r.IdentityInfo = doctorIdentityInfo{
		Resolved: resolved.StableKey,
		Source:   resolved.Step.String(),
		Stable:   resolved.Step.Stable(),
	}
	if !resolved.Step.Stable() {
		r.Warnings = append(r.Warnings, "identity is ephemeral; future sessions won't link")
		if r.Status == doctorHealthy {
			r.Status = doctorDegraded
		}
	}

	// 4. PATH conflicts
	if conflicts := findPathConflicts(BinaryName); len(conflicts) > 1 {
		r.PathsConflict = conflicts
		r.Warnings = append(r.Warnings,
			fmt.Sprintf("multiple lore binaries on PATH: %s", strings.Join(conflicts, ", ")))
		if r.Status == doctorHealthy {
			r.Status = doctorDegraded
		}
	}

	// 5. Stores (LORE_TEAM_SPEC §6.6, §9.4): untrusted host, unpushed
	// store work, CLAUDE.local.md.
	doctorStores(ctx, r)
	// 6. Signals (F7 Debug): ref size, expired files, atomic push support
	// (coord_doctor.go, lore-signals).
	doctorSignals(ctx, r)
	// 7. Resurrected rows: a live row file whose id its folder lists in
	// _purged.json (an old clone or a hand copy brought it back). One
	// check only (resurrectedFindings, doctor_stores.go).
	if coreApp != nil {
		for _, msg := range resurrectedFindings(ctx) {
			r.Warnings = append(r.Warnings, msg)
			degrade(r)
		}
	}
	// 8. Cache files (doctor_cache.go): a damaged one is broken.
	repoTop := ""
	if rctx != nil {
		repoTop = filepath.Dir(filepath.Dir(rctx.DBPath))
	}
	home, _ := loreHome()
	doctorCaches(ctx, repoTop, home, r)
	doctorTimeOutbox(ctx, home, r)      // TIME 11.9: run time queued on this machine (doctor_local.go)
	doctorTeamVersions(ctx, repoTop, r) // OQ7 Team versions
	for _, q := range metaQuarantineLines(ctx) {
		// METADATA 3.13: quarantined metadata is counted here
		r.Warnings = append(r.Warnings, "quarantined metadata: "+q)
		degrade(r)
	}
	doctorSharedMachineIDs(ctx, r) // 6.8 one machine id on two hosts (doctor_machines.go)
	doctorVersionPin(r)            // FORMAT_EVOLUTION 8.8: a pin below the floor
	doctorCIPins(repoTop, r)       // FORMAT_EVOLUTION 4: a CI workflow pinning lore below the floor
	// 9. Repo addresses (PROV2, Q5).
	doctorRepoAddresses(ctx, r)
	// 10. Duplicate imports (PROV9).
	doctorDuplicateImports(ctx, r)
	doctorTaskTree(ctx, r)
	doctorWireChecks(ctx, f.fix, r)    // TASK 14.4/18.3, SPEC 6.2, DQ4, META 3.8 (wire/doctor_checks.go)
	doctorTrash(ctx, r)                // DELETION 3.8: rows past trash.retention_days, tombstones
	doctorUnclassifiedSettings(ctx, r) // CFG12: v0.1.17 settings left in place (doctor_local.go)
	// 10b. Secret rules that are off + leftover LORE_SYNC_ALLOW_SECRETS (D2/D3, doctor_secrets.go).
	doctorSecretRules(ctx, r)
	// 11. Context budget and launch set (CONTEXT_BUDGET 6.11, CQ7).
	if repoTop != "" {
		cw, cn := contextDoctor(ctx, repoTop)
		r.Notes = append(r.Notes, cn...)
		for _, w := range cw {
			r.Warnings = append(r.Warnings, w)
			degrade(r)
		}
	}

	r.OK = r.Status == doctorHealthy
	return r, nil
}

// doctorSync fills the [sync] section and its warnings (E1, E8, E12, E30,
// E38). Each finding degrades health; none is fatal because the DB itself
// still works.
func doctorSync(ctx context.Context, rctx *projresolve.Context, db *sql.DB, r *doctorReport) {
	dataDir, _ := syncDataDir(rctx.DBPath)
	root := filepath.Dir(filepath.Dir(dataDir))
	st, err := lsync.ReadStatus(ctx, db, dataDir)
	if err != nil {
		r.Warnings = append(r.Warnings, "sync status: "+err.Error())
		degrade(r)
		return
	}
	info := &doctorSyncInfo{Status: st, ProjectFiles: lsync.CountRowFiles(dataDir, lsync.ProjectsTable)}
	r.Sync = info
	warn := func(msg string) {
		r.Warnings = append(r.Warnings, msg)
		degrade(r)
	}
	if !st.DataDirPresent && st.Baselined {
		warn(".lore/data is missing; run `lore sync export --all` to recreate it")
	}
	if st.PendingExports > 0 {
		warn(fmt.Sprintf("%d lore change(s) not yet written to .lore/data (run `lore sync`)", st.PendingExports))
	}
	if st.OpenConflicts > 0 {
		warn(fmt.Sprintf("%d sync conflict(s) kept aside; see `lore sync conflicts`", st.OpenConflicts))
	}
	// Validate files directly as well: errors recorded by sync passes only
	// cover files a pass has touched, and doctor must see a broken file even
	// when no command ran since it broke.
	// From the recorded errors keep only refused exports (secrets): file
	// shape errors are re-checked live, so a file fixed by hand since the
	// last pass is not reported stale.
	seen := map[string]bool{}
	var fileErrs []lsync.FileError
	for _, e := range st.Errors {
		if e.Kind == lsync.ErrKindSecret && refusedRowStillExists(ctx, db, e.Path) {
			fileErrs = append(fileErrs, e)
		}
	}
	if live, err := lsync.CheckFiles(dataDir); err == nil {
		fileErrs = append(fileErrs, live...)
	}
	for _, e := range fileErrs {
		if seen[e.Path] {
			continue
		}
		seen[e.Path] = true
		info.FileErrors = append(info.FileErrors, e)
		warn(fmt.Sprintf("lore file %s: %s (%s)", e.Path, e.Detail, e.Kind))
	}
	if refs, err := lsync.DanglingReferences(ctx, db); err == nil && len(refs) > 0 {
		info.DanglingRefs = len(refs)
		warn(fmt.Sprintf("%d reference(s) to rows that do not exist (normal after merging diverged branches; e.g. %s.%s → %s)",
			len(refs), refs[0].Table, refs[0].Column, refs[0].Target))
	}
	if groups, err := lsync.FindDuplicates(ctx, db); err == nil && len(groups) > 0 {
		info.DuplicateGroups = len(groups)
		warn(fmt.Sprintf("%d group(s) of duplicate rows; see `lore sync dupes`", len(groups)))
	}
	if info.ProjectFiles > 1 {
		warn(fmt.Sprintf("%d project files in .lore/data; run `lore sync fix-projects`", info.ProjectFiles))
	}
	if _, gerr := gitsetup.Open(ctx, root); gerr != nil {
		return // not a git checkout: the git checks below do not apply
	}
	info.InGitRepo = true
	if ign, err := gitsetup.IsIgnored(ctx, root, filepath.Join(projresolve.MarkerDir, lsync.DataDirName, lsync.MetaFileName)); err == nil && ign {
		info.DataIgnored = true
		warn(".lore/data is gitignored, so lore knowledge is never shared; remove the ignore rule")
	}
	if v, ok, err := gitsetup.ConfigGet(ctx, root, "merge."+mergeDriverLore+".driver"); err == nil && ok && v != "" {
		info.MergeDriverOK = true
	} else {
		warn("lore merge driver not configured in this clone (any lore command or `lore sync install-git` installs it)")
	}
	// Uncommitted lore files are normal work in progress (like uncommitted
	// code): reported as information, never a degradation.
	if paths, err := gitsetup.Uncommitted(ctx, root, filepath.Join(projresolve.MarkerDir, lsync.DataDirName)); err == nil {
		info.Uncommitted = len(paths)
	}
}

// doctorMigrations reports the schema_migrations replay log: an
// interrupted migration makes the DB "broken" (its shape is unknown); a
// definition-hash mismatch "degraded" (setup needed, or a tampered log).
func doctorMigrations(ctx context.Context, db *sql.DB, r *doctorReport) {
	findings, err := migrationlog.Check(ctx, db)
	if err != nil {
		r.Warnings = append(r.Warnings, "migration log: "+err.Error())
		degrade(r)
		return
	}
	for _, f := range findings {
		switch {
		case f.Fatal:
			r.Errors = append(r.Errors, f.Message)
			r.Status = doctorBroken
		case strings.HasPrefix(f.Message, "no schema migration recorded"):
			// DBs created before the log existed: informational only.
			r.Notes = append(r.Notes, f.Message)
		default:
			r.Warnings = append(r.Warnings, f.Message)
			degrade(r)
		}
	}
}

// sqliteCorruptionMarkers are fragments of SQLite errors that mean the file
// itself is damaged (as opposed to locked, missing or read-only).
var sqliteCorruptionMarkers = []string{"malformed", "not a database", "corrupt", "quick_check failed"}

// dbOpenError renders a DB-open failure; a damaged file is named as such
// with the recovery command, so neither a human nor a script has to decode
// SQLite's wording.
func dbOpenError(step string, err error) string {
	msg := err.Error()
	for _, m := range sqliteCorruptionMarkers {
		if strings.Contains(strings.ToLower(msg), m) {
			return fmt.Sprintf("%s: DB file is corrupt (%s: %s); recover with `lore repair --tier=2 --confirm` (latest backup) — shared rows also rebuild from .lore/data", errcodes.DBCorrupt, step, msg)
		}
	}
	return step + ": " + msg
}

func degrade(r *doctorReport) {
	if r.Status == doctorHealthy {
		r.Status = doctorDegraded
	}
}

func printDoctor(r *doctorReport) {
	statusStyle := style.Success
	switch r.Status {
	case doctorDegraded:
		statusStyle = style.Warn
	case doctorBroken:
		statusStyle = style.Error
	}
	fmt.Printf("Status: %s\n", statusStyle(strings.ToUpper(r.Status)))
	fmt.Printf("Binary: %s\n", r.BinaryVersion)
	fmt.Println()
	fmt.Printf("[db]\n")
	fmt.Printf("  ok:       %v\n", r.DBOK)
	fmt.Printf("  wal_size: %d bytes\n", r.WALSizeBytes)
	fmt.Println()
	if r.Sync != nil {
		fmt.Printf("[sync]\n")
		fmt.Printf("  baselined:        %v\n", r.Sync.Baselined)
		fmt.Printf("  pending exports:  %d\n", r.Sync.PendingExports)
		fmt.Printf("  open conflicts:   %d\n", r.Sync.OpenConflicts)
		fmt.Printf("  file errors:      %d\n", len(r.Sync.FileErrors))
		fmt.Printf("  merge driver:     %v\n", r.Sync.MergeDriverOK)
		fmt.Printf("  uncommitted:      %d\n", r.Sync.Uncommitted)
		fmt.Println()
	}
	fmt.Printf("[identity]\n")
	fmt.Printf("  resolved: %s\n", r.IdentityInfo.Resolved)
	fmt.Printf("  source:   %s\n", r.IdentityInfo.Source)
	stable := "yes"
	if !r.IdentityInfo.Stable {
		stable = style.Warn("NO (ephemeral)")
	}
	fmt.Printf("  stable:   %s\n", stable)
	if len(r.Errors) > 0 {
		fmt.Println()
		fmt.Println(style.Error("Errors:"))
		for _, e := range r.Errors {
			fmt.Println("  - " + e)
		}
	}
	if len(r.Notes) > 0 {
		fmt.Println()
		fmt.Println(style.Muted("Notes:"))
		for _, n := range r.Notes {
			fmt.Println("  - " + n)
		}
	}
	if len(r.Warnings) > 0 {
		fmt.Println()
		fmt.Println(style.Warn("Warnings:"))
		for _, w := range r.Warnings {
			fmt.Println("  - " + w)
		}
	}
}

func walSize(dbPath string) int64 {
	info, err := os.Stat(dbPath + "-wal")
	if err != nil {
		return 0
	}
	return info.Size()
}

func findPathConflicts(name string) []string {
	cmd := newProgramCmd(context.Background(), "which", "-a", name)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			paths = append(paths, line)
		}
	}
	// Dedup via filepath.Clean
	seen := map[string]bool{}
	var unique []string
	for _, p := range paths {
		c := filepath.Clean(p)
		if !seen[c] {
			seen[c] = true
			unique = append(unique, c)
		}
	}
	return unique
}

// refusedRowStillExists reports whether the row behind a recorded
// secret-refusal ("<table>/<id>.json") is still in the database. A row that
// was deleted since its export was refused can never be exported, so its
// recorded refusal is stale and doctor must not keep warning about it
// (BUG-OPS1). Anything doctor cannot decide (unknown table, malformed path,
// a failing query) counts as still there: a stale warning is better than a
// hidden secret.
func refusedRowStillExists(ctx context.Context, db *sql.DB, rel string) bool {
	table, file, ok := strings.Cut(rel, "/")
	id, isJSON := strings.CutSuffix(file, dataRowFileExt)
	if !ok || !isJSON || id == "" || strings.Contains(id, "/") {
		return true
	}
	reg, _ := lsync.NewRegistry()
	if _, synced := reg.Table(table); !synced {
		return true
	}
	// table is a registry name (a fixed schema identifier), never user text.
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM "`+table+`" WHERE id = ?`, id).Scan(&n); err != nil {
		return true
	}
	return n > 0
}

// dataRowFileExt is the extension of one row's file in .lore/data
// ("<table>/<id>.json").
const dataRowFileExt = ".json"

// doctorRepoAddresses is the "Repo addresses" check (PROVENANCE_DESIGN
// 10.3; read only, it mints and records nothing). PROV2: an unconfirmed
// address is listed as a note with the command that confirms it (it is
// normal until someone answers, so it does not degrade health). Q5: a
// clone whose repo_id disagrees with its remote (its remote is a confirmed
// address of another repo, typically a fork rebased onto its upstream,
// whose _meta.json then came from the upstream) is a warning: rows it
// writes would carry the wrong repo id.
func doctorRepoAddresses(ctx context.Context, r *doctorReport) {
	if coreApp == nil {
		return
	}
	rep, err := coreApp.RepoAddressStatus(ctx)
	if err != nil || rep.RepoID == "" {
		return
	}
	r.RepoAddresses = &rep
	for _, n := range rep.Notices {
		line := "repo address " + n.Key + " is unconfirmed (lore repo set-url " + n.Key + ", add --mirror for another address"
		if !n.CaseOnly {
			line += ", or lore repo fork in a fork"
		}
		r.Notes = append(r.Notes, line+")")
	}
	for _, w := range rep.Warnings {
		r.Warnings = append(r.Warnings, "a credential is in git remote "+w.Remote+"; remove it: git remote set-url "+w.Remote+" "+w.Stored)
		degrade(r)
	}
	if rep.Mismatch != "" {
		r.Warnings = append(r.Warnings, rep.Mismatch+"; if this clone is the fork, restore the fork's .lore/data/_meta.json (git checkout <fork commit> -- .lore/data/_meta.json) or run `lore repo fork`")
		degrade(r)
	}
}

// doctorDuplicateImports is the PROV9 check duplicate_import: two live tasks
// imported under one external id (two clones importing the same source
// before either synced, or the same file imported twice). PROV9: reported,
// never merged on their own, because the copies may have been edited
// differently; until a merge command exists (Q-R3-1) the fix keeps the
// oldest and deletes the newer copies.
func doctorDuplicateImports(ctx context.Context, r *doctorReport) {
	if coreApp == nil {
		return
	}
	dups, err := opstask.FindDuplicateImports(ctx, coreApp)
	if err != nil {
		r.Warnings = append(r.Warnings, "duplicate_import: the tasks could not be read: "+err.Error())
		degrade(r)
		return
	}
	for _, d := range dups {
		r.Warnings = append(r.Warnings, "duplicate_import "+d.Source+" "+d.ExtID+": "+strings.Join(d.IDs, ", ")+
			"; keep the oldest: "+strings.Join(d.FixCommand(), "; "))
		degrade(r)
	}
}

// doctorTaskTree is the sub-task tree check (TASK_SYSTEM_DESIGN 6.13: a
// merged cycle is shown in doctor): cycles, orphans, open children of closed
// parents, depth over tasks.max_depth. Shared sibling ranks are info only
// ("none needed") and skipped. Repair: lore task tree check --fix.
func doctorTaskTree(ctx context.Context, r *doctorReport) {
	if coreApp == nil {
		return
	}
	found, err := opstask.CheckTree(ctx, coreApp)
	if err != nil {
		r.Warnings = append(r.Warnings, "task_tree: the tasks could not be read: "+err.Error())
		degrade(r)
		return
	}
	for _, f := range found {
		if f.Kind == opstask.FindSharedRank {
			continue
		}
		line := "task_tree " + string(f.Kind) + ": " + f.Message
		if f.Fix != "" {
			line += "; fix: " + f.Fix
		}
		r.Warnings = append(r.Warnings, line)
		degrade(r)
	}
}

// doctorTrash is the trash check (DELETION 3.8, build step 9; opstrash.Doctor):
// rows past trash.retention_days warn and name the purge command (never
// purged here, DQ3); the trash and tombstone counts are a note.
func doctorTrash(ctx context.Context, r *doctorReport) {
	if coreApp == nil {
		return
	}
	warnings, notes, err := opstrash.Doctor(ctx, coreApp)
	if err != nil {
		r.Warnings = append(r.Warnings, "trash: the trash could not be read: "+err.Error())
		degrade(r)
		return
	}
	for _, w := range warnings {
		r.Warnings = append(r.Warnings, w)
		degrade(r)
	}
	r.Notes = append(r.Notes, notes...)
}

// doctorWireChecks runs the five checks of wire/doctor_checks.go (task lead
// missing from assignees, unknown tasks.triage_owner, store lock holder, an
// edit newer than a purge, internal turned off without the record). A
// warning degrades doctor's status, a note does not; fix is doctor --fix,
// which writes a missing lead back (P3-DOC-2). A check that fails to run is
// one warning naming the check, and the others still run.
func doctorWireChecks(ctx context.Context, fix bool, r *doctorReport) {
	if coreApp == nil {
		return
	}
	checks := []struct {
		name string
		run  func(context.Context) ([]wire.DoctorFinding, error)
	}{
		{string(wire.DoctorCheckTaskLead), func(c context.Context) ([]wire.DoctorFinding, error) { return coreApp.DoctorTaskLeads(c, fix) }},
		{string(wire.DoctorCheckTriageOwner), coreApp.DoctorTriageOwner},
		{string(wire.DoctorCheckStoreLock), coreApp.DoctorStoreLocks},
		{string(wire.DoctorCheckEditAfterPurge), coreApp.DoctorEditsAfterPurge},
		{string(wire.DoctorCheckInternalOff), coreApp.DoctorInternalOff},
	}
	for _, c := range checks {
		found, err := c.run(ctx)
		if err != nil {
			r.Warnings = append(r.Warnings, c.name+": the check could not run: "+err.Error())
			degrade(r)
			continue
		}
		for _, f := range found {
			if f.Level == wire.DoctorNote || f.Fixed {
				r.Notes = append(r.Notes, f.Line())
				continue
			}
			r.Warnings = append(r.Warnings, f.Line())
			degrade(r)
		}
	}
}
