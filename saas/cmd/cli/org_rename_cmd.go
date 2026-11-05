// org_rename_cmd.go — `lore org rename <old> <new>` (PROVENANCE 10.7,
// CLI_REFERENCE 3.x identity commands).
//
//	rename <old> <new>
//	  → resolve the org folder's prj_ id (attached here as store "<old>")
//	  → lorecfg.RenameOrg: entry name, its id:, repo_orgs choices and its
//	    own defaults.client_repos targets, one validated atomic write
//	  → print what moved, and that store rows naming the old label stay
//
// P2-ORG-1/2: the org's permanent id is its org folder's prj_ id (no org_
// prefix, CONFIG_GUIDE Q3); the name is this machine's label, so nothing
// in any row changes and references by id survive the rename.
package main

import (
	"context"
	"fmt"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/style"

	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	"github.com/thesatellite-ai/lore/store"
)

// runOrgRename is org.rename: the label change, one validated write.
func runOrgRename(ctx context.Context, in opsorgcmd.RenameIn) (opsorgcmd.RenameOut, error) {
	from, to := in.Old, in.New
	dir, err := identity.Dir()
	if err != nil {
		return opsorgcmd.RenameOut{}, errcodes.New(errcodes.Internal, "resolve HOME").WithCause(err)
	}
	cfg, _, err := lorecfg.LoadTeam(ctx, dir, lorecfg.Options{})
	if err != nil {
		return opsorgcmd.RenameOut{}, errcodes.New(errcodes.ConfigInvalid, "~/.lore/config.yaml is not valid").WithCause(err).
			WithHint("run `lore config check` for every problem with its location")
	}
	if cfg == nil || cfg.FindOrg(from) == nil {
		names := []string{}
		if cfg != nil {
			names = cfg.OrgNames()
		}
		return opsorgcmd.RenameOut{}, errcodes.New(errcodes.NotFound, fmt.Sprintf("no org entry named %q", from)).
			WithHint(fmt.Sprintf("entries: %v", names))
	}
	id := orgFolderID(ctx, from)
	res, err := lorecfg.RenameOrg(ctx, dir, from, to, id, lorecfg.Options{})
	if err != nil {
		return opsorgcmd.RenameOut{}, errcodes.New(errcodes.InvalidInput, "org rename refused").WithCause(err)
	}
	out := opsorgcmd.RenameOut{From: from, To: to, ID: id, IDRecorded: res.IDRecorded, RepoOrgs: res.RepoOrgs, Defaults: res.Defaults, Config: res.Path}
	out.Lines = orgRenameLines(out)
	return out, nil
}

// orgFolderID is the prj_ id of the org's folder when this repo attaches
// the org (the core names it store "<org>"; FLAG1 folder_id), else "".
// A rename never fails over it: the id is recorded the next time the
// rename runs where the org is attached, and the label change is local.
func orgFolderID(_ context.Context, org string) string {
	if coreApp == nil {
		return ""
	}
	ref := coreApp.DescribeSource(store.Source(org))
	if ref.FolderID.IsZero() {
		return ""
	}
	return ref.FolderID.String()
}

// orgRenameLines is the human output of `lore org rename`.
func orgRenameLines(o opsorgcmd.RenameOut) []string {
	lines := []string{fmt.Sprintf("%s org entry %s is now %s (this machine)", style.Success("✓"), o.From, style.Bold(o.To))}
	switch {
	case o.IDRecorded:
		lines = append(lines, fmt.Sprintf("  id %s recorded (the org folder's project id; it never changes)", o.ID))
	case o.ID == "":
		lines = append(lines, "  id not recorded: this repo does not attach the org; run it inside one of its repos to record it")
	}
	if o.RepoOrgs > 0 {
		lines = append(lines, fmt.Sprintf("  %d project choice(s) (lore org use) moved to %s", o.RepoOrgs, o.To))
	}
	if o.Defaults > 0 {
		lines = append(lines, fmt.Sprintf("  %d defaults.client_repos target(s) moved to %s", o.Defaults, o.To))
	}
	lines = append(lines, "  rows are unchanged; store rows that name the old label (routing.client_repos) and other machines keep it until edited there",
		"  written to "+o.Config)
	return plainLines(strings.Join(lines, "\n"))
}
