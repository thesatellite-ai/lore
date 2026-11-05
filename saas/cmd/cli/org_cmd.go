// org_cmd.go — the bodies of `lore org use|set|link` (LORE_TEAM_SPEC F10,
// F11, Q-b), run by the org.use|set|link operations through orgHost
// (org_ops_host.go, ops/orgcmd; PASS3 ONE-PATH):
//
//	use <entry>          machine-only choice for this project (repo_orgs)
//	use --clear          forget it
//	set <org> --commit   write the committed .lore/org.yaml (own repos only)
//	link <org> --project <folder> [--own] [--default]
//	                     validate, show the link, write it into the store
//	rename <old> <new>   label change, org_rename_cmd.go (PROVENANCE 10.7)
package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/style"

	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
)

// Flag names shared with other commands.
const (
	flagOwn         = "own"
	flagStore       = "store"
	flagRemote      = "remote"
	flagYes         = "yes"
	flagCommit      = "commit"
	flagOrgProject  = "project"
	flagLinkDefault = "default"
)

// orgHere gathers the org situation of the working folder.
func orgHere(ctx context.Context) (*orgSituation, string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, "", errcodes.New(errcodes.Internal, "getwd").WithCause(err)
	}
	s, err := gatherOrgSituation(ctx, dir)
	return s, dir, err
}

// runOrgUse is org.use: the machine-only choice for this project.
func runOrgUse(ctx context.Context, w *bytes.Buffer, in opsorgcmd.UseIn) error {
	s, _, err := orgHere(ctx)
	if err != nil {
		return err
	}
	if s.ProjectID == "" {
		return errcodes.New(errcodes.NotProjectRoot, "this directory has no lore project id (.lore/data/_meta.json)").
			WithHint("run it in a lore project root that shares its data through git")
	}
	if in.Clear {
		removed, removeErr := lorecfg.RemoveRepoOrg(ctx, s.ConfigDir, s.ProjectID, lorecfg.Options{})
		if removeErr != nil {
			return errcodes.New(errcodes.ConfigInvalid, "update ~/.lore/config.yaml").WithCause(removeErr)
		}
		if !removed {
			fmt.Fprintln(w, "(no choice was recorded for this project)")
			return nil
		}
		fmt.Fprintf(w, "%s cleared the org choice for %s\n", style.Success("✓"), s.ProjectID)
		return nil
	}
	if in.Entry == "" {
		return errcodes.New(errcodes.InvalidInput, "name the org entry to use").
			WithHint("entries: " + strings.Join(s.Config.OrgNames(), ", "))
	}
	if s.Config.FindOrg(in.Entry) == nil {
		return errcodes.New(errcodes.InvalidInput, fmt.Sprintf("no org entry named %q", in.Entry)).
			WithHint("entries: " + strings.Join(s.Config.OrgNames(), ", "))
	}
	path, err := lorecfg.SetRepoOrg(ctx, s.ConfigDir, s.ProjectID,
		lorecfg.RepoOrg{Org: in.Entry, Remotes: s.Remotes}, lorecfg.Options{})
	if err != nil {
		return errcodes.New(errcodes.ConfigInvalid, "update ~/.lore/config.yaml").WithCause(err)
	}
	fmt.Fprintf(w, "%s %s uses org %s on this machine\n", style.Success("✓"), s.ProjectID, style.Bold(in.Entry))
	fmt.Fprintf(w, "  recorded in %s (machine-only, nothing committed)\n", path)
	return nil
}

// runOrgSet is org.set: write the committed .lore/org.yaml (the operation
// already refused a run without --commit).
func runOrgSet(ctx context.Context, w *bytes.Buffer, in opsorgcmd.SetIn) error {
	s, dir, err := orgHere(ctx)
	if err != nil {
		return err
	}
	f := &lorecfg.OrgFile{Org: in.Org, Project: in.Project}
	if validateErr := f.Validate(); validateErr != nil {
		return errcodes.New(errcodes.InvalidInput, validateErr.Error())
	}
	// Dry-run F10 with the file in place: rule 3 must accept it,
	// or teammates would get a file lore ignores.
	res, rerr := orgresolve.Resolve(orgresolve.Inputs{Config: s.Config, Remotes: s.Remotes, Committed: f,
		Links: orgresolve.LinkResult{Status: s.Result.LinkStatus, Links: s.Result.Links, Detail: s.Result.LinkDetail}})
	if rerr != nil || res.Step != orgresolve.StepCommitted {
		reason := "rule 3 would not accept it"
		for _, t := range res.Trace {
			if t.Step == orgresolve.StepCommitted {
				reason = t.Reason
			}
		}
		return errcodes.New(errcodes.InvalidInput, "refusing to write .lore/org.yaml: "+reason).
			WithHint("only an org's own repos (matching its own: patterns) may name it; use `lore org use` for a machine-only choice")
	}
	path, err := lorecfg.WriteOrgFile(ctx, filepath.Join(dir, projresolve.MarkerDir), f, lorecfg.Options{})
	if err != nil {
		return errcodes.New(errcodes.Internal, "write .lore/org.yaml").WithCause(err)
	}
	fmt.Fprintf(w, "%s wrote %s (org %s)\n", style.Success("✓"), path, in.Org)
	fmt.Fprintln(w, "  commit it with git so teammates resolve the same org")
	return nil
}

// runOrgLink is org.link: validate, print the plan, and (confirmed, not a
// dry run) write the link into the store. It returns the question when
// it stopped unconfirmed.
func runOrgLink(ctx context.Context, w *bytes.Buffer, in opsorgcmd.LinkIn, yes bool) (string, error) {
	s, _, err := orgHere(ctx)
	if err != nil {
		return "", err
	}
	req := orgresolve.LinkRequest{Entry: in.Org, ProjectID: s.ProjectID, Remotes: s.Remotes, Folder: in.Project,
		Own: in.Own, Default: in.Default, StoreURL: in.Store, Remote: in.Remote}
	plan, err := orgresolve.PlanLink(s.Config, req)
	if err != nil {
		return "", errcodes.New(errcodes.InvalidInput, err.Error())
	}
	role := "a CLIENT repo"
	if plan.Own {
		role = "one of " + plan.Entry + "'s OWN repos"
	}
	fmt.Fprintf(w, "This marks %s as %s of %s: internal notes default to %s %s/,\n", plan.Remote, role, plan.Entry, plan.StoreURL, plan.Folder)
	if !plan.Own {
		fmt.Fprintf(w, "and the leak guards turn on for everyone in %s.\n", plan.Entry)
	}
	fmt.Fprintf(w, "  link: project %s at %s → %s@%s:%s/ (default=%v)\n", plan.ProjectID, plan.Remote, plan.StoreURL, plan.Branch, plan.Folder, plan.Default)
	if in.DryRun {
		fmt.Fprintln(w, "  --dry-run: nothing written")
		return "", nil
	}
	if !yes {
		return "Continue?", nil
	}
	// The store writer returns CLIErrors (E_NOT_A_STORE, E_OFFLINE, a
	// missing branch) with their own hints: pass them through as is.
	return "", storeLinkWriter{Out: w}.WriteLink(ctx, plan)
}

// confirmYes asks a yes/no question on the terminal; only "y"/"yes" is yes.
func confirmYes(prompt string) bool {
	fmt.Print(prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}
