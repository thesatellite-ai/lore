package doctor

import (
	"context"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
)

// TestFixRunsDoctorOnceWhenNothingToFix (D-739, public issue #7): lore fix
// on a project with nothing to repair runs the doctor pass once; the
// fixing run is the state now. Before, it always ran a second, plain pass
// (8.4 s of lore fix on a 2,400-task project, against 3.6 s of doctor).
func TestFixRunsDoctorOnceWhenNothingToFix(t *testing.T) {
	t.Parallel()
	stays := Finding{Check: "store_lock", Level: LevelWarning, Line: "store_lock: a stale lock needs a person"}
	runs := []CheckRun{{Check: "store_lock", Findings: []Finding{stays}}}
	env := &recheckEnv{fakeEnv: fakeEnv{host: &fakeHost{base: healthy()}}, before: runs, after: runs}
	r, err := FixProject(context.Background(), env, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(env.calls, []bool{true}) {
		t.Fatalf("checks ran with fix %v, want the fixing run only", env.calls)
	}
	if r.Status != StatusDegraded || !slices.Equal(r.Warnings, []string{stays.Line}) || len(r.Fixed) != 0 {
		t.Fatalf("status %s warnings %q fixed %q", r.Status, r.Warnings, r.Fixed)
	}
	// a finding that names a fix may have been repaired without saying so:
	// the plain re-check still runs and is the state printed
	fixable := Finding{Check: "rendered_copy", Level: LevelWarning, Line: "rendered_copy: .lore/LORE.md is stale", Fix: "lore render"}
	env2 := &recheckEnv{fakeEnv: fakeEnv{host: &fakeHost{base: healthy()}},
		before: []CheckRun{{Check: "rendered_copy", Findings: []Finding{fixable}}}, after: nil}
	r2, err := FixProject(context.Background(), env2, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(env2.calls) < 2 || !env2.calls[0] || env2.calls[1] {
		t.Fatalf("checks ran with fix %v, want the fixing run then a plain re-check", env2.calls)
	}
	if !slices.Equal(r2.Fixed, []string{fixable.Line}) || len(r2.Warnings) != 0 {
		t.Fatalf("after the re-check: fixed %q warnings %q", r2.Fixed, r2.Warnings)
	}
}

// snapEnv records whether its base and its checks saw one read snapshot.
type snapEnv struct {
	fakeEnv
	baseCtx, checkCtx context.Context
}

// snapHost is the Base of a snapEnv.
type snapHost struct{ e *snapEnv }

func (h snapHost) Base(ctx context.Context, _ bool) (Base, error) {
	h.e.baseCtx = ctx
	return healthy(), nil
}

func (e *snapEnv) DoctorHost(context.Context) (Host, bool) { return snapHost{e: e}, true }

func (e *snapEnv) DoctorChecks(ctx context.Context, _ bool) []CheckRun {
	e.checkCtx = ctx
	return nil
}

// TestDoctorPassSharesOneSnapshot (D-739): a plain doctor pass hands its
// base and its checks a context that already holds a read snapshot, so the
// store is read and decoded once per lore doctor, not once by the base and
// again by the checks (EnsureReadSnapshot keeps the one it finds).
func TestDoctorPassSharesOneSnapshot(t *testing.T) {
	t.Parallel()
	env := &snapEnv{}
	if _, err := Run(context.Background(), env, RunIn{}); err != nil {
		t.Fatal(err)
	}
	for name, ctx := range map[string]context.Context{"base": env.baseCtx, "checks": env.checkCtx} {
		if ctx == nil || engine.EnsureReadSnapshot(ctx) != ctx {
			t.Fatalf("the %s ran without the pass's read snapshot", name)
		}
	}
}
