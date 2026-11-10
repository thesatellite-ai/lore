package doctor

import (
	"context"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/setup"
)

// fixRunner records the commands doctor.fix runs; after, when set, runs
// once the command "ran" (the fix changing the repo).
type fixRunner struct {
	ran   [][]string
	dirs  []string
	exit  int
	after func()
}

func (f *fixRunner) RunFix(ctx context.Context, argv []string) (setup.FixRun, error) {
	return f.RunFixIn(ctx, "", argv)
}

func (f *fixRunner) RunFixIn(_ context.Context, dir string, argv []string) (setup.FixRun, error) {
	f.ran = append(f.ran, argv)
	f.dirs = append(f.dirs, dir)
	if f.after != nil {
		f.after()
	}
	return setup.FixRun{Argv: argv, Exit: f.exit, OutputTail: "done"}, nil
}

// fixEnv is a doctor Env with doctor.fix's upgrade.
type fixEnv struct {
	*fakeEnv
	runner *fixRunner
	top    string
}

func (e fixEnv) SetupFixRunner(context.Context) (setup.FixRunner, error) { return e.runner, nil }
func (e fixEnv) RepoTop(context.Context) (string, error)                 { return e.top, nil }

const (
	staleLine  = "rendered_copy: .lore/LORE.md is stale; fix: lore render"
	remoteLine = "a credential is in git remote origin; remove it: git remote set-url origin git@x:y"
)

// newFixEnv is a repo with a stale rendered copy (fix: lore render) and a
// warning whose fix is not a lore command.
func newFixEnv() fixEnv {
	b := healthy()
	b.Status, b.Warnings = StatusDegraded, []string{remoteLine}
	fe := &fakeEnv{host: &fakeHost{base: b}, runs: []CheckRun{{Check: "rendered_copy", Findings: []Finding{{Check: "rendered_copy", Level: LevelWarning, Line: staleLine, Fix: "lore render"}}}}}
	return fixEnv{fakeEnv: fe, runner: &fixRunner{}, top: "/work/acme"}
}

func keyOf(t *testing.T, env Env, line string) string {
	t.Helper()
	fs, err := repoFindings(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if f.Detail == line {
			return f.Target[len(TargetFinding):]
		}
	}
	t.Fatalf("no finding %q", line)
	return ""
}

func TestFixRunsTheFindingsCommand(t *testing.T) {
	env := newFixEnv()
	key := keyOf(t, env, staleLine)
	// The fix removes the finding (lore render refreshed the copy).
	env.runner.after = func() { env.fakeEnv.runs = nil }
	out, err := Fix(op.WithConfirmed(context.Background(), true), env, FixIn{Finding: key})
	if err != nil {
		t.Fatal(err)
	}
	if len(env.runner.ran) != 1 || !slices.Equal(env.runner.ran[0], []string{"lore", "render"}) || env.runner.dirs[0] != "/work/acme" {
		t.Fatalf("ran %q in %q, want lore render in the repo top", env.runner.ran, env.runner.dirs)
	}
	if !out.Fixed || out.StillFailing != "" || len(out.Findings) != 1 || out.Findings[0].Detail != remoteLine {
		t.Fatalf("out = %+v; want fixed, the other finding left", out)
	}
	// "repo <key>" (the whole target) is accepted too.
	env2 := newFixEnv()
	if _, err := Fix(op.WithConfirmed(context.Background(), true), env2, FixIn{Finding: TargetFinding + keyOf(t, env2, staleLine)}); err != nil {
		t.Fatalf("whole target: %v", err)
	}
}

func TestFixStillThere(t *testing.T) {
	env := newFixEnv()
	out, err := Fix(op.WithConfirmed(context.Background(), true), env, FixIn{Finding: keyOf(t, env, staleLine)})
	if err != nil {
		t.Fatal(err)
	}
	if out.Fixed || out.StillFailing == "" {
		t.Fatalf("a fix that leaves the finding reads as fixed: %+v", out)
	}
}

// Nothing runs without the confirmation, for an unknown or stale key, or
// for a finding whose fix is not one plain lore command.
func TestFixRefuses(t *testing.T) {
	env := newFixEnv()
	stale := keyOf(t, env, staleLine)
	cases := []struct {
		name string
		ctx  context.Context
		key  string
		code errs.Code
	}{
		{"not confirmed", context.Background(), stale, errs.NeedsConfirm},
		{"unknown key", op.WithConfirmed(context.Background(), true), "0000000000000000", errs.NotFound},
		{"empty key", op.WithConfirmed(context.Background(), true), "", errs.NotFound},
		{"not a lore command", op.WithConfirmed(context.Background(), true), keyOf(t, env, remoteLine), errs.FixRefused},
	}
	for _, c := range cases {
		_, err := Fix(c.ctx, env, FixIn{Finding: c.key})
		if !errs.Has(err, c.code) {
			t.Errorf("%s: err = %v, want %s", c.name, err, c.code)
		}
	}
	if len(env.runner.ran) != 0 {
		t.Fatalf("a refused fix ran %q", env.runner.ran)
	}
}

func TestFixNeedsSetupEngine(t *testing.T) {
	if _, err := Fix(context.Background(), &fakeEnv{}, FixIn{Finding: "x"}); !errs.Has(err, errs.Unsupported) {
		t.Fatalf("err = %v", err)
	}
}
