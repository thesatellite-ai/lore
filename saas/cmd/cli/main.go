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
	"context"
	"fmt"
	"os"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/guard"
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
	// Refuse root before any command touches the filesystem: files created
	// as root (lore.db, .lore/data) stay unwritable for the real user.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		allowed := os.Getenv(envAllowRoot) == envValueOn || os.Getenv(envAllowRootLegacy) == envValueOn
		if err := guard.CheckRoot(geteuid(), allowed); err != nil {
			return errcodes.New(errcodes.RootRefused, "lore refuses to run as root").
				WithHint("run as your normal user, or set " + envAllowRoot + "=1 if you really mean it")
		}
		return nil
	},
}

// geteuid is os.Geteuid, a variable so the root refusal can be tested
// without running the test suite as root.
var geteuid = os.Geteuid

// Root / network-FS overrides (see saas/pkg/aicoder/guard).
const (
	envAllowRoot = "LORE_ALLOW_ROOT"
	// envAllowRootLegacy is the pre-rename spelling, still honoured.
	envAllowRootLegacy = "MINI_ALLOW_ROOT"
	envAllowNetworkFS  = "LORE_ALLOW_NETWORK_FS"
)

// checkNetworkFS refuses a project or DB path on a cloud-sync / network
// filesystem (E_NETWORK_FS) unless LORE_ALLOW_NETWORK_FS=1.
func checkNetworkFS(path string) error {
	if os.Getenv(envAllowNetworkFS) == envValueOn {
		return nil
	}
	if err := guard.CheckNetworkFS(path); err != nil {
		return errcodes.New(errcodes.NetworkFS, err.Error()).
			WithHint("SQLite corrupts silently under iCloud/Dropbox/OneDrive/NFS; move the project to a local folder (git still shares the knowledge via .lore/data), or set " + envAllowNetworkFS + "=1")
	}
	return nil
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
	rootCmd.AddCommand(newSyncCommand())
	rootCmd.AddCommand(newMergeDriverCommand())
	rootCmd.AddCommand(newAuditCommand())
	registerExtraCommands(rootCmd)
}

func main() {
	style.Init(style.ParseMode(flagColor))
	err := rootCmd.Execute()
	// Export this command's writes to .lore/data (and wire git) even when the
	// command failed part-way: rows it already wrote must not be left behind.
	finishSyncSessions(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, style.Error("ERROR: ")+explainStorageError(err).Error())
		os.Exit(1)
	}
}

// sqliteFullMarker is SQLite's SQLITE_FULL message.
const sqliteFullMarker = "database or disk is full"

// sqliteCantOpenMarker is SQLite's SQLITE_CANTOPEN message, which is what a
// full disk produces when WAL/-shm files cannot be created.
const sqliteCantOpenMarker = "unable to open database file"

// explainStorageError turns a SQLite failure caused by a full disk into
// E_DISK_FULL with a clear hint. Without it the user saw E_INTERNAL "unable
// to open database file" (CH-7). Other errors pass through unchanged.
func explainStorageError(err error) error {
	msg := err.Error()
	full := strings.Contains(msg, sqliteFullMarker)
	if !full && strings.Contains(msg, sqliteCantOpenMarker) {
		if cwd, cerr := os.Getwd(); cerr == nil {
			full = guard.DiskFull(cwd)
		}
	}
	if !full {
		return err
	}
	return errcodes.New(errcodes.DiskFull, "the disk is full: lore could not write (nothing was changed)").
		WithCause(err).
		WithHint("free some space and run the command again; the DB is intact")
}
