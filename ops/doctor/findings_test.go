package doctor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/ops/setup"
)

// worstListed is the verdict a reader of the report reaches from what it
// lists (setups[].checks, machine.checks, findings), written here without
// statusOf so the test does not grade the code with itself: an info-level
// result is a note; a failing error-level result breaks; any other warn,
// fail or error result degrades.
func worstListed(r Report) Health {
	rank := map[Health]int{StatusHealthy: 0, StatusDegraded: 1, StatusBroken: 2}
	st := StatusHealthy
	see := func(rs []onboard.CheckResult) {
		for _, c := range rs {
			v := StatusHealthy
			switch {
			case c.Level == onboard.LevelInfo:
			case c.Status == onboard.StatusFail && c.Level == onboard.LevelError:
				v = StatusBroken
			case c.Status == onboard.StatusFail || c.Status == onboard.StatusWarn || c.Status == onboard.StatusError:
				v = StatusDegraded
			}
			if rank[v] > rank[st] {
				st = v
			}
		}
	}
	for _, s := range r.Setups {
		see(s.Checks)
	}
	see(r.Machine.Checks)
	see(r.Findings)
	return st
}

// countListed is how many results the report lists.
func countListed(r Report) int {
	n := len(r.Findings) + len(r.Machine.Checks)
	for _, s := range r.Setups {
		n += len(s.Checks)
	}
	return n
}

// TestStatusMatchesVisibleFindings is the invariant of findings.go over a
// matrix of sources × modes: the status is never worse than what the
// report lists (no "degraded" beside "warn 0 fail 0") and never better;
// every warning and error is one finding; the counts cover every listed
// result; the exit status follows the status.
func TestStatusMatchesVisibleFindings(t *testing.T) {
	type base struct {
		name string
		b    func() Base
	}
	bases := []base{
		{"healthy", healthy},
		{"base warning", func() Base {
			b := healthy()
			b.Status, b.Warnings = StatusDegraded, []string{"lore render copies are stale: .lore/LORE.md differs from its rows; run `lore render`"}
			return b
		}},
		{"base data ignored", func() Base {
			b := healthy()
			b.Status, b.Warnings = StatusDegraded, []string{".lore/data is gitignored, so lore knowledge is never shared; remove the ignore rule"}
			return b
		}},
		{"base error degrades", func() Base {
			b := healthy()
			b.Status, b.Errors = StatusDegraded, []string{"reversed supersedes link dec_1 -> dec_2"}
			return b
		}},
		{"base broken", func() Base {
			b := healthy()
			b.Status, b.Errors = StatusBroken, []string{"this lore is older than the repo's floor v0.2.0"}
			return b
		}},
		{"base status with no line", func() Base { b := healthy(); b.Status = StatusDegraded; return b }},
		{"base broken with no line", func() Base { b := healthy(); b.Status = StatusBroken; return b }},
		{"base signal line without degrade", func() Base {
			b := healthy()
			b.Warnings = []string{"the remote does not support atomic push"}
			return b
		}},
	}
	cores := map[string][]CheckRun{
		"no core":    nil,
		"core warn":  {{Check: "rendered_copy", Findings: []Finding{{Check: "rendered_copy", Level: LevelWarning, Line: "rendered_copy: .lore/LORE.md is stale", Fix: "lore render"}}}},
		"core error": {{Check: "cache", Findings: []Finding{{Check: "cache", Level: LevelError, Line: "cache: x is damaged"}}}},
		"core note":  {{Check: "internal_off", Findings: []Finding{{Check: "internal_off", Level: LevelNote, Line: "internal_off: note"}}}},
		"core err":   {{Check: "store_lock", Err: errors.New("boom")}},
	}
	probes := map[string]map[probeKey]setup.ProbeResult{
		"setups ok":   {},
		"setup warns": {{onboard.CheckGitHubSSHKey, TargetSetup}: {Status: onboard.StatusWarn}},
		"setup fails": {{onboard.CheckStoreWrite, TargetStore("agency")}: {Status: onboard.StatusFail}},
	}
	for _, b := range bases {
		for cn, runs := range cores {
			for pn, script := range probes {
				for _, all := range []bool{false, true} {
					name := fmt.Sprintf("%s/%s/%s/all=%v", b.name, cn, pn, all)
					env := newEnv(t)
					env.store.cfg.Setups = []onboard.SetupEntry{personal(env.home), acme(env.home)}
					env.store.active = setup.ActiveSetup{Name: "acme-hub", Why: setup.WhyFolder}
					env.fakeEnv = fakeEnv{host: &fakeHost{base: b.b()}, runs: runs}
					for k, v := range script {
						env.prober.script[k] = v
					}
					r, err := Run(context.Background(), env, RunIn{All: all})
					if err != nil {
						t.Fatalf("%s: %v", name, err)
					}
					if want := worstListed(r); r.Status != want {
						t.Errorf("%s: status %s, but the listed results say %s (findings %d)", name, r.Status, want, len(r.Findings))
					}
					if r.Exit != exitOf(r.Status) || r.OK != (r.Status == StatusHealthy) {
						t.Errorf("%s: exit %d ok %v for %s", name, r.Exit, r.OK, r.Status)
					}
					var lines []string
					for _, f := range r.Findings {
						lines = append(lines, f.Detail)
					}
					for _, l := range slices.Concat(r.Warnings, r.Errors) {
						if !slices.Contains(lines, l) {
							t.Errorf("%s: %q is in warnings/errors but not in findings", name, l)
						}
					}
					if len(r.Findings) != len(r.Warnings)+len(r.Errors) {
						t.Errorf("%s: %d findings for %d warnings + %d errors", name, len(r.Findings), len(r.Warnings), len(r.Errors))
					}
					c := r.Counts
					if total := c.OK + c.Warn + c.Fail + c.Skip + c.Error; total != countListed(r) {
						t.Errorf("%s: counts %+v total %d, the report lists %d results", name, c, total, countListed(r))
					}
				}
			}
		}
	}
}

// A finding names its fix: a core check's fix text, runnable when it is
// one plain lore command; a base line's backticked lore command; nothing
// runnable for a placeholder or a non-lore program. Its key is stable
// across runs and differs between findings.
func TestFindingFixes(t *testing.T) {
	b := healthy()
	b.Status = StatusDegraded
	b.Warnings = []string{
		"acme: unpushed store work (2 rows); `lore sync` pushes it",
		"a credential is in git remote origin; remove it: git remote set-url origin git@x:y",
		"quarantined metadata: dec dec_1: bad (fix with lore meta set dec_1 …, or lore meta unset)",
	}
	runs := []CheckRun{{Check: "rendered_copy", Findings: []Finding{{Check: "rendered_copy", Level: LevelWarning, Line: "rendered_copy: .lore/LORE.md is stale; fix: lore render", Fix: "lore render"}}}}
	env := &fakeEnv{host: &fakeHost{base: b}, runs: runs}
	r, err := Run(context.Background(), env, RunIn{})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]onboard.CheckResult{}
	for _, f := range r.Findings {
		by[f.Detail] = f
	}
	cases := []struct {
		line  string
		check onboard.CheckID
		cmd   []string
	}{
		{runs[0].Findings[0].Line, "rendered_copy", []string{"lore", "render"}},
		{b.Warnings[0], CheckWarning, []string{"lore", "sync"}},
		{b.Warnings[1], CheckWarning, nil},
		{b.Warnings[2], CheckWarning, nil},
	}
	keys := map[string]bool{}
	for _, c := range cases {
		f, ok := by[c.line]
		if !ok {
			t.Fatalf("no finding for %q", c.line)
		}
		if f.Check != c.check || f.Status != onboard.StatusWarn {
			t.Errorf("%q: check %s status %s", c.line, f.Check, f.Status)
		}
		if !slices.Equal(f.Fix.Command, c.cmd) && !(len(c.cmd) == 0 && len(f.Fix.Command) == 0) {
			t.Errorf("%q: fix command %q, want %q", c.line, f.Fix.Command, c.cmd)
		}
		if f.Fix.Runnable != (len(c.cmd) > 0) {
			t.Errorf("%q: runnable %v", c.line, f.Fix.Runnable)
		}
		if f.Target != TargetFinding+FindingKey(c.check, c.line) || keys[f.Target] {
			t.Errorf("%q: target %q is not its own stable key", c.line, f.Target)
		}
		keys[f.Target] = true
	}
	r2, _ := Run(context.Background(), env, RunIn{})
	if !slices.EqualFunc(r.Findings, r2.Findings, func(a, b onboard.CheckResult) bool { return a.Target == b.Target }) {
		t.Errorf("finding keys changed between two runs of the same state")
	}
}

func TestRunnableFix(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"lore render", []string{"lore", "render"}},
		{"run `lore sync` now", []string{"lore", "sync"}},
		{"lore config fmt", []string{"lore", "config", "fmt"}},
		{"lore setup enable <name>", nil},
		{"lore meta set dec_1 …", nil},
		{"git remote set-url origin x", nil},
		{"lore render; rm -rf /", nil},
		{"lore", nil},
		{"", nil},
		{"`lore render $(id)`", nil},
	}
	for _, c := range cases {
		if got := RunnableFix(c.in); !slices.Equal(got, c.want) {
			t.Errorf("RunnableFix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
