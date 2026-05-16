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
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
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
	Errors        []string           `json:"errors,omitempty"`
	BinaryVersion string             `json:"binary_version"`
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
			case "broken":
				os.Exit(2)
			case "degraded":
				os.Exit(1)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f.commonFlags)
	cmd.Flags().BoolVar(&f.jsonOutput, constants.FlagJSON, false, "JSON output (stable schema)")
	return cmd
}

func runDoctor(_ context.Context, f *doctorFlags) (*doctorReport, error) {
	r := &doctorReport{
		SchemaVersion: 1,
		BinaryVersion: version,
		Status:        "healthy",
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
		r.Status = "broken"
		// Continue with identity check even if project not resolvable
	}

	// 2. DB integrity
	if rctx != nil {
		db := dbent.InitDB(rctx.DBPath)
		defer db.Close()
		if err := dbent.ApplyPragmas(db); err != nil {
			r.Errors = append(r.Errors, "pragmas: "+err.Error())
			r.Status = "broken"
		} else if err := dbent.QuickCheck(db); err != nil {
			r.Errors = append(r.Errors, "quick_check: "+err.Error())
			r.Status = "broken"
		} else {
			r.DBOK = true
		}
		r.WALSizeBytes = walSize(rctx.DBPath)
		if r.WALSizeBytes > 100*1024*1024 {
			r.Warnings = append(r.Warnings,
				fmt.Sprintf("WAL is large (%d bytes); consider `lore maintain`", r.WALSizeBytes))
			if r.Status == "healthy" {
				r.Status = "degraded"
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
		if r.Status == "healthy" {
			r.Status = "degraded"
		}
	}

	// 4. PATH conflicts
	if conflicts := findPathConflicts("aicoder"); len(conflicts) > 1 {
		r.PathsConflict = conflicts
		r.Warnings = append(r.Warnings,
			fmt.Sprintf("multiple lore binaries on PATH: %s", strings.Join(conflicts, ", ")))
		if r.Status == "healthy" {
			r.Status = "degraded"
		}
	}

	r.OK = r.Status == "healthy"
	return r, nil
}

func printDoctor(r *doctorReport) {
	statusStyle := style.Success
	switch r.Status {
	case "degraded":
		statusStyle = style.Warn
	case "broken":
		statusStyle = style.Error
	}
	fmt.Printf("Status: %s\n", statusStyle(strings.ToUpper(r.Status)))
	fmt.Printf("Binary: %s\n", r.BinaryVersion)
	fmt.Println()
	fmt.Printf("[db]\n")
	fmt.Printf("  ok:       %v\n", r.DBOK)
	fmt.Printf("  wal_size: %d bytes\n", r.WALSizeBytes)
	fmt.Println()
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
