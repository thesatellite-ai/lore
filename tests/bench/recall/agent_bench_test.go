//go:build bench

package recall

// agent_bench_test.go — the blind agent run against real agents (task
// bench:agent). It costs money, so it runs only when asked: never in the
// gate, never on push. It writes the same store and tree as
// TestBenchRecall, asks every question of a lore agent and a grep agent on
// one small model, grades both answers and merges its section into
// tmp/bench/recall.md.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Environment knobs of the agent run (tests/bench/recall/README.md).
const (
	envAgent      = "LORE_BENCH_AGENT"         // the command that starts a Claude Code compatible CLI (default "claude")
	envAgentModel = "LORE_BENCH_AGENT_MODEL"   // the model both agents run on (default DefaultAgentModel)
	envAgentJobs  = "LORE_BENCH_AGENT_JOBS"    // agents at a time (default 4)
	envAgentLimit = "LORE_BENCH_AGENT_TIMEOUT" // one agent run is stopped after this (default 5m)
	envAgentTurns = "LORE_BENCH_AGENT_TURNS"   // the agent's turn limit (default 12)
	envAgentOnly  = "LORE_BENCH_AGENT_ONLY"    // comma-separated question ids to ask (default every question)
	envAnswers    = "LORE_BENCH_AGENT_ANSWERS" // TestBenchAgentRegrade: the saved answers file to grade again
)

func TestBenchAgent(t *testing.T) {
	launcher := strings.Fields(os.Getenv(envAgent))
	if len(launcher) == 0 {
		launcher = []string{"claude"}
	}
	model := envOr(envAgentModel, DefaultAgentModel)
	jobs := envInt(t, envAgentJobs, 4)
	turns := envInt(t, envAgentTurns, 12)
	limit := 5 * time.Minute
	if v := os.Getenv(envAgentLimit); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatalf("%s=%q: %v", envAgentLimit, v, err)
		}
		limit = d
	}
	p := prepare(t)
	skill, err := os.ReadFile(filepath.Join(p.root, "skills", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	// the lore the agent runs: the measured binary as the bench's person
	binDir := filepath.Join(p.dir, "agentbin")
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		t.Fatal(err)
	}
	var env strings.Builder
	for _, kv := range p.e.env {
		env.WriteString(" " + sq(kv))
	}
	wrapper := fmt.Sprintf("#!/bin/sh\nexec env%s %s \"$@\"\n", env.String(), sq(p.e.bin))
	if err := os.WriteFile(filepath.Join(binDir, "lore"), []byte(wrapper), 0o700); err != nil { //nolint:gosec // a script the agent runs
		t.Fatal(err)
	}
	qs := p.ds.Questions(p.useRG)
	if only := os.Getenv(envAgentOnly); only != "" {
		keep := map[string]bool{}
		for _, id := range strings.Split(only, ",") {
			keep[strings.TrimSpace(id)] = true
		}
		var sel []Question
		for _, q := range qs {
			if keep[q.ID] {
				sel = append(sel, q)
			}
		}
		qs = sel
	}
	setup := AgentSetup{Now: p.ds.Now, Skill: string(skill), LoreDir: p.home, LoreBin: binDir, GrepDir: p.tree, Model: model, MaxTurns: turns}
	agent := ClaudeAgent{Launcher: launcher, Model: model, MaxTurns: turns, Env: os.Environ()}
	started := time.Now()
	rs := RunAgents(context.Background(), agent, AgentTasks(qs, setup), jobs, limit, model)
	for _, r := range rs {
		t.Logf("%s %s %s: right %v, %d command(s), tokens in %d out %d, %.1f s %s", r.Q.ID, r.Arm, r.Q.Type, r.Correct, len(r.Run.Commands), r.Run.Tokens.In(), r.Run.Tokens.Output, r.Run.Wall.Seconds(), strings.Join(cut(r.Missing, 2), "; "))
	}
	section := AgentReport(AgentRunInfo{Started: started, Now: p.ds.Now, Model: model, Jobs: jobs, Lore: strings.TrimSpace(p.version)}, rs)
	path := os.Getenv(envReport)
	if path == "" {
		path = filepath.Join(p.root, "tmp", "bench", "recall.md")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(filepath.Clean(path))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(MergeSection(string(old), section)), 0o600); err != nil {
		t.Fatal(err)
	}
	answers, err := AgentAnswersJSON(rs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(strings.TrimSuffix(path, ".md")+"-agent.json", answers, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("report: %s (answers: %s-agent.json)\nsummary: %s", path, strings.TrimSuffix(path, ".md"), SummarizeAgents(rs).Line())
	if misses := AgentMisses(rs); len(misses) > 0 {
		t.Errorf("blind agent run: %d bar(s) missed (report %s):\n  %s", len(misses), path, strings.Join(misses, "\n  "))
	}
}

// envOr is the variable's value, or def when it is empty.
func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// envInt is the variable as a positive number, or def when it is empty.
func envInt(t *testing.T, name string, def int) int {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		t.Fatalf("%s=%q: want a positive number", name, v)
	}
	return n
}

// TestBenchAgentRegrade grades a saved run (<report>-agent.json) again
// with today's grader and rewrites the report section: no agent, no store,
// no cost. Same LORE_BENCH_SEED and LORE_BENCH_NOW as the run.
func TestBenchAgentRegrade(t *testing.T) {
	file := os.Getenv(envAnswers)
	if file == "" {
		t.Skipf("set %s to a saved answers file", envAnswers)
	}
	now := time.Now().UTC()
	if v := os.Getenv(envNow); v != "" {
		p, err := time.Parse(time.DateOnly, v)
		if err != nil {
			t.Fatalf("%s=%q: %v", envNow, v, err)
		}
		now = p
	}
	seed := uint64(DefaultSeed)
	if v := os.Getenv(envSeed); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			t.Fatalf("%s=%q: %v", envSeed, v, err)
		}
		seed = n
	}
	raw, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		t.Fatal(err)
	}
	ds := Generate(seed, now)
	model := envOr(envAgentModel, DefaultAgentModel)
	rs, err := RegradeAnswers(ds.Questions(true), raw, model)
	if err != nil {
		t.Fatal(err)
	}
	path := envOr(envReport, filepath.Join(repoRoot(t), "tmp", "bench", "recall.md"))
	old, err := os.ReadFile(filepath.Clean(path))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	section := AgentReport(AgentRunInfo{Started: time.Now(), Now: ds.Now, Model: model + " (regraded from " + filepath.Base(file) + ")", Jobs: 0, Lore: "saved answers"}, rs)
	if err := os.WriteFile(path, []byte(MergeSection(string(old), section)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("report: %s\nsummary: %s", path, SummarizeAgents(rs).Line())
	if misses := AgentMisses(rs); len(misses) > 0 {
		t.Errorf("blind agent run (regraded): %d bar(s) missed:\n  %s", len(misses), strings.Join(misses, "\n  "))
	}
}
