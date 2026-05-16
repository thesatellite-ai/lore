// setup_cmd.go — `lore setup` + the per-open staleness check
//
// Migration model:
//   - On install/upgrade, user runs `lore setup` (or `init` for
//     fresh projects, which calls setup automatically)
//   - Per-command path is fast: one SELECT against a single fingerprint
//     row. If it matches the binary's compiled-in fingerprint, do nothing
//     If it doesn't, print a one-line "stale; run setup" hint and continue
//   - No per-command schema work, no 23-entity loops
//
// What `setup` does:
//   - fts5.EnsureRegistrySchema — create+backfill all per-entity FTS tables
//   - (Future: any other heavy migrations land here)
//
// Fingerprint:
//
//	Stored in dbconfig under key 'fts_registry_fingerprint'. Compared
//	to fts5.RegistryFingerprint() at runtime
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"

	"dbent"
	"dbent/pkg/dbent_migrate"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/fts5"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

const setupFingerprintKey = "fts_registry_fingerprint"

// computeRegistryFingerprint hashes the current Registry so we can tell
// whether a stored DB was set up against this binary's registry shape
func computeRegistryFingerprint() string {
	h := sha256.New()
	for _, c := range fts5.Registry {
		fmt.Fprintf(h, "%s|%s|%v|%v|%d\n",
			c.Entity, c.Table, c.Columns, c.Weights, c.SchemaVersion)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// warnIfSetupStale runs once per DB open. One SELECT, no schema work
// Suppressed if LORE_NO_STALE_WARN=1 (lets CI runs stay quiet)
func warnIfSetupStale(ctx context.Context, db *sql.DB) {
	if os.Getenv("LORE_NO_STALE_WARN") == "1" {
		return
	}
	var stored string
	err := db.QueryRowContext(ctx,
		`SELECT value FROM config WHERE key = ?`, setupFingerprintKey).Scan(&stored)
	if err != nil {
		// Two cases both mean "setup never ran or DB pre-dates the feature":
		//   - ErrNoRows: table exists but no fingerprint row
		//   - other err: config table likely doesn't exist (very old DB)
		// Either way, prompt the user to run setup. Silent if LORE_NO_STALE_WARN=1
		fmt.Fprintln(os.Stderr, style.Hint("hint: run `lore setup` to enable FTS5 search"))
		return
	}
	if stored != computeRegistryFingerprint() {
		fmt.Fprintln(os.Stderr, style.Warn("⚠ search index is out of date"))
		fmt.Fprintln(os.Stderr, style.Hint("  run `lore setup` to rebuild"))
	}
}

func newSetupCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Run one-time migrations after install/upgrade (FTS index + future migrations)",
		Long: `Run after upgrading lore. Creates/rebuilds the FTS5 search index
for every registered entity and stamps the current registry fingerprint so
the per-command staleness warning goes away

Re-run any time after editing search-related schema (changing Registry
column lists, weights, SchemaVersion). Idempotent

Fresh installs: ` + "`lore init`" + ` calls setup automatically; you don't
need to run this manually unless upgrading.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			// Resolve project, but close ent client immediately. We manage our
			// own raw *sql.DB lifecycle so we can survive Migrate() closing
			// the connection it was handed
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			dbPath := rctx.DBPath
			client.Close()
			return runSetup(cmd.Context(), dbPath)
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

// runSetup runs the heavy schema work against a DB path (not a live client),
// because dbent_migrate.Migrate closes the *sql.DB it's handed via the ent
// driver's Close() chain. Each step opens its own connection
//
// Steps:
//  1. ent schema auto-migration (adds new columns / tables defined since
//     the DB was last set up). Migrate opens + closes its own connection
//  2. FTS5 base schema (memory_fts legacy table). Fresh conn
//  3. FTS5 per-entity registry (23 <kind>_fts tables + triggers + backfill)
//  4. Stamp the registry fingerprint so per-command warnings stop
func runSetup(ctx context.Context, dbPath string) error {
	// 1. ent migration. Open + immediately hand to Migrate (which closes)
	fmt.Println(style.Hint("→ running ent schema migration..."))
	{
		db := dbent.InitDB(dbPath)
		if err := dbent_migrate.Migrate(ctx, db); err != nil {
			return errcodes.New(errcodes.Internal, "ent migrate").WithCause(err)
		}
		// Migrate already closed db via the ent driver; don't double-close
	}

	// 2-4. FTS5 + fingerprint stamp on a fresh connection
	db := dbent.InitDB(dbPath)
	defer db.Close()
	if err := dbent.ApplyPragmas(db); err != nil {
		return errcodes.New(errcodes.Internal, "apply pragmas").WithCause(err)
	}
	if !fts5.Available(ctx, db) {
		fmt.Fprintln(os.Stderr, style.Warn(
			"⚠ FTS5 not compiled into this binary — search is degraded"))
		fmt.Fprintln(os.Stderr, style.Hint(
			"  rebuild with `task aicoder:build` (the sqlite_fts5 tag is set there)"))
		return nil
	}
	fmt.Println(style.Hint("→ building FTS5 search index..."))
	if err := fts5.EnsureSchema(ctx, db); err != nil {
		return errcodes.New(errcodes.Internal, "fts5 schema").WithCause(err)
	}
	if err := fts5.EnsureRegistrySchema(ctx, db); err != nil {
		return errcodes.New(errcodes.Internal, "fts5 registry").WithCause(err)
	}
	fp := computeRegistryFingerprint()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO config(id, key, value, created_at, updated_at, setting_updated_at)
		VALUES ('cfg_' || lower(hex(randomblob(16))), ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP, setting_updated_at = CURRENT_TIMESTAMP
	`, setupFingerprintKey, fp); err != nil {
		return errcodes.New(errcodes.Internal, "stamp fingerprint").WithCause(err)
	}
	fmt.Printf("%s setup complete (registry fingerprint: %s)\n", style.Success("✓"), fp)
	return nil
}
