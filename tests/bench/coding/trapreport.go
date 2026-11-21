package coding

// trapreport.go — the trap run's totals, bar and report section (merged
// into tmp/bench/coding.md between its own markers, so the context gate
// and the trap run can write the report in either order).

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/tests/bench/recall"
)

// Section markers of the trap run in tmp/bench/coding.md.
const (
	trapBegin = "<!-- blind agent trap run -->"
	trapEnd   = "<!-- /blind agent trap run -->"
)

// TrapArmTotals is one arm's totals over the traps.
type TrapArmTotals struct {
	N, Edited, Fell, Fixed, Failed int
	LoreCalls                      int
	Tokens                         recall.Tokens
	CostUSD                        float64
	Wall                           time.Duration
}

// TrapSummary is both arms' totals and the traps the plain agent did not
// fall into (they prove nothing).
type TrapSummary struct {
	Arms map[recall.Arm]*TrapArmTotals
	// Shown are the traps the plain agent fell into and the lore agent
	// avoided: the run's evidence.
	Shown []string
	// Unproven are the traps the plain agent did not fall into (or whose
	// plain run failed or ran lore): they show nothing either way.
	Unproven []string
}

// SummarizeTraps totals results per arm.
func SummarizeTraps(rs []TrapResult) TrapSummary {
	s := TrapSummary{Arms: map[recall.Arm]*TrapArmTotals{ArmLore: {}, ArmPlain: {}}}
	lore := map[string]TrapResult{}
	for _, r := range rs {
		a := s.Arms[r.Arm]
		a.N++
		if r.Edited {
			a.Edited++
		}
		if r.Fell {
			a.Fell++
		}
		if r.Fixed {
			a.Fixed++
		}
		if r.Err != "" {
			a.Failed++
		}
		a.LoreCalls += r.LoreCalls
		a.Tokens = addTokens(a.Tokens, r.Run.Tokens)
		a.CostUSD += r.Run.CostUSD
		a.Wall += r.Run.Wall
		if r.Arm == ArmLore {
			lore[r.Trap.Task] = r
		}
	}
	for _, r := range rs {
		if r.Arm != ArmPlain {
			continue
		}
		switch {
		case !r.Proves():
			s.Unproven = append(s.Unproven, r.Trap.Task)
		case lore[r.Trap.Task].Avoided():
			s.Shown = append(s.Shown, r.Trap.Task)
		}
	}
	return s
}

// Proves reports whether a plain run shows its trap is real: it ran
// blind (no lore), finished, and fell in.
func (r TrapResult) Proves() bool { return r.Err == "" && !r.RanLore && r.Fell }

// addTokens sums two token counts.
func addTokens(a, b recall.Tokens) recall.Tokens {
	return recall.Tokens{Input: a.Input + b.Input, CacheWrite: a.CacheWrite + b.CacheWrite, CacheRead: a.CacheRead + b.CacheRead, Output: a.Output + b.Output}
}

// Line is the summary line the coordinator copies.
func (s TrapSummary) Line() string {
	l, p := s.Arms[ArmLore], s.Arms[ArmPlain]
	return fmt.Sprintf("traps lore/plain: fell in %d/%d of %d, edited %d/%d, applied the hotfix %d/%d, failed %d/%d; shown %d (lore avoided, plain fell in), prove nothing %d; lore calls %d; tokens in %d/%d out %d/%d, wall %.0f/%.0f s, cost $%.2f/$%.2f",
		l.Fell, p.Fell, l.N, l.Edited, p.Edited, l.Fixed, p.Fixed, l.Failed, p.Failed, len(s.Shown), len(s.Unproven), l.LoreCalls,
		l.Tokens.In(), p.Tokens.In(), l.Tokens.Output, p.Tokens.Output, l.Wall.Seconds(), p.Wall.Seconds(), l.CostUSD, p.CostUSD)
}

// TrapMisses lists every bar the run misses (owner 2026-11-21: the blind
// agent with lore avoids the trap the hotfix describes where the agent
// without lore falls into it): the lore agent edited and fell into no
// trap, every lore run finished, and at least one trap shows it (a run
// where the plain agent fell into nothing shows nothing).
func TrapMisses(rs []TrapResult) []string {
	var out []string
	for _, r := range rs {
		if r.Arm != ArmLore {
			continue
		}
		id := r.Trap.Task + " (" + r.Trap.Hotfix + ")"
		switch {
		case r.Err != "":
			out = append(out, fmt.Sprintf("%s: lore agent run failed: %s", id, r.Err))
		case r.Fell:
			out = append(out, fmt.Sprintf("%s: lore agent fell into the trap: %s", id, r.Line))
		case !r.Edited:
			out = append(out, fmt.Sprintf("%s: lore agent made no edit in the trap's files", id))
		}
	}
	for _, r := range rs {
		if r.Arm == ArmPlain && r.RanLore {
			out = append(out, fmt.Sprintf("%s (%s): plain agent ran lore: the run is not blind", r.Trap.Task, r.Trap.Hotfix))
		}
	}
	if s := SummarizeTraps(rs); len(rs) > 0 && len(s.Shown) == 0 {
		out = append(out, "no trap shows the difference: the plain agent fell into none the lore agent avoided")
	}
	return out
}

// TrapRunInfo is the run's settings for the report.
type TrapRunInfo struct {
	Started time.Time
	Model   string
	Jobs    int
	Lore    string // lore --version
}

// TrapReport renders the trap run's report section.
func TrapReport(info TrapRunInfo, rs []TrapResult) string {
	var b strings.Builder
	s := SummarizeTraps(rs)
	misses := TrapMisses(rs)
	b.WriteString(trapBegin + "\n## Blind agent trap run\n\n")
	fmt.Fprintf(&b, "Run %s; model %s; %d at a time; %s. Per trap two agents make the same edit in their own checkout of orbit: the lore agent has the store and the lore skill, the plain agent the same files and no lore. Neither is told the trap; each diff is graded by Trap.FellIn.\n\n",
		info.Started.UTC().Format(time.RFC3339), info.Model, info.Jobs, info.Lore)
	fmt.Fprintf(&b, "Summary: `%s`\n\n", s.Line())
	if len(misses) == 0 {
		b.WriteString("Result: PASS (the lore agent fell into no trap; at least one trap shows the difference)\n\n")
	} else {
		b.WriteString("Result: FAIL\n\n")
		for _, m := range misses {
			b.WriteString("- " + m + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("| trap | hotfix | lore agent | lore calls | plain agent | shows |\n|---|---|---|---|---|---|\n")
	byTrap := map[string]map[recall.Arm]TrapResult{}
	var order []string
	for _, r := range rs {
		if byTrap[r.Trap.Task] == nil {
			byTrap[r.Trap.Task] = map[recall.Arm]TrapResult{}
			order = append(order, r.Trap.Task)
		}
		byTrap[r.Trap.Task][r.Arm] = r
	}
	for _, id := range order {
		l, p := byTrap[id][ArmLore], byTrap[id][ArmPlain]
		shows := "yes"
		switch {
		case !p.Proves():
			shows = "nothing: the plain agent did not fall in"
			if p.Err != "" {
				shows = "nothing: the plain run failed"
			} else if p.RanLore {
				shows = "nothing: the plain agent ran lore"
			}
		case !l.Avoided():
			shows = "no: the lore agent did not avoid it"
		}
		hf := l.Trap.Hotfix
		if hf == "" {
			hf = p.Trap.Hotfix
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %s | %s |\n", id, hf, verdict(l), l.LoreCalls, verdict(p), shows)
	}
	b.WriteString("\n" + trapEnd + "\n")
	return b.String()
}

// verdict is one arm's outcome in a table cell.
func verdict(r TrapResult) string {
	switch {
	case r.Err != "":
		return "failed: " + cell(r.Err)
	case r.Fell:
		return "fell in: `" + cell(r.Line) + "`"
	case !r.Edited:
		return "no edit"
	case r.Fixed:
		return "avoided (applied the hotfix)"
	}
	return "avoided"
}

// cell is s shortened and made safe for a table cell.
func cell(s string) string {
	s = strings.NewReplacer("|", "\\|", "`", "'", "\n", " ").Replace(s)
	if len(s) > 80 {
		s = s[:80] + "..."
	}
	return s
}

// MergeTrapSection puts the trap run's section into report (in place of
// an earlier one, else appended).
func MergeTrapSection(report, section string) string {
	return recall.MergeBetween(report, section, trapBegin, trapEnd)
}

// KeepTrapSection is the context gate's new report with the trap run's
// section of the old report kept (the gate rewrites the whole file).
func KeepTrapSection(old, report string) string {
	i := strings.Index(old, trapBegin)
	if i < 0 {
		return report
	}
	j := strings.Index(old[i:], trapEnd)
	if j < 0 {
		return report
	}
	return MergeTrapSection(report, old[i:i+j+len(trapEnd)]+"\n")
}

// trapAnswer is one saved run: what the agent did and the diff it left.
type trapAnswer struct {
	Task      string   `json:"task"`
	Hotfix    string   `json:"hotfix"`
	Arm       string   `json:"arm"`
	Fell      bool     `json:"fell"`
	Line      string   `json:"line,omitempty"`
	Edited    bool     `json:"edited"`
	Fixed     bool     `json:"fixed"`
	LoreCalls int      `json:"lore_calls"`
	Err       string   `json:"err,omitempty"`
	Answer    string   `json:"answer"`
	Commands  []string `json:"commands"`
	Denied    []string `json:"denied,omitempty"`
	TokensIn  int      `json:"tokens_in"`
	TokensOut int      `json:"tokens_out"`
	CostUSD   float64  `json:"cost_usd"`
	WallMS    int64    `json:"wall_ms"`
	Diff      string   `json:"diff"`
}

// TrapAnswersJSON is every run with its diff, for reading a miss later.
func TrapAnswersJSON(rs []TrapResult) ([]byte, error) {
	out := make([]trapAnswer, 0, len(rs))
	for _, r := range rs {
		out = append(out, trapAnswer{Task: r.Trap.Task, Hotfix: r.Trap.Hotfix, Arm: string(r.Arm), Fell: r.Fell, Line: r.Line, Edited: r.Edited, Fixed: r.Fixed,
			LoreCalls: r.LoreCalls, Err: r.Err, Answer: r.Run.Answer, Commands: r.Run.Commands, Denied: r.Run.Denied, TokensIn: r.Run.Tokens.In(),
			TokensOut: r.Run.Tokens.Output, CostUSD: r.Run.CostUSD, WallMS: r.Run.Wall.Milliseconds(), Diff: r.Diff})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("trap answers: %w", err)
	}
	return b, nil
}
