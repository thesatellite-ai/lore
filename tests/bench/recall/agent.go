package recall

// agent.go — the blind agent run (owner 2026-11-21, accuracy first): the
// same questions asked of two agents on one small model. The lore agent
// has the lore skill (skills/SKILL.md) and the lore command only; the
// grep agent has rg / grep / cat / ls over the markdown tree. Neither
// sees the ground truth: a question's prompt carries the question text
// and the run's pinned date, nothing of its Required facts. Both final
// answers are graded by Grade, the same grader as the scripted arms, and
// every run's tokens and wall time are recorded.
//
// The agent itself sits behind the Agent interface so the harness, the
// grading and the report are unit tested with a fake (agent_test.go); the
// real one (ClaudeAgent) starts a Claude Code compatible CLI in print mode
// and reads its stream-json events. Running it costs money, so only the
// bench-tagged TestBenchAgent (task bench:agent) ever starts one.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Arm is one side of the blind agent run.
type Arm string

// The two agents.
const (
	ArmLore Arm = "lore" // skills/SKILL.md, the lore command only
	ArmGrep Arm = "grep" // rg / grep / cat / ls over the markdown tree
)

// DefaultAgentModel is the model both agents run on (owner: a small model,
// so lore has to carry the retrieval, not the model).
const DefaultAgentModel = "claude-haiku-4-5"

// readOnlyTools are the commands the grep agent may start (the first word
// of each command; a pipe stage after the first may filter with any of
// them, in either arm).
var readOnlyTools = []string{"rg", "grep", "cat", "ls", "find", "head", "tail", "wc", "sort", "uniq", "cut", "awk", "sed", "echo", "cd", "pwd", "tr", "xargs", "date"}

// shellChecks are commands that read no notes: the lore agent may start
// them besides lore (checking that lore is there or that a folder has
// .lore/, as the skill's detect step does; printing a separator).
var shellChecks = []string{"command", "which", "type", "echo", "printf", "pwd", "date", "true", "cd", "[", "test"}

// denyFor is the tool rules the agent CLI is started with per arm: the
// lore agent cannot read files or the tree, the grep agent cannot run
// lore. The command check in OffTool is the second line (a rule the CLI
// does not apply still shows up as an off-tool command and a wrong
// answer).
var denyFor = map[Arm][]string{
	ArmLore: {"Bash(cat:*)", "Bash(rg:*)", "Bash(grep:*)", "Bash(find:*)", "Bash(ls:*)", "Bash(head:*)", "Bash(tail:*)", "Bash(sed:*)", "Bash(awk:*)", "Bash(sqlite3:*)", "Bash(git:*)", "Bash(python3:*)", "Bash(jq:*)"},
	ArmGrep: {"Bash(lore:*)"},
}

// AgentTask is one question for one agent.
type AgentTask struct {
	Arm        Arm
	Q          Question
	Dir        string // the folder the agent starts in
	System     string // appended to the agent's system prompt
	Prompt     string // the user turn: the question
	PathPrefix string // a folder put first on PATH (the lore agent's lore), or ""
	Deny       []string
	// Tools are the CLI tools the agent gets (default: Bash only, the
	// question arms). A coding task that edits files (the coding gate's
	// trap run) adds Read, Edit and Write; every tool named here is also
	// allowed without a prompt (--allowedTools), the deny rules still win.
	Tools []string
	// Checkout runs the CLI with the settings of the folder it starts in
	// (its hooks and CLAUDE.md, what lore init and lore setup
	// --agent-hooks installed there) instead of bare; the machine's user
	// settings, plugins and MCP servers stay out either way (devco D-835:
	// a bare run never fired lore's pre-edit context hook).
	Checkout bool
}

// Tokens is what one agent run consumed, summed over every model turn.
type Tokens struct {
	Input      int // uncached input
	CacheWrite int
	CacheRead  int
	Output     int
}

// In is every input token, cached or not.
func (t Tokens) In() int { return t.Input + t.CacheWrite + t.CacheRead }

// add sums two counts.
func (t Tokens) add(o Tokens) Tokens {
	return Tokens{t.Input + o.Input, t.CacheWrite + o.CacheWrite, t.CacheRead + o.CacheRead, t.Output + o.Output}
}

// AgentRun is what one agent run returned.
type AgentRun struct {
	Answer   string   // the final answer text
	Commands []string // every shell command it ran, in order (denied ones left out)
	Denied   []string // commands the deny rules refused (they read nothing)
	Tokens   Tokens
	CostUSD  float64
	Turns    int
	Models   []string // the models that answered (must be the one asked for)
	Wall     time.Duration
	Err      string // the run failed: why (an error run counts as wrong)
}

// Agent answers one task.
type Agent interface {
	Run(ctx context.Context, task AgentTask) AgentRun
}

// AgentResult is one question's graded run of one arm.
type AgentResult struct {
	Q       Question
	Arm     Arm
	Run     AgentRun
	Correct bool
	Missing []string // what Grade found missing or stale, the error, the off-tool commands
	OffTool []string // commands outside the arm's tools
}

// AgentSetup is what both arms are told; nothing in it comes from a
// question's expected facts.
type AgentSetup struct {
	Now      time.Time // the pinned now relative dates resolve against
	Skill    string    // skills/SKILL.md, the lore agent's only guide
	LoreDir  string    // where the lore agent starts (the person's home)
	LoreBin  string    // the folder holding the lore the agent runs
	GrepDir  string    // the markdown tree's root
	Model    string
	MaxTurns int
}

// answerRules is the answer shape both agents are asked for: the facts
// verbatim, so the same grader reads both.
const answerRules = `Answer the question from the notes alone. Quote every relevant fact verbatim as the notes write it (whole lines for status updates, decisions, reasons, tasks, meetings and journal events; names, roles and companies exactly as written). When a value changed, give the current value and say what it was before. List every item that answers the question, not a sample. No preamble and no follow-up offers.`

// AgentSystem is the system prompt part for an arm.
func AgentSystem(arm Arm, s AgentSetup) string {
	head := fmt.Sprintf("You answer questions about one person's notes. Today is %s (%s); resolve \"yesterday\", \"last week\" and the like against this date. The person is \"%s\" in task notes (owed by %s / owed to %s).\n\n", s.Now.Format(time.DateOnly), s.Now.Weekday(), Me, Me, Me)
	switch arm {
	case ArmLore:
		return head + "The notes are in a lore store. Read them only with the `lore` command (Bash tool), as the lore skill below documents; start with `lore ask --query \"<the question>\"` and use other documented lore read commands only when that answer is not enough. Run no other command and do not read files.\n\n" + answerRules + "\n\n# The lore skill\n\n" + s.Skill
	default:
		return head + "The notes are markdown files in the current folder. Read them with rg, grep, cat, ls, find, head (Bash tool); start with `ls` to see the layout. Run no other kind of command.\n\n" + answerRules
	}
}

// AgentTasks builds both arms' tasks for qs, lore first per question.
func AgentTasks(qs []Question, s AgentSetup) []AgentTask {
	var out []AgentTask
	for _, q := range qs {
		out = append(out,
			AgentTask{Arm: ArmLore, Q: q, Dir: s.LoreDir, System: AgentSystem(ArmLore, s), Prompt: q.Text, PathPrefix: s.LoreBin, Deny: denyFor[ArmLore]},
			AgentTask{Arm: ArmGrep, Q: q, Dir: s.GrepDir, System: AgentSystem(ArmGrep, s), Prompt: q.Text, Deny: denyFor[ArmGrep]})
	}
	return out
}

// RunAgents runs every task on a with at most jobs at a time, each
// stopped after limit, and grades the answers; results keep the tasks'
// order.
func RunAgents(ctx context.Context, a Agent, tasks []AgentTask, jobs int, limit time.Duration, model string) []AgentResult {
	jobs = max(jobs, 1)
	out := make([]AgentResult, len(tasks))
	next := make(chan int)
	var wg sync.WaitGroup
	for range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				tctx, cancel := context.WithTimeout(ctx, limit)
				start := time.Now()
				run := a.Run(tctx, tasks[i])
				if run.Wall == 0 {
					run.Wall = time.Since(start)
				}
				if tctx.Err() != nil && run.Err == "" {
					run.Err = fmt.Sprintf("stopped after %s", limit)
				}
				cancel()
				out[i] = GradeAgent(tasks[i], run, model)
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

// GradeAgent grades one run: GradeAnswer on the final answer, and wrong
// when the run failed, ran a command outside its arm's tools, or answered
// on another model than asked.
func GradeAgent(task AgentTask, run AgentRun, model string) AgentResult {
	r := AgentResult{Q: task.Q, Arm: task.Arm, Run: run}
	r.Correct, r.Missing = GradeAnswer(task.Q, run.Answer)
	if run.Err != "" {
		r.Correct = false
		r.Missing = append([]string{"run failed: " + run.Err}, r.Missing...)
	}
	for _, c := range run.Commands {
		if OffTool(task.Arm, c) {
			r.OffTool = append(r.OffTool, c)
		}
	}
	if len(r.OffTool) > 0 {
		r.Correct = false
		r.Missing = append(r.Missing, "off-tool: "+r.OffTool[0])
	}
	if model != "" {
		for _, m := range run.Models {
			if !strings.HasPrefix(m, model) {
				r.Correct = false
				r.Missing = append(r.Missing, "answered on "+m)
			}
		}
	}
	return r
}

// OffTool reports whether cmd starts a program outside arm's tools: every
// command of a list (split at &&, ||, ;, newlines) must start with lore
// or a shell check that reads no notes (the lore agent), or with a
// read-only tool and never lore (the grep agent); a pipe stage after the
// first may filter with a read-only tool.
func OffTool(arm Arm, cmd string) bool {
	for _, stages := range splitShell(cmd) {
		for i, st := range stages {
			w := firstWord(st)
			if w == "" {
				continue
			}
			switch {
			case arm == ArmLore && i == 0 && w != "lore" && !slices.Contains(shellChecks, w):
				return true
			case arm == ArmLore && i > 0 && w != "lore" && !slices.Contains(readOnlyTools, w):
				return true
			case arm == ArmGrep && !slices.Contains(readOnlyTools, w):
				return true
			}
		}
	}
	return false
}

// RunsLore reports whether cmd starts lore in any of its commands or
// pipe stages (the coding gate's trap run counts the lore agent's lore
// calls and refuses a plain agent that ran lore).
func RunsLore(cmd string) bool {
	for _, stages := range splitShell(cmd) {
		for _, st := range stages {
			if firstWord(st) == "lore" {
				return true
			}
		}
	}
	return false
}

// splitShell splits a shell command list into its commands (at &&, ||,
// ;, & and newlines) and each command into its pipe stages, outside
// quotes only: `rg "a|b" x` is one stage, its | is the pattern's.
func splitShell(cmd string) [][]string {
	var list [][]string
	var stages []string
	var cur strings.Builder
	endStage := func() { stages = append(stages, cur.String()); cur.Reset() }
	endCmd := func() { endStage(); list = append(list, stages); stages = nil }
	var quote rune
	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' && i+1 < len(rs) {
				cur.WriteRune(c)
				i++
				c = rs[i]
			} else if c == quote {
				quote = 0
			}
			cur.WriteRune(c)
		case c == '\\' && i+1 < len(rs):
			cur.WriteRune(c)
			i++
			cur.WriteRune(rs[i])
		case c == '\'' || c == '"':
			quote = c
			cur.WriteRune(c)
		case c == '|' && i+1 < len(rs) && rs[i+1] == '|', c == '&' && i+1 < len(rs) && rs[i+1] == '&':
			endCmd()
			i++
		case c == '|':
			endStage()
		case c == ';' || c == '\n':
			endCmd()
		case c == '&' && (i == 0 || rs[i-1] != '>') && (i+1 == len(rs) || rs[i+1] != '>'):
			endCmd()
		default:
			cur.WriteRune(c)
		}
	}
	endCmd()
	return list
}

// firstWord is the program a command stage starts: leading VAR=value
// assignments and a path are dropped (/x/bin/lore is lore).
func firstWord(stage string) string {
	for _, w := range strings.Fields(stage) {
		if strings.Contains(w, "=") && !strings.HasPrefix(w, "-") {
			continue
		}
		w = strings.Trim(w, "()")
		if i := strings.LastIndex(w, "/"); i >= 0 {
			w = w[i+1:]
		}
		return w
	}
	return ""
}

// ClaudeAgent runs a Claude Code compatible CLI in print mode: bare (no
// hooks, memory, CLAUDE.md discovery or skills of the machine; a
// Checkout task loads its folder's own settings only), the Bash
// tool only, the arm's deny rules, stream-json events read for the
// commands, the answer and the token counts.
type ClaudeAgent struct {
	Launcher []string // the command that starts the CLI, e.g. ["claude"]
	Model    string
	MaxTurns int
	Env      []string // the environment (PATH is extended per task)
}

// Args is the CLI arguments for one task (after the launcher).
func (c ClaudeAgent) Args(task AgentTask) []string {
	tools := "Bash"
	if len(task.Tools) > 0 {
		tools = strings.Join(task.Tools, ",")
	}
	isolate := []string{"--bare"}
	if task.Checkout {
		isolate = []string{"--setting-sources", "project,local", "--strict-mcp-config"}
	}
	args := append([]string{"-p", task.Prompt}, isolate...)
	args = append(args, "--disable-slash-commands", "--no-session-persistence",
		"--tools", tools, "--model", c.Model, "--max-turns", fmt.Sprint(c.MaxTurns),
		"--output-format", "stream-json", "--verbose", "--append-system-prompt", task.System)
	if len(task.Tools) > 0 {
		args = append(args, "--allowedTools", tools)
	}
	if len(task.Deny) > 0 {
		args = append(args, "--disallowedTools", strings.Join(task.Deny, ","))
	}
	return args
}

// Run starts the CLI for task and parses its events.
func (c ClaudeAgent) Run(ctx context.Context, task AgentTask) AgentRun {
	if len(c.Launcher) == 0 {
		return AgentRun{Err: "no agent launcher"}
	}
	argv := append(slices.Clone(c.Launcher[1:]), c.Args(task)...)
	cmd := exec.CommandContext(ctx, c.Launcher[0], argv...)
	cmd.Dir = task.Dir
	cmd.Env = withPath(c.Env, task.PathPrefix)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return AgentRun{Err: err.Error()}
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return AgentRun{Err: err.Error()}
	}
	run, perr := ParseStream(out)
	werr := cmd.Wait()
	run.Wall = time.Since(start)
	switch {
	case run.Err != "":
	case perr != nil:
		run.Err = perr.Error()
	case werr != nil && run.Answer == "":
		run.Err = fmt.Sprintf("%v: %s", werr, strings.TrimSpace(strings.SplitN(strings.TrimSpace(stderr.String()), "\n", 2)[0]))
	}
	return run
}

// withPath is env with dir put first on PATH (env unchanged for "").
func withPath(env []string, dir string) []string {
	out := slices.Clone(env)
	if dir == "" {
		return out
	}
	for i, kv := range out {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			out[i] = "PATH=" + dir + ":" + v
			return out
		}
	}
	return append(out, "PATH="+dir)
}

// streamEvent is the part of one stream-json event the harness reads.
type streamEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Message struct {
		Content []struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Input struct {
				Command string `json:"command"`
			} `json:"input"`
		} `json:"content"`
	} `json:"message"`
	Result  string `json:"result"`
	Denials []struct {
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	} `json:"permission_denials"`
	IsError    bool    `json:"is_error"`
	NumTurns   int     `json:"num_turns"`
	TotalCost  float64 `json:"total_cost_usd"`
	ModelUsage map[string]struct {
		Input      int `json:"inputTokens"`
		Output     int `json:"outputTokens"`
		CacheRead  int `json:"cacheReadInputTokens"`
		CacheWrite int `json:"cacheCreationInputTokens"`
	} `json:"modelUsage"`
}

// ParseStream reads stream-json events: the Bash commands of the
// assistant turns (less the ones the deny rules refused), then the result event's answer, turns, cost and token
// counts (summed over the models). Lines that are not JSON are skipped
// (a launcher may print notes); no result event is an error.
func ParseStream(r io.Reader) (AgentRun, error) {
	var run AgentRun
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	done := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var ev streamEvent
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "assistant":
			for _, c := range ev.Message.Content {
				if c.Type == "tool_use" {
					cmd := c.Input.Command
					if c.Name != "Bash" {
						cmd = c.Name + " tool"
					}
					run.Commands = append(run.Commands, cmd)
				}
			}
		case "result":
			done = true
			run.Answer, run.Turns, run.CostUSD = ev.Result, ev.NumTurns, ev.TotalCost
			if ev.IsError || (ev.Subtype != "" && ev.Subtype != "success") {
				run.Err = "agent ended with " + ev.Subtype
			}
			names := make([]string, 0, len(ev.ModelUsage))
			for m, u := range ev.ModelUsage {
				names = append(names, m)
				run.Tokens = run.Tokens.add(Tokens{Input: u.Input, CacheWrite: u.CacheWrite, CacheRead: u.CacheRead, Output: u.Output})
			}
			sort.Strings(names)
			run.Models = names
			for _, d := range ev.Denials {
				cmd := d.ToolInput.Command
				if d.ToolName != "Bash" {
					cmd = d.ToolName + " tool"
				}
				run.Denied = append(run.Denied, cmd)
				if i := slices.Index(run.Commands, cmd); i >= 0 {
					run.Commands = slices.Delete(run.Commands, i, i+1)
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return run, fmt.Errorf("read agent events: %w", err)
	}
	if !done {
		return run, fmt.Errorf("agent printed no result event")
	}
	return run, nil
}

// stopWords carry no fact: an answer may say "you" for the note's "me" or
// drop "the" and still state the fact.
var stopWords = map[string]bool{"a": true, "an": true, "the": true, "to": true, "of": true, "and": true, "or": true, "is": true, "was": true, "are": true, "on": true,
	"in": true, "at": true, "for": true, "with": true, "about": true, "me": true, "i": true, "my": true, "you": true, "your": true, "it": true, "by": true, "from": true}

// words is s lowercased and cut into words (letters, digits, - and .
// inside a word); a hyphenated word also adds its parts.
func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r > 127)
	}) {
		w = strings.Trim(w, "-.")
		if w == "" {
			continue
		}
		out[w] = true
		for _, p := range strings.Split(w, "-") {
			out[p] = true
		}
	}
	return out
}

// states reports whether answer states fact: the fact's text is in it, or
// every word of the fact that carries meaning is (an agent writes "owes
// you: the board deck (task #61)" for the note "owes me the board deck
// #61").
func states(answer, fact string) bool {
	if strings.Contains(strings.ToLower(answer), strings.ToLower(fact)) {
		return true
	}
	have := words(answer)
	need := 0
	for w := range words(fact) {
		// a hyphenated word's parts are in the set too: check those
		if stopWords[w] || strings.Contains(w, "-") {
			continue
		}
		need++
		if !have[w] {
			return false
		}
	}
	return need > 0
}

// GradeAnswer is Grade for an agent's own words: a required fact counts
// when the answer states it (states: its text, or every word of it that
// carries meaning), so the answer is graded on the facts, not on copying
// lines; a forbidden (stale or done) fact still counts only as written,
// unless the current value is stated too. The same grade for both arms.
func GradeAnswer(q Question, answer string) (correct bool, missing []string) {
	for _, r := range q.Required {
		if !states(answer, r) {
			missing = append(missing, "missing "+r)
		}
	}
	low := strings.ToLower(answer)
	for _, f := range q.Forbidden {
		if strings.Contains(low, strings.ToLower(f)) && (q.Current == "" || !states(answer, q.Current)) {
			missing = append(missing, "stale "+f)
		}
	}
	return len(missing) == 0, missing
}
