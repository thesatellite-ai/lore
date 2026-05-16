// backup.go — `lore backup` / `lore restore` (S2.7)
//
// VACUUM INTO is the canonical SQLite online backup mechanism (R23 #13)
// Safe on a live DB; produces a self-contained .sqlite file
//
// macOS quarantine xattr stripped on output (R16 #17)
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"saas/pkg/constants"
	"time"

	"dbent"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

func newBackupCommand() *cobra.Command {
	var f commonFlags
	var out string
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Create an online backup of the project DB",
		Long: `Creates a SQLite-online backup via VACUUM INTO. Safe on a live DB

Default output is .lore/backups/<ts>.sqlite

Strips macOS com.apple.quarantine xattr from the output so it can be opened
on a fresh machine without right-click-Open friction.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rctx, err := projresolve.Resolve(projresolve.Inputs{
				FlagDB: f.flagDB, FlagProject: f.flagProject,
			})
			if err != nil {
				return mapResolveError(err)
			}
			db := dbent.InitDB(rctx.DBPath)
			defer db.Close()

			if out == "" {
				dir := filepath.Join(rctx.ProjectRoot, projresolve.MarkerDir, projresolve.BackupDir)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return errcodes.New(errcodes.Internal, "mkdir backups").WithCause(err)
				}
				out = filepath.Join(dir, fmt.Sprintf("%s.sqlite", time.Now().UTC().Format("20060102-150405")))
			}

			// VACUUM INTO requires absolute path
			abs, _ := filepath.Abs(out)
			if _, err := db.ExecContext(cmd.Context(), "VACUUM INTO ?", abs); err != nil {
				return errcodes.New(errcodes.Internal, "VACUUM INTO").WithCause(err)
			}

			// Strip macOS quarantine xattr (R16 #17). Best-effort; ignore errors
			if runtime.GOOS == "darwin" {
				_ = exec.Command("xattr", "-d", "com.apple.quarantine", abs).Run()
			}

			info, _ := os.Stat(abs)
			fmt.Printf("%s %s (%d bytes)\n", style.Success("✓"), abs, info.Size())
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&out, "out", "", "output path (default: .lore/backups/<ts>.sqlite)")
	return cmd
}

func newRestoreCommand() *cobra.Command {
	var f commonFlags
	var confirm bool
	cmd := &cobra.Command{
		Use:   "restore <backup-path>",
		Short: "Restore the project DB from a backup file",
		Long: `Replaces the current .lore/lore.db with the contents of the
specified backup file. Requires --confirm to prevent accidents.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !confirm {
				return errcodes.New(errcodes.InvalidInput,
					"refusing to overwrite DB without --confirm")
			}
			rctx, err := projresolve.Resolve(projresolve.Inputs{
				FlagDB: f.flagDB, FlagProject: f.flagProject,
			})
			if err != nil {
				// Restore is special: the current DB may be GONE (user wiped
				// it before calling restore). If .lore/ marker dir exists
				// but no db/toml inside, fall back to the default Mode A path
				// so we can write the backup into place
				cwd, _ := os.Getwd()
				markerDB := cwd + "/" + projresolve.MarkerDir + "/" + projresolve.ModeAFile
				if _, statErr := os.Stat(cwd + "/" + projresolve.MarkerDir); statErr == nil {
					rctx = &projresolve.Context{
						Mode:        projresolve.ModeA,
						DBPath:      markerDB,
						ProjectRoot: cwd,
					}
				} else {
					return mapResolveError(err)
				}
			}

			src := args[0]
			if _, err := os.Stat(src); err != nil {
				return errcodes.New(errcodes.NotFound,
					fmt.Sprintf("backup file %q not found", src)).WithCause(err)
			}

			// Move current DB aside (forensics)
			dst := rctx.DBPath
			brokenName := dst + ".broken." + time.Now().UTC().Format("20060102-150405")
			if _, err := os.Stat(dst); err == nil {
				if err := os.Rename(dst, brokenName); err != nil {
					return errcodes.New(errcodes.Internal, "preserve old DB").WithCause(err)
				}
			}
			// Also move sidecars
			for _, suffix := range []string{"-wal", "-shm"} {
				_ = os.Remove(dst + suffix)
			}

			// Copy src → dst
			if err := copyFile(src, dst); err != nil {
				// Best-effort restore previous on error
				_ = os.Rename(brokenName, dst)
				return errcodes.New(errcodes.Internal, "copy backup").WithCause(err)
			}

			fmt.Printf("%s restored from %s\n", style.Success("✓"), src)
			fmt.Printf("  previous DB preserved at: %s\n", brokenName)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&confirm, constants.FlagConfirm, false, "required to overwrite the current DB")
	return cmd
}

func copyFile(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, in, 0o644)
}

// repair.go — `lore repair` (S2.6)
//
// 3-tier DR (R16 #8, R23 #44):
//   tier 1: SQLite .recover SQL (rebuild from WAL fragments)
//   tier 2: restore from latest .lore/backups/<ts>.sqlite
//   tier 3: bootstrap empty DB (last resort)
//
// `lore repair` defaults to tier 2 (most reliable when backup exists)

func newRepairCommand() *cobra.Command {
	var f commonFlags
	var tier int
	var confirm bool
	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Recover from a corrupted DB",
		Long: `repair attempts disaster recovery. Three tiers:

  --tier=1   SQLite .recover (rebuild from WAL fragments)
  --tier=2   restore from latest .lore/backups/*.sqlite (recommended)
  --tier=3   bootstrap empty DB (last resort)

Requires --confirm to make destructive changes.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !confirm {
				return errcodes.New(errcodes.InvalidInput,
					"repair is destructive; pass --confirm to proceed")
			}
			rctx, err := projresolve.Resolve(projresolve.Inputs{
				FlagDB: f.flagDB, FlagProject: f.flagProject,
			})
			if err != nil {
				return mapResolveError(err)
			}

			switch tier {
			case 2:
				return repairTier2(cmd.Context(), rctx)
			case 3:
				return repairTier3(rctx)
			default:
				return errcodes.New(errcodes.NotImplemented,
					fmt.Sprintf("repair tier %d not implemented in v0.1", tier)).
					WithHint("use --tier=2 (recommended) or --tier=3")
			}
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().IntVar(&tier, "tier", 2, "DR tier: 1 .recover | 2 restore-from-backup | 3 bootstrap-empty")
	cmd.Flags().BoolVar(&confirm, constants.FlagConfirm, false, "required to proceed")
	return cmd
}

func repairTier2(ctx context.Context, rctx *projresolve.Context) error {
	// Find latest backup
	dir := filepath.Join(rctx.ProjectRoot, projresolve.MarkerDir, projresolve.BackupDir)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return errcodes.New(errcodes.NotFound,
			"no backups found at "+dir).
			WithHint("run `lore backup` regularly OR fall back to --tier=3")
	}
	var newest os.FileInfo
	var newestPath string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".sqlite" {
			continue
		}
		info, _ := e.Info()
		if newest == nil || info.ModTime().After(newest.ModTime()) {
			newest = info
			newestPath = filepath.Join(dir, e.Name())
		}
	}
	if newest == nil {
		return errcodes.New(errcodes.NotFound, "no .sqlite files in backups dir")
	}

	brokenName := rctx.DBPath + ".broken." + time.Now().UTC().Format("20060102-150405")
	if _, err := os.Stat(rctx.DBPath); err == nil {
		_ = os.Rename(rctx.DBPath, brokenName)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(rctx.DBPath + suffix)
	}
	if err := copyFile(newestPath, rctx.DBPath); err != nil {
		_ = os.Rename(brokenName, rctx.DBPath)
		return errcodes.New(errcodes.Internal, "restore from backup").WithCause(err)
	}

	// Verify by opening + quick_check
	db := dbent.InitDB(rctx.DBPath)
	defer db.Close()
	if err := dbent.ApplyPragmas(db); err != nil {
		return errcodes.New(errcodes.Internal, "post-repair pragmas").WithCause(err)
	}
	if err := dbent.QuickCheck(db); err != nil {
		return errcodes.New(errcodes.DBCorrupt,
			"post-repair quick_check failed").WithCause(err)
	}

	fmt.Printf("%s restored from %s\n", style.Success("✓"), newestPath)
	fmt.Printf("  previous (broken) DB at: %s\n", brokenName)
	_ = ctx
	return nil
}

func repairTier3(rctx *projresolve.Context) error {
	brokenName := rctx.DBPath + ".broken." + time.Now().UTC().Format("20060102-150405")
	if _, err := os.Stat(rctx.DBPath); err == nil {
		_ = os.Rename(rctx.DBPath, brokenName)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(rctx.DBPath + suffix)
	}

	// Re-init via the same path as `lore init` would
	db := dbent.InitDB(rctx.DBPath)
	defer db.Close()
	if err := dbent.ApplyPragmas(db); err != nil {
		return errcodes.New(errcodes.Internal, "pragmas").WithCause(err)
	}
	// Note: caller still needs to run schema migration. We don't run it here
	// because tier-3 is a destructive last-resort and we want the user to
	// re-init properly via `lore init` after this completes

	fmt.Printf("%s empty DB created at %s\n", style.Warn("⚠"), rctx.DBPath)
	fmt.Printf("  previous DB at: %s\n", brokenName)
	fmt.Println("  Next: run `lore init` to apply schema, then `lore learn-from docs` to repopulate")
	return nil
}
