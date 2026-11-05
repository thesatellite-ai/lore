// project_repo.go — the `lore project` and `lore repo` groups.
//
// list/show/archive/unarchive/delete of projects and add/list/archive/
// unarchive/delete of repos are served by the 0.1.18 core (ops/infra,
// mounted into these groups by core_mount.go: cli.Mount reuses a pure
// group), as are the retired v0.1.17 Mode B refusals (project
// shared-init|shared-list, ops/projectshared; Q-MODEB).
package main

import (
	"context"
	"fmt"
	"os"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/stores"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

// newProjectCommand is the `lore project` group (a pure group, so the
// core's project verbs mount into it).
func newProjectCommand() *cobra.Command {
	// shared-init|shared-list are the retired-refusal operations of
	// ops/projectshared (PASS3 ONE-PATH), mounted with the core's verbs.
	return &cobra.Command{Use: "project", Short: "Manage projects"}
}

// newRepoCommand is the `lore repo` group; every verb is the core's.
//
// Q7: after a successful `lore repo set-url` (cobra runs PostRun only on
// success) the store links follow the confirmed addresses: the org store
// folders that link this repo get a v0.1.17-shape link entry for each
// confirmed address they lack, so org rule 4 keeps matching a renamed or
// mirrored repo. The core verb cannot do it: the org stores are still
// v0.1.17 code until the org noun migrates.
func newRepoCommand() *cobra.Command {
	return &cobra.Command{
		Use: "repo", Short: "Manage repos within a project",
		PersistentPostRun: func(cmd *cobra.Command, _ []string) {
			if cmd.Name() == repoSetURLVerb {
				followStoreLinks(cmd.Context())
			}
		},
	}
}

// repoSetURLVerb is the CLI spelling of opsinfra.OpRepoSetURL's verb.
const repoSetURLVerb = "set-url"

// followStoreLinks adds, in every store folder that links this repo under
// one of its confirmed addresses, a link entry for each confirmed address
// the folder lacks (Q7: a v0.1.17-shape {project_id, remote} entry, which
// the store merge unions by (remote, project_id), so a racing clone's
// write survives). It never removes a link, and it reports on stderr so
// `--json` output stays clean. A store that cannot be read or written is
// reported and skipped: the repo's own record is already confirmed.
func followStoreLinks(ctx context.Context) {
	if coreApp == nil {
		return
	}
	meta, _, err := coreApp.ReadRepoMeta(ctx)
	if err != nil || len(meta.URLs) == 0 {
		return
	}
	pid, err := coreApp.Project(ctx)
	if err != nil {
		return
	}
	cfg, err := loadTeamConfig(ctx)
	if err != nil || len(cfg.Orgs) == 0 {
		return
	}
	src, err := storeLinkSource(cfg)
	if err != nil {
		return
	}
	res, err := src.Links(ctx, pid.String(), meta.URLs)
	if err != nil {
		fmt.Fprintln(os.Stderr, style.Warn("warning: ")+"store links not updated: "+err.Error())
		return
	}
	type folderKey struct{ entry, store, folder string }
	have := map[folderKey]map[string]orgresolve.Link{}
	var order []folderKey
	for _, l := range res.Links {
		k := folderKey{l.Entry, l.StoreURL, l.Folder}
		if have[k] == nil {
			have[k] = map[string]orgresolve.Link{}
			order = append(order, k)
		}
		have[k][l.Remote] = l
	}
	for _, k := range order {
		var tmpl orgresolve.Link
		for _, l := range have[k] {
			tmpl = l
			break
		}
		added := 0
		for _, remote := range meta.URLs {
			if _, ok := have[k][remote]; ok {
				continue
			}
			if err := addStoreLink(ctx, cfg, k.entry, k.store, k.folder, pid.String(), remote, tmpl.Own); err != nil {
				fmt.Fprintln(os.Stderr, style.Warn("warning: ")+"store "+k.store+", folder "+k.folder+": link not updated: "+err.Error())
				continue
			}
			added++
		}
		if added > 0 {
			fmt.Fprintf(os.Stderr, "%s store %s, folder %s: link updated (%d confirmed addresses)\n", style.Success("✓"), k.store, k.folder, len(meta.URLs))
		}
	}
}

// addStoreLink writes one link entry {projectID, remote} into folder of
// the org entry's store (the storeLinkWriter path without its stdout
// line). Own is copied from the folder's existing link of this repo.
func addStoreLink(ctx context.Context, cfg *lorecfg.Config, entry, storeURL, folder, projectID, remote string, own bool) error {
	org := cfg.FindOrg(entry)
	if org == nil {
		return errcodes.New(errcodes.InvalidInput, "no org entry named "+entry)
	}
	target := stores.StoreFor(org, storeURL)
	if target == nil {
		return errcodes.New(errcodes.InvalidInput, storeURL+" is not one of "+entry+"'s stores")
	}
	m, err := newStoreManager(cfg, org)
	if err != nil {
		return err
	}
	st, err := m.Open(ctx, target.URL)
	if err != nil {
		return storeCLIError(err, "open the store")
	}
	_, err = st.WriteLink(ctx, stores.LinkChange{
		Branch: target.EffectiveBranch(), Folder: folder,
		Link:       stores.LinkRecord{ProjectID: projectID, Remote: remote, Own: own},
		NewProject: newStoreProjectFiles(st.Norm, target.EffectiveBranch()),
		Signature:  storeSignature(org),
		NoPush:     !storeAutopush(cfg, org),
	})
	return storeCLIError(err, "write the link")
}

// isUniqueViolation matches SQLite's UNIQUE constraint error string
func isUniqueViolation(err error) bool {
	s := err.Error()
	return contains(s, "UNIQUE") || contains(s, "constraint failed")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(sub) > 0 && indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
