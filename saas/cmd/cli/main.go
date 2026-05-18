// lore is the standalone CLI binary for lore
//
// Build:    go build -trimpath -ldflags='-buildid= -s -w' -o bin/lore ./saas/cmd/cli
// Run:      ./bin/lore <command>
//
// Distinct from the existing saas/cmd/cli binary (which depends on PKL config
// and the long-running server stack). This binary is self-contained: each
// invocation opens its own SQLite connection, applies pragmas, runs the
// requested command, and exits
//
// Per PLAN.md Round 26 ship-gate / canonical v0.1 spec
package main

import (
	"fmt"
	"os"

	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

// version is set at build time via -ldflags '-X main.version=<value>'
var version = "0.1.0-dev"

// BinaryName is the name of this binary as it appears on the user's PATH and
// in every user-facing help/hint string. ALL new code that prints command
// suggestions must use this constant via fmt.Sprintf, not a literal
// Renaming the binary is then one constant edit + rebuild
const BinaryName = "lore"

// rootCmd is the cobra root for the lore binary
var rootCmd = &cobra.Command{
	Use:   BinaryName,
	Short: "lore — local-first memory and context compiler for AI coding agents",
	Long: `lore is a local-first memory and context compiler for AI coding agents

It collects project knowledge (rules, memories, decisions, hotfixes, patterns,
snapshots, playbooks), retrieves the relevant parts via hybrid search, and
renders compact context files such as CLAUDE.md

This binary is part of the v0.1 ship gate. See PLAN.md (canonical spec at
the top) for design rationale.`,
	Version:       version,
	SilenceErrors: true, // we render errors ourselves via style + JSON envelope
	SilenceUsage:  true,
}

// flagColor is the global --color flag bound by Init()
var flagColor string

func init() {
	rootCmd.PersistentFlags().StringVar(&flagColor, "color", "auto", "color output: auto | always | never")
	rootCmd.AddCommand(newInitCommand())
	rootCmd.AddCommand(newMemoryCommand())
	rootCmd.AddCommand(newRuleCommand())
	rootCmd.AddCommand(newDecisionCommand())
	rootCmd.AddCommand(newHotfixCommand())
	rootCmd.AddCommand(newRenderCommand())
	rootCmd.AddCommand(newDoctorCommand())
	rootCmd.AddCommand(newTablesCommand())
	rootCmd.AddCommand(newWhyContextCommand())
	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newErrorsCommand())
	rootCmd.AddCommand(newBackupCommand())
	rootCmd.AddCommand(newRestoreCommand())
	rootCmd.AddCommand(newRepairCommand())
	rootCmd.AddCommand(newLearnCommand())
	rootCmd.AddCommand(newLearnFromRootAliasCommand()) // lore learn-from docs at root
	rootCmd.AddCommand(newIdentityCommand())
	rootCmd.AddCommand(newSupportBundleCommand())
	rootCmd.AddCommand(newTaskCommand())
	rootCmd.AddCommand(newMissionCommand())
	rootCmd.AddCommand(newProjectCommand())
	rootCmd.AddCommand(newRepoCommand())
	rootCmd.AddCommand(newCommentCommand())
	rootCmd.AddCommand(newTagCommand())
	rootCmd.AddCommand(newBenchCommand())
	rootCmd.AddCommand(newSkillCommand())
	rootCmd.AddCommand(newDirectiveCommand())
	rootCmd.AddCommand(newActorCommand())
	rootCmd.AddCommand(newSnapshotCommand())
	rootCmd.AddCommand(newPluginCommand())
	rootCmd.AddCommand(newPIIPatternCommand())
	rootCmd.AddCommand(newTaskViewCommand())
	rootCmd.AddCommand(newExternalSourceCommand())
	rootCmd.AddCommand(newTechDocCommand())
	rootCmd.AddCommand(newMountAliasCommand())
	rootCmd.AddCommand(newConfigCommand())
	rootCmd.AddCommand(newSearchAdminCommand())
	rootCmd.AddCommand(newSetupCommand())
	rootCmd.AddCommand(newLinkCommand())
	rootCmd.AddCommand(newCommitShowCommand())
	rootCmd.AddCommand(buildTUICommand())
	registerExtraCommands(rootCmd)
}

func main() {
	style.Init(style.ParseMode(flagColor))
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, style.Error("ERROR: ")+err.Error())
		os.Exit(1)
	}
}
