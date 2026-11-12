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
