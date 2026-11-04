// core_mount.go — the strangler seam between this v0.1.17 binary and the
// 0.1.18 core (github.com/thesatellite-ai/lore).
//
// The migrated nouns (memory, migrate) are mounted from the core's
// operation registry (wire + surface/cli), so `lore memory …` reads and
// writes `_v: 2` row files through the new engine. Everything else stays
// on lore.db; the old readers of memories (render, search, TUI, …) see
// the new rows because the lsync import still copies changed row files
// into lore.db before each old command.
//
// Also here: `lore api` (the local REST surface over the same registry and
// engine) and `lore spec docs|openapi` (the generated reference).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/surface/cli"
	"github.com/thesatellite-ai/lore/surface/docs"
	"github.com/thesatellite-ai/lore/surface/rest"
	"github.com/thesatellite-ai/lore/wire"

	"saas/pkg/aicoder/errcodes"
	oldids "saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/projresolve"
	oldstores "saas/pkg/aicoder/stores"
)

// coreApp is the lazily opened 0.1.18 core of this process: built at
// start-up (no disk access), opened by the first migrated command. One per
// process, like rootCmd; nil only when the working directory or home could
// not be read, in which case mounting reported it.
var coreApp *wire.App

// Defaults of the generated reference (`lore spec`), relative to the
// directory the command runs in (the repo root when regenerating).
const (
	specDefaultDir      = "docs/reference"
	specCommandsFile    = "cli-commands.md"
	specOpenAPIFile     = "openapi.json"
	apiDefaultPortValue = 7461
	// apiNote is printed under the listening line: the token's location,
	// never the token.
	apiNote = "token: "
)

// mountCore builds the core and mounts its commands under root. A failure
// here is a programmer error (a registry or mount clash) and stops the
// binary, like op.Define's panics.
func mountCore(root *cobra.Command) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	home, herr := loreHome()
	if herr != nil {
		home = filepath.Join(os.TempDir(), "lore-home-unresolved")
	}
	app, err := wire.New(wire.Options{RepoDir: cwd, Home: home, ResolveActor: resolveCoreActor, GuardRepoWrite: guardCoreRepoWrite})
	if err != nil {
		panic(err)
	}
	coreApp = app
	if err := cli.Mount(root, app.Registry(), app,
		cli.WithInteractive(func() bool { return stdinIsTTY() }),
	); err != nil {
		panic(err)
	}
	root.AddCommand(newAPICommand(), newSpecCommand())
}

// resolveCoreActor is the actor of the core's writes: the v0.1.17
// identity chain of this binary (org entry, identity file, git email, …)
// turned into the actor's natural id (ids.Deterministic over the stable
// key, the id v0.1.17 gives the actor row), WITHOUT opening lore.db or
// writing an actor row: a read like `lore memory list` must leave a fresh
// clone clean, and the row envelope's created_by is not a reference the
// engine checks.
//
// It also makes sure .gitignore lists the core's local files (cache,
// lock), which `lore init` of v0.1.17 did not know about.
func resolveCoreActor(ctx context.Context) (ids.ID[ids.Actor], error) {
	r, err := resolveIdentityHere(ctx)
	if err != nil {
		return ids.ID[ids.Actor]{}, coreErr(err)
	}
	if pctx, perr := projresolve.Resolve(projresolve.Inputs{}); perr == nil && os.Getenv(envReadOnly) != envValueOn {
		if gerr := ensureGitignore(ctx, pctx.ProjectRoot); gerr != nil {
			return ids.ID[ids.Actor]{}, errcodes.New(errcodes.Internal, "update .gitignore").WithCause(gerr)
		}
	}
	id, err := oldids.Deterministic(oldids.PrefixActor, r.StableKey)
	if err != nil {
		return ids.ID[ids.Actor]{}, errcodes.New(errcodes.IdentityFailed, "derive the actor id").WithCause(err)
	}
	return ids.Parse[ids.Actor](id)
}

// coreRouteKinds maps a core table to the kind name v0.1.17's routing
// rules (defaults.client_repos, F14) use for it.
var coreRouteKinds = map[row.Table]string{schema.TableMemories: "memory"}

// guardCoreRepoWrite keeps v0.1.17's write routing in force for the rows
// the core writes into the repo store: in a repo whose org config sends
// that kind elsewhere (a client repo: internal knowledge must never land
// in it, D8), or whose store links cannot be read (F10 fail closed,
// E_LINK_UNKNOWN), the write is refused; a write the rules keep in this
// repo still passes the F16 leak check. The core cannot write into a
// v0.1.17 store yet, so "goes elsewhere" is a refusal naming --to.
//
// explicit is `--to repo`: the person chose this repo, which v0.1.17
// accepted (the leak check still runs).
func guardCoreRepoWrite(ctx context.Context, table row.Table, text string, explicit bool) error {
	kind, ok := coreRouteKinds[table]
	if !ok {
		return nil
	}
	var to toFlags
	if explicit {
		to.To = toRepo
	}
	wt, err := resolveWriteTarget(ctx, to, text)
	if err != nil {
		return coreErr(err)
	}
	in, err := targetInputs(ctx, wt.Situation, wt.Target, kind)
	if err != nil {
		return coreErr(err)
	}
	dec, err := oldstores.ResolveTarget(in)
	if err != nil {
		return coreErr(targetCLIError(err))
	}
	if wt.Personal != nil || !dec.Repo {
		return errs.New(errs.Unsupported, "this repository's lore routing sends a "+kind+" to a store ("+dec.Rule+"), and the 0.1.18 core cannot write to a v0.1.17 store yet").
			WithHint("write it to a store listed in .lore/stores.yaml with --to <name>, or choose this repo with --to repo")
	}
	return coreErr(leakCheckRouteWrite(ctx, writeRoute{Repo: true, Rule: dec.Rule, Situation: wt.Situation}, kind, text))
}

// coreErr turns a v0.1.17 error (errcodes.CLIError) returned into a core
// command into the core's error type, keeping its code, message and hint:
// the core's surfaces print any other error as a bare E_INTERNAL "internal
// error" (they never leak foreign error text), which would hide a real
// refusal like E_LINK_UNKNOWN. A code the core does not register becomes
// E_INTERNAL with the old text kept as the cause.
func coreErr(err error) error {
	if err == nil {
		return nil
	}
	var old *errcodes.CLIError
	if !errors.As(err, &old) {
		return err
	}
	code := errs.Code(old.Code)
	if !code.Known() {
		return errs.Wrap(errs.Internal, "lore: "+old.Message, err)
	}
	e := errs.New(code, old.Message)
	if old.Hint != "" {
		e = e.WithHint(old.Hint)
	}
	return e
}

// coreAddMemory writes one memory through the core's memory.add operation
// (the one write path: scan, validate, audit). Old commands that create a
// memory as a side effect (task done --lesson, learn promote) call it
// instead of writing lore.db. Returns the new id.
func coreAddMemory(ctx context.Context, body, sourceKind, sourceRef string) (string, error) {
	if coreApp == nil {
		return "", errcodes.New(errcodes.Internal, "the 0.1.18 core is not mounted")
	}
	e, ok := coreApp.Registry().Lookup(opsmemory.OpAdd)
	if !ok {
		return "", errcodes.New(errcodes.Internal, "memory.add is not registered")
	}
	in, err := json.Marshal(opsmemory.AddIn{Body: body, Source: sourceKind, SourceRef: sourceRef})
	if err != nil {
		return "", errcodes.New(errcodes.Internal, "encode memory").WithCause(err)
	}
	res, err := coreApp.Run(ctx, e, op.JSONDecoder{Body: in}, op.Caller{Interactive: stdinIsTTY()})
	if err != nil {
		return "", err
	}
	raw, err := res.JSON()
	if err != nil {
		return "", err
	}
	var out opsmemory.WriteOut
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", errcodes.New(errcodes.Internal, "decode memory.add result").WithCause(err)
	}
	return out.ID, nil
}

// newAPICommand is `lore api` (spec 9.7): the REST surface of every
// migrated operation on 127.0.0.1, closed by default (bearer token from
// <home>/api-token, created on first use), served through the same engine
// Run path as the CLI.
func newAPICommand() *cobra.Command {
	port := apiDefaultPortValue
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Serve the local REST API (127.0.0.1, bearer token in ~/.lore/api-token)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			home, err := loreHome()
			if err != nil {
				return err
			}
			eng, err := coreApp.Engine(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = coreApp.Close() }() // the server's error is what the caller needs
			actor, err := coreApp.Actor(ctx)
			if err != nil {
				return err
			}
			tok, err := rest.LoadOrCreateToken(home)
			if err != nil {
				return err
			}
			srv, err := rest.New(coreApp.Registry(), eng, rest.Options{Token: tok, Actor: actor, LoreVersion: version})
			if err != nil {
				return err
			}
			ln, err := rest.Listen(port)
			if err != nil {
				return err
			}
			return srv.Serve(ctx, ln, cmd.ErrOrStderr(), apiNote+rest.TokenPath(home))
		},
	}
	cmd.Flags().IntVar(&port, "port", apiDefaultPortValue, "port on 127.0.0.1 (0 picks a free one)")
	tok := &cobra.Command{Use: "token", Short: "Manage the local API token"}
	tok.AddCommand(&cobra.Command{
		Use:   "rotate",
		Short: "Replace the API token (every client holding the old one gets 401)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := loreHome()
			if err != nil {
				return err
			}
			if _, err := rest.RotateToken(home); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "new token written to "+rest.TokenPath(home))
			return err
		},
	})
	cmd.AddCommand(tok)
	return cmd
}

// newSpecCommand is `lore spec docs|openapi`: write the generated command
// and error reference and the OpenAPI document of the real registry. The
// drift test (wire/docs_test.go) fails when the committed files differ.
func newSpecCommand() *cobra.Command {
	dir := specDefaultDir
	cmd := &cobra.Command{Use: "spec", Short: "Write the generated CLI reference and OpenAPI file"}
	cmd.PersistentFlags().StringVar(&dir, "out", specDefaultDir, "folder holding "+specCommandsFile+" and "+specOpenAPIFile)
	write := func(file string) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			path := filepath.Join(dir, file)
			if err := docs.Update(coreApp.Registry(), path); err != nil {
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "wrote "+path)
			return err
		}
	}
	cmd.AddCommand(
		&cobra.Command{Use: "docs", Short: "Write " + specCommandsFile + " (commands and errors blocks)", Args: cobra.NoArgs, RunE: write(specCommandsFile)},
		&cobra.Command{Use: "openapi", Short: "Write " + specOpenAPIFile, Args: cobra.NoArgs, RunE: write(specOpenAPIFile)},
	)
	return cmd
}

// coreExitCode returns the exit status of an error a core command already
// printed (surface/cli prints its own errors), and false for any other
// error, which main prints in the v0.1.17 format.
//
// Limitation: a core error whose class exits 1 (E_INTERNAL, cobra usage)
// cannot be told apart from an old error by its status, so main prints it
// a second time in the old format; the status is right either way.
func coreExitCode(err error) (int, bool) {
	code := cli.ExitCode(err)
	return code, code != cli.ExitUsage
}
