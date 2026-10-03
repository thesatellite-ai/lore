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
	"os/exec"
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

	"github.com/spf13/cobra"
)

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
			switch report.Status {
			case doctorBroken:
				os.Exit(2)
			case doctorDegraded:
				os.Exit(1)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f.commonFlags)
	cmd.Flags().BoolVar(&f.jsonOutput, constants.FlagJSON, false, "JSON output (stable schema)")
	return cmd
}

func runDoctor(ctx context.Context, f *doctorFlags) (*doctorReport, error) {
	r := &doctorReport{
		SchemaVersion: 1,
		BinaryVersion: version,
		Status:        doctorHealthy,
	}

	// 1. Resolve project context (errors don't fail the doctor — they go in
	// the report so JSON consumers see structured status)
	rctx, err := projresolve.Resolve(projresolve.Inputs{
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
		defer db.Close()
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
		}
		if r.DBOK && syncApplies(rctx) {
			doctorSync(ctx, rctx, db, r)
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
	if conflicts := findPathConflicts("aicoder"); len(conflicts) > 1 {
		r.PathsConflict = conflicts
		r.Warnings = append(r.Warnings,
			fmt.Sprintf("multiple lore binaries on PATH: %s", strings.Join(conflicts, ", ")))
		if r.Status == doctorHealthy {
			r.Status = doctorDegraded
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
		if e.Kind == lsync.ErrKindSecret {
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
	cmd := exec.Command("which", "-a", name)
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
