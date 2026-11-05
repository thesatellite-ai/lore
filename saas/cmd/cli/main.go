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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/guard"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/surface/cli"
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
		if runtime.GOOS == goosWindows {
			removeOldWindowsBinary() // leftover lore.exe.old of an upgrade (FORMAT_EVOLUTION 8.3)
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

// coreMountDirs gives mountCore the repo folder and lore home of the
// package-level core (coreApp). mountCore runs at package init, before any
// test sets up its own dirs, so a test binary replaces this func in a
// package-level var initializer (core_mount_dirs_test.go; Go runs every
// var initializer before any init) with a temp repo and home: unit tests
// then never write into the source checkout's .lore or the real ~/.lore
// (cl-gate). The binary keeps the process's own folder and home.
var coreMountDirs = processCoreDirs

// processCoreDirs is the working directory ("." when unreadable) and the
// lore home (a temp fallback when HOME cannot be resolved).
func processCoreDirs() (dir, home string) {
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	home, herr := loreHome()
	if herr != nil {
		home = filepath.Join(os.TempDir(), "lore-home-unresolved")
	}
	return dir, home
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
	rootCmd.AddCommand(newMoveCommand())
	rootCmd.AddCommand(newContextCommand())
	rootCmd.AddCommand(newSupportBundleCommand())
	rootCmd.AddCommand(newProjectCommand())
	rootCmd.AddCommand(newRepoCommand())
	rootCmd.AddCommand(newBenchCommand())
	rootCmd.AddCommand(newSkillCommand())
	rootCmd.AddCommand(newDirectiveCommand())
	rootCmd.AddCommand(newActorCommand())
	rootCmd.AddCommand(newPluginCommand())
	rootCmd.AddCommand(newPIIPatternCommand())
	rootCmd.AddCommand(newMountAliasCommand())
	rootCmd.AddCommand(newConfigCommand())
	rootCmd.AddCommand(newSearchAdminCommand())
	rootCmd.AddCommand(newSetupCommand())
	rootCmd.AddCommand(newUpgradeCommand()) // FORMAT_EVOLUTION 8
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
	// lore trash --local / restore --local / purge --archived-before run
	// the lore.db forms (CLI_REFERENCE 3.9); after mountCore made the noun.
	attachTrashLocal(rootCmd)
	// FLAG2: last, so it sees every old command registered above.
	classifyInputErrors(rootCmd)
}

// classifyInputErrors makes cobra's own refusals of the old (non-generated)
// commands input errors.
//
// FLAG2: a missing or invalid argument exits 20 E_INVALID_INPUT everywhere
// (the generated core commands already do, surface/cli usageError); exit 1
// is kept for unexpected internal errors only. cobra answers a wrong
// argument count (cobra.ExactArgs, …) and an unknown or malformed flag
// with a plain error, which main used to print with exit 1; this wraps the
// Args validator of every old command and sets the root's flag-error
// function (inherited by every old command that sets none), so both carry
// E_INVALID_INPUT. An unknown root command word is classified in main
// (inputError), because cobra raises it before any command runs.
func classifyInputErrors(root *cobra.Command) {
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		usageFailed = true
		return errcodes.New(errcodes.InvalidInput, err.Error())
	})
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		if cli.Generated(c) || c.Args == nil {
			return
		}
		args := c.Args
		c.Args = func(cmd *cobra.Command, a []string) error {
			if err := args(cmd, a); err != nil {
				var known *errcodes.CLIError
				if errors.As(err, &known) {
					return err // a validator that already names its code
				}
				usageFailed = true
				return errcodes.New(errcodes.InvalidInput, err.Error())
			}
			return nil
		}
	}
	walk(root)
}

// inputError classifies err, the error rootCmd.Execute returned for args,
// as E_INVALID_INPUT when it is one of cobra's own input refusals raised
// outside any hook classifyInputErrors can wrap (FLAG2): the unknown
// command word (Find raises it before any command runs), a required flag
// not set and a flag-group violation (cobra checks both after the Args
// validator, with no hook). Each is recognised by asking cobra the same
// question again and comparing the answer, never by matching text, so any
// other error is returned unchanged.
func inputError(err error, args []string) error {
	var known *errcodes.CLIError
	if err == nil || errors.As(err, &known) {
		return err
	}
	cmd, _, ferr := rootCmd.Find(args)
	if ferr != nil {
		if ferr.Error() == err.Error() {
			usageFailed = true
			return errcodes.New(errcodes.InvalidInput, err.Error())
		}
		return err
	}
	for _, again := range []func() error{cmd.ValidateRequiredFlags, cmd.ValidateFlagGroups} {
		if verr := again(); verr != nil && verr.Error() == err.Error() {
			usageFailed = true
			return errcodes.New(errcodes.InvalidInput, err.Error())
		}
	}
	return err
}

func main() {
	style.Init(style.ParseMode(flagColor))
	err := inputError(rootCmd.Execute(), os.Args[1:])
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
	if err == nil {
		maybeUpgradeNotice(os.Args[1:]) // once-a-day notice / opt-in auto_upgrade, after the output (FORMAT_EVOLUTION 8.6, 8.7)
	}
	if err != nil {
		// FORMAT_EVOLUTION 8.11: "no such column/table" from an old lore.db is
		// E_SCHEMA_OLD with the `lore setup` hint.
		err = explainSchemaError(explainStorageError(err))
		if wantsJSONError(os.Args[1:]) {
			printJSONError(os.Stdout, err)
			os.Exit(exitStatus(err))
		}
		fmt.Fprintln(os.Stderr, style.Error("ERROR: ")+err.Error())
		if hint := errorHint(err); hint != "" {
			fmt.Fprintln(os.Stderr, hintPrefix+hint)
		}
		os.Exit(exitStatus(err))
	}
}

// usageFailed is set when cobra refused the command line itself (FLAG2): a
// usage error is found before the command runs, so it prints the text line
// on stderr even under --json (CLI_REFERENCE 1.5).
var usageFailed bool

// wantsJSONError reports whether the failed command asked for --json and
// was not refused as a usage error. The executed command is found again
// from args; after Execute its flags (own and inherited) are parsed.
func wantsJSONError(args []string) bool {
	if usageFailed {
		return false
	}
	cmd, _, err := rootCmd.Find(args)
	if err != nil {
		return false
	}
	f := cmd.Flag("json")
	return f != nil && f.Value.String() == "true"
}

// printJSONError writes err as the stable --json error object (decision
// OQ5, CLI_REFERENCE 1.4): exactly one object on stdout with code, exit,
// class, retryable, needs_person, hint and next, the same shape the mounted
// core commands print (surface/cli printError, writeIndented). A v0.1.17 CLIError becomes a
// core error of the same code, so class and the retry/person defaults come
// from the one registry (model/errs); exit is the process exit status.
func printJSONError(w io.Writer, err error) {
	env := errs.EnvelopeOf(asCoreError(err))
	env.Error.Exit = exitStatus(err)
	// Envelope is a fixed struct of strings, ints and bools: Marshal cannot
	// fail on it, and a failing writer has nowhere left to report to.
	// Indented like the mounted core commands print it (writeIndented).
	b, mErr := json.MarshalIndent(env, "", jsonIndent)
	if mErr == nil {
		_, _ = w.Write(append(b, '\n'))
	}
}

// asCoreError is err as a core *errs.Error: the outermost lore error of the
// chain (firstLoreError's rule), a v0.1.17 CLIError converted field by
// field (its code is the same E_ string; folded codes keep their own name),
// or E_INTERNAL around anything else.
func asCoreError(err error) error {
	var ce *errcodes.CLIError
	var ee *errs.Error
	hasOld, hasCore := errors.As(err, &ce), errors.As(err, &ee)
	if hasCore && (!hasOld || !errors.As(ce.Wrapped, &ee)) {
		return ee
	}
	if hasOld {
		out := errs.New(errs.Code(ce.Code), ce.Message).WithHint(ce.Hint)
		out.DocURL = ce.DocURL
		return out
	}
	return errs.Wrap(errs.Internal, err.Error(), err)
}

// hintPrefix starts the line under ERROR that carries a CLIError's next step,
// so the spec's two-line messages (LORE_TEAM_SPEC F10, F12) read as written.
const hintPrefix = "  hint: "

// errorHint is the actionable next step of the first lore error in err's
// chain (a v0.1.17 CLIError or a core *errs.Error), or "" when there is none.
// Hint is kept out of Error() on purpose (errcodes/error.go, model/errs), so
// the CLI is the one place that prints it.
//
// EXACT ERRORS: a core *errs.Error that reaches the old path (an old command
// calling core code) prints its hint line exactly as the core surface
// prints it (surface/cli/output.go), instead of losing it.
func errorHint(err error) string {
	_, hint := firstLoreError(err)
	return hint
}

// exitStatus is the process exit status for a failed command.
//
// Q-R2-5: an old command exits with the core class exit of its error's code,
// exactly like the core surface (surface/cli/exit.go): E_NEEDS_CONFIRM 26,
// E_NOT_FOUND 27, the refused class 23, bad_input 20 (FLAG2), the team
// coordination numbers 10-18, guard 19. Exit 1 (errcodes.ExitGeneric) is
// kept for unexpected failures only: E_INTERNAL, an unregistered code, or
// an error that carries no lore code at all.
func exitStatus(err error) int {
	code, _ := firstLoreError(err)
	if code == "" {
		return errcodes.ExitGeneric
	}
	return errcodes.ExitCode(code)
}

// firstLoreError returns the code and hint of the first lore error in err's
// chain: a v0.1.17 *errcodes.CLIError or a core *errs.Error, whichever is
// outermost (the one whose message is printed first). When the chain holds
// both, the outer one is the one whose own chain contains the other. code is
// "" when the chain holds neither.
func firstLoreError(err error) (errcodes.Code, string) {
	var ce *errcodes.CLIError
	var ee *errs.Error
	hasOld, hasCore := errors.As(err, &ce), errors.As(err, &ee)
	switch {
	case hasOld && hasCore:
		var inner *errs.Error
		if errors.As(ce.Wrapped, &inner) {
			return ce.Code, ce.Hint
		}
		return errcodes.Code(ee.Code), ee.Hint
	case hasOld:
		return ce.Code, ce.Hint
	case hasCore:
		return errcodes.Code(ee.Code), ee.Hint
	}
	return "", ""
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
