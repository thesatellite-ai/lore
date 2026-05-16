// project_repo.go — `lore project list` and `lore repo add/list` (NEW)
//
// Project: just `list` for v0.1 (init creates the row)
// Repo: add/list for multi-repo scoping per Round 17
package main

import (
	"context"
	"fmt"
	"saas/pkg/constants"

	"dbent/gen/ent"
	entProject "dbent/gen/ent/project"
	entRepo "dbent/gen/ent/repo"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"

	"github.com/spf13/cobra"
)

// ── Project ──────────────────────────────────────────────────────────────

func newProjectCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "project", Short: "Manage projects"}
	cmd.AddCommand(newProjectListCommand())
	cmd.AddCommand(newProjectShowCommand())
	cmd.AddCommand(newProjectSharedInitCommand())
	cmd.AddCommand(newProjectSharedListCommand())
	a, u := archiveCmdPair(projectArchiveTarget)
	cmd.AddCommand(a)
	cmd.AddCommand(u)
	cmd.AddCommand(newDeleteCommand(projectArchiveTarget))
	return cmd
}

func newProjectListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List projects in the current DB",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			rows, err := client.Project.Query().Order(ent.Asc(entProject.FieldCreatedAt)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list projects").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindProjectList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no projects)"))
				return nil
			}
			for _, p := range rows {
				archived := ""
				if p.ArchivedAt != nil {
					archived = " " + style.Warn("[archived]")
				}
				origin := ""
				if p.OriginURL != nil {
					origin = "  " + *p.OriginURL
				}
				fmt.Printf("%s  %s%s%s\n", style.Code(p.ID), p.Name, origin, archived)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newProjectShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id-or-name>",
		Short: "Show project details (with attached repos)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, args[0])
			if err != nil {
				return err
			}
			p, err := client.Project.Get(cmd.Context(), projectID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, "project not found")
			}
			repos, _ := client.Repo.Query().Where(entRepo.ProjectID(p.ID)).All(cmd.Context())
			if jsonOut {
				rb := make([]repoBrief, 0, len(repos))
				for _, r := range repos {
					b := repoBrief{ID: r.ID, MountName: r.MountName}
					if r.DisplayName != nil {
						b.DisplayName = *r.DisplayName
					}
					if r.OriginURL != nil {
						b.OriginURL = *r.OriginURL
					}
					if r.ArchivedAt != nil {
						b.ArchivedAt = r.ArchivedAt.Format("2006-01-02T15:04:05Z07:00")
					}
					rb = append(rb, b)
				}
				out := projectWithRepos{
					ID:           p.ID,
					Name:         p.Name,
					CreatedAt:    p.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
					LastActiveAt: p.LastActiveAt.Format("2006-01-02T15:04:05Z07:00"),
					Repos:        rb,
				}
				if p.OriginURL != nil {
					out.OriginURL = *p.OriginURL
				}
				if p.ArchivedAt != nil {
					out.ArchivedAt = p.ArchivedAt.Format("2006-01-02T15:04:05Z07:00")
				}
				printJSON(constants.KindProjectShow, out, 0)
				return nil
			}
			fmt.Printf("Project: %s\n", style.Code(p.ID))
			fmt.Printf("  name:    %s\n", p.Name)
			if p.OriginURL != nil {
				fmt.Printf("  origin:  %s\n", *p.OriginURL)
			}
			fmt.Printf("  created: %s\n", p.CreatedAt.Format("2006-01-02 15:04:05Z"))
			fmt.Printf("  active:  %s\n", p.LastActiveAt.Format("2006-01-02 15:04:05Z"))
			if len(repos) > 0 {
				fmt.Println()
				fmt.Println(style.Muted("Repos:"))
				for _, r := range repos {
					fmt.Printf("  %s  %s\n", style.Code(r.ID), r.MountName)
				}
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output (with eager-loaded repos)")
	return cmd
}

// ── Repo ─────────────────────────────────────────────────────────────────

func newRepoCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "repo", Short: "Manage repos within a project"}
	cmd.AddCommand(newRepoAddCommand())
	cmd.AddCommand(newRepoListCommand())
	a, u := archiveCmdPair(repoArchiveTarget)
	cmd.AddCommand(a)
	cmd.AddCommand(u)
	cmd.AddCommand(newDeleteCommand(repoArchiveTarget))
	return cmd
}

func newRepoAddCommand() *cobra.Command {
	var f commonFlags
	var origin, displayName string
	cmd := &cobra.Command{
		Use:   "add <mount_name>",
		Short: "Register a repo within the current project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mountName, err := textnorm.ValidateIdentifier(args[0])
			if err != nil {
				return errcodes.New(errcodes.InvalidIdentifier,
					fmt.Sprintf("mount_name %q failed validation", args[0])).WithCause(err)
			}

			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, rctx.ProjectID)
			if err != nil {
				return err
			}
			create := client.Repo.Create().
				SetProjectID(projectID).
				SetMountName(mountName)
			if origin != "" {
				create.SetOriginURL(origin)
			}
			if displayName != "" {
				create.SetDisplayName(displayName)
			}
			r, err := create.Save(cmd.Context())
			if err != nil {
				if isUniqueViolation(err) {
					return errcodes.New(errcodes.MountNameTaken,
						fmt.Sprintf("mount_name %q already exists in this project", mountName))
				}
				return errcodes.New(errcodes.Internal, "create repo").WithCause(err)
			}
			fmt.Printf("%s repo %s registered (%s)\n", style.Success("✓"), mountName, style.Code(r.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&origin, "origin", "", "git remote URL")
	cmd.Flags().StringVar(&displayName, constants.FlagDisplayName, "", "free-form display name")
	return cmd
}

func newRepoListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List repos in the current project",
		RunE: func(cmd *cobra.Command, args []string) error {
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, rctx.ProjectID)
			if err != nil {
				return err
			}
			rows, err := client.Repo.Query().Where(entRepo.ProjectID(projectID)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list repos").WithCause(err)
			}
			if jsonOut {
				printJSON(constants.KindRepoList, rows, len(rows))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no repos)"))
				return nil
			}
			for _, r := range rows {
				archived := ""
				if r.ArchivedAt != nil {
					archived = " " + style.Warn("[archived]")
				}
				origin := ""
				if r.OriginURL != nil {
					origin = "  " + *r.OriginURL
				}
				fmt.Printf("%s  %s%s%s\n", style.Code(r.ID), r.MountName, origin, archived)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
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

// silence unused-import warnings for shared helpers
var _ = entProject.ID
var _ context.Context
