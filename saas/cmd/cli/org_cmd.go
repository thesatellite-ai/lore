// org_cmd.go — `lore org use|set|link` (LORE_TEAM_SPEC F10, F11, Q-b).
//
//	use <entry>          machine-only choice for this project (repo_orgs)
//	use --clear          forget it
//	set <org> --commit   write the committed .lore/org.yaml (own repos only)
//	link <org> --project <folder> [--own] [--default]
//	                     validate and show the link; the store-side write
//	                     arrives with stores (LinkWriter)
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/projresolve"
	"saas/pkg/aicoder/style"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
)

// Flag names local to `lore org`.
const (
	flagClear       = "clear"
	flagCommit      = "commit"
	flagOrgProject  = "project"
	flagOwn         = "own"
	flagLinkDefault = "default"
	flagStore       = "store"
	flagRemote      = "remote"
	flagYes         = "yes"
)

func newOrgCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "org",
		Short: "Choose and inspect which org a repo belongs to",
		Long: `An org is one entry under orgs: in ~/.lore/config.yaml. Each repo resolves
to at most one org; run ` + "`lore whoami --explain`" + ` to see why.`,
	}
	// No store-side link writer exists yet; the stores work passes one here.
	cmd.AddCommand(newOrgUseCommand(), newOrgSetCommand(), newOrgLinkCommand(storeLinkWriter{}), newOrgCreateCommand())
	return cmd
}

func newOrgUseCommand() *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   "use <entry>",
		Short: "Choose the org entry for this project on this machine (not committed)",
		Example: `  lore org use agency-stag     # work against the staging store branch here
  lore org use --clear             # back to automatic resolution`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			dir, err := os.Getwd()
			if err != nil {
				return errcodes.New(errcodes.Internal, "getwd").WithCause(err)
			}
			s, err := gatherOrgSituation(ctx, dir)
			if err != nil {
				return err
			}
			if s.ProjectID == "" {
				return errcodes.New(errcodes.NotProjectRoot, "this directory has no lore project id (.lore/data/_meta.json)").
					WithHint("run it in a lore project root that shares its data through git")
			}
			if clear {
				if len(args) > 0 {
					return errcodes.New(errcodes.InvalidInput, "pass an entry or --clear, not both")
				}
				removed, err := lorecfg.RemoveRepoOrg(ctx, s.ConfigDir, s.ProjectID, lorecfg.Options{})
				if err != nil {
					return errcodes.New(errcodes.ConfigInvalid, "update ~/.lore/config.yaml").WithCause(err)
				}
				if !removed {
					fmt.Println("(no choice was recorded for this project)")
					return nil
				}
				fmt.Printf("%s cleared the org choice for %s\n", style.Success("✓"), s.ProjectID)
				return nil
			}
			if len(args) != 1 {
				return errcodes.New(errcodes.InvalidInput, "name the org entry to use").
					WithHint("entries: " + strings.Join(s.Config.OrgNames(), ", "))
			}
			entry := args[0]
			if s.Config.FindOrg(entry) == nil {
				return errcodes.New(errcodes.InvalidInput, fmt.Sprintf("no org entry named %q", entry)).
					WithHint("entries: " + strings.Join(s.Config.OrgNames(), ", "))
			}
			path, err := lorecfg.SetRepoOrg(ctx, s.ConfigDir, s.ProjectID,
				lorecfg.RepoOrg{Org: entry, Remotes: s.Remotes}, lorecfg.Options{})
			if err != nil {
				return errcodes.New(errcodes.ConfigInvalid, "update ~/.lore/config.yaml").WithCause(err)
			}
			fmt.Printf("%s %s uses org %s on this machine\n", style.Success("✓"), s.ProjectID, style.Bold(entry))
			fmt.Printf("  recorded in %s (machine-only, nothing committed)\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&clear, flagClear, false, "forget the choice for this project")
	return cmd
}

func newOrgSetCommand() *cobra.Command {
	var commit bool
	var project string
	cmd := &cobra.Command{
		Use:   "set <org> --commit",
		Short: "Write the committed .lore/org.yaml (only in the org's own repos)",
		Long: `Writes .lore/org.yaml so every teammate's lore picks the org in this repo
with no config of their own. Names only, never URLs. Accepted only when the
repo's remote matches the org's own: patterns; refused in client repos.
Commit the file with git afterwards.`,
		Example: `  lore org set agency --commit
  lore org set agency --commit --project lore-tools`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !commit {
				return errcodes.New(errcodes.InvalidInput, "lore org set writes a COMMITTED file; pass --commit to confirm").
					WithHint("for a machine-only choice use `lore org use " + args[0] + "`")
			}
			ctx := cmd.Context()
			dir, err := os.Getwd()
			if err != nil {
				return errcodes.New(errcodes.Internal, "getwd").WithCause(err)
			}
			s, err := gatherOrgSituation(ctx, dir)
			if err != nil {
				return err
			}
			f := &lorecfg.OrgFile{Org: args[0], Project: project}
			if err := f.Validate(); err != nil {
				return errcodes.New(errcodes.InvalidInput, err.Error())
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
			fmt.Printf("%s wrote %s (org %s)\n", style.Success("✓"), path, args[0])
			fmt.Println("  commit it with git so teammates resolve the same org")
			return nil
		},
	}
	cmd.Flags().BoolVar(&commit, flagCommit, false, "confirm writing the committed .lore/org.yaml")
	cmd.Flags().StringVar(&project, flagOrgProject, "", "the internal project folder (optional)")
	return cmd
}

// newOrgLinkCommand builds `lore org link`. writer is the store-side half
// (F11 steps 2–5); nil means this build cannot write links, and the command
// validates, shows the plan and refuses with E_NOT_IMPLEMENTED.
func newOrgLinkCommand(writer orgresolve.LinkWriter) *cobra.Command {
	var req orgresolve.LinkRequest
	var dryRun, yes bool
	cmd := &cobra.Command{
		Use:   "link <org> --project <folder>",
		Short: "Link this repo to an internal project in the org's store",
		Example: `  lore org link agency --project client
  lore org link agency --project lore-tools --own
  lore org link agency --project client2 --default --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			dir, err := os.Getwd()
			if err != nil {
				return errcodes.New(errcodes.Internal, "getwd").WithCause(err)
			}
			s, err := gatherOrgSituation(ctx, dir)
			if err != nil {
				return err
			}
			req.Entry, req.ProjectID, req.Remotes = args[0], s.ProjectID, s.Remotes
			plan, err := orgresolve.PlanLink(s.Config, req)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, err.Error())
			}
			role := "a CLIENT repo"
			if plan.Own {
				role = "one of " + plan.Entry + "'s OWN repos"
			}
			fmt.Printf("This marks %s as %s of %s: internal notes default to %s %s/,\n", plan.Remote, role, plan.Entry, plan.StoreURL, plan.Folder)
			if !plan.Own {
				fmt.Printf("and the leak guards turn on for everyone in %s.\n", plan.Entry)
			}
			fmt.Printf("  link: project %s at %s → %s@%s:%s/ (default=%v)\n", plan.ProjectID, plan.Remote, plan.StoreURL, plan.Branch, plan.Folder, plan.Default)
			if dryRun {
				fmt.Println(style.Muted("  --dry-run: nothing written"))
				return nil
			}
			if !yes {
				if !stdinIsTTY() {
					return errcodes.New(errcodes.InvalidInput, "linking changes the guards for the whole team; confirm with --yes when there is no terminal")
				}
				if !confirmYes("Continue? [y/N] ") {
					fmt.Println("nothing written")
					return nil
				}
			}
			if writer == nil {
				return errcodes.New(errcodes.NotImplemented, "the link is valid, but writing links into a store is not available in this build").
					WithHint("nothing was written; the store-side `lore org link` arrives with stores. Use --dry-run to check a link")
			}
			// The store writer returns CLIErrors (E_NOT_A_STORE, E_OFFLINE, a
			// missing branch) with their own hints: pass them through as is.
			if err := writer.WriteLink(ctx, plan); err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&req.Folder, flagOrgProject, "", "the internal project folder in the store (required)")
	cmd.Flags().BoolVar(&req.Own, flagOwn, false, "this is one of the org's own repos (only for own: patterns)")
	cmd.Flags().BoolVar(&req.Default, flagLinkDefault, false, "the link `--to <org>` uses when this repo has several")
	cmd.Flags().StringVar(&req.StoreURL, flagStore, "", "which of the org's stores (default: the first)")
	cmd.Flags().StringVar(&req.Remote, flagRemote, "", "which remote, when the repo has several")
	cmd.Flags().BoolVar(&dryRun, constants.FlagDryRun, false, "validate and show the link, write nothing")
	cmd.Flags().BoolVar(&yes, flagYes, false, "confirm without a prompt")
	_ = cmd.MarkFlagRequired(flagOrgProject)
	return cmd
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
