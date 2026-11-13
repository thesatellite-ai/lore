package app

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/op"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/surface/setupweb"
	"github.com/thesatellite-ai/lore/wire"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// TestPageOpsExist: every operation on the setup page's allowlist is a real
// operation of the binary's registry (ONB 5.2). Red until the onboarding
// ops (setup.*, team.invite*, skill.*, repo.init) are all registered.
func TestPageOpsExist(t *testing.T) {
	wd, home := t.TempDir(), t.TempDir()
	base, _ := wire.StandaloneOptionsOrg(wd, home, wd, version)
	app, err := wire.New(hosthooks.Options(base, hosthooks.Default(wd)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	reg := app.Registry()
	has := func(name string) bool { _, ok := reg.Lookup(name); return ok }
	if missing := pageOpsMissing(setupweb.AllowedOps(), has); len(missing) > 0 {
		t.Fatalf("page allowlist names operations the binary lacks: %v", missing)
	}
}

// TestPageOpens: which results open the page (Q1, contracts): setup.run
// only on a terminal or with --web; never with --json; setup.join as
// setup.run (TestJoinPageNeedsTerminal);
// --schema-only and --agent-hooks (the op leaves Web.Open false) never.
func TestPageOpens(t *testing.T) {
	web := opssetup.WebStart{Open: true, Port: 0, NoOpen: true}
	run := func(out opssetup.RunOut) op.Result {
		e := op.Define(op.Spec{Name: opssetup.OpRun, Summary: "x", Permission: op.PermRead, Access: op.AccessRead, Local: true},
			func(context.Context, struct{}) (opssetup.RunOut, error) { return out, nil })
		res, err := e.Invoke(context.Background(), mustDecode(t, e))
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	cases := []struct {
		name string
		res  op.Result
		args []string
		tty  bool
		want bool
	}{
		{"bare on a terminal", run(opssetup.RunOut{Web: web}), []string{"setup"}, true, true},
		{"bare in a script", run(opssetup.RunOut{Web: web}), []string{"setup"}, false, false},
		{"--web in a script", run(opssetup.RunOut{Web: web}), []string{"setup", "--web"}, false, true},
		{"setup web alias", run(opssetup.RunOut{Web: web}), []string{"setup", "web"}, false, true},
		{"bare, only Open set", run(opssetup.RunOut{Web: opssetup.WebStart{Open: true}}), []string{"setup"}, true, true},
		{"--schema-only", run(opssetup.RunOut{}), []string{"setup", "--schema-only"}, true, false},
		{"--agent-hooks", run(opssetup.RunOut{}), []string{"setup", "--agent-hooks", "--dry-run"}, true, false},
		{"onboard on a terminal", run(opssetup.RunOut{Web: opssetup.WebStart{Open: true}}), []string{"onboard"}, true, true},
	}
	for _, c := range cases {
		r := pageRunner{env: pageEnv{args: c.args, stdoutTTY: func() bool { return c.tty }}}
		if _, _, got := r.webStart(opssetup.OpRun, c.res); got != c.want {
			t.Errorf("%s: open=%v, want %v", c.name, got, c.want)
		}
	}
}

func mustDecode(t *testing.T, e op.Entry) op.Input {
	t.Helper()
	in, err := e.Decode(op.JSONDecoder{Body: []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// TestPreListen: the page's port is bound before setup.run's schema
// update exactly when the run may open the page (a terminal, or --web),
// so a taken --port fails before any store fetch; --json, a script
// without --web, --schema-only, --agent-hooks and other ops bind nothing.
func TestPreListen(t *testing.T) {
	runOp := op.Define(op.Spec{Name: opssetup.OpRun, Summary: "x", Permission: op.PermRead, Access: op.AccessRead, Local: true},
		func(context.Context, opssetup.RunIn) (opssetup.RunOut, error) { return opssetup.RunOut{}, nil })
	joinOp := op.Define(op.Spec{Name: opssetup.OpJoin, Summary: "x", Permission: op.PermRead, Access: op.AccessRead, Local: true},
		func(context.Context, opssetup.RunIn) (opssetup.RunOut, error) { return opssetup.RunOut{}, nil })
	cases := []struct {
		name string
		e    op.Entry
		body string
		args []string
		tty  bool
		bind bool
	}{
		{"setup web", runOp, `{"web":true}`, []string{"setup", "web"}, false, true},
		{"bare on a terminal", runOp, `{}`, []string{"setup"}, true, true},
		{"bare in a script", runOp, `{}`, []string{"setup"}, false, false},
		{"--json", runOp, `{"web":true}`, []string{"setup", "web", "--json"}, true, false},
		{"--schema-only", runOp, `{"schema_only":true}`, []string{"setup", "--schema-only"}, true, false},
		{"--agent-hooks", runOp, `{"agent_hooks":true}`, []string{"setup", "--agent-hooks"}, true, false},
		{"another op", joinOp, `{}`, []string{"setup", "--web"}, true, false},
	}
	for _, c := range cases {
		r := pageRunner{env: pageEnv{args: c.args, stdoutTTY: func() bool { return c.tty }}}
		ln, err := r.preListen(c.e, op.JSONDecoder{Body: []byte(c.body)})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := ln != nil; got != c.bind {
			t.Errorf("%s: bound %v, want %v", c.name, got, c.bind)
		}
		if ln != nil {
			_ = ln.Close()
		}
	}
}
