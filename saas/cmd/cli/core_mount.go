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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"saas/pkg/constants"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	opsarchitecturenote "github.com/thesatellite-ai/lore/ops/architecturenote"
	opsbehaviour "github.com/thesatellite-ai/lore/ops/behaviour"
	opscomment "github.com/thesatellite-ai/lore/ops/comment"
	opscookbookrecipe "github.com/thesatellite-ai/lore/ops/cookbookrecipe"
	opsdecision "github.com/thesatellite-ai/lore/ops/decision"
	opshandoff "github.com/thesatellite-ai/lore/ops/handoff"
	opshotfix "github.com/thesatellite-ai/lore/ops/hotfix"
	opsincident "github.com/thesatellite-ai/lore/ops/incident"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opslink "github.com/thesatellite-ai/lore/ops/link"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsmission "github.com/thesatellite-ai/lore/ops/mission"
	opspattern "github.com/thesatellite-ai/lore/ops/pattern"
	opsplan "github.com/thesatellite-ai/lore/ops/plan"
	opsplaybook "github.com/thesatellite-ai/lore/ops/playbook"
	opsprompt "github.com/thesatellite-ai/lore/ops/prompt"
	opsreminder "github.com/thesatellite-ai/lore/ops/reminder"
	opsrule "github.com/thesatellite-ai/lore/ops/rule"
	opssnapshot "github.com/thesatellite-ai/lore/ops/snapshot"
	opssuggestion "github.com/thesatellite-ai/lore/ops/suggestion"
	opstag "github.com/thesatellite-ai/lore/ops/tag"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	opstasklist "github.com/thesatellite-ai/lore/ops/tasklist"
	opstastepref "github.com/thesatellite-ai/lore/ops/tastepref"
	opstechdoc "github.com/thesatellite-ai/lore/ops/techdoc"
	opsworkflow "github.com/thesatellite-ai/lore/ops/workflow"
	opsworkspace "github.com/thesatellite-ai/lore/ops/workspace"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/surface/cli"
	"github.com/thesatellite-ai/lore/surface/docs"
	"github.com/thesatellite-ai/lore/surface/rest"
	"github.com/thesatellite-ai/lore/wire"

	"dbent"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitsetup"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/lorecfg"
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
var coreRunner *compatRunner

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
	return wire.Options{RepoDir: repoDir, Home: home, Org: coreOrg, GuardRepoWrite: guardCoreRepoWrite,
		DefaultTarget: coreDefaultTarget, RepoIsClient: coreRepoIsClient, OrgStores: coreOrgStores, OrgSigners: coreOrgSigners,
		WorkDir: coreWorkDir(), Version: version, Audit: &coreAuditSink{},
		ModeB: modeBExporter{}, Notice: coreNotice, WrapBackend: coreWrapBackend, ProjectHint: coreProjectHint, AllowNetworkFS: os.Getenv(envAllowNetworkFS) == envValueOn, Wall: cliClock().Now}
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

// coreOrgSigners lists every org entry of ~/.lore/config.yaml that has an
// identity, with its store urls and storeSignature's author (Q-R2-1: the
// org identity signs every commit to an org store, never the person's own
// git email). An unreadable config lists none (the FLAG4 rule signs).
func coreOrgSigners(ctx context.Context) ([]wire.OrgSigner, error) {
	dir, err := identity.Dir()
	if err != nil {
		return nil, err
	}
	cfg, _, err := lorecfg.LoadTeam(ctx, dir, lorecfg.Options{})
	if err != nil || cfg == nil {
		return nil, err
	}
	var out []wire.OrgSigner
	for i := range cfg.Orgs {
		org := &cfg.Orgs[i]
		if strings.TrimSpace(org.Identity.Key) == "" || len(org.Stores) == 0 {
			continue
		}
		sig := storeSignature(org)
		s := wire.OrgSigner{Name: sig.Name, Email: sig.Email}
		for _, st := range org.Stores {
			s.StoreURLs = append(s.StoreURLs, st.URL)
		}
		out = append(out, s)
	}
	return out, nil
}

// mountCore builds the core and mounts its commands under root. A failure
// here is a programmer error (a registry or mount clash) and stops the
// binary, like op.Define's panics.
func mountCore(root *cobra.Command) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	home, herr := loreHome()
	if herr != nil {
		home = filepath.Join(os.TempDir(), "lore-home-unresolved")
	}
	app, err := wire.New(coreOptions(cwd, home))
	if err != nil {
		panic(err)
	}
	coreApp = app
	runner := &compatRunner{app: app}
	coreRunner = runner
	if err := cli.Mount(root, app.Registry(), runner,
		cli.WithInteractive(func() bool { return stdinIsTTY() }),
	); err != nil {
		panic(err)
	}
	for _, noun := range coreNouns {
		if g, _, ferr := root.Find([]string{noun}); ferr == nil && g.Name() == noun {
			for _, c := range g.Commands() {
				// Only the surface's operation commands: a host command
				// mounted under a core noun (project shared-init) prints
				// its own errors, and a group has no RunE to wrap.
				if cli.Generated(c) && c.RunE != nil {
					addCompatFlags(c, runner)
					wrapCoreNotFound(c, runner)
					if noun == opstask.Noun && c.Name() == op.VerbList {
						wrapTaskListMergedCheck(c)
					}
				}
			}
		}
	}
	markCorePrinted(root)
	mountSQLShortcut(root)
	mountRowShortcuts(root) // CLI 2.7/2.13: lore show <id>, bare lore lint
	// The coordination verbs and mission status stay on the v0.1.17 signals
	// machinery, mounted under the core nouns (after the compat loop: they
	// carry their own v0.1.17 flags).
	if t, _, ferr := root.Find([]string{opstask.Noun}); ferr == nil && t.Name() == opstask.Noun {
		t.AddCommand(taskCoordCommands()...)
		guardTaskCoreCommands(root)
	}
	if m, _, ferr := root.Find([]string{opsmission.Noun}); ferr == nil && m.Name() == opsmission.Noun {
		m.AddCommand(newMissionStatusCommand())
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

// coreNouns are the command groups mounted from the core that keep
// v0.1.17's common flags (addCompatFlags). One list: a migrated noun adds
// its name here.
var coreNouns = []string{
	opsmemory.Noun,
	opspattern.Noun, opsplaybook.Noun, opsprompt.Noun,
	opsrule.Noun, opsdecision.Noun, opshotfix.Noun,
	opstag.Noun, opscomment.Noun, opslink.Noun, opslink.CommitNoun,
	opstask.Noun, opstasklist.Noun, opsmission.Noun, opsplan.Noun,
	opsreminder.Noun,
	opsinfra.NounActor, opsinfra.NounProject, opsinfra.NounRepo, opsinfra.NounMachine,
	opsarchitecturenote.Noun, opsbehaviour.Noun, opscookbookrecipe.Noun, opsincident.Noun,
	opssuggestion.Noun, opstastepref.Noun, opsworkflow.Noun, opsworkspace.Noun,
	opshandoff.Noun, opssnapshot.Noun, opstechdoc.Noun, externalSourceNoun,
}

// externalSourceNoun is the CLI spelling of opsexternalsource.Noun
// ("external_source").
const externalSourceNoun = "external-source"

// v0.1.17 flags every `lore memory …` command still accepts on the core
// (CLI parity, phase 3). --read-only and --project keep working; --db and
// --confirm are refused with the replacement named (spec-deltas.md);
// --allow-secrets is gone with no alias (O12a: cobra's unknown flag).
const (
	jsonIndent = "  "
)

// compatRunner is the core's Runner as the old binary mounts it: it adds
// v0.1.17's read-only mode (--read-only, LORE_READ_ONLY=1: a write
// operation is refused with E_READ_ONLY before it runs) and returns the
// refusal a compat flag set for this run. REST (`lore api`) runs through
// the same runner (coreRunner); the CLI-only flags are never set there.
type compatRunner struct {
	app *wire.App
	// pending is the refusal of the running command's compat flags (one
	// command runs per process; set by the command's RunE wrapper).
	pending error
	// readOnly is --read-only of the running command.
	readOnly bool
	// cmd and args are the running command (wrapCoreNotFound), for the
	// F1 not-found explanation.
	cmd  *cobra.Command
	args []string
}

// Run implements op.Runner.
func (r *compatRunner) Run(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	if r.pending != nil {
		return op.Result{}, r.pending
	}
	// --read-only writes nothing, not even the .gitignore lines or the git
	// wiring (v0.1.17: a read-only command leaves the checkout untouched).
	if !r.readOnly {
		if err := ensureCoreGitignore(ctx); err != nil {
			return op.Result{}, coreErr(err)
		}
	}
	if e.Spec().Access == op.AccessWrite && (r.readOnly || os.Getenv(envReadOnly) == envValueOn) {
		return op.Result{}, errs.New(errs.ReadOnly, "command requires write access but read-only mode is active").
			WithHint("unset --read-only and LORE_READ_ONLY")
	}
	if err := coreOrgWriteCheck(ctx, e, d); err != nil {
		return op.Result{}, err
	}
	res, err := r.app.Run(ctx, e, d, c)
	if err != nil {
		return res, r.explainNotFound(ctx, err)
	}
	return res, nil
}

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

// addCompatFlags gives one mounted memory command v0.1.17's common flags
// (--db, --project where the op has none, --read-only, and --confirm on the
// commands that ask) and v0.1.17's indented --json output.
func addCompatFlags(c *cobra.Command, r *compatRunner) {
	var db, project string
	var readOnly, confirm bool
	if c.Flags().Lookup(constants.FlagDB) == nil {
		c.Flags().StringVar(&db, constants.FlagDB, "", "v0.1.17 lore.db path: not used by this command (refused)")
	}
	if c.Flags().Lookup(constants.FlagProject) == nil {
		// show/edit/delete/… address a memory by its id, which is unique
		// across projects: --project is accepted and has nothing to do.
		c.Flags().StringVar(&project, constants.FlagProject, "", "accepted for v0.1.17 scripts; a row id already names its project")
	}
	if c.Flags().Lookup(constants.FlagReadOnly) == nil {
		c.Flags().BoolVar(&readOnly, constants.FlagReadOnly, false, "refuse writes (also LORE_READ_ONLY=1)")
	}
	if c.Flags().Lookup(cli.FlagYes) != nil && c.Flags().Lookup(constants.FlagConfirm) == nil {
		c.Flags().BoolVar(&confirm, constants.FlagConfirm, false, "v0.1.17 spelling: refused, use --yes")
	}
	flagErr := c.FlagErrorFunc()
	c.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		corePrinted = true // the surface prints cobra's usage error itself
		return flagErr(cmd, err)
	})
	inner := c.RunE
	c.RunE = func(cmd *cobra.Command, args []string) error {
		// Every error a generated command returns was printed by surface/cli.
		defer func() { corePrinted = true }()
		r.readOnly = readOnly
		switch {
		case cmd.Flags().Changed(constants.FlagConfirm):
			r.pending = errs.New(errs.InvalidInput, "--confirm was renamed: confirm with --yes").
				WithHint("rerun with --yes instead of --confirm")
		case cmd.Flags().Changed(constants.FlagDB):
			r.pending = errs.New(errs.Unsupported, "--db is not supported by this command: its rows are row files read from the repository, not lore.db").
				WithHint("run the command inside the repository (or name a store with --store / --to)")
		}
		applyEnvDefaults(cmd)
		coreSyncBefore(cmd.Context(), readOnly)
		asJSON, _ := cmd.Flags().GetBool(cli.FlagJSON)
		if !asJSON {
			// Buffered so v0.1.17's "filed:" line (printRoute: where a
			// routed write went and why) goes right under the success line,
			// before the op's own detail lines (scope: …). Prompts go to
			// stderr, so nothing interactive waits behind the buffer.
			var buf bytes.Buffer
			out := cmd.OutOrStdout()
			cmd.SetOut(&buf)
			err := inner(cmd, args)
			cmd.SetOut(out)
			b := buf.Bytes()
			if err == nil && coreRouted != "" {
				b = insertAfterFirstLine(b, "  filed: "+coreRouted+"\n")
			}
			_, werr := out.Write(b)
			return errors.Join(err, werr)
		}
		var buf bytes.Buffer
		out := cmd.OutOrStdout()
		cmd.SetOut(&buf)
		err := inner(cmd, args)
		cmd.SetOut(out)
		return errors.Join(err, writeIndented(out, buf.Bytes()))
	}
}

// envFlagDefaults are v0.1.17's environment fallbacks for a row command's
// scope flags (projresolve: flag > env): LORE_PROJECT_ID for --project,
// LORE_REPO for --repo. LORE_DB is not one: --db is refused on core
// commands, and so is its variable (memparity).
var envFlagDefaults = [][2]string{
	{constants.FlagProject, projresolve.EnvProjectID},
	{constants.FlagRepo, projresolve.EnvRepo},
}

// applyEnvDefaults sets each scope flag the command has, and the caller did
// not give, from its environment variable when that is set.
func applyEnvDefaults(cmd *cobra.Command) {
	for _, fe := range envFlagDefaults {
		fl := cmd.Flags().Lookup(fe[0])
		v := os.Getenv(fe[1])
		if fl == nil || fl.Changed || v == "" {
			continue
		}
		_ = cmd.Flags().Set(fe[0], v) // a string flag accepts any value; the op validates it
	}
}

// insertAfterFirstLine returns b with line inserted after its first line
// (at the end when b has no newline; b itself when empty is just line).
func insertAfterFirstLine(b []byte, line string) []byte {
	i := bytes.IndexByte(b, '\n')
	if i < 0 {
		if len(b) > 0 {
			return append(append(append([]byte{}, b...), '\n'), line...)
		}
		return []byte(line)
	}
	out := make([]byte, 0, len(b)+len(line))
	out = append(out, b[:i+1]...)
	out = append(out, line...)
	return append(out, b[i+1:]...)
}

// writeIndented prints JSON the way v0.1.17's printJSON did
// (json.MarshalIndent with two spaces, one trailing newline); anything that
// is not one JSON value is written unchanged.
func writeIndented(w io.Writer, b []byte) error {
	var ind bytes.Buffer
	if err := json.Indent(&ind, bytes.TrimSpace(b), "", jsonIndent); err != nil {
		_, werr := w.Write(b)
		return werr
	}
	ind.WriteByte('\n')
	_, err := w.Write(ind.Bytes())
	return err
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
	if pctx, err := projresolve.Resolve(projresolve.Inputs{}); err == nil {
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

// coreRouteKinds maps a core table to the kind name v0.1.17's routing
// rules (defaults.client_repos, F14) use for it.
var coreRouteKinds = map[row.Table]string{
	schema.TableMemories:  "memory",
	schema.TableRules:     "rule",
	schema.TableDecisions: "decision",
	schema.TableComments:  "comment",
	schema.TableTasks:     "task",
	schema.TableMissions:  "mission",
}

// guardCoreRepoWrite keeps v0.1.17's write routing in force for the rows
// the core writes into the repo store: in a repo whose org config sends
// that kind elsewhere (a client repo: internal knowledge must never land
// in it, D8), or whose store links cannot be read (F10 fail closed,
// E_LINK_UNKNOWN), the write is refused; a write the rules keep in this
// repo still passes the F16 leak check. The core cannot write into a
// v0.1.17 store yet, so "goes elsewhere" is a refusal naming --to.
//
// explicit is `--to repo`: the person chose this repo, which v0.1.17
// accepted (the leak check still runs).
func guardCoreRepoWrite(ctx context.Context, q wire.WriteQuery, explicit bool) error {
	kind, ok := coreRouteKinds[q.Table]
	if !ok {
		return nil
	}
	var to toFlags
	if explicit {
		to.To = toRepo
	}
	wt, dec, err := coreRouteDecision(ctx, to, kind, q)
	if err != nil {
		return err
	}
	if wt.Personal != nil || !dec.Repo {
		// coreDefaultTarget sends this row to the org store when that
		// folder is attached; reaching here means it is not.
		hint := "write it to a store listed in .lore/stores.yaml with --to <name>, or choose this repo with --to repo"
		if wt.Personal == nil && wt.Situation != nil && wt.Situation.Result.Org != nil {
			org := wt.Situation.Result.Org.Name
			hint = "write it to " + org + "'s store with --to " + org + "/" + dec.Folder + " (the folder " + dec.Folder + "/), or choose this repo with --to repo"
		}
		return errs.New(errs.Unsupported, "this repository's lore routing sends a "+kind+" to a store folder lore has not attached ("+dec.Rule+"); name the target with --to").
			WithHint(hint)
	}
	return coreErr(leakCheckRouteWrite(ctx, writeRoute{Repo: true, Rule: dec.Rule, Situation: wt.Situation}, kind, q.Text))
}

// coreRouteDecision is v0.1.17's F14 target decision (routeWrite) for one
// core create of kind: the org situation, the --to given, the team's
// client-repo defaults from lore-org.yaml (until `lore migrate run` moves
// them) joined with the routing.client_repos policy rows (CFG8; per kind a
// store target beats repo, as v0.1.17's effectiveDefault).
func coreRouteDecision(ctx context.Context, to toFlags, kind string, q wire.WriteQuery) (writeTarget, oldstores.TargetDecision, error) {
	wt, err := resolveWriteTarget(ctx, to, q.Text)
	if err != nil {
		return writeTarget{}, oldstores.TargetDecision{}, coreErr(err)
	}
	in, err := targetInputs(ctx, wt.Situation, wt.Target, kind)
	if err != nil {
		return writeTarget{}, oldstores.TargetDecision{}, coreErr(err)
	}
	if len(q.Routes) > 0 {
		merged := maps.Clone(in.StoreDefaults)
		if merged == nil {
			merged = map[string]string{}
		}
		for k, v := range q.Routes {
			if cur, seen := merged[k]; !seen || (cur == oldstores.DefaultRepo && v != oldstores.DefaultRepo) {
				merged[k] = v
			}
		}
		in.StoreDefaults = merged
	}
	dec, err := oldstores.ResolveTarget(in)
	if err != nil {
		return writeTarget{}, oldstores.TargetDecision{}, coreErr(targetCLIError(err))
	}
	return wt, dec, nil
}

// coreDefaultTarget is wire.Options.DefaultTarget for this binary: a
// create of a routed kind (coreRouteKinds) that names no --to goes where
// v0.1.17's routeWrite sent it. In a client repo that is the org store's
// linked folder (attached as the org's name by coreOrgStores), or the
// folder a client-repo default names ("<org>/<folder>", the org folder
// attached as such); in the org's own repo, or with no org, the repo
// store (""), where guardCoreRepoWrite still runs the leak check. A folder
// the core has not attached answers "" too, so the guard refuses it with
// the explicit --to to use (fail closed). The decision's rule is kept in
// coreRouted for the "filed:" line v0.1.17 printed.
func coreDefaultTarget(ctx context.Context, q wire.WriteQuery) (store.Source, error) {
	kind, ok := coreRouteKinds[q.Table]
	if !ok {
		return "", nil
	}
	wt, dec, err := coreRouteDecision(ctx, toFlags{}, kind, q)
	if err != nil {
		return "", err
	}
	if wt.Personal != nil || dec.Repo || wt.Situation == nil || wt.Situation.Result.Org == nil {
		return "", nil
	}
	attached, err := coreOrgStores(ctx)
	if err != nil {
		return "", coreErr(err)
	}
	for _, s := range attached {
		if s.Folder == dec.Folder {
			coreRouted = dec.Rule
			return s.Name, nil
		}
	}
	return "", nil
}

// coreRouted is the F14 rule of the routed create this process made (one
// command per process); printed as v0.1.17's "  filed: <rule>" line.
var coreRouted string

// coreRepoIsClient is wire.Options.RepoIsClient for this binary: the repo
// is a client repo when org resolution (F10, gatherOrgSituation) says its
// role is client (a non-own link in an attached org store, a repos:
// pattern, or an org chosen on purpose that nobody declared own), or when
// a staging entry's own store branch links it that way (entryLinks, read
// by gatherOrgSituation). Links
// that cannot be read, an org resolution refusal or any error answer
// client (fail closed). No org at all, or the org's own repo, is internal.
// The answer is computed once per process: it runs on every core command
// and may read the org stores.
func coreRepoIsClient(ctx context.Context) (bool, error) {
	if !repoClientChecked {
		repoClientChecked = true
		repoClientAnswer, repoClientErr = repoIsClientNow(ctx)
	}
	return repoClientAnswer, repoClientErr
}

// Cache of coreRepoIsClient (one command per process, like rootCmd).
var (
	repoClientChecked bool
	repoClientAnswer  bool
	repoClientErr     error
)

// repoIsClientNow computes coreRepoIsClient's answer.
func repoIsClientNow(ctx context.Context) (bool, error) {
	dir, err := os.Getwd()
	if err != nil {
		return true, err
	}
	s, err := gatherOrgSituation(ctx, dir)
	if err != nil {
		return true, err
	}
	if s.ResolveErr != nil {
		return true, s.ResolveErr
	}
	r := s.Result
	if r.LinkUnknown {
		return true, errors.New("the store links of this repo cannot be read: " + r.LinkDetail)
	}
	if r.Role == orgresolve.RoleClient {
		return true, nil
	}
	// A staging entry chosen on purpose: gatherOrgSituation replaced the
	// links with that entry's own (entryLinks) after the role was decided.
	for _, l := range r.Links {
		if !l.Own {
			return true, nil
		}
	}
	return false, nil
}

// coreOrgStores is wire.Options.OrgStores for this binary (CONFIG_GUIDE
// section 4 step 5b): when an org resolves for the working directory, its
// store is attached under the org entry's name at the folder this repo is
// linked to (the default link, else the first; else the committed
// .lore/org.yaml project; else the store root), and the org folder store
// under "<org>/<org folder>" at its org folder (`--to agency/org`). Nothing
// is written to the repo: in a client repo this is how the internal store
// is reached without its url in a file the client reads (CFG7).
//
// Fail closed: an org refusal or links that cannot be read is an error
// (wire then attaches nothing). A store entry pinned to a non-default
// branch (a staging entry) is attached on that branch (attach.Store.Branch:
// its own clone, its own cache path).
func coreOrgStores(ctx context.Context) ([]attach.Store, error) {
	s, err := coreSituationOnce(ctx)
	if err != nil {
		return nil, err
	}
	if s.ResolveErr != nil {
		return nil, s.ResolveErr
	}
	org := s.Result.Org
	if org == nil || len(org.Stores) == 0 {
		return nil, nil
	}
	if s.Result.LinkUnknown {
		return nil, errors.New("the store links of this repo cannot be read: " + s.Result.LinkDetail)
	}
	attachable := func(sc *lorecfg.Store) bool { return sc != nil }
	// branchOf is the attach branch of an entry store: "" for the default.
	branchOf := func(sc *lorecfg.Store) string {
		if b := sc.EffectiveBranch(); b != lorecfg.DefaultStoreBranch {
			return b
		}
		return ""
	}
	var out []attach.Store
	var linked *orgresolve.Link
	for i := range s.Result.Links {
		l := &s.Result.Links[i]
		if l.Entry != org.Name {
			continue
		}
		if linked == nil || (l.Default && !linked.Default) {
			linked = l
		}
	}
	switch {
	case linked != nil:
		if sc := oldstores.StoreFor(org, linked.StoreURL); attachable(sc) {
			out = append(out, attach.Store{Name: store.Source(org.Name), URL: sc.URL, Folder: linked.Folder, Branch: branchOf(sc)})
		}
	case s.Committed != nil && s.Committed.Org == org.Name && s.Committed.Project != "":
		if sc := &org.Stores[0]; attachable(sc) {
			out = append(out, attach.Store{Name: store.Source(org.Name), URL: sc.URL, Folder: s.Committed.Project, Branch: branchOf(sc)})
		}
	default:
		if sc := &org.Stores[0]; attachable(sc) {
			out = append(out, attach.Store{Name: store.Source(org.Name), URL: sc.URL, Branch: branchOf(sc)})
		}
	}
	for i := range org.Stores {
		sc := &org.Stores[i]
		if sc.OrgFolder == "" || !attachable(sc) {
			continue
		}
		company := attach.Store{Name: store.Source(org.Name + "/" + sc.OrgFolder), URL: sc.URL, Folder: sc.OrgFolder, Branch: branchOf(sc)}
		// org_folder "." is a dedicated company store (F13): its root is
		// the company folder, attached as "<org>/." so company rules
		// (`--to <org>/.`) land there and every teammate listing that
		// store reads them. It is skipped only when the linked-folder
		// entry above already attaches that same root (no link, first
		// store): one store folder keeps one name.
		if sc.OrgFolder == oldstores.RootFolder && slices.ContainsFunc(out, func(o attach.Store) bool { return sameStoreRoot(o, company) }) {
			break
		}
		out = append(out, company)
		break // one org folder per entry (F13)
	}
	return out, nil
}

// sameStoreRoot reports whether a and b both attach the root folder of the
// same store url on the same branch.
func sameStoreRoot(a, b attach.Store) bool {
	isRoot := func(f string) bool { f = strings.Trim(f, "/"); return f == "" || f == oldstores.RootFolder }
	return a.URL == b.URL && a.Branch == b.Branch && isRoot(a.Folder) && isRoot(b.Folder)
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

// coreAddMemory writes one memory through the core's memory.add operation
// (the one write path: scan, validate, audit). Old commands that create a
// memory as a side effect (task done --lesson, learn promote) call it
// instead of writing lore.db. Returns the new id.
func coreAddMemory(ctx context.Context, body, sourceKind, sourceRef string) (string, error) {
	if coreApp == nil {
		return "", errcodes.New(errcodes.Internal, "the 0.1.18 core is not mounted")
	}
	e, ok := coreApp.Registry().Lookup(opsmemory.OpAdd)
	if !ok {
		return "", errcodes.New(errcodes.Internal, "memory.add is not registered")
	}
	in, err := json.Marshal(opsmemory.AddIn{Body: body, Source: sourceKind, SourceRef: sourceRef})
	if err != nil {
		return "", errcodes.New(errcodes.Internal, "encode memory").WithCause(err)
	}
	if err := ensureCoreGitignore(ctx); err != nil {
		return "", coreErr(err)
	}
	res, err := coreApp.Run(ctx, e, op.JSONDecoder{Body: in}, op.Caller{Interactive: stdinIsTTY()})
	if err != nil {
		return "", err
	}
	raw, err := res.JSON()
	if err != nil {
		return "", err
	}
	var out opsmemory.WriteOut
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", errcodes.New(errcodes.Internal, "decode memory.add result").WithCause(err)
	}
	return out.ID, nil
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
			if _, err := coreApp.Engine(ctx); err != nil {
				return err
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
			if _, err := rest.RotateToken(home); err != nil {
				return err
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
				if err := docs.CheckError(drift); err != nil {
					return err // E_CONFIG_INVALID naming every stale file + "run: lore spec docs && lore spec openapi"
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

// coreNotice prints a core notice (wire.Options.Notice) on stderr, so
// stdout (--json, piped output) stays the command's own.
func coreNotice(msg string) { fmt.Fprintln(os.Stderr, msg) }

// wrapCoreNotFound records the running core command and its arguments on
// the runner, so compatRunner.Run can give an E_NOT_FOUND for the id
// argument v0.1.17's F1 explanation before the surface prints it.
// wireLive wraps only the commands registered before the core is mounted.
func wrapCoreNotFound(c *cobra.Command, r *compatRunner) {
	run := c.RunE
	c.RunE = func(cmd *cobra.Command, args []string) error {
		r.cmd, r.args = cmd, args
		return run(cmd, args)
	}
}

// explainNotFound is v0.1.17's F1 answer (live_flags.go
// liveMissingExplain) for an E_NOT_FOUND of the running command's id
// argument: what live knows about the id, else the `lore sync peek
// <remote>/<branch> <table>` hint. An error that already carries a hint,
// or a command with no argument, is returned as it is.
func (r *compatRunner) explainNotFound(ctx context.Context, err error) error {
	var e *errs.Error
	if r.cmd == nil || len(r.args) == 0 || !errors.As(err, &e) || e.Code != errs.NotFound || e.Hint != "" {
		return err
	}
	why, hint, ok := liveMissingExplain(ctx, r.cmd, r.args[0])
	switch {
	case !ok:
		return err
	case why != "":
		return errs.New(errs.NotFound, why)
	}
	return e.WithHint(hint)
}
