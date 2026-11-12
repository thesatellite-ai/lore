package doctor

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// recheckEnv answers its first checks (the fixing run) with before, every
// later one with after: what a fix changed on disk.
type recheckEnv struct {
	fakeEnv
	before, after []CheckRun
	calls         []bool
}

func (e *recheckEnv) DoctorChecks(_ context.Context, fix bool) []CheckRun {
	e.calls = append(e.calls, fix)
	if len(e.calls) == 1 {
		return e.before
	}
	return e.after
}

// TestFixRechecks (owner report 2026-11-12, brief 3.9): doctor --fix
// checks again after fixing; the status is the state after the fixes, and
// Fixed lists what the fixes cleared and what a check fixed itself, in the
// text right under the status.
func TestFixRechecks(t *testing.T) {
	t.Parallel()
	gone := Finding{Check: "task_lead", Level: LevelWarning, Line: "task_lead: tsk_1 has no lead"}
	self := Finding{Check: "config_style", Level: LevelWarning, Fixed: true, Line: "config_style: rewrote config.yaml in block style"}
	stays := Finding{Check: "store_lock", Level: LevelWarning, Line: "store_lock: a stale lock needs a person"}
	env := &recheckEnv{fakeEnv: fakeEnv{host: &fakeHost{base: healthy()}},
		before: []CheckRun{{Check: "task_lead", Findings: []Finding{gone}}, {Check: "config_style", Findings: []Finding{self}}, {Check: "store_lock", Findings: []Finding{stays}}},
		after:  []CheckRun{{Check: "store_lock", Findings: []Finding{stays}}}}
	r, err := Run(context.Background(), env, RunIn{Fix: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(env.calls, []bool{true, false}) {
		t.Fatalf("checks ran with fix %v, want one fixing run then one check", env.calls)
	}
	if !slices.Equal(r.Fixed, []string{gone.Line, self.Line}) {
		t.Fatalf("fixed = %q", r.Fixed)
	}
	if r.Status != StatusDegraded || !slices.Equal(r.Warnings, []string{stays.Line}) {
		t.Fatalf("after the fixes: status %s warnings %q", r.Status, r.Warnings)
	}
	txt := r.Text()
	if !strings.HasPrefix(txt, "Status: DEGRADED\n") || !strings.Contains(txt, "Fixed (2, checked again after fixing; the status above is now):\n  - "+gone.Line) {
		t.Fatalf("text:\n%s", txt)
	}
	// without --fix nothing runs twice and nothing is listed as fixed
	env2 := &recheckEnv{fakeEnv: fakeEnv{host: &fakeHost{base: healthy()}}, before: env.before, after: env.after}
	r2, err := Run(context.Background(), env2, RunIn{})
	if err != nil || len(env2.calls) != 1 || len(r2.Fixed) != 0 {
		t.Fatalf("plain doctor: calls %v fixed %q %v", env2.calls, r2.Fixed, err)
	}
}

// TestFixKeepsWhatTheFixSaid: the re-check after --fix keeps the fixing
// run's notes it does not repeat ("repaired 1 --supersedes link(s): …"),
// once each, after its own (userscen TestDoctorFix_ReversedSupersedesLink).
func TestFixKeepsWhatTheFixSaid(t *testing.T) {
	t.Parallel()
	got := withFixNotes([]string{"live: last fetch 0s ago"}, []string{"live: last fetch 0s ago", "repaired 1 --supersedes link(s): rule a → b", "repaired 1 --supersedes link(s): rule a → b"})
	want := []string{"live: last fetch 0s ago", "repaired 1 --supersedes link(s): rule a → b"}
	if !slices.Equal(got, want) {
		t.Fatalf("notes = %q, want %q", got, want)
	}
}

// fixingHost is a Host whose fixing run (fix) repaired something itself
// and says so in Base.Fixed (the --supersedes repair); the re-check after
// it finds nothing.
type fixingHost struct{ line string }

func (h fixingHost) Base(_ context.Context, fix bool) (Base, error) {
	b := healthy()
	if fix {
		b.Fixed, b.Notes = []string{h.line}, []string{h.line}
	}
	return b, nil
}

// TestFixStepNamesBaseRepair (item 46): a repair this binary's own base
// made in the fixing run (84 --supersedes links written and pushed) is a
// fixed line of the doctor fixes step, so lore fix never prints that step
// as "current" after it wrote.
func TestFixStepNamesBaseRepair(t *testing.T) {
	t.Parallel()
	const line = "2 --supersedes links repaired, commit abc1234 in store shared: rule a → b, rule c → d"
	r, err := FixProject(context.Background(), baseEnv{recheckEnv: &recheckEnv{}, host: fixingHost{line: line}}, false)
	if err != nil {
		t.Fatal(err)
	}
	var st FixStep
	for _, s := range r.Steps {
		if s.Step == StepDoctorFix {
			st = s
		}
	}
	if !st.Changed || st.Result != "fixed 1" || !slices.Equal(st.Detail, []string{line}) {
		t.Fatalf("doctor fixes step = %+v, want fixed 1 naming the repair", st)
	}
}

// baseEnv is recheckEnv with another Host.
type baseEnv struct {
	*recheckEnv
	host Host
}

func (e baseEnv) DoctorHost(context.Context) (Host, bool) { return e.host, true }
