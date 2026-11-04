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
// User documentation: docs/README.md.
package main

import (
	"context"
	"errors"
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
	Short: "lore — local-first memory and context compiler for coding agents",
	Long: `lore is a local-first memory and context compiler for coding agents

It collects project knowledge (rules, memories, decisions, hotfixes, patterns,
snapshots, playbooks), retrieves the relevant parts via hybrid search, and
renders compact context files such as CLAUDE.md

Documentation: docs/README.md in the lore repository.`,
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
	rootCmd.AddCommand(newWhoamiCommand())
	rootCmd.AddCommand(newOrgCommand())
	rootCmd.AddCommand(newStoreCommand())
	rootCmd.AddCommand(newCacheCommand())
	rootCmd.AddCommand(newCheckCommand())
	rootCmd.AddCommand(newShareCommand())
	rootCmd.AddCommand(newMoveCommand())
	rootCmd.AddCommand(newContextCommand())
	rootCmd.AddCommand(newSupportBundleCommand())
	rootCmd.AddCommand(newTaskCommand())
	rootCmd.AddCommand(newMissionCommand())
	rootCmd.AddCommand(newProjectCommand())
	rootCmd.AddCommand(newRepoCommand())
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
	rootCmd.AddCommand(buildTUICommand())
	rootCmd.AddCommand(newSyncCommand())
	rootCmd.AddCommand(newMergeDriverCommand())
	rootCmd.AddCommand(newAuditCommand())
	registerExtraCommands(rootCmd)
	attachRosterCommands(rootCmd)
	attachStoreSubcommands(rootCmd)
	addCoordCommands(rootCmd) // lore-signals: agent, resource, gate, inbox, handoff, finding, plan apply (coord_cmd.go)
	// Live rows (LORE_TEAM_SPEC F1–F8): --live/--no-live, land, live, and the
	// pre-run hook. Last, so it sees every command registered above.
	wireLive(rootCmd)
	// The 0.1.18 core's nouns (memory, migrate) plus `lore api` and `lore
	// spec`: after wireLive, which must not wrap the core's commands (they
	// never touch lore.db).
	mountCore(rootCmd)
}

func main() {
	style.Init(style.ParseMode(flagColor))
	err := rootCmd.Execute()
	// Export this command's writes to .lore/data (and wire git) even when the
	// command failed part-way: rows it already wrote must not be left behind.
	// A refusal found while storing the command's writes (a leak-blocked
	// live write) fails the command when nothing else did.
	if ferr := finishSyncSessions(context.Background()); ferr != nil && err == nil {
		err = ferr
	}
	// Rows written into a store or personal project reach their home
	// (LORE_TEAM_SPEC §3.2: committed and pushed at once).
	if ferr := flushExternalProjects(context.Background()); ferr != nil && err == nil {
		err = ferr
	}
	if code, printed := coreExitCode(err); printed {
		os.Exit(code) // a core command already printed its error
	}
	if err != nil {
		err = explainStorageError(err)
		fmt.Fprintln(os.Stderr, style.Error("ERROR: ")+err.Error())
		if hint := errorHint(err); hint != "" {
			fmt.Fprintln(os.Stderr, hintPrefix+hint)
		}
		os.Exit(exitStatus(err))
	}
}

// hintPrefix starts the line under ERROR that carries a CLIError's next step,
// so the spec's two-line messages (LORE_TEAM_SPEC F10, F12) read as written.
const hintPrefix = "  hint: "

// errorHint is the actionable next step of the first CLIError in err's chain,
// or "" when there is none. Hint is kept out of CLIError.Error() on purpose
// (errcodes/error.go), so the CLI is the one place that prints it.
func errorHint(err error) string {
	var cli *errcodes.CLIError
	if errors.As(err, &cli) {
		return cli.Hint
	}
	return ""
}

// exitStatus is the process exit status for a failed command: the code's
// dedicated status when the error carries a registered code that has one
// (errcodes.ExitCode; LORE_TEAM_SPEC §1), else errcodes.ExitGeneric.
func exitStatus(err error) int {
	var cli *errcodes.CLIError
	if errors.As(err, &cli) {
		return errcodes.ExitCode(cli.Code)
	}
	return errcodes.ExitGeneric
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
