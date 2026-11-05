// core_mount.go — the strangler seam between this v0.1.17 binary and the
// 0.1.18 core (github.com/thesatellite-ai/lore).
//
// The migrated nouns (memory, migrate) are mounted from the core's
// operation registry (wire + surface/cli), so `lore memory …` reads and
// writes `_v: 2` row files through the new engine. Everything else stays
// on lore.db; the old readers of memories (render, search, TUI, …) see
// the new rows because the lsync import still copies changed row files
// into lore.db before each old command (Q-TUI + Q-LOCAL: the lsync bridge
// and machine-local tables stay on ent/lore.db for 0.1.18).
//
// Also here: `lore api` (the local REST surface over the same registry and
// engine) and `lore spec docs|openapi` (the generated reference).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	opsaudit "github.com/thesatellite-ai/lore/ops/audit"
	opslink "github.com/thesatellite-ai/lore/ops/link"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/surface/cli"
	"github.com/thesatellite-ai/lore/surface/docs"
	"github.com/thesatellite-ai/lore/surface/rest"
	"github.com/thesatellite-ai/lore/wire"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"

	"dbent"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitsetup"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/leakcheck"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/projresolve"
	oldstores "saas/pkg/aicoder/stores"
	"saas/pkg/aicoder/style"
)

// coreApp is the lazily opened 0.1.18 core of this process: built at
// start-up (no disk access), opened by the first migrated command. One per
// process, like rootCmd; nil only when the working directory or home could
// not be read, in which case mounting reported it.
var coreApp *wire.App

// coreRunner is the one op.Runner of this process: the CLI's generated
// commands and `lore api`'s REST routes both run through it (spec 9.2: one
// Run path for every surface), so the alias handling, the --to
// <store>/<folder> attach, the Mode B refusal, the client-repo stores.yaml
// confirm (CFG7) and the repo-write guards apply to REST exactly as to the
// CLI. Its CLI-only state (compat flag refusals, --read-only, the running
// command for the not-found explanation) is never set while `lore api`
// serves, so a REST request sees only the shared path.
var coreRunner *hosthooks.Runner

// Defaults of the generated reference (`lore spec`), relative to the
// directory the command runs in (the repo root when regenerating).
const (
	specDefaultDir      = "docs/reference"
	specCommandsFile    = "cli-commands.md"
	specOpenAPIFile     = "openapi.json"
	apiDefaultPortValue = 7461
	// apiNote is printed under the listening line: the token's location,
	// never the token.
	apiNote = "token: "
)

// coreOptions is the one wire.Options builder of this binary's core: every
// host hook (routing, client-repo detection, org stores, audit, Mode B,
// notices, the live-row wrapper, LORE_ALLOW_NETWORK_FS). mountCore and the
// test fixtures call it, so a hook added here reaches both.
func coreOptions(repoDir, home string) wire.Options {
	o := wire.Options{RepoDir: repoDir, Home: home, Local: coreLocalPorts(),
		WorkDir: coreWorkDir(), Version: version, Audit: &coreAuditSink{root: repoDir}, // OVERNIGHT Qs subfolder: audit at the repo top
		ModeB: modeBExporter{}, Roster: cliRoster{}, WrapBackend: coreWrapBackend, ProjectHint: coreProjectHint, LiveRef: coreLiveRef, AllowNetworkFS: os.Getenv(envAllowNetworkFS) == envValueOn, Wall: cliClock().Now,
		Context:     wire.ContextHosts{RenderHistory: renderHistoryHost{}, Setup: setupHost{}, SupportBundle: supportBundleHost{}, Fetch: contextHost{}, Render: renderHost{}, Doctor: doctorHost{}},
		Sync:        syncHost{},                                                    // P3-SYNC-1: lore sync|land|explain are ops/sync operations run by this host
		Coord:       coordHost{},                                                   // P3-COORD-1: task claim|next|…, hold, gate, inbox, mission status (coord_ops_host.go)
		Sessions:    wire.SessionHosts{Session: sessionHost{}, Agent: agentHost{}}, // P3-RUNS-4, P3-RUNS-6: session list, agent start
		OrgCommands: orgHost{}, Whoami: whoamiHost{}}                               // PASS3 ONE-PATH: lore org, lore whoami (p3-g-org)
	// Org, RepoIsClient, OrgStores, OrgEntryStore, OrgSigners, DefaultTarget
	// and GuardRepoWrite: the core's org resolution, the same code cmd/lore
	// runs (FA-OLDCODE-1, P3-ORG-3).
	coreOrgResolution().Apply(&o)
	return hosthooks.Options(o, coreHookList())
}

// coreHooks are this binary's host hooks (wire/host): the shared ones
// (notices, compat flags, resource/meta delete --purge) with this binary's
// compat options, its own .gitignore + git wiring step (the git hooks call
// v0.1.17's sync commands, P3-HOOKS-2), the org write check (v0.1.17 org
// resolution) and the F1 not-found explanation (the live ref). One list,
// built once (coreHookList): coreOptions and mountCore share it.
func buildCoreHooks() []hosthooks.Hook {
	compat := hosthooks.CompatOptions{
		Printed: func() { corePrinted = true },
		BeforeCommand: func(cmd *cobra.Command, readOnly bool) {
			// P3-AUDIT-4: `lore audit …` runs no start-of-command sync
			// pass: the pass would record as external writes (and so
			// accept) the very changes audit verify looks for.
			if cmd.Parent() != nil && cmd.Parent().Name() == opsaudit.Noun {
				return
			}
			coreSyncBefore(cmd.Context(), readOnly)
		},
		Filed: func() string { return coreOrgResolution().Routed() },
		Wrap: func(c *cobra.Command) {
			if c.Parent() != nil && c.Parent().Name() == opstask.Noun && c.Name() == op.VerbList {
				wrapTaskListMergedCheck(c)
			}
		},
	}
	hooks := hosthooks.Default(coreWorkDir())
	hooks = hosthooks.Replace(hooks, hosthooks.Hook{Name: hosthooks.HookGitignore, Before: func(ctx context.Context, run *hosthooks.Run) error {
		// --read-only writes nothing, not even the .gitignore lines or
		// the git wiring (v0.1.17); a live ref op writes no row file.
		if !hosthooks.NeedsGitignore(run) {
			return nil
		}
		if err := ensureCoreGitignore(ctx); err != nil {
			return coreErr(err)
		}
		return nil
	}})
	hooks = hosthooks.Replace(hooks, hosthooks.CompatFlagsHook(compat))
	return append(hooks,
		hosthooks.Hook{Name: "org-write-check", Before: func(ctx context.Context, run *hosthooks.Run) error {
			return coreOrgWriteCheck(ctx, run.Entry, run.Decoder)
		}},
		hosthooks.Hook{Name: "not-found", OnError: explainNotFound},
	)
}

// coreHooksOnce / coreHooks cache buildCoreHooks (a function, not a var
// initializer: the hooks reach coreOptions through the sync pass, which
// would be an initialization cycle).
var (
	coreHooksOnce sync.Once
	coreHooks     []hosthooks.Hook
)

// coreHookList returns this process's host hooks, built on first use.
func coreHookList() []hosthooks.Hook {
	coreHooksOnce.Do(func() { coreHooks = buildCoreHooks() })
	return coreHooks
}

// coreOrgRes is the core's org resolution of this process (one command per
// process, like rootCmd), built by coreOrgResolution on first use.
var (
	coreOrgResOnce sync.Once
	coreOrgRes     *wire.OrgResolution
)

// coreOrgResolution is the org resolution for the working directory as of
// its first use (v0.1.17 read the working directory when a hook first
// asked, not at mount time), with the person's terminal for F14's
// prompt-or-refuse and v0.1.17's F16 write-time leak check
// (coreRepoWriteCheck). A HOME that cannot be resolved leaves the lore
// home empty: config.yaml is then not found and no org resolves, as
// gatherOrgSituation's error answered before (writes stay in the repo,
// and the commit hooks still check).
func coreOrgResolution() *wire.OrgResolution {
	coreOrgResOnce.Do(func() {
		home, _ := identity.Dir() // no HOME: no config, no org (see above)
		dir, err := os.Getwd()
		if err != nil {
			dir = "."
		}
		coreOrgRes = wire.StandaloneOrgResolution(home, dir)
		coreOrgRes.Interactive = stdinIsTTY
		coreOrgRes.RepoWriteCheck = coreRepoWriteCheck
	})
	return coreOrgRes
}

// coreRepoWriteCheck is OrgResolution.RepoWriteCheck: v0.1.17's F16 check
// of a row about to be written into a client repo (leakCheckRouteWrite's
// body; the resolution already decided the repo needs it). A row moved out
// of a store may quote its own source row (movedSource).
func coreRepoWriteCheck(ctx context.Context, root, kind, text string) error {
	it := leakcheck.Item{Where: "this " + kind, Content: []byte(text)}
	lc := leakContext{action: leakActionWrite, what: "this " + kind}
	if src := movedSource(ctx); src != "" {
		it.AllowRows = []string{src}
		lc = leakContext{action: leakActionMove, what: "this move", source: src}
	}
	return coreErr(runLeakCheck(ctx, root, []leakcheck.Item{it}, false, lc))
}

// coreWorkDir is the folder the command runs in, for `_o.dir` (PROV6:
// relative to the repo root, omitted at the root). "" when the working
// directory cannot be read: wire then uses RepoDir, so dir is left out.
func coreWorkDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

// mountCore builds the core and mounts its commands under root. A failure
// here is a programmer error (a registry or mount clash) and stops the
// binary, like op.Define's panics.
func mountCore(root *cobra.Command) {
	cwd, home := coreMountDirs() // cl-gate: a test binary points this at a temp repo (main.go)
	app, err := wire.New(coreOptions(cwd, home))
	if err != nil {
		panic(err)
	}
	coreApp = app
	runner := hosthooks.NewRunner(app, coreHookList())
	coreRunner = runner
	if err := cli.Mount(root, app.Registry(), runner,
		cli.WithInteractive(stdinIsTTY),
	); err != nil {
		panic(err)
	}
	// compat flags (+ the task list merged check), resource/meta delete
	// --purge, the running command for the F1 explanation (wire/host).
	hosthooks.Install(root, runner)
	markCorePrinted(root)
	mountSQLShortcut(root)
	mountSearchShortcut(root) // CLI_REFERENCE 2.5: lore search "<q>" (P3-MISC-1)
	mountBareVerbs(root)      // P3-MISC-3: lore why-context [--cut] (misc_shortcuts.go)
	mountRowShortcuts(root)   // CLI 2.7/2.13: lore show <id>, bare lore lint
	attachSyncHost(root)      // P3-SYNC-1: bare lore sync|land|explain, lore sync trash, the hidden lore sync hook
	withMigrateTeamLock(root) // FORMAT_EVOLUTION 7.2: migrate run|rollback|unlock --team get the signals-ref team lock
	// The coordination verbs (task claim|next|…, hold, gate, inbox, mission
	// status) are ops/coord operations over coordHost (P3-COORD-1); only
	// `task hook` stays a host command under the task noun.
	if t, _, ferr := root.Find([]string{opstask.Noun}); ferr == nil && t.Name() == opstask.Noun {
		t.AddCommand(taskCoordCommands()...)
		guardTaskCoreCommands(root)
		withTaskShowSignals(root) // F7 Debug: task show --signals (task_show_signals.go)
	}
	if alias := newCommitShowAlias(root); alias != nil {
		root.AddCommand(alias)
	}
	root.AddCommand(newAPICommand(), newSpecCommand())
}

// commitShowAlias is v0.1.17's spelling of `lore commit show <sha>`.
const commitShowAlias = "commit-show"

// newCommitShowAlias is the hidden `lore commit-show <sha>` of v0.1.17: it
// parses its arguments with the mounted `lore commit show` command's flags
// and runs that command, so both spellings share one implementation. nil
// when commit show is not mounted.
func newCommitShowAlias(root *cobra.Command) *cobra.Command {
	show, _, err := root.Find([]string{opslink.CommitNoun, "show"})
	if err != nil || show.Name() != "show" || show.RunE == nil {
		return nil
	}
	return &cobra.Command{
		Use:                commitShowAlias + " <sha>",
		Short:              "v0.1.17 spelling of `lore commit show`",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			show.SetContext(cmd.Context())
			show.SetOut(cmd.OutOrStdout())
			show.SetErr(cmd.ErrOrStderr())
			if err := show.ParseFlags(args); err != nil {
				return show.FlagErrorFunc()(show, err)
			}
			if h, _ := show.Flags().GetBool("help"); h {
				return show.Help()
			}
			return show.RunE(show, show.Flags().Args())
		},
	}
}

// jsonIndent is v0.1.17's printJSON indent (main.go's error envelope; the
// core commands' indented --json is wire/host's compat flags hook).
const jsonIndent = "  "

// coreOrgWriteCheck runs v0.1.17's org checks on a write that takes --to
// (an op with a `to` input), before the core opens or guards anything:
//
//   - no --to while LORE_ORG / `lore org use` names another org than the
//     store link (F10): E_ORG_DISAGREES. It comes first so an unreachable
//     store of the wrongly chosen org never hides it behind E_LINK_UNKNOWN;
//     naming the target with --to is the way through (DisagreesHint).
//   - --to <org> (or team) while the repo has several links to that org
//     and none is the default (TEAM-032): refused with the candidates.
//     The core attaches the org under one linked folder for reads; without
//     this check the write landed silently in whichever link came first.
//
// Anything it cannot decide (decode error, org resolution failure) is left
// to the normal path, which reports it.
func coreOrgWriteCheck(ctx context.Context, e op.Entry, d op.Decoder) error {
	if e.Spec().Access != op.AccessWrite {
		return nil
	}
	in, err := e.Decode(d)
	if err != nil {
		return nil // the engine's Run reports the decode error itself
	}
	to, takesTo := "", false
	for _, tv := range in.Texts() {
		if tv.Field == engine.InputTarget {
			to, takesTo = tv.Text, true
		}
	}
	if !takesTo {
		return nil
	}
	s, err := coreSituationOnce(ctx)
	if err != nil || s == nil || s.ResolveErr != nil {
		return nil
	}
	r := s.Result
	if to == "" {
		if r.Disagreement != nil {
			return coreErr(orgResolveCLIError(&orgresolve.Error{Kind: orgresolve.KindDisagrees, Message: r.Disagreement.Message, Hint: orgresolve.DisagreesHint}))
		}
		return nil
	}
	if r.Org == nil || (to != r.Org.Name && to != attach.ReservedTeam) {
		return nil
	}
	ti := oldstores.TargetInputs{To: oldstores.ToOrg, Org: r.Org.Name}
	for _, l := range r.Links {
		if l.Entry == r.Org.Name {
			ti.Links = append(ti.Links, oldstores.TargetLink{Folder: l.Folder, Default: l.Default, Own: l.Own})
		}
	}
	if len(ti.Links) < 2 {
		return nil
	}
	if _, rerr := oldstores.ResolveTarget(ti); errors.Is(rerr, oldstores.ErrTwoLinksNoDefault) {
		return coreErr(targetCLIError(rerr))
	}
	return nil
}

// ensureCoreGitignore makes sure the repo's .gitignore lists the core's
// local files (cache, lock), which `lore init` of v0.1.17 did not know
// about. It runs once per process, on the first core command (never at
// mount: unrelated commands and folders outside a project are left alone),
// and not in read-only mode. The actor itself is resolved by wire's
// identity port (wire.Options.Org).
func ensureCoreGitignore(ctx context.Context) error {
	if gitignoreChecked {
		return nil
	}
	gitignoreChecked = true
	if os.Getenv(envReadOnly) == envValueOn {
		return nil
	}
	root := coreProjectRoot()
	if root == "" {
		return nil
	}
	if err := ensureGitignore(ctx, root); err != nil {
		return errcodes.New(errcodes.Internal, "update .gitignore").WithCause(err)
	}
	ensureCoreGitSetup(ctx, root)
	return nil
}

// coreProjectRoot is the lore project the running command works in: the
// v0.1.17 resolution (.lore/lore.db or lore.yaml), else the nearest .lore/
// folder inside this git checkout. Why the fallback: lore.db is gitignored,
// so a fresh clone where only core commands ran has .lore/data and no
// lore.db, and the old resolution alone skipped its git wiring: no merge
// driver, so the first pull of a row both sides wrote (an actor row on two
// machines, F18) was a plain text conflict. "" = not in a lore project.
func coreProjectRoot() string {
	if pctx, err := projresolve.Resolve(projresolve.Inputs{Dir: oldProjectDir()}); err == nil {
		return pctx.ProjectRoot
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return liveProjectRoot(cwd)
}

// ensureCoreGitSetup wires git for lore data (merge driver, .gitattributes,
// the hooks) in the project at root, as v0.1.17's per-command sync did.
//
// Why: the core never runs that sync, so a clone where only core commands
// ran (a teammate's fresh checkout of a client repo) had no pre-commit /
// commit-msg / pre-push hooks at all, and the F16 leak check that lives in
// them never ran: internal text and full actor rows committed silently.
// Not a git repo, or no git, is fine (rows still work on files); another
// failure is a warning, never a refused command.
func ensureCoreGitSetup(ctx context.Context, root string) {
	// LORE_SYNC=0 turns sync off, git wiring included, and LORE_SYNC_GIT=0
	// turns only the wiring off, as in v0.1.17 (finishSyncSessions; the
	// caller already skips read-only runs): no hook may then run a sync
	// pass behind the person's back (a pre-commit bootstrap baselined
	// lore.db, so turning sync on later found nothing to export).
	if os.Getenv(envSync) == envValueOff || os.Getenv(envSyncGit) == envValueOff {
		return
	}
	// With the lore.db read copy present, wire through v0.1.17's wireGit:
	// its fingerprint of the watched files (git config, .gitattributes, the
	// ignore files, hooks) skips the git calls while nothing changed and
	// re-checks after an edit, so "git ignores lore data" is said once per
	// state of the ignore files, as every v0.1.17 command did.
	dbPath := filepath.Join(root, projresolve.MarkerDir, projresolve.ModeAFile)
	if fi, serr := os.Lstat(dbPath); serr == nil && fi.Mode().IsRegular() {
		db := dbent.InitDB(dbPath)
		defer func() { _ = db.Close() }() // a read-mostly state cache; nothing to recover on close
		if dbent.ApplyPragmas(db) == nil {
			wireGit(ctx, db, root)
			return
		}
	}
	res, err := ensureGitWiring(ctx, root)
	if err != nil {
		if !errors.Is(err, gitsetup.ErrNotRepo) && !errors.Is(err, gitsetup.ErrNoGit) {
			fmt.Fprintln(os.Stderr, style.Warn(syncLogPrefix+"git wiring: "+err.Error()))
		}
		return
	}
	reportGitWiring(root, res, false)
}

// gitignoreChecked records that ensureCoreGitignore ran (one command per
// process, like rootCmd).
var gitignoreChecked bool

// guardCoreRepoWrite is wire.Options.GuardRepoWrite for this binary's
// other core apps (lore init): the core's F14 decision (OrgResolution).
func guardCoreRepoWrite(ctx context.Context, q wire.WriteQuery, explicit bool) error {
	return coreOrgResolution().GuardRepoWrite(ctx, q, explicit)
}

// coreRepoIsClient is the repo's role for the old commands that still ask
// (render scopes, roster): the core's answer (OrgResolution, fail closed).
func coreRepoIsClient(ctx context.Context) (bool, error) {
	return coreOrgResolution().RepoIsClient(ctx)
}

// coreOrgStores is the org stores the core attaches, for the old commands
// that still ask (the leak index): the core's answer (OrgResolution).
func coreOrgStores(ctx context.Context) ([]attach.Store, error) {
	return coreOrgResolution().OrgStores(ctx)
}

// coreSituationOnce is gatherOrgSituation for the working directory,
// computed once per process (the core asks for it at open; it may fetch
// the org stores).
func coreSituationOnce(ctx context.Context) (*orgSituation, error) {
	if !coreSituationDone {
		coreSituationDone = true
		dir, err := os.Getwd()
		if err != nil {
			coreSituationErr = err
		} else {
			coreSituation, coreSituationErr = gatherOrgSituation(ctx, dir)
		}
	}
	return coreSituation, coreSituationErr
}

// Cache of coreSituationOnce (one command per process, like rootCmd).
var (
	coreSituationDone bool
	coreSituation     *orgSituation
	coreSituationErr  error
)

// coreErr turns a v0.1.17 error (errcodes.CLIError) returned into a core
// command into the core's error type, keeping its code, message and hint:
// the core's surfaces print any other error as a bare E_INTERNAL "internal
// error" (they never leak foreign error text), which would hide a real
// refusal like E_LINK_UNKNOWN. A code the core does not register becomes
// E_INTERNAL with the old text kept as the cause.
func coreErr(err error) error {
	if err == nil {
		return nil
	}
	var old *errcodes.CLIError
	if !errors.As(err, &old) {
		return err
	}
	code := errs.Code(old.Code)
	if !code.Known() {
		return errs.Wrap(errs.Internal, "lore: "+old.Message, err)
	}
	e := errs.New(code, old.Message)
	if old.Hint != "" {
		e = e.WithHint(old.Hint)
	}
	return e
}

// newAPICommand is `lore api` (spec 9.7): the REST surface of every
// migrated operation on 127.0.0.1, closed by default (bearer token from
// <home>/api-token, created on first use), served through the same engine
// Run path as the CLI.
func newAPICommand() *cobra.Command {
	port := apiDefaultPortValue
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Serve the local REST API (127.0.0.1, bearer token in ~/.lore/api-token)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			home, err := loreHome()
			if err != nil {
				return err
			}
			// Open the core now so a broken store fails at start, not on
			// the first request.
			if _, engineErr := coreApp.Engine(ctx); engineErr != nil {
				return engineErr
			}
			defer func() { _ = coreApp.Close() }() // the server's error is what the caller needs
			actor, err := coreApp.Actor(ctx)
			if err != nil {
				return err
			}
			tok, err := rest.LoadOrCreateToken(home)
			if err != nil {
				return err
			}
			srv, err := rest.New(coreApp.Registry(), &apiRunner{inner: coreRunner}, rest.Options{Token: tok, Actor: actor, LoreVersion: version})
			if err != nil {
				return err
			}
			ln, err := rest.Listen(port)
			if err != nil {
				return err
			}
			return srv.Serve(ctx, ln, cmd.ErrOrStderr(), apiNote+rest.TokenPath(home))
		},
	}
	cmd.Flags().IntVar(&port, "port", apiDefaultPortValue, "port on 127.0.0.1 (0 picks a free one)")
	tok := &cobra.Command{Use: "token", Short: "Manage the local API token"}
	tok.AddCommand(&cobra.Command{
		Use:   "rotate",
		Short: "Replace the API token (every client holding the old one gets 401)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := loreHome()
			if err != nil {
				return err
			}
			if _, rotateErr := rest.RotateToken(home); rotateErr != nil {
				return rotateErr
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "new token written to "+rest.TokenPath(home))
			return err
		},
	})
	cmd.AddCommand(tok)
	return cmd
}

// newSpecCommand is `lore spec docs|openapi`: write the generated command
// and error reference and the OpenAPI document of the real registry. The
// drift test (wire/docs_test.go) fails when the committed files differ.
func newSpecCommand() *cobra.Command {
	dir := specDefaultDir
	cmd := &cobra.Command{Use: "spec", Short: "Write the generated CLI reference and OpenAPI file"}
	cmd.PersistentFlags().StringVar(&dir, "out", specDefaultDir, "folder holding "+specCommandsFile+" and "+specOpenAPIFile)
	// spec 9.6: --check writes nothing and fails when the committed
	// reference is stale (a CI-able drift gate).
	check := false
	cmd.PersistentFlags().BoolVar(&check, "check", false, "write nothing; exit non-zero when the committed file is stale")
	write := func(file string) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			path := filepath.Join(dir, file)
			if check {
				drift, err := docs.Stale(coreApp.Registry(), path)
				if err != nil {
					return err
				}
				if checkErr := docs.CheckError(drift); checkErr != nil {
					return checkErr // E_CONFIG_INVALID naming every stale file + "run: lore spec docs && lore spec openapi"
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "current "+path)
				return err
			}
			if err := docs.Update(coreApp.Registry(), path); err != nil {
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "wrote "+path)
			return err
		}
	}
	cmd.AddCommand(
		&cobra.Command{Use: "docs", Short: "Write " + specCommandsFile + " (commands and errors blocks)", Args: cobra.NoArgs, RunE: write(specCommandsFile)},
		&cobra.Command{Use: "openapi", Short: "Write " + specOpenAPIFile, Args: cobra.NoArgs, RunE: write(specOpenAPIFile)},
	)
	return cmd
}

// markCorePrinted makes every generated command outside the compat nouns
// (store, migrate, …) mark its error as printed by surface/cli, so main
// never prints it a second time: an E_INTERNAL (exit 1, the same code as a
// usage error) was printed twice, once with and once without its cause.
func markCorePrinted(c *cobra.Command) {
	for _, sub := range c.Commands() {
		markCorePrinted(sub)
	}
	if !cli.Generated(c) || c.RunE == nil {
		return
	}
	// A flag error happens before RunE: the surface prints cobra's usage
	// error itself, so mark it printed there too (one line, not two).
	flagErr := c.FlagErrorFunc()
	c.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		corePrinted = true
		return flagErr(cmd, err)
	})
	inner := c.RunE
	c.RunE = func(cmd *cobra.Command, args []string) error {
		defer func() { corePrinted = true }()
		return inner(cmd, args)
	}
}

// corePrinted is set when a mounted memory command ran (or cobra refused
// its flags): its error, if any, is already printed. One command runs per
// process, like rootCmd.
var corePrinted bool

// coreExitCode returns the exit status of an error a core command already
// printed (surface/cli prints its own errors), and false for any other
// error, which main prints in the v0.1.17 format. An E_INTERNAL or a cobra
// usage error (both exit 1) of a mounted memory command counts as printed
// too, so it is never printed twice.
func coreExitCode(err error) (int, bool) {
	code := cli.ExitCode(err)
	// FLAG2: a usage error is now an exitError with its own exit (20), so
	// "not printed by the core" is the fallback exit for foreign errors.
	return code, code != cli.ExitUnknown || (err != nil && corePrinted)
}

// coreSetSuccessor sets (next != "") or clears (next == "") the
// superseded_by_id of row id in table through the core's write path, and
// sets status too when it is not "" (a superseded decision). The
// --supersedes chain and the supersede repair of `lore doctor --fix` use
// it, since the core-owned tables are never written through lore.db.
func coreSetSuccessor(ctx context.Context, table row.Table, id, next, status string) error {
	if coreApp == nil {
		return errcodes.New(errcodes.Internal, "the 0.1.18 core is not mounted")
	}
	rid, err := ids.ParseAny(id)
	if err != nil {
		return errs.Wrap(errs.InvalidID, id+" is not an id", err)
	}
	var v value.Value = value.Null{}
	if next != "" {
		nid, perr := ids.ParseAny(next)
		if perr != nil {
			return errs.Wrap(errs.InvalidID, next+" is not an id", perr)
		}
		if v, err = value.RefOf(nid); err != nil {
			return err
		}
	}
	patch := map[string]value.Value{fieldSupersededBy: v}
	if status != "" {
		t, terr := value.TextOf(status)
		if terr != nil {
			return terr
		}
		patch[fieldStatus] = t
	}
	_, err = coreApp.Update(ctx, engine.Ref{Table: table, ID: rid}, patch)
	return err
}

// coreTableOf returns the core-owned table whose ids carry id's prefix
// (wire.CoreTables), false for any other id.
func coreTableOf(id string) (row.Table, bool) {
	p, _, ok := strings.Cut(id, "_")
	if !ok {
		return "", false
	}
	for _, d := range schema.Builtin() {
		if string(d.Prefix) == p && slices.Contains(wire.CoreTables(), d.Name) {
			return d.Name, true
		}
	}
	return "", false
}

// Core field names the helpers here read and write (schema/builtin_*.go).
const (
	fieldBody         = "body"
	fieldTitle        = "title"
	fieldSupersededBy = "superseded_by_id"
	fieldStatus       = "status"
)

// explainNotFound is v0.1.17's F1 answer (live_flags.go
// liveMissingExplain) for an E_NOT_FOUND of the running command's id
// argument: what live knows about the id, else the `lore sync peek
// <remote>/<branch> <table>` hint. An error that already carries a hint,
// or a command with no argument, is returned as it is.
func explainNotFound(ctx context.Context, run *hosthooks.Run, err error) error {
	var e *errs.Error
	if run.Cmd == nil || len(run.Args) == 0 || !errors.As(err, &e) || e.Code != errs.NotFound || e.Hint != "" {
		return err
	}
	why, hint, ok := liveMissingExplain(ctx, run.Cmd, run.Args[0])
	switch {
	case !ok:
		return err
	case why != "":
		return errs.New(errs.NotFound, why)
	}
	return e.WithHint(hint)
}
