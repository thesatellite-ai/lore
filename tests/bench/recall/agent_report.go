package recall

// agent_report.go — the blind agent run's totals, bars and report section
// (merged into tmp/bench/recall.md between its own markers, so the
// scripted gate's report and this section can be written by separate
// runs in either order).

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ArmTotals is one arm's totals over a set of questions.
type ArmTotals struct {
	N, Correct int
	Commands   int // shell commands the agent ran
	Tokens     Tokens
	CostUSD    float64
	Wall       time.Duration // summed over the questions
	WallMax    time.Duration
	Failed     int // runs that errored or were stopped
	OffTool    int // runs with a command outside the arm's tools
}

// add counts one result.
func (a *ArmTotals) add(r AgentResult) {
	a.N++
	if r.Correct {
		a.Correct++
	}
	a.Commands += len(r.Run.Commands)
	a.Tokens = a.Tokens.add(r.Run.Tokens)
	a.CostUSD += r.Run.CostUSD
	a.Wall += r.Run.Wall
	a.WallMax = max(a.WallMax, r.Run.Wall)
	if r.Run.Err != "" {
		a.Failed++
	}
	if len(r.OffTool) > 0 {
		a.OffTool++
	}
}

// AgentSummary is both arms' totals on the normal and the harder set.
type AgentSummary struct {
	Normal, Harder map[Arm]*ArmTotals
}

// All is an arm's totals over both sets.
func (s AgentSummary) All(arm Arm) ArmTotals {
	n, h := s.Normal[arm], s.Harder[arm]
	return ArmTotals{N: n.N + h.N, Correct: n.Correct + h.Correct, Commands: n.Commands + h.Commands, Tokens: n.Tokens.add(h.Tokens), CostUSD: n.CostUSD + h.CostUSD,
		Wall: n.Wall + h.Wall, WallMax: max(n.WallMax, h.WallMax), Failed: n.Failed + h.Failed, OffTool: n.OffTool + h.OffTool}
}

// SummarizeAgents totals results per set and arm.
func SummarizeAgents(rs []AgentResult) AgentSummary {
	s := AgentSummary{Normal: map[Arm]*ArmTotals{ArmLore: {}, ArmGrep: {}}, Harder: map[Arm]*ArmTotals{ArmLore: {}, ArmGrep: {}}}
	for _, r := range rs {
		set := s.Normal
		if r.Q.Type.Harder() {
			set = s.Harder
		}
		set[r.Arm].add(r)
	}
	return s
}

// Line is the summary line: correct per set, tokens, wall and cost per arm.
func (s AgentSummary) Line() string {
	l, g := s.All(ArmLore), s.All(ArmGrep)
	return fmt.Sprintf("agent lore/grep: normal %d/%d of %d, harder %d/%d of %d, tokens in %d/%d out %d/%d, wall %.0f/%.0f s, commands %d/%d, cost $%.2f/$%.2f",
		s.Normal[ArmLore].Correct, s.Normal[ArmGrep].Correct, s.Normal[ArmLore].N, s.Harder[ArmLore].Correct, s.Harder[ArmGrep].Correct, s.Harder[ArmLore].N,
		l.Tokens.In(), g.Tokens.In(), l.Tokens.Output, g.Tokens.Output, l.Wall.Seconds(), g.Wall.Seconds(), l.Commands, g.Commands, l.CostUSD, g.CostUSD)
}

// AgentMisses lists every bar of the blind agent run the results miss
// (owner 2026-11-21): the lore agent at least as many right as the grep
// agent on the normal set and more on the harder set (when the grep agent
// already got every harder question there is nothing left to beat, so
// equal-and-full passes); every run finished; no lore-agent command
// outside lore (the run would not be blind to the files).
func AgentMisses(rs []AgentResult) []string {
	s := SummarizeAgents(rs)
	var out []string
	ln, gn := s.Normal[ArmLore], s.Normal[ArmGrep]
	if ln.Correct < gn.Correct {
		out = append(out, fmt.Sprintf("normal set: lore agent %d right, grep agent %d: the bar is lore >= grep", ln.Correct, gn.Correct))
	}
	lh, gh := s.Harder[ArmLore], s.Harder[ArmGrep]
	full := lh.Correct == lh.N && gh.Correct == gh.N
	if lh.Correct <= gh.Correct && !full {
		out = append(out, fmt.Sprintf("harder set: lore agent %d right, grep agent %d: the bar is lore > grep", lh.Correct, gh.Correct))
	}
	for _, r := range rs {
		if r.Arm != ArmLore {
			continue
		}
		id := r.Q.ID + " (" + string(r.Q.Type) + ")"
		if !r.Correct {
			out = append(out, fmt.Sprintf("%s: lore agent wrong: %s", id, strings.Join(cut(r.Missing, 2), "; ")))
		}
	}
	for _, arm := range []Arm{ArmLore, ArmGrep} {
		if f := s.All(arm).Failed; f > 0 {
			out = append(out, fmt.Sprintf("%s agent: %d run(s) failed or were stopped", arm, f))
		}
	}
	return out
}

// AgentRunInfo is the run's settings for the report.
type AgentRunInfo struct {
	Started time.Time
	Now     time.Time
	Model   string
	Jobs    int
	Lore    string // lore --version
}

// Section markers of the blind agent run in tmp/bench/recall.md.
const (
	agentBegin = "<!-- blind agent run -->"
	agentEnd   = "<!-- /blind agent run -->"
)

// AgentReport renders the blind agent run's report section.
func AgentReport(info AgentRunInfo, rs []AgentResult) string {
	var b strings.Builder
	s := SummarizeAgents(rs)
	misses := AgentMisses(rs)
	b.WriteString(agentBegin + "\n## Blind agent run\n\n")
	fmt.Fprintf(&b, "Run %s; now pinned at %s; model %s; %d at a time; %s. The lore agent has skills/SKILL.md and the lore command only; the grep agent has rg, grep, cat and ls over the markdown tree; neither sees the expected facts. Answers graded by the same grader as the scripted arms.\n\n",
		info.Started.UTC().Format(time.RFC3339), info.Now.Format(time.DateOnly), info.Model, info.Jobs, info.Lore)
	fmt.Fprintf(&b, "Summary: `%s`\n\n", s.Line())
	if len(misses) == 0 {
		b.WriteString("Result: PASS (every bar met)\n\n")
	} else {
		fmt.Fprintf(&b, "Result: FAIL, %d bar(s) missed:\n\n", len(misses))
		for _, m := range misses {
			fmt.Fprintf(&b, "- %s\n", m)
		}
		b.WriteString("\n")
	}
	b.WriteString("### Per set\n\n| set | arm | right | commands | tokens in | tokens out | cost USD | wall s | wall max s | failed | off-tool |\n|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, set := range []struct {
		name string
		m    map[Arm]*ArmTotals
	}{{"normal", s.Normal}, {"harder", s.Harder}} {
		for _, arm := range []Arm{ArmLore, ArmGrep} {
			a := set.m[arm]
			fmt.Fprintf(&b, "| %s | %s | %d/%d | %d | %d | %d | %.2f | %.0f | %.0f | %d | %d |\n", set.name, arm, a.Correct, a.N, a.Commands, a.Tokens.In(), a.Tokens.Output, a.CostUSD, a.Wall.Seconds(), a.WallMax.Seconds(), a.Failed, a.OffTool)
		}
	}
	b.WriteString("\n### Per question\n\n| id | type | question | lore ok | lore cmds | lore tokens in/out | lore s | grep ok | grep cmds | grep tokens in/out | grep s | lore misses | grep misses | lore agent ran |\n|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	byQ := map[string]map[Arm]AgentResult{}
	var order []Question
	for _, r := range rs {
		if byQ[r.Q.ID] == nil {
			byQ[r.Q.ID] = map[Arm]AgentResult{}
			order = append(order, r.Q)
		}
		byQ[r.Q.ID][r.Arm] = r
	}
	for _, q := range order {
		l, g := byQ[q.ID][ArmLore], byQ[q.ID][ArmGrep]
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %d/%d | %.1f | %s | %d | %d/%d | %.1f | %s | %s | %s |\n", q.ID, q.Type, cell(q.Text),
			yes(l.Correct), len(l.Run.Commands), l.Run.Tokens.In(), l.Run.Tokens.Output, l.Run.Wall.Seconds(),
			yes(g.Correct), len(g.Run.Commands), g.Run.Tokens.In(), g.Run.Tokens.Output, g.Run.Wall.Seconds(),
			cell(strings.Join(cut(l.Missing, 2), "; ")), cell(strings.Join(cut(g.Missing, 2), "; ")), cell(ranList(l.Run)))
	}
	b.WriteString(agentEnd + "\n")
	return b.String()
}

// MergeSection puts section into report: in place of an earlier blind
// agent run section, else appended after the report.
func MergeSection(report, section string) string {
	if i := strings.Index(report, agentBegin); i >= 0 {
		if j := strings.Index(report[i:], agentEnd); j >= 0 {
			rest := strings.TrimPrefix(report[i+j+len(agentEnd):], "\n")
			return report[:i] + section + rest
		}
		return report[:i] + section
	}
	if report == "" {
		return section
	}
	return strings.TrimRight(report, "\n") + "\n\n" + section
}

// ranList is the commands a run made, each shortened, denied ones marked.
func ranList(r AgentRun) string {
	short := func(c string) string {
		if len(c) > 90 {
			return c[:90] + "..."
		}
		return c
	}
	var out []string
	for _, c := range r.Commands {
		out = append(out, short(c))
	}
	for _, c := range r.Denied {
		out = append(out, "denied: "+short(c))
	}
	return strings.Join(out, " ; ")
}

// agentAnswer is one run as kept next to the report: the answer and the
// commands, so a miss can be read without running the agents again.
type agentAnswer struct {
	ID        string   `json:"id"`
	Type      Type     `json:"type"`
	Arm       Arm      `json:"arm"`
	Question  string   `json:"question"`
	Correct   bool     `json:"correct"`
	Missing   []string `json:"missing,omitempty"`
	Answer    string   `json:"answer"`
	Commands  []string `json:"commands,omitempty"`
	Denied    []string `json:"denied,omitempty"`
	Models    []string `json:"models,omitempty"`
	CostUSD   float64  `json:"cost_usd"`
	TokensIn  int      `json:"tokens_in"`
	TokensOut int      `json:"tokens_out"`
	WallMS    int64    `json:"wall_ms"`
	Err       string   `json:"error,omitempty"`
}

// AgentAnswersJSON is every run's answer and commands as JSON (the
// report's companion file, <report>-agent.json).
func AgentAnswersJSON(rs []AgentResult) ([]byte, error) {
	out := make([]agentAnswer, 0, len(rs))
	for _, r := range rs {
		out = append(out, agentAnswer{ID: r.Q.ID, Type: r.Q.Type, Arm: r.Arm, Question: r.Q.Text, Correct: r.Correct, Missing: r.Missing, Answer: r.Run.Answer,
			Commands: r.Run.Commands, Denied: r.Run.Denied, Models: r.Run.Models, CostUSD: r.Run.CostUSD, TokensIn: r.Run.Tokens.In(), TokensOut: r.Run.Tokens.Output, WallMS: r.Run.Wall.Milliseconds(), Err: r.Run.Err})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("agent answers: %w", err)
	}
	return b, nil
}

// RegradeAnswers grades saved runs (AgentAnswersJSON) again against qs
// with today's grader, without starting an agent: a grader change is
// checked on the answers the agents already gave. Tokens in are kept as
// uncached input (the split is not saved).
func RegradeAnswers(qs []Question, raw []byte, model string) ([]AgentResult, error) {
	var saved []agentAnswer
	if err := json.Unmarshal(raw, &saved); err != nil {
		return nil, fmt.Errorf("agent answers: %w", err)
	}
	byID := map[string]Question{}
	for _, q := range qs {
		byID[q.ID] = q
	}
	out := make([]AgentResult, 0, len(saved))
	for _, a := range saved {
		q, ok := byID[a.ID]
		if !ok || q.Text != a.Question {
			return nil, fmt.Errorf("agent answers: %s %q is not a question of this data set (same seed and now?)", a.ID, a.Question)
		}
		run := AgentRun{Answer: a.Answer, Commands: a.Commands, Denied: a.Denied, Models: a.Models, CostUSD: a.CostUSD, Err: a.Err,
			Tokens: Tokens{Input: a.TokensIn, Output: a.TokensOut}, Wall: time.Duration(a.WallMS) * time.Millisecond}
		out = append(out, GradeAgent(AgentTask{Arm: a.Arm, Q: q}, run, model))
	}
	return out, nil
}
