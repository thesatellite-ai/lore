package app

// setupweb.go — where the CLI opens the local setup page (ONB 5.1, Q1,
// contracts "Bare lore setup"):
//
//	lore setup [--web] [--no-open] [--port N]   setup.run: schema update, then RunOut.Web
//	lore setup web                              alias of lore setup --web
//	lore onboard                                alias of lore setup
//	lore setup join <invite>                    setup.join: JoinOut.Web (join flow)
//	lore setup repair <name>                    setup.repair: RepairOut.Web, when set
//
// The ops never open anything (they are L5 and Local, and REST only
// reports `web`). pageRunner wraps the CLI's runner: after one of those
// ops returns a non-zero WebStart, and the output is not --json, it prints
// the op's own lines, then serves surface/setupweb until the page is
// closed, idle, or Ctrl-C. setup.run opens the page only when stdout is a
// terminal or --web was given, so scripts and CI keep the plain schema
// update; `lore upgrade` uses --schema-only, which never sets Web.
//
// The page serves the allowlisted ops through the unwrapped runner, one
// request at a time (apiRunner), so a page request never opens a page.

import (
	"context"
	"io"
	"net"
	"os"
	"reflect"
	"slices"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/surface/cli"
	"github.com/thesatellite-ai/lore/surface/setupweb"
	"github.com/thesatellite-ai/lore/wire"
)

// Setup page command words and flags.
const (
	setupNoun      = "setup"
	setupWebVerb   = "web"
	onboardCommand = "onboard"
	flagWeb        = "--web"
)

// pageEnv is what the page needs from the binary.
type pageEnv struct {
	app *wire.App
	// home is the lore home (~/.lore): setup.idle_minutes, setup.open_browser.
	home string
	// inner is the CLI's host runner (every hook); the page serves through it.
	inner op.Runner
	// args are the process arguments (--json, --web are read from them).
	args []string
	// stdoutTTY reports whether stdout is a terminal.
	stdoutTTY func() bool
	out, errw io.Writer
	// open opens the browser (nil: never).
	open func(context.Context, string) error
}

// pageRunner is the CLI's runner with the page hook (see the file comment).
type pageRunner struct{ env pageEnv }

var _ op.Runner = pageRunner{}

// newPageRunner wraps inner for cli.Build. `lore api` keeps inner itself.
func newPageRunner(app *wire.App, home string, inner op.Runner, args []string) pageRunner {
	return pageRunner{env: pageEnv{
		app: app, home: home, inner: inner, args: args,
		stdoutTTY: func() bool { return term.IsTerminal(int(os.Stdout.Fd())) },
		out:       os.Stdout, errw: os.Stderr, open: setupweb.OpenBrowser,
	}}
}

// Run implements op.Runner.
func (r pageRunner) Run(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	ln, err := r.preListen(e, d)
	if err != nil {
		return op.Result{}, err
	}
	// Until serve takes it, the early listener is this function's to close.
	release := func() {
		if ln != nil {
			_ = ln.Close()
		}
	}
	// One fetch scope (gitbackend.FetchScope) for the op and the page's
	// actor lookup: the schema update and pageActor each open the attached
	// stores, and a store whose remote refused the first open is not
	// fetched again over a second ssh connection (setupe2e startfetch). The
	// page itself (serve) runs outside it, so a store fixed on the page is
	// fetched afresh.
	scoped := gitbackend.WithFetchScope(ctx)
	res, err := r.env.inner.Run(scoped, e, d, c)
	if err != nil || cli.JSONRequested(r.env.args) {
		release()
		return res, err
	}
	web, flow, ok := r.webStart(e.Spec().Name, res)
	if !ok {
		release()
		return res, nil
	}
	// The op's own lines first (the schema update's, if any), then the
	// page; the result then prints nothing more.
	if text, has := res.Text(); has && text != "" {
		_, _ = io.WriteString(r.env.out, text) // a closed stdout has no reader to tell
	}
	// One line about the folder lore setup was started in (fix-all item
	// 3); the page's Home banner gets the same answer through the session.
	here, herr := r.env.app.SetupHere(ctx)
	if flow == setupweb.FlowJoin {
		// the folder is offered the member setup being joined, not solo
		here = opssetup.HereForJoin(opssetup.WithJoin(ctx, joinOf(res, web)), here)
	}
	if herr == nil {
		_, _ = io.WriteString(r.env.out, onboard.HereLine(here)+"\n")
	}
	actor := pageActor(scoped, r.env.app)
	if err := r.serve(opssetup.WithJoin(ctx, joinOf(res, web)), ln, web, flow, here, actor); err != nil {
		return res, err
	}
	return res.WithTextEdit(func(string) string { return "" }), nil
}

// webStart reads the WebStart of a page-opening op's result.
func (r pageRunner) webStart(name string, res op.Result) (opssetup.WebStart, setupweb.Flow, bool) {
	v := res.Value()
	if !v.IsValid() || !v.CanInterface() {
		return opssetup.WebStart{}, "", false
	}
	// Interface() is the reflect boundary; it is narrowed at once.
	switch out := v.Interface().(type) {
	case opssetup.RunOut:
		// Q1: bare `lore setup` opens the page on a terminal only, or when
		// --web asked for it; a script keeps the schema update.
		// The op sets Web.Open on a page run (never for --schema-only, as
		// lore upgrade runs it, nor --agent-hooks).
		open := name == opssetup.OpRun && out.Web.Open &&
			(r.env.stdoutTTY() || webAsked(r.env.args))
		return out.Web, setupweb.FlowHome, open
	case opssetup.JoinOut:
		// B-10: like bare `lore setup`, the page needs a terminal or
		// --web; a script, an agent or a pipe gets the op's text, the
		// `lore setup apply` lines, and an exit instead of a page that
		// waits 30 idle minutes.
		return out.Web, setupweb.FlowJoin, name == opssetup.OpJoin &&
			(r.env.stdoutTTY() || webAsked(r.env.args))
	case opssetup.RepairOut:
		return out.Web, setupweb.FlowHome, name == opssetup.OpRepair && out.Web != (opssetup.WebStart{}) && r.env.stdoutTTY()
	}
	return opssetup.WebStart{}, "", false
}

// preListen binds the page's port before setup.run does anything when
// this run may open the page: setup.run first runs the schema update,
// which fetches the attached stores over the network, so a taken --port
// would otherwise fail only after that fetch. It answers the listen error
// at once instead (setupweb.Listen: the port and "--port 0 picks a free
// one"). nil, nil when the run cannot open the page (another op, --json,
// a script without --web, --schema-only or --agent-hooks); a decode
// error is left to the op, which reports it as usual.
func (r pageRunner) preListen(e op.Entry, d op.Decoder) (net.Listener, error) {
	if e.Spec().Name != opssetup.OpRun || cli.JSONRequested(r.env.args) ||
		!(r.env.stdoutTTY() || webAsked(r.env.args)) {
		return nil, nil
	}
	var in opssetup.RunIn
	// reflect is the decoder's own boundary; in is the concrete input.
	if d.Decode(reflect.ValueOf(&in).Elem(), e.InputFields()) != nil || in.SchemaOnly || in.AgentHooks {
		return nil, nil
	}
	return setupweb.Listen(in.Port)
}

// serve runs the page until it stops, on ln when preListen bound it
// already, else on a listener for web.Port; actor is pageActor's answer.
func (r pageRunner) serve(ctx context.Context, ln net.Listener, web opssetup.WebStart, flow setupweb.Flow, here onboard.Here, actor ids.ID[ids.Actor]) error {
	if ln == nil {
		var err error
		if ln, err = setupweb.Listen(web.Port); err != nil {
			return err
		}
	}
	// A config.yaml that does not read keeps the design defaults (30
	// minutes, open the browser); lore doctor reports the config itself.
	conf, _ := wire.ReadSetupsConfig(r.env.home)
	cfg := conf.Settings
	page, err := setupweb.New(ln, setupweb.Options{
		Registry: r.env.app.Registry(), Runner: &apiRunner{inner: r.env.inner, join: opssetup.JoinOf(ctx)},
		Actor: actor, LoreVersion: version,
		IdleMinutes: cfg.IdleMinutes, Flow: flow, Invite: web.Invite, Setup: web.Setup,
		UI: setupUI, DebugLog: wire.SetupDebugLogPath(r.env.home), Here: here,
	})
	if err != nil {
		_ = ln.Close()
		return err
	}
	var opener func(context.Context, string) error
	if !web.NoOpen && cfg.OpenBrowser {
		opener = r.env.open
	}
	return page.Serve(ctx, ln, r.env.errw, opener)
}

// webAsked reports whether the arguments ask for the page explicitly:
// --web, or the `lore setup web` alias.
func webAsked(args []string) bool {
	if slices.Contains(args, flagWeb) {
		return true
	}
	var words []string
	for _, a := range args {
		if a == "--" {
			break
		}
		if len(a) > 0 && a[0] != '-' {
			words = append(words, a)
		}
	}
	return len(words) >= 2 && words[0] == setupNoun && words[1] == setupWebVerb
}

// pageActor is the machine's person when lore knows one. A fresh machine
// has none yet, and setting one up is what the page is for, so its absence
// is not an error here: the allowlisted ops are Local or check their own
// identity. In a repo lore is not set up in, the actor is not looked up:
// that opens the repo store, which writes .lore/, git hooks and
// .gitattributes before the person chose anything (A13 bug 3; the
// this_folder step's lore init does that on click).
func pageActor(ctx context.Context, app *wire.App) ids.ID[ids.Actor] {
	if wd, err := os.Getwd(); err != nil || !opssetup.LoreRepoAt(wd) {
		return ids.ID[ids.Actor]{}
	}
	a, err := app.Actor(ctx)
	if err != nil {
		return ids.ID[ids.Actor]{}
	}
	return a
}

// mountSetupWeb adds `lore setup web` (alias of `lore setup --web`) and
// `lore onboard` (alias of `lore setup`). Call it after mountShortcuts, so
// the setup group runs setup.run when bare.
func mountSetupWeb(root *cobra.Command) {
	g := find(root, setupNoun)
	if g == nil || g.RunE == nil {
		return
	}
	g.AddCommand(&cobra.Command{
		Use:                setupWebVerb,
		Short:              "Open the local setup page (lore setup --web)",
		Example:            "  # open the setup page in the browser\n  lore setup web",
		DisableFlagParsing: true,
		RunE:               func(cmd *cobra.Command, args []string) error { return g.RunE(cmd, append([]string{flagWeb}, args...)) },
	})
	root.AddCommand(&cobra.Command{
		Use:                onboardCommand,
		Short:              "Alias of lore setup: open the guided setup page",
		Example:            "  # first run on a new machine\n  lore onboard",
		DisableFlagParsing: true,
		RunE:               func(cmd *cobra.Command, args []string) error { return g.RunE(cmd, args) },
	})
}

// pageOpsMissing lists the allowlisted operations reg lacks (the test that
// every allowlist name is a real operation, ONB 5.2).
func pageOpsMissing(names []string, has func(string) bool) []string {
	var out []string
	for _, n := range names {
		if !has(n) {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// joinOf is the join a setup.join result serves (zero for any other op).
func joinOf(res op.Result, web opssetup.WebStart) opssetup.JoinContext {
	v := res.Value()
	if !v.IsValid() || !v.CanInterface() {
		return opssetup.JoinContext{}
	}
	// Interface() is the reflect boundary; it is narrowed at once.
	out, ok := v.Interface().(opssetup.JoinOut)
	if !ok {
		return opssetup.JoinContext{}
	}
	return opssetup.JoinContext{Setup: web.Setup, Org: out.Org}
}
