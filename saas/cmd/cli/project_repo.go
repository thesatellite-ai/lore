// project_repo.go — the `lore project` and `lore repo` groups.
//
// list/show/archive/unarchive/delete of projects and add/list/archive/
// unarchive/delete of repos are served by the 0.1.18 core (ops/infra,
// mounted into these groups by core_mount.go: cli.Mount reuses a pure
// group). What stays here is the retired v0.1.17 Mode B pair (project
// shared-init|shared-list), which now only refuses (Q-MODEB,
// project_shared.go).
package main

import (
	"github.com/spf13/cobra"
)

// newProjectCommand is the `lore project` group (a pure group, so the
// core's project verbs mount into it).
func newProjectCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "project", Short: "Manage projects"}
	cmd.AddCommand(newProjectSharedInitCommand())
	cmd.AddCommand(newProjectSharedListCommand())
	return cmd
}

// newRepoCommand is the `lore repo` group; every verb is the core's.
func newRepoCommand() *cobra.Command {
	return &cobra.Command{Use: "repo", Short: "Manage repos within a project"}
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
