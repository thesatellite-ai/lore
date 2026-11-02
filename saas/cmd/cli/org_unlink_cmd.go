// org_unlink_cmd.go — `lore org unlink <org> --project <folder>` (F11):
// remove this repo's link from an internal project's _meta.json in the
// store. Like link it changes the guards for the whole team, so it asks
// first (--yes without a terminal). The project folder and its rows stay.
package main

import (
	"fmt"
	"os"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
)

// newOrgUnlinkCommand builds `lore org unlink`; it is registered under
// `lore org` next to link.
func newOrgUnlinkCommand() *cobra.Command {
	var req orgresolve.LinkRequest
	var dryRun, yes bool
	cmd := &cobra.Command{
		Use:   "unlink <org> --project <folder>",
		Short: "Remove this repo's link to an internal project in the org's store",
		Example: `  lore org unlink northwind --project acme
  lore org unlink northwind --project acme --yes`,
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
			fmt.Printf("This removes the link of %s to %s %s/: it stops being a CLIENT repo of %s for everyone in %s.\n",
				plan.Remote, plan.StoreURL, plan.Folder, plan.Entry, plan.Entry)
			if dryRun {
				fmt.Println("  --dry-run: nothing written")
				return nil
			}
			if !yes {
				if !stdinIsTTY() {
					return errcodes.New(errcodes.InvalidInput, "unlinking changes the guards for the whole team; confirm with --yes when there is no terminal")
				}
				if !confirmYes("Continue? [y/N] ") {
					fmt.Println("nothing written")
					return nil
				}
			}
			return storeLinkWriter{Remove: true}.WriteLink(ctx, plan)
		},
	}
	cmd.Flags().StringVar(&req.Folder, flagOrgProject, "", "the internal project folder in the store (required)")
	cmd.Flags().StringVar(&req.StoreURL, flagStore, "", "which of the org's stores (default: the one holding the folder)")
	cmd.Flags().StringVar(&req.Remote, flagRemote, "", "which remote, when the repo has several")
	cmd.Flags().BoolVar(&dryRun, constants.FlagDryRun, false, "show what would change, write nothing")
	cmd.Flags().BoolVar(&yes, flagYes, false, "confirm without a prompt")
	_ = cmd.MarkFlagRequired(flagOrgProject)
	return cmd
}
