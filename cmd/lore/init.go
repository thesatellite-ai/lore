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

// initCommand is `lore init [--name] [--non-interactive]`.
func (h *host) initCommand(wd string) *cobra.Command {
	name := ""
	// Read now, before any command opens the store (and creates the cache).
	local := hasLocalState(wd)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a lore project in this repository (project row and repo id)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if pid, err := h.app.Project(ctx); err == nil {
				if local {
					return errs.New(errs.AlreadyInitialized, "a lore project already exists here ("+pid.String()+")")
				}
				// E1: the checkout carries the shared project: adopt it.
				ensureGitignore(ctx, cmd, wd)
				if h.live != nil {
					h.live.Prime(ctx)
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "✓ adopted the shared lore project at %s\n    project_id: %s\n", wd, pid)
				return err
			} else if !errs.Has(err, errs.NotFound) {
				return err
			}
			n := strings.ToLower(strings.TrimSpace(name))
			if n == "" {
				n = strings.ToLower(filepath.Base(wd))
			}
			t, err := value.TextOf(n)
			if err != nil {
				return errs.Wrap(errs.InvalidInput, "init: project name", err)
			}
			w, err := h.app.Create(ctx, store.SourceRepo, schema.TableProjects, map[string]value.Value{projectNameField: t})
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
			repoID, _, err := h.app.EnsureRepoIdentity(ctx)
			if err != nil {
				return err
			}
			ensureGitignore(ctx, cmd, wd)
			if h.live != nil {
				h.live.Prime(ctx) // the first fetch happens here, as v0.1.17's init did
			}
			out := cmd.OutOrStdout()
			_, err = fmt.Fprintf(out, "lore initialized\n    project_id: %s\n    name:       %s\n    repo_id:    %s\n", w.Row.ID, n, repoID)
			return err
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "project name (default: the folder name)")
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
