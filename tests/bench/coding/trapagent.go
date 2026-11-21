package coding

// trapagent.go — the blind agent half of the coding gate (devco D-823,
// owner 2026-11-21): for each Trap, two agents on one small model make
// the edit its prompt asks for, each in its own checkout of orbit. The
// lore agent's checkout has the lore store (every Row) and the lore skill
// in its system prompt; the plain agent's checkout is the same files
// with no lore at all. Trap.FellIn grades each agent's diff. The bar: the
// lore agent edits and falls into no trap. The plain agent shows the trap
// is real: a trap it avoids proves nothing, and the report says so per
// trap.
//
// The agents and their CLI are the recall gate's (recall.Agent,
// recall.ClaudeAgent): the same blind, bare run, with Read, Edit and
// Write added so they can change files. Only the bench-tagged
// TestBenchTraps (task bench:agent) starts real agents.

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/tests/bench/recall"
)

// The two arms of the trap run.
const (
	ArmLore  recall.Arm = "lore"  // the store and the lore skill
	ArmPlain recall.Arm = "plain" // the same checkout without lore
)

// trapTools are the agent CLI tools of a trap run: a coding agent reads
// and edits files and runs commands.
var trapTools = []string{"Bash", "Read", "Edit", "Write", "Glob", "Grep"}

// trapDeny keeps the plain agent away from lore (its checkout has no
// store; a lore on the machine must not answer either).
var trapDeny = map[recall.Arm][]string{ArmPlain: {"Bash(lore:*)"}}

// codingRules is what both agents are told about the job.
const codingRules = `You are a coding agent working in the orbit repository (the current folder). Make the change the user asks for by editing the files with the Edit or Write tool. Keep the change small and in the style of the code around it. Do not ask questions, do not commit, and do not run builds or tests (the repository's dependencies are not installed). When the edit is made, reply with one line saying what you changed.`

// TrapSystem is the system prompt part for an arm: the same job for
// both; the lore agent is also told the repo has a lore store and gets
// the lore skill. Nothing of a trap's Bad or Good pattern is in it.
func TrapSystem(arm recall.Arm, skill string) string {
	if arm != ArmLore {
		return codingRules
	}
	return codingRules + "\n\nThe team keeps its knowledge for this repository (rules, hotfixes, decisions, patterns) in a lore store; the lore skill below says how to read it with the `lore` command (Bash tool).\n\n# The lore skill\n\n" + skill
}

// TrapTask is one trap for one arm.
type TrapTask struct {
	Trap  Trap
	Agent recall.AgentTask
}

// TrapDirs are a trap's two checkouts and the lore agent's PATH folder.
type TrapDirs struct {
	Lore, Plain string // the checkouts
	LoreBin     string // the folder holding the lore the lore agent runs
}

// TrapTasks builds both arms' tasks for traps, lore first per trap; dirs
// gives each trap's checkouts.
func TrapTasks(traps []Trap, skill string, dirs func(Trap) TrapDirs) []TrapTask {
	var out []TrapTask
	for _, tr := range traps {
		d := dirs(tr)
		q := recall.Question{ID: tr.Task, Text: tr.Prompt}
		out = append(out,
			TrapTask{Trap: tr, Agent: recall.AgentTask{Arm: ArmLore, Q: q, Dir: d.Lore, System: TrapSystem(ArmLore, skill), Prompt: tr.Prompt, PathPrefix: d.LoreBin, Deny: trapDeny[ArmLore], Tools: trapTools}},
			TrapTask{Trap: tr, Agent: recall.AgentTask{Arm: ArmPlain, Q: q, Dir: d.Plain, System: TrapSystem(ArmPlain, skill), Prompt: tr.Prompt, Deny: trapDeny[ArmPlain], Tools: trapTools}})
	}
	return out
}

// TrapResult is one arm's run on one trap, graded.
type TrapResult struct {
	Trap      Trap
	Arm       recall.Arm
	Run       recall.AgentRun
	Diff      string
	Edited    bool   // the diff adds a line in a file the trap reads
	Fell      bool   // Trap.FellIn on the diff
	Line      string // the line that fell in
	Fixed     bool   // the diff adds the hotfix's Good line
	LoreCalls int    // commands that ran lore
	RanLore   bool   // the plain agent ran lore (its run is not blind)
	Err       string // the run or the diff failed
}

// Avoided reports whether the arm made the edit and fell into nothing.
func (r TrapResult) Avoided() bool { return r.Err == "" && r.Edited && !r.Fell }

// Differ reads what an agent changed in its checkout.
type Differ func(ctx context.Context, dir string) (string, error)

// lorePathspec is every path of a checkout but the lore store's folder
// (lore writes its caches there while the agent reads).
var lorePathspec = []string{"--", ".", ":(exclude).lore"}

// GitSnapshot stages every file of dir and returns the tree it makes: the
// checkout as the agent found it (lore init's files included), to diff
// against afterwards.
func GitSnapshot(ctx context.Context, dir string) (string, error) {
	if err := gitIn(ctx, dir, append([]string{"add", "-A"}, lorePathspec...)...); err != nil {
		return "", err
	}
	out, err := gitOut(ctx, dir, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// GitDiff is every change in dir since the tree base (GitSnapshot), new
// files included, the lore store's folder left out.
func GitDiff(ctx context.Context, dir, base string) (string, error) {
	if err := gitIn(ctx, dir, append([]string{"add", "-A"}, lorePathspec...)...); err != nil {
		return "", err
	}
	return gitOut(ctx, dir, append([]string{"diff", "--cached", "--no-color", "--no-ext-diff", base}, lorePathspec...)...)
}

// gitIn runs git in dir.
func gitIn(ctx context.Context, dir string, args ...string) error {
	_, err := gitOut(ctx, dir, args...)
	return err
}

// gitOut runs git in dir and returns its output.
func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s in %s: %w: %s", args[0], dir, err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// RunTraps runs every task on a, at most jobs at a time, each stopped
// after limit, reads each checkout's diff with diff and grades it;
// results keep the tasks' order.
func RunTraps(ctx context.Context, a recall.Agent, tasks []TrapTask, jobs int, limit time.Duration, diff Differ) []TrapResult {
	jobs = max(jobs, 1)
	out := make([]TrapResult, len(tasks))
	next := make(chan int)
	var wg sync.WaitGroup
	for range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				tctx, cancel := context.WithTimeout(ctx, limit)
				start := time.Now()
				run := a.Run(tctx, tasks[i].Agent)
				if run.Wall == 0 {
					run.Wall = time.Since(start)
				}
				if tctx.Err() != nil && run.Err == "" {
					run.Err = fmt.Sprintf("stopped after %s", limit)
				}
				cancel()
				d, err := diff(ctx, tasks[i].Agent.Dir)
				if err != nil && run.Err == "" {
					run.Err = err.Error()
				}
				out[i] = GradeTrap(tasks[i], run, d)
			}
		}()
	}
	for i := range tasks {
		next <- i
	}
	close(next)
	wg.Wait()
	return out
}

// GradeTrap grades one arm's run on its trap from the diff it left.
func GradeTrap(task TrapTask, run recall.AgentRun, diff string) TrapResult {
	tr := task.Trap
	r := TrapResult{Trap: tr, Arm: task.Agent.Arm, Run: run, Diff: diff, Err: run.Err}
	r.Fell, r.Line = tr.FellIn(diff)
	r.Edited, r.Fixed = addsIn(tr, diff)
	for _, c := range run.Commands {
		if recall.RunsLore(c) {
			r.LoreCalls++
		}
	}
	if r.Arm == ArmPlain && r.LoreCalls > 0 {
		r.RanLore = true
	}
	return r
}

// addsIn reports whether diff adds a line in a file tr reads, and whether
// one of those lines is tr's Good line.
func addsIn(tr Trap, diff string) (edited, good bool) {
	file := ""
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(l, "+++ "), "b/")
		case strings.HasPrefix(l, "+") && tr.Path.MatchString(file):
			add := strings.TrimSpace(strings.TrimPrefix(l, "+"))
			if add != "" {
				edited = true
			}
			if tr.Good != nil && tr.Good.MatchString(add) {
				good = true
			}
		}
	}
	return edited, good
}
