package recall

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// agentSetup is a setup over the generated questions with a stand-in skill.
func agentSetup() (AgentSetup, []Question) {
	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	ds := Generate(DefaultSeed, now)
	return AgentSetup{Now: now, Skill: "LORE-SKILL-TEXT", LoreDir: "/h", LoreBin: "/b", GrepDir: "/t", Model: DefaultAgentModel, MaxTurns: 8}, ds.Questions(true)
}

// The run is blind: nothing an agent is given names an expected fact the
// question itself does not spell.
func TestAgentPromptsBlind(t *testing.T) {
	s, qs := agentSetup()
	tasks := AgentTasks(qs, s)
	if len(tasks) != 2*len(qs) {
		t.Fatalf("%d tasks for %d questions, want two each", len(tasks), len(qs))
	}
	for _, task := range tasks {
		given := strings.ToLower(task.System + "\n" + task.Prompt)
		for _, f := range append(slices.Clone(task.Q.Required), task.Q.Forbidden...) {
			if strings.Contains(strings.ToLower(task.Q.Text), strings.ToLower(f)) {
				continue
			}
			if strings.Contains(given, strings.ToLower(f)) {
				t.Errorf("%s %s: the agent is given the expected fact %q", task.Q.ID, task.Arm, f)
			}
		}
		if task.Prompt != task.Q.Text {
			t.Errorf("%s %s: prompt %q, want the question as written", task.Q.ID, task.Arm, task.Prompt)
		}
	}
	lore, grep := tasks[0], tasks[1]
	if lore.Arm != ArmLore || !strings.Contains(lore.System, "LORE-SKILL-TEXT") || lore.PathPrefix != "/b" || lore.Dir != "/h" {
		t.Errorf("lore task: %+v", lore)
	}
	if grep.Arm != ArmGrep || strings.Contains(grep.System, "LORE-SKILL-TEXT") || grep.PathPrefix != "" || grep.Dir != "/t" {
		t.Errorf("grep task: %+v", grep)
	}
	if !slices.Contains(grep.Deny, "Bash(lore:*)") || !slices.Contains(lore.Deny, "Bash(cat:*)") {
		t.Errorf("deny rules: lore %v, grep %v", lore.Deny, grep.Deny)
	}
}

func TestOffTool(t *testing.T) {
	cases := []struct {
		arm  Arm
		cmd  string
		want bool
	}{
		{ArmLore, `lore ask --query "who is alice-ng"`, false},
		{ArmLore, `/x/agentbin/lore ask --query q | head -50`, false},
		{ArmLore, `NO_COLOR=1 lore search --query x && lore resource show --kind person --name y`, false},
		{ArmLore, `command -v lore >/dev/null 2>&1 && lore version || echo "MISSING"`, false},
		{ArmLore, `[ -d .lore ] && echo "yes" || echo "no"`, false},
		{ArmLore, `cat people/alice-ng.md`, true},
		{ArmLore, `which lore && ls ~/.lore`, true},
		{ArmLore, `lore ask --query x; rg alice ~/.lore`, true},
		{ArmLore, `lore ask --query x | python3 -c 1`, true},
		{ArmLore, `Read tool`, true},
		{ArmGrep, `rg -n alice . | head`, false},
		{ArmGrep, `ls && cat people/alice-ng.md`, false},
		{ArmGrep, `rg -i "grace.?lee|owed" --type md`, false},
		{ArmGrep, `grep -r "2026-11-12\\|11-12" . --include="*.md" 2>&1 | head`, false},
		{ArmGrep, `rg 'a|b' x; ls journal/ | grep -E "2026-11-20|Nov-20"`, false},
		{ArmGrep, `rg x 2>&1 | head`, false},
		{ArmGrep, `rg "a|b" x && lore ask`, true},
		{ArmGrep, `lore ask --query x`, true},
		{ArmGrep, `rg x | lore ask`, true},
	}
	for _, c := range cases {
		if got := OffTool(c.arm, c.cmd); got != c.want {
			t.Errorf("OffTool(%s, %q) = %v, want %v", c.arm, c.cmd, got, c.want)
		}
	}
}

func TestParseStream(t *testing.T) {
	in := strings.Join([]string{
		`a launcher note`,
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"content":[{"type":"thinking"},{"type":"tool_use","name":"Bash","input":{"command":"lore ask --query x"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/etc/x"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"find . -name x"}}]}}`,
		`{"type":"result","subtype":"success","result":"alice-ng, engineer","num_turns":3,"total_cost_usd":0.25,"permission_denials":[{"tool_name":"Bash","tool_input":{"command":"find . -name x"}}],"modelUsage":{"m-b":{"inputTokens":10,"outputTokens":5,"cacheReadInputTokens":100,"cacheCreationInputTokens":50},"m-a":{"inputTokens":1,"outputTokens":2,"cacheReadInputTokens":3,"cacheCreationInputTokens":4}}}`,
	}, "\n")
	run, err := ParseStream(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if run.Answer != "alice-ng, engineer" || run.Turns != 3 || run.CostUSD != 0.25 || run.Err != "" {
		t.Errorf("run %+v", run)
	}
	if want := []string{"lore ask --query x", "Read tool"}; !slices.Equal(run.Commands, want) {
		t.Errorf("commands %q, want %q", run.Commands, want)
	}
	if want := (Tokens{Input: 11, CacheWrite: 54, CacheRead: 103, Output: 7}); run.Tokens != want || run.Tokens.In() != 168 {
		t.Errorf("tokens %+v, want %+v", run.Tokens, want)
	}
	if !slices.Equal(run.Denied, []string{"find . -name x"}) {
		t.Errorf("denied %q, want the refused find (and not in commands)", run.Denied)
	}
	if !slices.Equal(run.Models, []string{"m-a", "m-b"}) {
		t.Errorf("models %v", run.Models)
	}
	if _, err := ParseStream(strings.NewReader(`{"type":"assistant"}`)); err == nil {
		t.Error("no result event: want an error")
	}
	bad, err := ParseStream(strings.NewReader(`{"type":"result","subtype":"error_max_turns","is_error":true}`))
	if err != nil || !strings.Contains(bad.Err, "error_max_turns") {
		t.Errorf("max turns: %+v %v", bad, err)
	}
}

// fakeAgent answers from a function of the task.
type fakeAgent func(ctx context.Context, task AgentTask) AgentRun

func (f fakeAgent) Run(ctx context.Context, task AgentTask) AgentRun { return f(ctx, task) }

// rightRun answers task with every required fact through one command.
func rightRun(task AgentTask) AgentRun {
	cmd := "lore ask --query q"
	if task.Arm == ArmGrep {
		cmd = "rg -n x ."
	}
	return AgentRun{Answer: strings.Join(task.Q.Required, "\n"), Commands: []string{cmd}, Models: []string{DefaultAgentModel + "-20251001"}, Tokens: Tokens{Input: 10, Output: 2}, Wall: time.Second}
}

func TestRunAgentsGradesAndBars(t *testing.T) {
	s, qs := agentSetup()
	tasks := AgentTasks(qs, s)
	// lore right everywhere; grep misses the harder set
	a := fakeAgent(func(_ context.Context, task AgentTask) AgentRun {
		r := rightRun(task)
		if task.Arm == ArmGrep && task.Q.Type.Harder() {
			r.Answer = "no idea"
		}
		return r
	})
	rs := RunAgents(context.Background(), a, tasks, 3, time.Minute, DefaultAgentModel)
	if len(rs) != len(tasks) {
		t.Fatalf("%d results for %d tasks", len(rs), len(tasks))
	}
	for i, r := range rs {
		if r.Q.ID != tasks[i].Q.ID || r.Arm != tasks[i].Arm {
			t.Fatalf("result %d is %s %s, want the task order", i, r.Q.ID, r.Arm)
		}
	}
	if m := AgentMisses(rs); len(m) != 0 {
		t.Errorf("lore right everywhere, grep wrong on the harder set: want a pass, got %q", m)
	}
	sum := SummarizeAgents(rs)
	nh := sum.Harder[ArmLore].N
	if sum.Harder[ArmGrep].Correct != 0 || sum.Harder[ArmLore].Correct != nh || sum.All(ArmLore).Tokens.In() != 10*len(qs) {
		t.Errorf("summary %s", sum.Line())
	}

	// equal on the harder set and not full: lore does not beat grep
	worse := fakeAgent(func(_ context.Context, task AgentTask) AgentRun {
		r := rightRun(task)
		if task.Q.Type == TypeTypo {
			r.Answer = "no idea"
		}
		if task.Arm == ArmLore && task.Q.Type == TypeWho {
			r.Answer = ""
		}
		return r
	})
	m := strings.Join(AgentMisses(RunAgents(context.Background(), worse, tasks, 2, time.Minute, DefaultAgentModel)), "\n")
	for _, want := range []string{"normal set: lore agent", "harder set: lore agent", "(who is X): lore agent wrong"} {
		if !strings.Contains(m, want) {
			t.Errorf("misses lack %q:\n%s", want, m)
		}
	}
	// both full on the harder set: nothing left to beat
	if m := AgentMisses(RunAgents(context.Background(), fakeAgent(func(_ context.Context, task AgentTask) AgentRun { return rightRun(task) }), tasks, 4, time.Minute, DefaultAgentModel)); len(m) != 0 {
		t.Errorf("both arms right everywhere: want a pass, got %q", m)
	}
}

func TestGradeAgentWrongWhenNotBlindOrFailed(t *testing.T) {
	s, qs := agentSetup()
	task := AgentTasks(qs[:1], s)[0]
	cases := map[string]func(r *AgentRun){
		"off-tool":    func(r *AgentRun) { r.Commands = append(r.Commands, "cat ~/.lore/x.json") },
		"run failed":  func(r *AgentRun) { r.Err = "boom" },
		"answered on": func(r *AgentRun) { r.Models = []string{"another-model"} },
	}
	for want, change := range cases {
		run := rightRun(task)
		change(&run)
		g := GradeAgent(task, run, DefaultAgentModel)
		if g.Correct || !strings.Contains(strings.Join(g.Missing, ";"), want) {
			t.Errorf("%s: correct %v, missing %q", want, g.Correct, g.Missing)
		}
	}
	if g := GradeAgent(task, rightRun(task), DefaultAgentModel); !g.Correct {
		t.Errorf("a right answer graded wrong: %q", g.Missing)
	}
}

func TestRunAgentsStopsASlowAgent(t *testing.T) {
	s, qs := agentSetup()
	slow := fakeAgent(func(ctx context.Context, task AgentTask) AgentRun {
		<-ctx.Done()
		return rightRun(task)
	})
	rs := RunAgents(context.Background(), slow, AgentTasks(qs[:1], s), 1, 20*time.Millisecond, DefaultAgentModel)
	if rs[0].Correct || !strings.Contains(rs[0].Run.Err, "stopped after") {
		t.Errorf("slow agent: %+v", rs[0])
	}
	if m := strings.Join(AgentMisses(rs), "\n"); !strings.Contains(m, "lore agent: 1 run(s) failed") {
		t.Errorf("misses: %s", m)
	}
}

func TestClaudeAgentRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake launcher is a shell script")
	}
	dir := t.TempDir()
	// the fake CLI answers with the first PATH entry and keeps its
	// arguments in a file
	script := `#!/bin/sh
first=${PATH%%:*}
printf '%s ' "$@" > "$(dirname "$0")/args"
printf 'note\n{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"lore ask"}}]}}\n'
printf '{"type":"result","subtype":"success","result":"path %s","num_turns":2,"modelUsage":{"claude-haiku-4-5":{"inputTokens":7,"outputTokens":3}}}\n' "$first"
`
	bin := filepath.Join(dir, "fake-cli")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil { //nolint:gosec // a test launcher
		t.Fatal(err)
	}
	s, qs := agentSetup()
	task := AgentTasks(qs[:1], s)[0]
	task.Dir, task.PathPrefix = dir, "/lore-bin"
	a := ClaudeAgent{Launcher: []string{"sh", bin}, Model: DefaultAgentModel, MaxTurns: 8, Env: []string{"PATH=/usr/bin:/bin"}}
	run := a.Run(context.Background(), task)
	if run.Err != "" {
		t.Fatal(run.Err)
	}
	if run.Answer != "path /lore-bin" {
		t.Errorf("answer %q, want the lore folder first on PATH", run.Answer)
	}
	args, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--bare", "--tools Bash", "--model " + DefaultAgentModel, "--max-turns 8", "--output-format stream-json", "Bash(cat:*),Bash(rg:*)", "-p " + task.Q.Text} {
		if !strings.Contains(string(args), want) {
			t.Errorf("arguments %q lack %q", args, want)
		}
	}
	if run.Tokens.In() != 7 || run.Tokens.Output != 3 || !slices.Equal(run.Commands, []string{"lore ask"}) || run.Wall <= 0 {
		t.Errorf("run %+v", run)
	}
	if r := (ClaudeAgent{Launcher: []string{"sh", "-c", "exit 3"}}).Run(context.Background(), task); r.Err == "" {
		t.Error("a launcher that prints nothing: want an error")
	}
}

func TestMergeSection(t *testing.T) {
	sec := func(n int) string {
		return fmt.Sprintf("%s\n## Blind agent run\n\nrun %d\n%s\n", agentBegin, n, agentEnd)
	}
	if got := MergeSection("", sec(1)); got != sec(1) {
		t.Errorf("empty report: %q", got)
	}
	gate := "# lore beats grep\n\nSummary: x\n"
	one := MergeSection(gate, sec(1))
	if !strings.HasPrefix(one, gate) || !strings.HasSuffix(one, sec(1)) {
		t.Errorf("appended: %q", one)
	}
	two := MergeSection(one, sec(2))
	if strings.Contains(two, "run 1") || !strings.Contains(two, "run 2") || strings.Count(two, agentBegin) != 1 || !strings.HasPrefix(two, gate) {
		t.Errorf("replaced: %q", two)
	}
	// the gate's report rewritten (it drops the section): the next agent run appends again
	if got := MergeSection(gate, sec(3)); strings.Count(got, agentBegin) != 1 {
		t.Errorf("re-append: %q", got)
	}
}

func TestAgentAnswersJSON(t *testing.T) {
	s, qs := agentSetup()
	rs := RunAgents(context.Background(), fakeAgent(func(_ context.Context, task AgentTask) AgentRun { return rightRun(task) }), AgentTasks(qs[:1], s), 1, time.Minute, DefaultAgentModel)
	b, err := AgentAnswersJSON(rs)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id": "` + qs[0].ID + `"`, `"arm": "lore"`, `"arm": "grep"`, `"answer": "` + qs[0].Required[0], `"commands": [`, `"correct": true`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("answers lack %q:\n%s", want, b)
		}
	}
}

func TestAgentReport(t *testing.T) {
	s, qs := agentSetup()
	rs := RunAgents(context.Background(), fakeAgent(func(_ context.Context, task AgentTask) AgentRun { return rightRun(task) }), AgentTasks(qs[:3], s), 2, time.Minute, DefaultAgentModel)
	out := AgentReport(AgentRunInfo{Started: time.Unix(0, 0), Now: s.Now, Model: DefaultAgentModel, Jobs: 2, Lore: "lore x"}, rs)
	for _, want := range []string{agentBegin, "## Blind agent run", "Summary: `agent lore/grep: normal 3/3 of 3", "Result: PASS", "| normal | lore | 3/3 |", "| " + qs[2].ID + " | ", "| lore ask --query q |", agentEnd} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

func TestGradeAnswerOnFacts(t *testing.T) {
	q := Question{Required: []string{"ulrich-brandt owes me the board deck #61", "send the tax receipt #29 to elena-ruiz"}, Forbidden: []string{"is 40k"}, Current: "moved to 55k (was 40k)"}
	right := "Elena Ruiz: **I owe elena-ruiz:** send the tax receipt (#29). ulrich-brandt owes you: 1. **The board deck** (task `#61`)"
	if ok, m := GradeAnswer(q, right); !ok {
		t.Errorf("the facts in other words graded wrong: %q", m)
	}
	if ok, _ := GradeAnswer(q, "ulrich-brandt owes you the board deck #16; send the tax receipt #29 to elena-ruiz"); ok {
		t.Error("a wrong task number graded right")
	}
	if ok, _ := GradeAnswer(q, "send the tax receipt #29 to elena; ulrich-brandt owes me the board deck #61"); ok {
		t.Error("half a name graded right")
	}
	if ok, m := GradeAnswer(q, right+" The budget is 40k."); ok || !strings.Contains(strings.Join(m, ";"), "stale is 40k") {
		t.Errorf("a stale value alone graded right: %q", m)
	}
	if ok, m := GradeAnswer(q, right+" The budget is 40k, now it moved to 55k (was 40k)."); !ok {
		t.Errorf("a stale value next to the current one graded wrong: %q", m)
	}
	changed := Question{Required: []string{"changed to thursdays (was tuesdays)"}}
	if ok, m := GradeAnswer(changed, "Your dentist appointments are now **on Thursdays** (changed from Tuesdays as of November 1, 2026)."); !ok {
		t.Errorf("a change in other words graded wrong: %q", m)
	}
	if ok, _ := GradeAnswer(changed, "Your dentist appointments are on Tuesdays."); ok {
		t.Error("the old value alone graded right")
	}
}

func TestRegradeAnswers(t *testing.T) {
	s, qs := agentSetup()
	rs := RunAgents(context.Background(), fakeAgent(func(_ context.Context, task AgentTask) AgentRun { return rightRun(task) }), AgentTasks(qs[:2], s), 1, time.Minute, DefaultAgentModel)
	b, err := AgentAnswersJSON(rs)
	if err != nil {
		t.Fatal(err)
	}
	again, err := RegradeAnswers(qs, b, DefaultAgentModel)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != len(rs) || SummarizeAgents(again).Line() != SummarizeAgents(rs).Line() {
		t.Errorf("regrade %s, want %s", SummarizeAgents(again).Line(), SummarizeAgents(rs).Line())
	}
	other := slices.Clone(qs)
	other[0].Text = "another question"
	if _, err := RegradeAnswers(other, b, DefaultAgentModel); err == nil {
		t.Error("answers of another data set: want an error")
	}
}

func TestClaudeAgentArgsTools(t *testing.T) {
	s, qs := agentSetup()
	task := AgentTasks(qs[:1], s)[0]
	a := ClaudeAgent{Model: DefaultAgentModel, MaxTurns: 8}
	plain := strings.Join(a.Args(task), " ")
	if !strings.Contains(plain, "--tools Bash ") || strings.Contains(plain, "--allowedTools") {
		t.Errorf("question task: %q, want Bash only and no allow list", plain)
	}
	task.Tools = []string{"Bash", "Read", "Edit", "Write"}
	edit := strings.Join(a.Args(task), " ")
	for _, want := range []string{"--tools Bash,Read,Edit,Write ", "--allowedTools Bash,Read,Edit,Write", "--disallowedTools Bash(cat:*)"} {
		if !strings.Contains(edit, want) {
			t.Errorf("coding task: %q lacks %q", edit, want)
		}
	}
}
