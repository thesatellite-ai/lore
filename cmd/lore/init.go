package main

// init.go — `lore init` of the standalone binary: the core part of v0.1.17's
// init (FA-OLDCODE-2). It writes the project row file and the permanent repo
// id (PROV1) through the engine; the v0.1.17 parts that still live in the old
// binary (lore.db, git merge driver and hooks, tool pointer files, the first
// lsync pass) are listed in the old-code removal plan as parity gaps.
//
// Flow: refuse when a live project exists (E_ALREADY_INITIALIZED) →
// name (--name, else the folder name, lower-cased) → projects row in the repo
// store → _meta.json (wire.SeedRepoMeta) → EnsureRepoIdentity → print the ids.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// projectNameField is the projects table's name field (schema/builtin.go).
const projectNameField = "name"

// initCommand is `lore init [--name]`.
func (h *host) initCommand(wd string) *cobra.Command {
	name := ""
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a lore project in this repository (project row and repo id)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if pid, err := h.app.Project(ctx); err == nil {
				return errs.New(errs.AlreadyInitialized, "a lore project already exists here ("+pid.String()+")")
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
			out := cmd.OutOrStdout()
			_, err = fmt.Fprintf(out, "lore initialized\n    project_id: %s\n    name:       %s\n    repo_id:    %s\n", w.Row.ID, n, repoID)
			return err
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "project name (default: the folder name)")
	return cmd
}
