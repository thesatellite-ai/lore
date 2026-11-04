// core_mount.go — the strangler seam between this v0.1.17 binary and the
// 0.1.18 core (github.com/thesatellite-ai/lore).
//
// The migrated nouns (memory, migrate) are mounted from the core's
// operation registry (wire + surface/cli), so `lore memory …` reads and
// writes `_v: 2` row files through the new engine. Everything else stays
// on lore.db; the old readers of memories (render, search, TUI, …) see
// the new rows because the lsync import still copies changed row files
// into lore.db before each old command.
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
	"os"
	"path/filepath"
	"saas/pkg/constants"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	opscomment "github.com/thesatellite-ai/lore/ops/comment"
	opsdecision "github.com/thesatellite-ai/lore/ops/decision"
	opshotfix "github.com/thesatellite-ai/lore/ops/hotfix"
	opslink "github.com/thesatellite-ai/lore/ops/link"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsmission "github.com/thesatellite-ai/lore/ops/mission"
	opspattern "github.com/thesatellite-ai/lore/ops/pattern"
	opsplan "github.com/thesatellite-ai/lore/ops/plan"
	opsplaybook "github.com/thesatellite-ai/lore/ops/playbook"
	opsprompt "github.com/thesatellite-ai/lore/ops/prompt"
	opsrule "github.com/thesatellite-ai/lore/ops/rule"
	opstag "github.com/thesatellite-ai/lore/ops/tag"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	opstasklist "github.com/thesatellite-ai/lore/ops/tasklist"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/surface/cli"
	"github.com/thesatellite-ai/lore/surface/docs"
	"github.com/thesatellite-ai/lore/surface/rest"
	"github.com/thesatellite-ai/lore/wire"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/projresolve"
	oldstores "saas/pkg/aicoder/stores"
)

// coreApp is the lazily opened 0.1.18 core of this process: built at
// start-up (no disk access), opened by the first migrated command. One per
// process, like rootCmd; nil only when the working directory or home could
// not be read, in which case mounting reported it.
var coreApp *wire.App

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
	app, err := wire.New(wire.Options{RepoDir: cwd, Home: home, Org: coreOrg, GuardRepoWrite: guardCoreRepoWrite, Audit: &coreAuditSink{}})
	if err != nil {
		panic(err)
	}
	coreApp = app
	runner := &compatRunner{app: app}
	if err := cli.Mount(root, app.Registry(), runner,
		cli.WithInteractive(func() bool { return stdinIsTTY() }),
	); err != nil {
		panic(err)
	}
	for _, noun := range coreNouns {
		if g, _, ferr := root.Find([]string{noun}); ferr == nil && g.Name() == noun {
			for _, c := range g.Commands() {
				addCompatFlags(c, runner)
			}
		}
	}
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
}

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
// refusal a compat flag set for this run. REST (`lore api`) uses the App
// directly, without these CLI-only flags.
type compatRunner struct {
	app *wire.App
	// pending is the refusal of the running command's compat flags (one
	// command runs per process; set by the command's RunE wrapper).
	pending error
	// readOnly is --read-only of the running command.
	readOnly bool
}

// Run implements op.Runner.
func (r *compatRunner) Run(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	if r.pending != nil {
		return op.Result{}, r.pending
	}
	if err := ensureCoreGitignore(ctx); err != nil {
		return op.Result{}, coreErr(err)
	}
	if e.Spec().Access == op.AccessWrite && (r.readOnly || os.Getenv(envReadOnly) == envValueOn) {
		return op.Result{}, errs.New(errs.ReadOnly, "command requires write access but read-only mode is active").
			WithHint("unset --read-only and LORE_READ_ONLY")
	}
	return r.app.Run(ctx, e, d, c)
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
		asJSON, _ := cmd.Flags().GetBool(cli.FlagJSON)
		if !asJSON {
			return inner(cmd, args)
		}
		var buf bytes.Buffer
		out := cmd.OutOrStdout()
		cmd.SetOut(&buf)
		err := inner(cmd, args)
		cmd.SetOut(out)
		return errors.Join(err, writeIndented(out, buf.Bytes()))
	}
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
	pctx, perr := projresolve.Resolve(projresolve.Inputs{})
	if perr != nil || os.Getenv(envReadOnly) == envValueOn {
		return nil
	}
	if err := ensureGitignore(ctx, pctx.ProjectRoot); err != nil {
		return errcodes.New(errcodes.Internal, "update .gitignore").WithCause(err)
	}
	return nil
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
func guardCoreRepoWrite(ctx context.Context, table row.Table, text string, explicit bool) error {
	kind, ok := coreRouteKinds[table]
	if !ok {
		return nil
	}
	var to toFlags
	if explicit {
		to.To = toRepo
	}
	wt, err := resolveWriteTarget(ctx, to, text)
	if err != nil {
		return coreErr(err)
	}
	in, err := targetInputs(ctx, wt.Situation, wt.Target, kind)
	if err != nil {
		return coreErr(err)
	}
	dec, err := oldstores.ResolveTarget(in)
	if err != nil {
		return coreErr(targetCLIError(err))
	}
	if wt.Personal != nil || !dec.Repo {
		return errs.New(errs.Unsupported, "this repository's lore routing sends a "+kind+" to a store ("+dec.Rule+"), and the 0.1.18 core cannot write to a v0.1.17 store yet").
			WithHint("write it to a store listed in .lore/stores.yaml with --to <name>, or choose this repo with --to repo")
	}
	return coreErr(leakCheckRouteWrite(ctx, writeRoute{Repo: true, Rule: dec.Rule, Situation: wt.Situation}, kind, text))
}

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
			eng, err := coreApp.Engine(ctx)
			if err != nil {
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
			srv, err := rest.New(coreApp.Registry(), eng, rest.Options{Token: tok, Actor: actor, LoreVersion: version})
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
	write := func(file string) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			path := filepath.Join(dir, file)
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
	return code, code != cli.ExitUsage || (err != nil && corePrinted)
}

// coreMoveRow is `lore move <id> --to <store>` for a row of a core-owned
// table: v0.1.17's guard 4 flow (show the text, ask; agents can never
// confirm with --yes), then the move, which keeps the id (PROV5) and
// tombstones the old copy (memory.move for a memory, the App's Move for the
// other core tables). A move into this repo while it is a client repo is
// refused when the row holds secret references (BATCH 5), before anything
// is asked.
func coreMoveRow(ctx context.Context, table row.Table, id, to string, yes bool) error {
	if coreApp == nil {
		return errcodes.New(errcodes.Internal, "the 0.1.18 core is not mounted")
	}
	if to == "" {
		return errcodes.New(errcodes.InvalidInput, "--to is required (a store name from .lore/stores.yaml, or repo)")
	}
	me, err := resolveIdentityHere(ctx)
	if err != nil {
		return err
	}
	if yes && me.Kind == identityKindAgent {
		return errcodes.New(errcodes.InvalidInput, "agents cannot confirm a move with --yes; a person must run it (§6.4)").
			WithHint("ask the owner to run `lore move " + id + " --to " + to + "`")
	}
	rid, err := ids.ParseAny(id)
	if err != nil {
		return errs.Wrap(errs.InvalidID, id+" is not an id", err)
	}
	ref := engine.Ref{Table: table, ID: rid}
	_, r, err := coreApp.Get(ctx, ref)
	if err != nil {
		return err
	}
	body := ""
	for _, f := range []string{fieldBody, fieldTitle} {
		if t, ok := r.Fields[f].(value.Text); ok && body == "" {
			body = t.String()
		}
	}
	if store.Source(to) == store.SourceRepo {
		if cwd, werr := os.Getwd(); werr == nil && repoIsClient(ctx, cwd) {
			var found []string
			for _, ref := range refs.References(body) {
				if ref.Scheme == refs.SchemeSecret {
					found = append(found, ref.String())
				}
			}
			if len(found) > 0 {
				return errs.New(errs.Unsupported, id+" holds secret references and cannot move into a client repo: "+strings.Join(found, ", ")).
					WithHint("remove the references from the row first (BATCH 5)")
			}
		}
	}
	fmt.Printf("This becomes visible to everyone with access to store %s:\n  %q\n", to, oneLine(body))
	if !yes {
		if !stdinIsTTY() || me.Kind == identityKindAgent {
			return errcodes.New(errcodes.InvalidInput, "not moved: confirm on a terminal (or --yes, people only); nothing written")
		}
		if !confirmYes("Move it? [y/N] ") {
			fmt.Println("nothing written")
			return nil
		}
	}
	if err := ensureCoreGitignore(ctx); err != nil {
		return coreErr(err)
	}
	if table != schema.TableMemories {
		if _, err := coreApp.Move(ctx, ref, store.Source(to)); err != nil {
			return err
		}
		fmt.Printf("✓ %s moved to store %s (same id; the old copy is tombstoned)\n", id, to)
		return nil
	}
	e, ok := coreApp.Registry().Lookup(opsmemory.OpMove)
	if !ok {
		return errcodes.New(errcodes.Internal, "memory.move is not registered")
	}
	in, err := json.Marshal(opsmemory.MoveIn{ID: id, To: to})
	if err != nil {
		return errcodes.New(errcodes.Internal, "encode the move").WithCause(err)
	}
	if _, err := coreApp.Run(ctx, e, op.JSONDecoder{Body: in}, op.Caller{Interactive: stdinIsTTY(), Yes: true}); err != nil {
		return err
	}
	fmt.Printf("✓ %s moved to store %s (same id; the old copy is tombstoned)\n", id, to)
	return nil
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
