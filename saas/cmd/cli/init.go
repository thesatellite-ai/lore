// aicoder_init.go — implements `lore init`
//
// Story S2.1 (PLAN.md). Creates a fresh Mode A project at cwd:
//   - Refuses if .lore/lore.db or .lore/lore.yaml (or legacy lore.toml) exists (E_ALREADY_INITIALIZED)
//   - Auto-suggests project name from git remote
//   - Initializes SQLite DB with schema migration
//   - Seeds DBConfig singletons (schema_version, db_uuid, db_created_at)
//   - Auto-adds .lore/lore.db to .gitignore
//   - INSERTs initial Project row
//   - Resolves identity via 8-step chain and creates initial Actor
//   - Wires the repo's AI tool files to .lore/LORE.md (TOOL NEUTRAL)
//
// Catches: R21 #61 (first-run wizard), R22 #21-23, R23 #34, R26 ship-gate item
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"saas/pkg/constants"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/wire"

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
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

var (
	initFlagName           string
	initFlagNonInteractive bool
	// initFlagTargets are --target values (tool profiles or files); empty
	// means the tools detected in the repo, else AGENTS.md (Q-R2-4: the
	// override of init's printed choice, never a prompt).
	initFlagTargets []string
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
  • point the AI tools the repo already uses at .lore/LORE.md: a pointer
    block in each tool file found (CLAUDE.md, AGENTS.md, GEMINI.md,
    .cursor/rules/lore.mdc, .github/copilot-instructions.md, or your own
    profiles); with none found, AGENTS.md. Pick others with --target. The
    choice is printed and saved as the repo's context.targets setting
    (change it later with lore policy set context.targets <profiles> --yes)

Refuses if a project already exists (.lore/lore.db or .lore/lore.yaml).`,
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
	// Q-R2-4: init never prompts (it prints the targets it chose and --target
	// overrides them), so --non-interactive is kept for v0.1.17 scripts and
	// changes nothing.
	cmd.Flags().BoolVar(&initFlagNonInteractive, constants.FlagNonInteractive, false, "skip prompts and use auto-detected defaults")
	cmd.Flags().StringSliceVar(&initFlagTargets, constants.FlagTarget, nil,
		"AI tool profile(s) or file(s) to point at .lore/LORE.md (default: tool files found in the repo, else AGENTS.md)")
	return cmd
}

func runInit(ctx context.Context, projectRoot string) (err error) {
	// 1. A checkout that already carries shared lore data (.lore/data from a
	// teammate) but no local DB: adopt it instead of minting a second
	// project (E1).
	markerDir := filepath.Join(projectRoot, projresolve.MarkerDir)
	if checkErr := checkNetworkFS(projectRoot); checkErr != nil {
		return checkErr
	}
	if needsMaterialize(projectRoot) {
		if materializeErr := materializeFromData(ctx, projectRoot); materializeErr != nil {
			return errcodes.New(errcodes.Internal, "build lore.db from .lore/data").WithCause(materializeErr)
		}
		meta, readProjectErr := lsync.ReadProjectMeta(filepath.Join(markerDir, lsync.DataDirName))
		if readProjectErr != nil {
			return errcodes.New(errcodes.Internal, "read .lore/data/_meta.json").WithCause(readProjectErr)
		}
		fmt.Printf("%s adopted the shared lore project at %s\n", style.Success("✓"), projectRoot)
		fmt.Printf("    project_id: %s\n", style.Code(meta.ProjectID))
		return nil
	}

	// Refuse when a project already exists here. A .lore/ holding only
	// leftovers (an empty state/ dir from a lock, backups) is not a project
	// and must not block init.
	if p, ok := wire.FindModeBPointer(projectRoot); ok {
		return retiredModeBError(p) // Q-MODEB: convert it, never init over it
	}
	for _, f := range projresolve.ProjectMarkerFiles() {
		if _, lstatErr := os.Lstat(filepath.Join(markerDir, f)); lstatErr == nil {
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

	// Resolve the identity before anything is created: an org refusal
	// (ambiguous org, missing org identity) must leave no half-made project.
	resolved, err := resolveIdentityIn(ctx, projectRoot)
	if err != nil {
		return err
	}

	// 3. Create marker dir + state subdir
	if mkdirErr := os.MkdirAll(filepath.Join(markerDir, projresolve.StateDir), cliDirMode); mkdirErr != nil {
		return errcodes.New(errcodes.Internal, "create .lore/state").WithCause(mkdirErr)
	}

	// 4. Create .lore/lore.db with schema, config singletons and FTS
	dbPath := filepath.Join(markerDir, projresolve.ModeAFile)
	if createErr := createProjectDB(ctx, dbPath); createErr != nil {
		return createErr
	}
	db := dbent.InitDB(dbPath)
	defer joinClose(&err, db, "the DB")
	if applyErr := dbent.ApplyPragmas(db); applyErr != nil {
		return errcodes.New(errcodes.Internal, "apply pragmas").WithCause(applyErr)
	}
	entdb := dbent.New(db)
	client := entdb.Client()
	client.Use(naturalIDHook())
	client.Use(txAtMonotonicHook())
	defer joinClose(&err, client, "the project DB")

	// 6. Seed the initial actor row (identity resolved before step 3)
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
	// Times from the CLI clock (LORE_NOW in test builds), not the schema
	// defaults, so `lore project show` is stable in docs examples.
	now := cliClock().Now()
	create := client.Project.Create().SetName(name).SetCreatedAt(now).SetUpdatedAt(now).SetLastActiveAt(now)
	if originURL != "" {
		create.SetOriginURL(originURL)
	}
	proj, err := create.Save(ctx)
	if err != nil {
		return errcodes.New(errcodes.Internal, "create project").WithCause(err)
	}
	// The project row FILE is the core's (one writer of .lore/data/projects).
	if err := coreSeedProject(ctx, projectRoot, proj.ID, proj.Name, originURL); err != nil {
		return errcodes.New(errcodes.Internal, "write the project row through the core").WithCause(err)
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

	// 9b. TOOL NEUTRAL: wire the AI tools this repo already uses (their
	// files exist), else AGENTS.md, the cross-tool file — never CLAUDE.md
	// by default. A repo that has a CLAUDE.md keeps it as a target. Only a
	// pointer block is written; `lore render` writes LORE.md itself.
	if err := wireToolFiles(ctx, projectRoot, initFlagTargets); err != nil {
		fmt.Fprintln(os.Stderr, style.Warn("WARN: could not wire AI tool files: "+err.Error()))
	}
	fmt.Println(style.Hint("  next: lore render            to write .lore/LORE.md for those tools"))

	// 10. First sync pass: writes .lore/data (the git-tracked copy of the
	// project + actor rows) so the very first commit already carries it.
	if os.Getenv(envSync) != envValueOff {
		rep, err := runSyncPass(ctx, db, projectRoot, filepath.Join(markerDir, lsync.DataDirName))
		if err != nil {
			fmt.Fprintln(os.Stderr, style.Warn(syncLogPrefix+err.Error()))
		} else {
			printSyncReport(rep)
			// PROV1: lore init writes the permanent repo_id beside project_id
			// (design 10.1), right after this first pass wrote _meta.json (so
			// the pass stays a bootstrap, never an adopt). A failure is not
			// fatal: the next lore sync mints it, so say so.
			if coreApp != nil {
				if _, _, rerr := coreApp.EnsureRepoIdentity(ctx); rerr != nil {
					fmt.Fprintln(os.Stderr, style.Warn("WARN: the repo id was not written yet ("+rerr.Error()+"); `lore sync` writes it"))
				}
			}
		}
		syncSessions[dbPath] = &syncSession{root: projectRoot, dbPath: dbPath, dataDir: filepath.Join(markerDir, lsync.DataDirName)}
	}
	return nil
}

// wireToolFiles stitches the lore pointer block into each context target of
// the repo at projectRoot (values from --target, else the repo's
// context.targets row, else toolprofile.Resolve), pointing at the canonical
// .lore/LORE.md, prints the chosen targets and records them as the repo's
// context.targets settings row.
//
// Q-R2-4: init stays non-interactive. It never asks which tools to wire: it
// prints what it chose and why on one line, and --target overrides the
// choice, so the same command behaves the same in a terminal, a script and
// an agent session (--non-interactive is accepted and changes nothing).
func wireToolFiles(ctx context.Context, projectRoot string, values []string) error {
	home, err := loreHome()
	if err != nil {
		home = filepath.Join(os.TempDir(), "lore-home-unresolved")
	}
	opts, sink := initCoreOptions(projectRoot, home)
	app, err := wire.New(opts)
	if err != nil {
		return err
	}
	sink.app = app // TestE10: the session lookup reads this App's identity
	defer func() { _ = app.Close() }()
	pick, err := resolveTargets(ctx, projectRoot, values, func(ctx context.Context) ([]string, bool, error) {
		return contextTargetsOf(ctx, app)
	})
	if err != nil {
		return err
	}
	fmt.Printf("%s context targets: %s (%s)\n", style.Muted("·"), describeTargets(pick.targets), initTargetReason(len(values) > 0, pick.sel.Reason))
	out := filepath.Join(projectRoot, filepath.FromSlash(toolprofile.CanonicalOutput))
	for _, t := range pick.targets {
		var lines strings.Builder
		err := stitchPointer(&lines, projectRoot, t, out)
		fmt.Print(lines.String())
		if err != nil {
			return err
		}
	}
	if len(values) == 0 && pick.sel.Reason == toolprofile.ReasonSetting {
		return nil // the row already says it (a re-init over shared data)
	}
	var names []string
	for _, t := range pick.targets {
		if t.known && !slices.Contains(names, t.profile.Name) {
			names = append(names, t.profile.Name)
		} else if !t.known {
			fmt.Printf("%s %s is not a tool profile, so it is wired but not saved in %s\n", style.Muted("·"), t.file, policy.KeyContextTargets)
		}
	}
	if len(names) == 0 {
		return nil
	}
	if err := writeContextTargets(ctx, app, names); err != nil {
		return err
	}
	fmt.Printf("%s saved as %s; change with: lore policy set %s <profiles> --yes\n", style.Muted("·"), policy.KeyContextTargets, policy.KeyContextTargets)
	return nil
}

// initTargetReason says in words why init chose its targets.
func initTargetReason(fromFlag bool, r toolprofile.Reason) string {
	if fromFlag {
		return "from --target"
	}
	return describeReason(r)
}

// initCoreOptions are the core options of init's own writes and of
// coreSeedProject (infra_core_seed.go; one copy, r3-targets), minimal because
// the org's stores have no local copy yet while init runs, so the client/org
// hooks of coreOptions would refuse a repo write (TestE8, TestE10); the
// live-row wrapper is kept so the actor row the seed wrote is seen (FLAG6).
// The audit sink is returned too: the caller points it at the built App
// (coreAuditSink.app) so the session lookup never opens the org's stores.
func initCoreOptions(root, home string) (wire.Options, *coreAuditSink) {
	sink := &coreAuditSink{root: root}
	// r3-origin: WorkDir and Version, so the rows init writes carry _o.dir
	// (PROV6) and the _w version stamp (OQ7) like every other core write.
	return wire.Options{RepoDir: root, Home: home, Org: coreOrgResolution().Org, GuardRepoWrite: guardCoreRepoWrite, Audit: sink, ModeB: modeBExporter{},
		WrapBackend: coreWrapBackend, AllowNetworkFS: os.Getenv(envAllowNetworkFS) == envValueOn, Wall: cliClock().Now,
		WorkDir: coreWorkDir(), Version: version}, sink
}

// writeContextTargets writes names as the repo's context.targets policies
// row through the core (the one writer of .lore/data/policies), updating the
// repo's row when one exists.
//
// Q-R2-3 (SETTINGS ARE DATA): the targets init chose become a synced row,
// the same row `lore policy set context.targets` edits, so every clone
// renders into the same tools without re-detecting them from files.
func writeContextTargets(ctx context.Context, app *wire.App, names []string) error {
	fields, err := settings.ContextTargetsFields(names)
	if err != nil {
		return err
	}
	rows, err := app.List(ctx, store.SourceRepo, schema.TablePolicies)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if k, ok := r.Fields[policyFieldKey].(value.Text); ok && !r.Deleted && k.String() == string(policy.KeyContextTargets) {
			_, err = app.Update(ctx, engine.Ref{Store: store.SourceRepo, Table: schema.TablePolicies, ID: r.ID}, map[string]value.Value{policyFieldValue: fields[policyFieldValue]})
			return err
		}
	}
	_, err = app.Create(ctx, store.SourceRepo, schema.TablePolicies, fields)
	return err
}

// Field names of a policies row (schema.Builtin) init reads and updates.
const (
	policyFieldKey   = "key"
	policyFieldValue = "value"
)

// createProjectDB creates a ready-to-use lore.db at dbPath: pragmas, the
// full schema, config singletons and the FTS5 search tables. Shared by
// `lore init` and by the fresh-clone path (materializeFromData), which
// must produce an identical cache before importing .lore/data.
func createProjectDB(ctx context.Context, dbPath string) (err error) {
	db := dbent.InitDB(dbPath)
	if applyErr := dbent.ApplyPragmas(db); applyErr != nil {
		_ = db.Close() // the pragma error is the one to report
		return errcodes.New(errcodes.Internal, "apply pragmas").WithCause(applyErr)
	}
	// Migrate closes the handle it is given (it wraps it in its own client).
	if migrateErr := dbent_migrate.Migrate(ctx, db); migrateErr != nil {
		return errcodes.New(errcodes.Internal, "migrate schema").WithCause(migrateErr)
	}
	db = dbent.InitDB(dbPath)
	defer joinClose(&err, db, "the DB")
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

// inferProjectNameFromGit returns the repo name from origin's configured URL
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

// readGitOriginURL returns origin's first URL as written in git config (not
// rewritten by url.<base>.insteadOf; see rawRemoteURLs), or "" without one.
func readGitOriginURL(dir string) string {
	if rs := rawRemoteURLs(context.Background(), dir, gitOriginRemote); len(rs) > 0 {
		return rs[0].URL
	}
	return ""
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
func ensureGitignore(ctx context.Context, projectRoot string) (err error) {
	gi := filepath.Join(projectRoot, ".gitignore")
	required := []string{
		".lore/lore.db",
		".lore/lore.db-shm",
		".lore/lore.db-wal",
		// restore/repair keep the replaced DB as lore.db.broken.<stamp>: a
		// whole database with private rows, never to be committed.
		".lore/lore.db.broken.*",
		".lore/state/",
		".lore/backups/",
		// The 0.1.18 core's local files: the repo store's cache (never
		// .lore/lore.db) and its lock, both rebuilt or recreated at will.
		".lore/lore-cache.db",
		".lore/lore-cache.db-shm",
		".lore/lore-cache.db-wal",
		".lore/lock",
	}

	existing := ""
	if data, readFileErr := os.ReadFile(filepath.Clean(gi)); readFileErr == nil {
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

	f, err := os.OpenFile(filepath.Clean(gi), os.O_CREATE|os.O_WRONLY|os.O_APPEND, cliFileMode)
	if err != nil {
		return err
	}
	defer joinClose(&err, f, "the file")

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

// upsertActor finds-or-creates an actor by stable_key. The lookup goes
// through actorByKey first, so a person whose row arrived as a client-repo
// projection (a fresh clone on a second machine) reuses that row instead of
// minting a second actor (N1); see actorByKey for the dedup rule.
func upsertActor(ctx context.Context, client *ent.Client, r identity.Resolved) (*ent.Actor, error) {
	existing, err := actorByKey(ctx, client, r.StableKey, true)
	if err == nil {
		return existing, nil
	}
	if !ent.IsNotFound(err) {
		return nil, err
	}
	actor, err := client.Actor.Create().
		SetKind(actorKind(r.Kind)).
		SetDisplayName(r.DisplayName).
		SetStableKey(r.StableKey).
		Save(ctx)
	if err == nil {
		// actors are core-owned in the checkout: the row FILE is written by
		// the core (F18 projection), never exported from lore.db.
		if cerr := coreEnsureActor(ctx); cerr != nil {
			return nil, cerr
		}
		return actor, nil
	}
	// A concurrent writer on this lore.db created it between the lookup and
	// the insert: find that row.
	if again, qerr := actorByKey(ctx, client, r.StableKey, true); qerr == nil {
		return again, nil
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
