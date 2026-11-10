package doctor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
)

// healthEnv keeps what doctor runs record and answers it back, the way
// wire's doctor-health.json does (HealthSink + LastSource).
type healthEnv struct {
	*setupEnv
	got map[string][]onboard.CheckResult
}

func (e healthEnv) RecordHealth(_ context.Context, setup string, rs []onboard.CheckResult) error {
	e.got[setup] = append([]onboard.CheckResult{}, rs...)
	return nil
}

func (e healthEnv) SetupHealth(_ context.Context, setup string) ([]onboard.CheckResult, bool) {
	rs, ok := e.got[setup]
	return rs, ok && len(rs) > 0
}

func lastT(t *testing.T, env Env) LastOut {
	t.Helper()
	out, err := Last(context.Background(), env)
	if err != nil {
		t.Fatalf("doctor.last: %v", err)
	}
	return out
}

// doctor.last probes nothing: every setup is listed, none checked yet,
// and the prober saw no request (the page opens Doctor on this answer).
func TestLastRunsNoCheck(t *testing.T) {
	env := newEnv(t)
	off := acme(env.home)
	off.Disabled = true
	env.store.cfg.Setups = []onboard.SetupEntry{personal(env.home), off}
	he := healthEnv{setupEnv: env, got: map[string][]onboard.CheckResult{}}
	out := lastT(t, he)
	if len(env.prober.reqs) != 0 {
		t.Fatalf("doctor.last probed %d checks; it must only read", len(env.prober.reqs))
	}
	if len(out.Setups) != 2 || out.Setups[0].Name != "personal" || out.Setups[1].Name != "acme-hub" {
		t.Fatalf("setups = %+v, want personal then acme-hub (config order)", out.Setups)
	}
	for _, s := range out.Setups {
		if s.Status != "" || !s.CheckedAt.IsZero() || s.Checks == nil || len(s.Checks) != 0 {
			t.Errorf("%s: want not checked yet (no status, no time, empty checks), got %+v", s.Name, s)
		}
	}
	if !out.Setups[1].Disabled || out.Setups[0].Disabled {
		t.Errorf("disabled flags = %v, %v; want false, true", out.Setups[0].Disabled, out.Setups[1].Disabled)
	}
	if txt := out.Text(); !strings.Contains(txt, "not checked yet (lore doctor --all --setup personal)") || !strings.Contains(txt, "acme-hub (member)  disabled") {
		t.Errorf("text:\n%s", txt)
	}
}

// After a run of one setup, doctor.last shows that setup's results and the
// time of the newest one; the other setup stays not checked.
func TestLastAfterOneSetupRun(t *testing.T) {
	env := newEnv(t)
	env.store.cfg.Setups = []onboard.SetupEntry{personal(env.home), acme(env.home)}
	he := healthEnv{setupEnv: env, got: map[string][]onboard.CheckResult{}}
	rep := runAllT(t, he, RunIn{Setup: "acme-hub"})
	if len(rep.Setups) != 1 || rep.Setups[0].Name != "acme-hub" {
		t.Fatalf("run checked %+v, want acme-hub only", rep.Setups)
	}
	probed := len(env.prober.reqs)
	out := lastT(t, he)
	if len(env.prober.reqs) != probed {
		t.Fatalf("doctor.last probed after the run")
	}
	p, a := out.Setups[0], out.Setups[1]
	if !p.CheckedAt.IsZero() || p.Status != "" {
		t.Errorf("personal was not run but reads as checked: %+v", p)
	}
	if a.CheckedAt.IsZero() || a.Status != rep.Setups[0].Status || len(a.Checks) != len(rep.Setups[0].Checks) {
		t.Fatalf("acme-hub: got %+v, want the run's %d checks, status %s", a, len(rep.Setups[0].Checks), rep.Setups[0].Status)
	}
	var want time.Time
	for _, c := range rep.Setups[0].Checks {
		if c.CheckedAt.After(want) {
			want = c.CheckedAt
		}
	}
	if !a.CheckedAt.Equal(want) {
		t.Errorf("checked_at = %v, want the newest check %v", a.CheckedAt, want)
	}
	if !strings.Contains(out.Text(), "acme-hub (member)  "+string(a.Status)+" · ") {
		t.Errorf("text:\n%s", out.Text())
	}
}

// The verdict follows the recorded results: a failing error-level check
// is broken, a warning degraded.
func TestLastStatusOfRecorded(t *testing.T) {
	env := newEnv(t)
	env.store.cfg.Setups = []onboard.SetupEntry{personal(env.home), acme(env.home)}
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	he := healthEnv{setupEnv: env, got: map[string][]onboard.CheckResult{
		"personal": {{Check: onboard.CheckStoreRead, Status: onboard.StatusFail, Level: onboard.LevelError, CheckedAt: at}},
		"acme-hub": {{Check: onboard.CheckStoreRead, Status: onboard.StatusWarn, CheckedAt: at.Add(-time.Hour)}, {Check: onboard.CheckStoreWrite, Status: onboard.StatusOK}},
	}}
	out := lastT(t, he)
	if out.Setups[0].Status != StatusBroken || out.Setups[1].Status != StatusDegraded {
		t.Errorf("statuses = %s, %s; want broken, degraded", out.Setups[0].Status, out.Setups[1].Status)
	}
	if !out.Setups[1].CheckedAt.Equal(at.Add(-time.Hour)) {
		t.Errorf("checked_at ignores a result without a time: got %v", out.Setups[1].CheckedAt)
	}
}

// Without the setup engine doctor.last says so instead of an empty list
// that would read as "no setups on this machine".
func TestLastNeedsSetupEngine(t *testing.T) {
	_, err := Last(context.Background(), &fakeEnv{})
	if !errs.Has(err, errs.Unsupported) {
		t.Fatalf("err = %v, want E_UNSUPPORTED", err)
	}
}
