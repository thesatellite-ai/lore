// memory.go — the shared scope flags and project/repo resolution every old
// command uses (resolveContext and friends).
//
// The `lore memory …` commands themselves moved to the 0.1.18 core
// (ops/memory, mounted by core_mount.go): memories are written only through
// the new engine as `_v: 2` row files, and the old readers see them through
// the lsync import into .lore/lore.db.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"saas/pkg/constants"

	"github.com/thesatellite-ai/lore/wire"

	"dbent"
	"dbent/gen/ent"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/fts5"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/projresolve"

	"github.com/spf13/cobra"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// commonFlags are the universal scope flags applied to every subcommand
// that resolves a project/repo context
type commonFlags struct {
	flagDB       string
	flagProject  string
	flagRepo     string
	flagReadOnly bool
}

func bindCommonFlags(cmd *cobra.Command, c *commonFlags) {
	cmd.Flags().StringVar(&c.flagDB, constants.FlagDB, "", "override DB path (default: cwd's .lore/lore.db)")
	cmd.Flags().StringVar(&c.flagProject, constants.FlagProject, "", "override project (id or name)")
	cmd.Flags().StringVar(&c.flagRepo, constants.FlagRepo, "", "scope to a specific repo (mount_name or rep_id)")
	cmd.Flags().BoolVar(&c.flagReadOnly, constants.FlagReadOnly, false, "skip lock acquisition; refuse writes")
}

// refuseIfReadOnly returns an E_READ_ONLY error when read-only mode is active
// Call this at the top of any write command (memory add, rule add, etc.)
// before doing any state mutation. The check honors both --read-only flag
// (which sets LORE_READ_ONLY=1) and the env var directly so CI can
// enforce read-only via the environment regardless of caller
func refuseIfReadOnly(c *commonFlags) error {
	if c == nil {
		return nil
	}
	if c.flagReadOnly || os.Getenv("LORE_READ_ONLY") == "1" {
		return errcodes.New(errcodes.ReadOnly,
			"command requires write access but read-only mode is active").
			WithHint("unset --read-only and LORE_READ_ONLY")
	}
	return nil
}

// resolveContext runs projresolve + opens the SQLite DB + returns an ent client
// The caller is responsible for closing the client
func resolveContext(c *commonFlags) (*projresolve.Context, *ent.Client, error) {
	if c.flagReadOnly {
		_ = os.Setenv("LORE_READ_ONLY", "1")
	}

	// Fresh clone (or deleted cache): .lore/data exists but lore.db does
	// not. Build the cache from the files before resolving the project.
	if err := maybeMaterialize(context.Background(), c); err != nil {
		return nil, nil, materializeError(err)
	}

	ctx, err := projresolve.Resolve(projresolve.Inputs{
		Dir:         oldProjectDir(),
		FlagDB:      c.flagDB,
		FlagProject: c.flagProject,
		FlagRepo:    c.flagRepo,
	})
	if err != nil {
		return nil, nil, mapResolveError(err)
	}
	// Mode B (a .lore/lore.yaml or lore.toml pointer at a shared lore.db) is
	// retired (Q-MODEB): refuse rather than open the shared file.
	if ctx.Mode == projresolve.ModeB {
		p, ok := wire.FindModeBPointer(ctx.ProjectRoot)
		if !ok {
			p = projresolve.ModeBFile
		}
		return nil, nil, retiredModeBError(p)
	}

	if err := checkNetworkFS(ctx.DBPath); err != nil {
		return nil, nil, err
	}

	// Refuse if lore.db is a symlink (R16 #13, R18 #13, SC-22): an
	// attacker who can write to .lore/ could replace the DB with a
	// symlink to /etc/passwd or another sensitive file; our subsequent
	// PRAGMA / write would clobber the target
	if li, lerr := os.Lstat(ctx.DBPath); lerr == nil && (li.Mode()&os.ModeSymlink) != 0 {
		return nil, nil, errcodes.New(errcodes.SymlinkDB,
			"refusing to open lore.db: it is a symlink").
			WithHint("delete the symlink and re-init, or restore from a backup")
	}

	// An older lore.db is updated before anything reads it (FORMAT_EVOLUTION
	// 8.11 part 1; reads take the same path, a missing column fails them too).
	if err := ensureDBSchema(context.Background(), ctx.DBPath); err != nil {
		return nil, nil, err
	}

	db := dbent.InitDB(ctx.DBPath)
	if err := dbent.ApplyPragmas(db); err != nil {
		return nil, nil, errcodes.New(errcodes.Internal, "apply pragmas").WithCause(errors.Join(err, db.Close()))
	}
	if err := dbent.QuickCheck(db); err != nil {
		return nil, nil, errcodes.New(errcodes.DBCorrupt, "DB failed quick_check").
			WithCause(errors.Join(err, db.Close())).
			WithHint("move .lore/lore.db away, run `lore sync` to rebuild it from .lore/data, then `lore search rebuild`")
	}

	// One-query staleness check (no schema work done here). Per-command
	// cost is sub-millisecond. Heavy lifting moves to `lore setup`
	if fts5.Available(context.Background(), db) {
		warnIfSetupStale(context.Background(), db)
	}

	// Bring in whatever git changed since the last command (pull, checkout,
	// merge…) BEFORE the command reads anything. Never fails the command.
	preCommandSync(context.Background(), ctx, db)

	entdb := dbent.New(db)
	client := entdb.Client()
	// Natural-key rows (tags, repos, actors…) get ids derived from their
	// key so two clones creating "the same" row write the same file (E2).
	client.Use(naturalIDHook())
	// tx_at never goes backwards when the wall clock does (R27 #29).
	client.Use(txAtMonotonicHook())
	// Cache the raw *sql.DB by client pointer so callers needing raw SQL
	// (FTS5, audit log) can recover it without re-opening the file. ent's
	// Driver() accessor is unexported, so we maintain a sidecar map
	clientToRawDB[client] = db
	return ctx, client, nil
}

// clientToRawDB lets code paths that need raw SQL recover the *sql.DB
// from an ent.Client. Populated by resolveContext at open time. Single-
// threaded CLI lifetime; cleared lazily on process exit
var clientToRawDB = map[*ent.Client]*sql.DB{}

// rawDBFromClient returns the raw *sql.DB associated with this client, or
// nil if the client was constructed outside resolveContext
func rawDBFromClient(client *ent.Client) *sql.DB {
	return clientToRawDB[client]
}

// mapResolveError wraps projresolve sentinels with appropriate error codes.
// An error that already carries a code (projresolve's E_BAD_PATH refusal,
// with its hint) is passed through unchanged, so it is never re-labelled
// E_INTERNAL.
func mapResolveError(err error) error {
	var coded *errcodes.CLIError
	switch {
	case errors.As(err, &coded):
		return err
	case errors.Is(err, projresolve.ErrNotProjectRoot):
		return errcodes.New(errcodes.NotProjectRoot,
			"current directory is not a lore project (no .lore/lore.db or lore.yaml)").
			WithHint("run `lore init` here, or cd into a project root")
	case errors.Is(err, projresolve.ErrAmbiguousProject):
		return errcodes.New(errcodes.AmbiguousProject,
			"both .lore/lore.db AND a .lore/lore.yaml (or lore.toml) pointer present").
			WithHint("delete the one you don't need (Mode A uses lore.db; Mode B uses lore.yaml)")
	case errors.Is(err, projresolve.ErrBadPath):
		return errcodes.New(errcodes.BadPath, err.Error())
	default:
		return errcodes.New(errcodes.Internal, "resolve project").WithCause(err)
	}
}

// resolveProjectID converts a flag/env/toml project value (which may be a
// name OR an opaque ID) to the actual prj_* row ID
func resolveProjectID(ctx context.Context, projectIDOrName string) (string, error) {
	// Already opaque?
	if err := ids.Validate(projectIDOrName, ids.PrefixProject); err == nil {
		return projectIDOrName, nil
	}

	// Mode A "local" source means we don't have a project_id at all yet —
	// query the projects table; if exactly one row, use it; refuse if 0 or 2+
	if projectIDOrName == "" {
		all, err := coreProjectIDs(ctx, nil)
		if err != nil {
			return "", err
		}
		if len(all) == 0 {
			return "", errcodes.New(errcodes.ProjectNotFound,
				"no projects in this DB").
				WithHint("run `lore init` to register one")
		}
		if len(all) > 1 {
			return "", errcodes.New(errcodes.AmbiguousProject,
				"DB has multiple projects; specify --project=<id|name>").
				WithHint("run `lore project list` to see available IDs")
		}
		return all[0], nil
	}

	// Otherwise treat as name; refuse on collision
	name := projectIDOrName
	matches, err := coreProjectIDs(ctx, &name)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", errcodes.New(errcodes.ProjectNotFound,
			fmt.Sprintf("no project named %q", projectIDOrName))
	}
	if len(matches) > 1 {
		return "", errcodes.New(errcodes.ProjectNameCollision,
			fmt.Sprintf("multiple projects share name %q; specify by opaque ID", projectIDOrName)).
			WithHint("run `lore project list` to see opaque IDs")
	}
	return matches[0], nil
}

// coreProjectIDs lists the ids of the live projects rows of the checkout's
// own store (the repo store holds this repository's project row; store
// folders name theirs in _meta.json), optionally only those called name.
// projects is core-owned in a checkout, so the row files are read through
// the core, never .lore/lore.db.
func coreProjectIDs(ctx context.Context, name *string) ([]string, error) {
	if coreApp == nil {
		return nil, errcodes.New(errcodes.Internal, "the core is not mounted")
	}
	rows, err := coreApp.List(ctx, store.SourceRepo, schema.TableProjects)
	if err != nil {
		return nil, coreErr(err)
	}
	var out []string
	for _, r := range rows {
		if r.Deleted {
			continue
		}
		if name != nil {
			if v, ok := r.Fields[fieldName]; !ok || v == nil || v.String() != *name {
				continue
			}
		}
		out = append(out, r.ID.String())
	}
	return out, nil
}

// resolveRepoID converts a --repo flag (mount_name OR rep_id) to repo row ID
// Returns ("", nil) for empty input (= master scope)
func resolveRepoID(ctx context.Context, projectID, repoFlag string) (string, error) {
	if repoFlag == "" {
		return "", nil
	}
	if err := ids.Validate(repoFlag, ids.PrefixRepo); err == nil {
		return repoFlag, nil
	}
	notFound := errcodes.New(errcodes.RepoNotFound,
		fmt.Sprintf("no repo with mount_name %q in current project", repoFlag))
	if coreApp == nil {
		return "", notFound
	}
	if _, err := coreApp.Engine(ctx); err != nil { // open it: Stores is empty before
		return "", coreErr(err)
	}
	found, err := engine.FindByKey(ctx, coreApp, schema.TableRepos, map[string]string{fieldProjectID: projectID, fieldMountName: repoFlag})
	if err != nil || len(found) != 1 {
		return "", notFound
	}
	return found[0].Row.ID.String(), nil
}

// Row fields the project / repo resolution matches on.
const (
	fieldName      = "name"
	fieldProjectID = "project_id"
	fieldMountName = "mount_name"
)
