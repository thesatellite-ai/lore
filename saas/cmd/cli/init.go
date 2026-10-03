// aicoder_init.go — implements `lore init`
//
// Story S2.1 (PLAN.md). Creates a fresh Mode A project at cwd:
//   - Refuses if .lore/lore.db or .lore/lore.toml exists (E_ALREADY_INITIALIZED)
//   - Auto-suggests project name from git remote
//   - Initializes SQLite DB with schema migration
//   - Seeds DBConfig singletons (schema_version, db_uuid, db_created_at)
//   - Auto-adds .lore/lore.db to .gitignore
//   - INSERTs initial Project row
//   - Resolves identity via 8-step chain and creates initial Actor
//
// Catches: R21 #61 (first-run wizard), R22 #21-23, R23 #34, R26 ship-gate item
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"saas/pkg/constants"
	"strings"
	"time"

	"dbent"
	"dbent/gen/ent"
	entActor "dbent/gen/ent/actor"
	"dbent/pkg/dbent_migrate"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/fts5"
	"saas/pkg/aicoder/gitsetup"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/migrationlog"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"

	"github.com/spf13/cobra"
)

var (
	initFlagName           string
	initFlagNonInteractive bool
)

func newInitCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init [path]",
		Short: "Initialize a new lore project at the given path (default cwd)",
		Long: `init creates a fresh lore Mode A project

It will:
  • create .lore/lore.db with the full schema applied
  • seed singleton config rows (schema_version, db_uuid, db_created_at)
  • auto-add .lore/lore.db to your .gitignore
  • register the initial Project row using the auto-detected name from
    your git remote (or the directory basename if no remote)

Refuses if a project already exists (.lore/lore.db or .lore/lore.toml).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) == 1 {
				path = args[0]
			}
			abs, err := filepath.Abs(path)
			if err != nil {
				return errcodes.New(errcodes.BadPath, fmt.Sprintf("resolve %q", path)).WithCause(err)
			}
			return runInit(cmd.Context(), abs)
		},
	}
	cmd.Flags().StringVar(&initFlagName, constants.FlagName, "", "explicit project name (default: from git remote OR dir basename)")
	cmd.Flags().BoolVar(&initFlagNonInteractive, constants.FlagNonInteractive, false, "skip prompts and use auto-detected defaults")
	return cmd
}

func runInit(ctx context.Context, projectRoot string) error {
	// 1. A checkout that already carries shared lore data (.lore/data from a
	// teammate) but no local DB: adopt it instead of minting a second
	// project (E1).
	markerDir := filepath.Join(projectRoot, projresolve.MarkerDir)
	if err := checkNetworkFS(projectRoot); err != nil {
		return err
	}
	if needsMaterialize(projectRoot) {
		if err := materializeFromData(ctx, projectRoot); err != nil {
			return errcodes.New(errcodes.Internal, "build lore.db from .lore/data").WithCause(err)
		}
		meta, err := lsync.ReadProjectMeta(filepath.Join(markerDir, lsync.DataDirName))
		if err != nil {
			return errcodes.New(errcodes.Internal, "read .lore/data/_meta.json").WithCause(err)
		}
		fmt.Printf("%s adopted the shared lore project at %s\n", style.Success("✓"), projectRoot)
		fmt.Printf("    project_id: %s\n", style.Code(meta.ProjectID))
		return nil
	}

	// Refuse when a project already exists here. A .lore/ holding only
	// leftovers (an empty state/ dir from a lock, backups) is not a project
	// and must not block init.
	for _, f := range []string{projresolve.ModeAFile, projresolve.ModeBFile} {
		if _, err := os.Lstat(filepath.Join(markerDir, f)); err == nil {
			return errcodes.New(errcodes.AlreadyInitialized,
				fmt.Sprintf("a lore project already exists at %s (.lore/%s)", projectRoot, f)).
				WithHint("delete .lore/" + f + " first if you intend to re-init")
		}
	}

	// 2. Determine project name
	name := strings.TrimSpace(initFlagName)
	if name == "" {
		name = inferProjectNameFromGit(projectRoot)
	}
	if name == "" {
		name = filepath.Base(projectRoot)
	}
	// Apply textnorm to identifier — refuses non-ASCII / mixed-script names
	cleanName, err := textnorm.ValidateIdentifier(strings.ToLower(name))
	if err != nil {
		return errcodes.New(errcodes.InvalidIdentifier,
			fmt.Sprintf("project name %q failed validation", name)).WithCause(err)
	}
	name = cleanName

	originURL := readGitOriginURL(projectRoot)

	// 3. Create marker dir + state subdir
	if err := os.MkdirAll(filepath.Join(markerDir, projresolve.StateDir), 0o755); err != nil {
		return errcodes.New(errcodes.Internal, "create .lore/state").WithCause(err)
	}

	// 4. Create .lore/lore.db with schema, config singletons and FTS
	dbPath := filepath.Join(markerDir, projresolve.ModeAFile)
	if err := createProjectDB(ctx, dbPath); err != nil {
		return err
	}
	db := dbent.InitDB(dbPath)
	defer db.Close()
	if err := dbent.ApplyPragmas(db); err != nil {
		return errcodes.New(errcodes.Internal, "apply pragmas").WithCause(err)
	}
	entdb := dbent.New(db)
	client := entdb.Client()
	client.Use(naturalIDHook())
	client.Use(txAtMonotonicHook())
	defer client.Close()

	// 6. Resolve identity → seed initial actor row
	resolved := identity.Resolve(identity.Inputs{})
	if !resolved.Step.Stable() {
		fmt.Fprintln(os.Stderr, style.Warn("WARN: identity resolved via ephemeral session salt;"))
		fmt.Fprintln(os.Stderr, style.Warn("      audit-log entries from this session won't link to future sessions."))
		fmt.Fprintln(os.Stderr, style.Warn("      Set LORE_ACTOR or run `lore identity set` to fix."))
	}
	actor, err := upsertActor(ctx, client, resolved)
	if err != nil {
		return errcodes.New(errcodes.Internal, "create initial actor").WithCause(err)
	}

	// 7. Create initial Project row
	create := client.Project.Create().SetName(name)
	if originURL != "" {
		create.SetOriginURL(originURL)
	}
	proj, err := create.Save(ctx)
	if err != nil {
		return errcodes.New(errcodes.Internal, "create project").WithCause(err)
	}

	// 8. Auto-update .gitignore
	if err := ensureGitignore(ctx, projectRoot); err != nil {
		// Non-fatal — surface as warning
		fmt.Fprintln(os.Stderr, style.Warn("WARN: could not update .gitignore: "+err.Error()))
	}

	// 9. Print success summary
	fmt.Printf("%s lore initialized at %s\n", style.Success("✓"), projectRoot)
	fmt.Printf("    project_id: %s\n", style.Code(proj.ID))
	fmt.Printf("    name:       %s\n", proj.Name)
	if originURL != "" {
		fmt.Printf("    origin:     %s\n", originURL)
	}
	fmt.Printf("    db:         %s\n", dbPath)
	fmt.Printf("    identity:   %s (%s)\n", actor.StableKey, resolved.Step)
	fmt.Println()
	fmt.Println(style.Hint("  next: lore learn-from docs   to bootstrap from existing markdown"))

	// 10. First sync pass: writes .lore/data (the git-tracked copy of the
	// project + actor rows) so the very first commit already carries it.
	if os.Getenv(envSync) != envValueOff {
		rep, err := runSyncPass(ctx, db, projectRoot, filepath.Join(markerDir, lsync.DataDirName))
		if err != nil {
			fmt.Fprintln(os.Stderr, style.Warn(syncLogPrefix+err.Error()))
		} else {
			printSyncReport(rep)
		}
		syncSessions[dbPath] = &syncSession{root: projectRoot, dbPath: dbPath, dataDir: filepath.Join(markerDir, lsync.DataDirName)}
	}
	return nil
}

// createProjectDB creates a ready-to-use lore.db at dbPath: pragmas, the
// full schema, config singletons and the FTS5 search tables. Shared by
// `lore init` and by the fresh-clone path (materializeFromData), which
// must produce an identical cache before importing .lore/data.
func createProjectDB(ctx context.Context, dbPath string) error {
	db := dbent.InitDB(dbPath)
	if err := dbent.ApplyPragmas(db); err != nil {
		_ = db.Close() // the pragma error is the one to report
		return errcodes.New(errcodes.Internal, "apply pragmas").WithCause(err)
	}
	// Migrate closes the handle it is given (it wraps it in its own client).
	if err := dbent_migrate.Migrate(ctx, db); err != nil {
		return errcodes.New(errcodes.Internal, "migrate schema").WithCause(err)
	}
	db = dbent.InitDB(dbPath)
	defer db.Close()
	if err := dbent.ApplyPragmas(db); err != nil {
		return errcodes.New(errcodes.Internal, "apply pragmas").WithCause(err)
	}
	client := dbent.New(db).Client()
	if err := seedDBConfig(ctx, client); err != nil {
		return errcodes.New(errcodes.Internal, "seed config").WithCause(err)
	}
	if err := migrationlog.Record(ctx, db); err != nil {
		return errcodes.New(errcodes.Internal, "record schema migration").WithCause(err)
	}
	setupSearchIndex(ctx, db)
	return nil
}

// setupSearchIndex creates the FTS5 search tables and stamps the registry
// fingerprint, so search works immediately on a new DB without `lore setup`.
// Shared by every path that creates a DB (`lore init`, fresh-clone build,
// `lore project shared-init`). Failures are warnings: the DB is usable and
// `lore setup` repairs the index.
func setupSearchIndex(ctx context.Context, db *sql.DB) {
	if fts5.Available(ctx, db) {
		if err := fts5.EnsureSchema(ctx, db); err != nil {
			fmt.Fprintln(os.Stderr, style.Warn("WARN: fts5 schema: "+err.Error()))
		} else if err := fts5.EnsureRegistrySchema(ctx, db); err != nil {
			fmt.Fprintln(os.Stderr, style.Warn("WARN: fts5 registry: "+err.Error()))
		} else {
			fp := computeRegistryFingerprint()
			if _, err := db.ExecContext(ctx, `
				INSERT INTO config(id, key, value, created_at, updated_at, setting_updated_at)
				VALUES ('cfg_' || lower(hex(randomblob(16))), ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
				ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP, setting_updated_at = CURRENT_TIMESTAMP
			`, setupFingerprintKey, fp); err != nil {
				fmt.Fprintln(os.Stderr, style.Warn("WARN: stamp fingerprint: "+err.Error()))
			}
		}
	}
}

// inferProjectNameFromGit returns the repo name from `git remote get-url origin`
// Returns "" if no git remote configured
//
// Examples:
//
//	git@github.com:khanakia/aicoder-cli-go.git → aicoder-cli-go
//	https://github.com/vercel/next.js.git      → next.js
//	gitlab.com:org/sub/internal-api.git        → internal-api
func inferProjectNameFromGit(dir string) string {
	url := readGitOriginURL(dir)
	if url == "" {
		return ""
	}
	// Trim trailing .git
	url = strings.TrimSuffix(url, ".git")
	// Take everything after the last slash OR colon (ssh URLs use ':')
	for i := len(url) - 1; i >= 0; i-- {
		if url[i] == '/' || url[i] == ':' {
			return url[i+1:]
		}
	}
	return url
}

// readGitOriginURL runs `git remote get-url origin` in dir
func readGitOriginURL(dir string) string {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// alreadyIgnored reports whether git already ignores path (e.g. a broader
// `.lore/` rule), so init does not append a redundant line. Without git the
// answer is "no" and the literal check above decides.
func alreadyIgnored(ctx context.Context, projectRoot, path string) bool {
	probe := strings.TrimSuffix(path, "/")
	if strings.HasSuffix(path, "/") {
		probe += "/x" // check-ignore needs a path inside an ignored dir
	}
	ignored, err := gitsetup.IsIgnored(ctx, projectRoot, probe)
	return err == nil && ignored
}

// ensureGitignore appends standard lore paths to .gitignore if missing
func ensureGitignore(ctx context.Context, projectRoot string) error {
	gi := filepath.Join(projectRoot, ".gitignore")
	required := []string{
		".lore/lore.db",
		".lore/lore.db-shm",
		".lore/lore.db-wal",
		".lore/state/",
		".lore/backups/",
	}

	existing := ""
	if data, err := os.ReadFile(gi); err == nil {
		existing = string(data)
	}

	var toAdd []string
	for _, line := range required {
		if strings.Contains(existing, line) || alreadyIgnored(ctx, projectRoot, line) {
			continue
		}
		toAdd = append(toAdd, line)
	}
	if len(toAdd) == 0 {
		return nil
	}

	f, err := os.OpenFile(gi, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if existing != "" && !strings.HasSuffix(existing, "\n") {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}
	if _, err := f.WriteString("\n# lore\n"); err != nil {
		return err
	}
	for _, line := range toAdd {
		if _, err := f.WriteString(line + "\n"); err != nil {
			return err
		}
	}
	return nil
}

// seedDBConfig inserts the canonical DBConfig singletons at init time
func seedDBConfig(ctx context.Context, client *ent.Client) error {
	dbUUID := ids.MustNew(ids.PrefixDBConfig)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	seeds := map[string]string{
		"schema_version":     "1",
		"db_uuid":            dbUUID,
		"db_created_at":      now,
		"last_vacuum_at":     "",
		"last_checkpoint_at": "",
	}
	for k, v := range seeds {
		if _, err := client.DBConfig.Create().SetKey(k).SetValue(v).Save(ctx); err != nil {
			return fmt.Errorf("seed %s: %w", k, err)
		}
	}
	return nil
}

// upsertActor finds-or-creates an actor by stable_key
func upsertActor(ctx context.Context, client *ent.Client, r identity.Resolved) (*ent.Actor, error) {
	// Try lookup first via raw query because we don't have predicate access
	// ent generates Where helpers; use them
	// (Imports kept light by inlining here without the predicate package.)

	// First insert; on UNIQUE conflict, fall back to lookup
	actor, err := client.Actor.Create().
		SetKind(actorKind(r.Kind)).
		SetDisplayName(r.DisplayName).
		SetStableKey(r.StableKey).
		Save(ctx)
	if err == nil {
		return actor, nil
	}
	// Otherwise it must be a duplicate — find existing
	existing, qerr := client.Actor.Query().
		Where(entActor.StableKey(r.StableKey)).
		Only(ctx)
	if qerr == nil && existing != nil {
		return existing, nil
	}
	return nil, err
}

// actorKind maps the identity-package kind string to ent's enum type
func actorKind(s string) entActor.Kind {
	switch s {
	case "human":
		return entActor.KindHuman
	case "agent":
		return entActor.KindAgent
	case "hook":
		return entActor.KindHook
	case "plugin":
		return entActor.KindPlugin
	case "cron":
		return entActor.KindCron
	case "system":
		return entActor.KindSystem
	default:
		return entActor.KindHuman
	}
}
