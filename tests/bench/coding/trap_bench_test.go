//go:build bench

package coding

// trap_bench_test.go — the blind agent trap run against real agents (task
// bench:agent). It costs money, so it runs only when asked: never in the
// gate, never on push. Per trap it writes two checkouts of orbit (one
// with the lore store, one without), has an agent with lore and an agent
// without it make the trap's edit, grades both diffs and merges its
// section into tmp/bench/coding.md.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/bench/recall"
)

// Environment knobs of the trap run (README.md lists them); the agent
// ones are the recall gate's blind agent run's.
const (
	envAgent      = "LORE_BENCH_AGENT"         // the command that starts a Claude Code compatible CLI (default "claude")
	envAgentModel = "LORE_BENCH_AGENT_MODEL"   // the model both agents run on (default recall.DefaultAgentModel)
	envAgentJobs  = "LORE_BENCH_AGENT_JOBS"    // agents at a time (default 4)
	envAgentLimit = "LORE_BENCH_AGENT_TIMEOUT" // one agent run is stopped after this (default 5m)
	envTrapTurns  = "LORE_BENCH_TRAP_TURNS"    // the agent's turn limit (default 25: it reads, asks and edits)
	envTrapOnly   = "LORE_BENCH_TRAP_ONLY"     // comma-separated trap task ids (default every trap)
)

func TestBenchTraps(t *testing.T) {
	started := time.Now()
	launcher := strings.Fields(os.Getenv(envAgent))
	if len(launcher) == 0 {
		launcher = []string{"claude"}
	}
	model := envOr(envAgentModel, recall.DefaultAgentModel)
	jobs := envInt(t, envAgentJobs, 4)
	turns := envInt(t, envTrapTurns, 25)
	limit := 5 * time.Minute
	if v := os.Getenv(envAgentLimit); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatalf("%s=%q: %v", envAgentLimit, v, err)
		}
		limit = d
	}
	root := repoRoot(t)
	dir := runDir(t)
	bin := loreBin(t, root, dir)
	skill, err := os.ReadFile(filepath.Join(root, "skills", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	traps := Traps()
	if only := os.Getenv(envTrapOnly); only != "" {
		keep := map[string]bool{}
		for _, id := range strings.Split(only, ",") {
			keep[strings.TrimSpace(id)] = true
		}
		var sel []Trap
		for _, tr := range traps {
			if keep[tr.Task] {
				sel = append(sel, tr)
			}
		}
		traps = sel
	}
	ctx := context.Background()
	version := ""
	dirs := map[string]TrapDirs{}
	bases := map[string]string{}
	for _, tr := range traps {
		at := filepath.Join(dir, "traps", tr.Task)
		// the lore agent's checkout: the store with every row
		le := newEnv(t, bin, filepath.Join(at, "lore-home"), filepath.Join(at, "lore", "orbit"))
		writeRepo(t, le, RepoFiles())
		if version == "" {
			version, _ = le.lore("--version")
		}
		binDir := filepath.Join(at, "lore-bin")
		if err := os.MkdirAll(binDir, 0o750); err != nil {
			t.Fatal(err)
		}
		var envs strings.Builder
		for _, kv := range le.env {
			envs.WriteString(" " + sq(kv))
		}
		wrapper := fmt.Sprintf("#!/bin/sh\nexec env%s %s \"$@\"\n", envs.String(), sq(le.bin))
		if err := os.WriteFile(filepath.Join(binDir, "lore"), []byte(wrapper), 0o700); err != nil { //nolint:gosec // a script the agent runs
			t.Fatal(err)
		}
		// the plain agent's checkout: the same files, no lore
		pe := newEnv(t, bin, filepath.Join(at, "plain-home"), filepath.Join(at, "plain", "orbit"))
		writeFiles(t, pe, RepoFiles())
		d := TrapDirs{Lore: le.repo, Plain: pe.repo, LoreBin: binDir}
		dirs[tr.Task] = d
		for _, c := range []string{d.Lore, d.Plain} {
			base, err := GitSnapshot(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			bases[c] = base
		}
	}
	tasks := TrapTasks(traps, string(skill), func(tr Trap) TrapDirs { return dirs[tr.Task] })
	agent := recall.ClaudeAgent{Launcher: launcher, Model: model, MaxTurns: turns, Env: os.Environ()}
	differ := func(ctx context.Context, dir string) (string, error) { return GitDiff(ctx, dir, bases[dir]) }
	rs := RunTraps(ctx, agent, tasks, jobs, limit, differ)
	for _, r := range rs {
		t.Logf("%s %s %s: %s, lore calls %d, %d command(s), tokens in %d out %d, %.1f s", r.Trap.Task, r.Trap.Hotfix, r.Arm, verdict(r), r.LoreCalls, len(r.Run.Commands), r.Run.Tokens.In(), r.Run.Tokens.Output, r.Run.Wall.Seconds())
	}
	section := TrapReport(TrapRunInfo{Started: started, Model: model, Jobs: jobs, Lore: strings.TrimSpace(version)}, rs)
	path := os.Getenv(envReport)
	if path == "" {
		path = filepath.Join(root, "tmp", "bench", "coding.md")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(filepath.Clean(path))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(MergeTrapSection(string(old), section)), 0o600); err != nil {
		t.Fatal(err)
	}
	answers, err := TrapAnswersJSON(rs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(strings.TrimSuffix(path, ".md")+"-traps.json", answers, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("report: %s (runs and diffs: %s-traps.json)\nsummary: %s", path, strings.TrimSuffix(path, ".md"), SummarizeTraps(rs).Line())
	if misses := TrapMisses(rs); len(misses) > 0 {
		t.Errorf("blind agent trap run: %d bar(s) missed (report %s):\n  %s", len(misses), path, strings.Join(misses, "\n  "))
	}
}

// sq is s quoted for sh.
func sq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

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
