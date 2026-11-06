package main

// init.go — `lore init` of the standalone binary: the core part of v0.1.17's
// init (FA-OLDCODE-2). It writes the project row file and the permanent repo
// id (PROV1) through the engine; the v0.1.17 parts that still live in the old
// binary (lore.db, git merge driver and hooks, tool pointer files, the first
// lsync pass) are listed in the old-code removal plan as parity gaps.
//
// Flow: local state (.lore/lore-cache.db, or v0.1.17's lore.db / lore.yaml)
// existed before this process → refuse when the repo store has a project
// (E_ALREADY_INITIALIZED); no local state but the store has a project (a
// fresh clone of a repo carrying .lore/data, or a removed cache) → adopt it,
// as v0.1.17's needsMaterialize did (E1), minting nothing → otherwise name
// (--name, else the folder name, lower-cased) → projects row in the repo
// store → _meta.json (wire.SeedRepoMeta) → EnsureRepoIdentity → print the
// ids. Both success paths make sure .gitignore lists lore's local files.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// projectNameField is the projects table's name field (schema/builtin.go).
const projectNameField = "name"

// flagNonInteractive is v0.1.17's `lore init --non-interactive`. Init never
// asks (CLI_REFERENCE 2.14, Q-R2-4), so the flag is accepted for v0.1.17
// scripts and changes nothing, as on the old binary (P3-CMDLORE-1).
const flagNonInteractive = "non-interactive"

// localStateFiles are the files under .lore/ that mark a checkout as
// already set up on this machine: the core's repo cache, and v0.1.17's
// lore.db and its Mode-B pointers (lore.yaml, the legacy lore.toml). All are
// gitignored, so a fresh clone carries none of them.
var localStateFiles = []string{filepath.Base(wire.RepoCacheFile), "lore.db", "lore.yaml", "lore.toml"}

// hasLocalState reports whether any localStateFiles entry exists in
// wd/.lore. It must be read before the app opens the repo store, which
// creates the cache.
func hasLocalState(wd string) bool {
	for _, f := range localStateFiles {
		if _, err := os.Lstat(filepath.Join(wd, rowfs.LoreDir, f)); err == nil {
			return true
		}
	}
	return false
}

// initScope narrows o to what `lore init` needs, as v0.1.17's
// initCoreOptions did: the org (so the rows init writes carry the person)
// and the repo-write guard stay; the org's stores, its entry stores, the
// client-repo role and the default target go. Init writes only the repo
// store, and the org's stores may have no local copy yet or be broken: a
// code repo listed as a store is refused by every later command
// (E_NOT_A_STORE, F12), but it must not stop the repo from being set up.
func initScope(o *wire.Options) {
	o.OrgStores = nil
	o.OrgEntryStore = nil
	o.RepoIsClient = nil
	o.DefaultTarget = nil
}

// flagTarget is v0.1.17's `lore init --target` (repeatable, comma-separated).
const flagTarget = "target"

// initNextHint ends a fresh init: the pointer files are wired, LORE.md is
// written by render.
const initNextHint = "  next: lore render            to write .lore/LORE.md for those tools"

// initCommand is `lore init [<path>] [--name] [--target] [--non-interactive]`;
// run has already taken <path> off the arguments (initpath.go).
func (h *host) initCommand(wd string) *cobra.Command {
	name := ""
	var targets []string
	// Read now, before any command opens the store (and creates the cache).
	local := hasLocalState(wd)
	cmd := &cobra.Command{
		Use:   "init [path]",
		Short: "Initialize a lore project in this repository (project row and repo id)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 { // run takes the path off first; reaching here means it could not
				return errs.New(errs.InvalidInput, "init: run lore init "+args[0]+" as the whole command")
			}
			ctx := cmd.Context()
			// G-MONO-1: the repo id the checkout's other projects carry. Two ids
			// refuse before the store opens (it would leave a cache file), unless
			// wd already holds a project (adopt / already initialised: no new id).
			shared, hasShared, sharedErr := sharedRepoMeta(ctx, wd)
			if sharedErr != nil {
				if _, own, _ := rowfs.ReadRepoMeta(wd); !own {
					return sharedErr
				}
			}
			// OWNERQ-1: the project is rooted here, never at a .lore above (initsub.go).
			undo, err := rootInitAt(wd)
			if err != nil {
				return err
			}
			ok := false
			defer func() {
				if !ok {
					undo()
				}
			}()
			if pid, perr := h.app.Project(ctx); perr == nil {
				if local {
					return errs.New(errs.AlreadyInitialized, "a lore project already exists here ("+pid.String()+")")
				}
				// E1: the checkout carries the shared project: adopt it.
				ensureGitignore(ctx, cmd, wd)
				if h.live != nil {
					h.live.Prime(ctx)
				}
				ok = true
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "✓ adopted the shared lore project at %s\n    project_id: %s\n", wd, pid)
				return err
			} else if !errs.Has(perr, errs.NotFound) {
				return perr
			}
			if sharedErr != nil { // a new project would take a second id (G-MONO-1)
				return sharedErr
			}
			n := strings.ToLower(strings.TrimSpace(name))
			if n == "" {
				n = defaultProjectName(ctx, wd) // initname.go: origin's repo name, else the folder (v0.1.17)
			}
			t, err := value.TextOf(n)
			if err != nil {
				return errs.Wrap(errs.InvalidInput, "init: project name", err)
			}
			fields := map[string]value.Value{projectNameField: t}
			if origin := projectOriginURL(ctx, wd); origin != "" { // v0.1.17 wrote origin_url too (project list/show)
				ov, oerr := value.TextOf(origin)
				if oerr != nil {
					return errs.Wrap(errs.InvalidInput, "init: origin url", oerr)
				}
				fields[projectOriginField] = ov
			}
			w, err := h.app.Create(ctx, store.SourceRepo, schema.TableProjects, fields)
			if err != nil {
				return err
			}
			pid, err := ids.As[ids.Project](w.Row.ID)
			if err != nil {
				return err
			}
			if serr := h.app.SeedRepoMeta(ctx, pid); serr != nil {
				return serr
			}
			if hasShared {
				if ierr := h.inheritRepoID(ctx, shared); ierr != nil { // one repo id per checkout (initsub.go)
					return ierr
				}
			}
			repoID, _, err := h.app.EnsureRepoIdentity(ctx)
			if err != nil {
				return err
			}
			ok = true
			ensureGitignore(ctx, cmd, wd)
			if h.live != nil {
				h.live.Prime(ctx) // the first fetch happens here, as v0.1.17's init did
			}
			out := cmd.OutOrStdout()
			if _, err = fmt.Fprintf(out, "lore initialized\n    project_id: %s\n    name:       %s\n    repo_id:    %s\n\n", w.Row.ID, n, repoID); err != nil {
				return err
			}
			h.wireToolFiles(cmd, wd, targets)
			_, err = fmt.Fprintln(out, initNextHint)
			return err
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "project name (default: the repo name of origin's URL, else the folder name)")
	cmd.Flags().StringSliceVar(&targets, flagTarget, nil, "AI tool profile(s) or file(s) to point at .lore/LORE.md (default: tool files found in the repo, else AGENTS.md)")
	cmd.Flags().Bool(flagNonInteractive, false, "accepted for v0.1.17 scripts; init never asks")
	return cmd
}

// ensureGitignore adds lore's local files to wd's .gitignore, as v0.1.17's
// init did. Not fatal: the project is set up either way, so a failure is a
// warning on stderr.
func ensureGitignore(ctx context.Context, cmd *cobra.Command, wd string) {
	if err := hosthooks.EnsureGitignore(ctx, wd); err != nil {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "WARN: could not update .gitignore: "+err.Error()) // stderr gone: nothing left to report to
	}
}

// wireToolFiles points the AI tools this repo uses (--target, else the
// context.targets row, else their files, else AGENTS.md) at .lore/LORE.md
// and records the choice (TOOL NEUTRAL, renderhost.WireToolFiles). Not
// fatal, as on v0.1.17: a failure is a warning on stderr.
func (h *host) wireToolFiles(cmd *cobra.Command, wd string, targets []string) {
	if h.render == nil {
		return
	}
	var lines strings.Builder
	err := h.render.WireToolFiles(cmd.Context(), &lines, wd, targets)
	_, _ = io.WriteString(cmd.OutOrStdout(), lines.String()) // stdout gone: nothing left to report to
	if err != nil {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "WARN: could not wire AI tool files: "+err.Error()) // stderr gone: nothing left to report to
	}
}
