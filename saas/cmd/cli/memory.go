// memory.go — implements `lore memory add` (S2.2 partial)
//
// Resolves project + repo via the universal flag chain (R20), runs
// secret-scrubbing + content normalization, then INSERTs a memory row
//
// Future commands (search, list, show, archive, supersede) follow the same
// shape — share resolveContext + openDB helpers
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"saas/pkg/constants"
	"strings"

	"dbent"
	"dbent/gen/ent"
	entMemory "dbent/gen/ent/memory"
	entProject "dbent/gen/ent/project"
	entRepo "dbent/gen/ent/repo"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/fts5"
	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/security"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"

	"github.com/spf13/cobra"
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

	ctx, err := projresolve.Resolve(projresolve.Inputs{
		FlagDB:      c.flagDB,
		FlagProject: c.flagProject,
		FlagRepo:    c.flagRepo,
	})
	if err != nil {
		return nil, nil, mapResolveError(err)
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

	db := dbent.InitDB(ctx.DBPath)
	if err := dbent.ApplyPragmas(db); err != nil {
		db.Close()
		return nil, nil, errcodes.New(errcodes.Internal, "apply pragmas").WithCause(err)
	}
	if err := dbent.QuickCheck(db); err != nil {
		db.Close()
		return nil, nil, errcodes.New(errcodes.DBCorrupt, "DB failed quick_check").
			WithCause(err).
			WithHint("run `lore repair` to attempt recovery")
	}

	// One-query staleness check (no schema work done here). Per-command
	// cost is sub-millisecond. Heavy lifting moves to `lore setup`
	if fts5.Available(context.Background(), db) {
		warnIfSetupStale(context.Background(), db)
	}

	entdb := dbent.New(db)
	client := entdb.Client()
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

// mapResolveError wraps projresolve sentinels with appropriate error codes
func mapResolveError(err error) error {
	switch {
	case err == projresolve.ErrNotProjectRoot:
		return errcodes.New(errcodes.NotProjectRoot,
			"current directory is not an lore project (no .lore/lore.db or lore.toml)").
			WithHint("run `lore init` here, or cd into a project root")
	case err == projresolve.ErrAmbiguousProject:
		return errcodes.New(errcodes.AmbiguousProject,
			"both .lore/lore.db AND .lore/lore.toml present").
			WithHint("delete the one you don't need (Mode A uses .db; Mode B uses .toml)")
	case err == projresolve.ErrBadPath:
		return errcodes.New(errcodes.BadPath, err.Error())
	default:
		return errcodes.New(errcodes.Internal, "resolve project").WithCause(err)
	}
}

// resolveProjectID converts a flag/env/toml project value (which may be a
// name OR an opaque ID) to the actual prj_* row ID
func resolveProjectID(ctx context.Context, client *ent.Client, projectIDOrName string) (string, error) {
	// Already opaque?
	if err := ids.Validate(projectIDOrName, ids.PrefixProject); err == nil {
		return projectIDOrName, nil
	}

	// Mode A "local" source means we don't have a project_id at all yet —
	// query the projects table; if exactly one row, use it; refuse if 0 or 2+
	if projectIDOrName == "" {
		all, err := client.Project.Query().All(ctx)
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
		return all[0].ID, nil
	}

	// Otherwise treat as name; refuse on collision
	matches, err := client.Project.Query().
		Where(entProject.Name(projectIDOrName)).
		All(ctx)
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
	return matches[0].ID, nil
}

// resolveRepoID converts a --repo flag (mount_name OR rep_id) to repo row ID
// Returns ("", nil) for empty input (= master scope)
func resolveRepoID(ctx context.Context, client *ent.Client, projectID, repoFlag string) (string, error) {
	if repoFlag == "" {
		return "", nil
	}
	if err := ids.Validate(repoFlag, ids.PrefixRepo); err == nil {
		return repoFlag, nil
	}
	r, err := client.Repo.Query().
		Where(entRepo.ProjectID(projectID), entRepo.MountName(repoFlag)).
		Only(ctx)
	if err != nil {
		return "", errcodes.New(errcodes.RepoNotFound,
			fmt.Sprintf("no repo with mount_name %q in current project", repoFlag))
	}
	return r.ID, nil
}

// readBody returns the body for `<entity> add` from (in priority order):
//   - args (joined with spaces)
//   - stdin if not a TTY
//   - error
func readBody(args []string) (string, error) {
	if len(args) > 0 {
		// Reject bodies that look like a flag (e.g. starts with `-` or
		// contains a stray `--`). Cobra would silently treat these as
		// unknown flags before we ever see them — but if they slip through
		// (shell quoting + interspersed flags), warn loudly so the user
		// knows to add the `--` separator
		if strings.HasPrefix(args[0], "-") {
			return "", errcodes.New(errcodes.InvalidInput,
				"body starts with `-` — looks like a flag").
				WithHint("use `-- ` to separate flags from the body: `lore <cmd> -- \"<body>\"`")
		}
		return strings.Join(args, " "), nil
	}
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		// stdin is piped
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		return sb.String(), nil
	}
	return "", errcodes.New(errcodes.InvalidInput,
		"no body provided").
		WithHint("pass body as arg, pipe via stdin, or use --edit (deferred to v0.2)")
}

// memoryAddFlags are the per-command flags for `memory add`
type memoryAddFlags struct {
	commonFlags
	body         string
	kind         string
	tag          []string
	allowSecrets bool
	source       string
	sourceRef    string
	supersedes   string
	createdBy    string
	validatedBy  string
}

func newMemoryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Manage memories (free-form learned knowledge)",
	}
	cmd.AddCommand(newMemoryAddCommand())
	cmd.AddCommand(newMemoryEditCommand())
	cmd.AddCommand(newMemoryListCommand())
	cmd.AddCommand(newMemoryShowCommand())
	cmd.AddCommand(newMemoryInvalidateCommand())
	memArch, memUn := archiveCmdPair(memoryArchiveTarget)
	cmd.AddCommand(memArch)
	cmd.AddCommand(memUn)
	cmd.AddCommand(newDeleteCommand(memoryArchiveTarget))
	cmd.AddCommand(newMemorySearchFTSCommand())
	return cmd
}

func newMemoryAddCommand() *cobra.Command {
	f := &memoryAddFlags{}
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new memory",
		Long: `Add a new memory to the project's knowledge base

Body is passed via --body=<text> or piped via stdin:

  lore memory add --body="use Tailwind v4"
  echo "use Tailwind v4" | lore memory add

Scope defaults to current repo (if --repo set) or project-master.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := resolveBodyInput(args, f.body)
			if err != nil {
				return err
			}
			return runMemoryAdd(cmd.Context(), f, body)
		},
	}
	bindCommonFlags(cmd, &f.commonFlags)
	cmd.Flags().StringVar(&f.body, constants.FlagBody, "", "body (required; --body=<v> or pipe via stdin)")
	cmd.Flags().StringVar(&f.kind, constants.FlagKind, "retrieved",
		"memory kind: core | retrieved | episodic | procedural | archival")
	cmd.Flags().StringSliceVar(&f.tag, "tag", nil, "tags (repeatable)")
	cmd.Flags().BoolVar(&f.allowSecrets, constants.FlagAllowSecrets, false,
		"override secret-pattern refusal (logged loud)")
	cmd.Flags().StringVar(&f.source, constants.FlagSource, "manual",
		"source_kind: manual | learn-from | agent-proposal | plugin | imported")
	cmd.Flags().StringVar(&f.sourceRef, constants.FlagSourceRef, "", "free-form provenance pointer")
	cmd.Flags().StringVar(&f.supersedes, constants.FlagSupersedes, "", "memory_id (mem_*) this entry replaces")
	cmd.Flags().StringVar(&f.createdBy, constants.FlagCreatedBy, "", "actor_id (act_*); defaults to current identity")
	cmd.Flags().StringVar(&f.validatedBy, constants.FlagValidatedBy, "", "actor_id (act_*) that validated this memory")
	return cmd
}

func runMemoryAdd(ctx context.Context, f *memoryAddFlags, rawBody string) error {
	if err := refuseIfReadOnly(&f.commonFlags); err != nil {
		return err
	}
	// 1. Normalize body
	body, err := textnorm.Normalize(rawBody)
	if err != nil {
		return errcodes.New(errcodes.EmptyBody, err.Error()).
			WithHint("body must be non-empty after stripping whitespace and BOM/bidi chars")
	}

	// 2. Secret scrub. Refuse unless --allow-secrets
	scanner := security.NewScanner()
	if matches := scanner.Scan(body); len(matches) > 0 && !f.allowSecrets {
		preview := matches[0].Preview
		return errcodes.New(errcodes.SecretDetected,
			fmt.Sprintf("body contains a credential pattern: %s (preview: %s)",
				matches[0].PatternName, preview)).
			WithHint("use --allow-secrets to override (logged loud); see also ~/.lore/pii-patterns.txt")
	}

	// 3. Resolve project + repo
	rctx, client, err := resolveContext(&f.commonFlags)
	if err != nil {
		return err
	}
	defer client.Close()

	projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
	if err != nil {
		return err
	}
	repoID, err := resolveRepoID(ctx, client, projectID, rctx.RepoMount)
	if err != nil {
		return err
	}

	create := client.Memory.Create().
		SetProjectID(projectID).
		SetBody(body).
		SetKind(memoryKind(f.kind)).
		SetSourceKind(f.source)
	if repoID != "" {
		create.SetRepoID(repoID)
	}
	if f.sourceRef != "" {
		create.SetSourceRef(f.sourceRef)
	}
	if f.supersedes != "" {
		create.SetSupersededByID(f.supersedes)
	}
	createdBy, aerr := resolveActorIDFlag(ctx, client, f.createdBy)
	if aerr != nil {
		return aerr
	}
	if createdBy == "" {
		createdBy, aerr = resolveCurrentActorID(ctx, client)
		if aerr != nil {
			return aerr
		}
	}
	create.SetCreatedByActorID(createdBy)
	if validatedBy, verr := resolveActorIDFlag(ctx, client, f.validatedBy); verr != nil {
		return verr
	} else if validatedBy != "" {
		create.SetValidatedByActorID(validatedBy)
	}
	mem, err := create.Save(ctx)
	if err != nil {
		return errcodes.New(errcodes.Internal, "create memory").WithCause(err)
	}

	// 6. Output success
	fmt.Printf("%s %s %s\n", style.Success("✓"), mem.ID,
		style.Code(mem.ID))
	if repoID != "" {
		fmt.Printf("  scope: %s\n", style.ScopeBadge("repo:"+rctx.RepoMount))
	} else {
		fmt.Printf("  scope: %s\n", style.ScopeBadge("master"))
	}
	return nil
}

// memoryKind parses a flag value via ent's generated validator
// No hand-written switch — direct cast + validator call
func memoryKind(s string) entMemory.Kind {
	k := entMemory.Kind(s)
	if err := entMemory.KindValidator(k); err != nil {
		return entMemory.KindRetrieved
	}
	return k
}
