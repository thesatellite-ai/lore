package recall

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// The bars of the recall gate (the raised bar of 2026-11-21).
const (
	BarWarm      = 50 * time.Millisecond  // every warm lore call under this
	BarCold      = 300 * time.Millisecond // every cold lore call under this
	BarCalls     = 1                      // lore calls per question
	BarTypedGain = 10                     // typed answers: grep chars / lore chars at least this
)

// LoreRun is the lore arm's result for one question.
type LoreRun struct {
	Calls   int
	Warm    time.Duration // the second run of the call
	Cold    time.Duration // the first run after the store changed
	Chars   int           // output of the warm run (stdout + stderr)
	Correct bool
	Missing []string
	Exit    int
}

// GrepRun is the grep arm's result for one question.
type GrepRun struct {
	Calls   int
	Wall    time.Duration // all its commands
	Chars   int
	Correct bool
	Missing []string
}

// Result is one question asked of both arms.
type Result struct {
	Q    Question
	Lore LoreRun
	Grep GrepRun
}

// Summary is the one line the coordinator copies into the gate status.
type Summary struct {
	LoreCalls, GrepCalls     int
	WarmP50, WarmMax         time.Duration
	ColdMax                  time.Duration
	LoreChars, GrepChars     int
	LoreCorrect, GrepCorrect int
	Harder, HarderOf         int
	Questions                int
}

// Summarize totals results.
func Summarize(rs []Result) Summary {
	var s Summary
	var warm []time.Duration
	for _, r := range rs {
		s.Questions++
		s.LoreCalls += r.Lore.Calls
		s.GrepCalls += r.Grep.Calls
		s.LoreChars += r.Lore.Chars
		s.GrepChars += r.Grep.Chars
		warm = append(warm, r.Lore.Warm)
		s.WarmMax = max(s.WarmMax, r.Lore.Warm)
		s.ColdMax = max(s.ColdMax, r.Lore.Cold)
		if r.Lore.Correct {
			s.LoreCorrect++
		}
		if r.Grep.Correct {
			s.GrepCorrect++
		}
		if r.Q.Type.Harder() {
			s.HarderOf++
			if r.Lore.Correct {
				s.Harder++
			}
		}
	}
	if len(warm) > 0 {
		slices.Sort(warm)
		s.WarmP50 = warm[len(warm)/2]
	}
	return s
}

// Line is the summary line: `calls L/G, warm p50/max ms, cold max ms,
// chars L/G, correct L/G, harder L/20`.
func (s Summary) Line() string {
	return fmt.Sprintf("calls %d/%d, warm p50/max %d/%d ms, cold max %d ms, chars %d/%d, correct %d/%d of %d, harder %d/%d",
		s.LoreCalls, s.GrepCalls, s.WarmP50.Milliseconds(), s.WarmMax.Milliseconds(), s.ColdMax.Milliseconds(),
		s.LoreChars, s.GrepChars, s.LoreCorrect, s.GrepCorrect, s.Questions, s.Harder, s.HarderOf)
}

// typeTotals is one type's totals.
type typeTotals struct {
	n, loreOK, grepOK    int
	loreChars, grepChars int
	loreCalls, grepCalls int
	warmMax, coldMax     time.Duration
}

// byType totals results per question type.
func byType(rs []Result) map[Type]*typeTotals {
	m := map[Type]*typeTotals{}
	for _, r := range rs {
		t, ok := m[r.Q.Type]
		if !ok {
			t = &typeTotals{}
			m[r.Q.Type] = t
		}
		t.n++
		t.loreChars += r.Lore.Chars
		t.grepChars += r.Grep.Chars
		t.loreCalls += r.Lore.Calls
		t.grepCalls += r.Grep.Calls
		t.warmMax = max(t.warmMax, r.Lore.Warm)
		t.coldMax = max(t.coldMax, r.Lore.Cold)
		if r.Lore.Correct {
			t.loreOK++
		}
		if r.Grep.Correct {
			t.grepOK++
		}
	}
	return m
}

// Misses lists every bar the run misses, one line each; empty = pass.
func Misses(rs []Result) []string {
	var out []string
	for _, r := range rs {
		id := r.Q.ID + " (" + string(r.Q.Type) + ")"
		if r.Lore.Calls != BarCalls {
			out = append(out, fmt.Sprintf("%s: %d lore calls, the bar is %d", id, r.Lore.Calls, BarCalls))
		}
		if r.Lore.Warm >= BarWarm {
			out = append(out, fmt.Sprintf("%s: warm call %d ms, the bar is < %d ms", id, r.Lore.Warm.Milliseconds(), BarWarm.Milliseconds()))
		}
		if r.Lore.Cold >= BarCold {
			out = append(out, fmt.Sprintf("%s: cold call %d ms, the bar is < %d ms", id, r.Lore.Cold.Milliseconds(), BarCold.Milliseconds()))
		}
		if !r.Lore.Correct {
			out = append(out, fmt.Sprintf("%s: lore answer wrong: %s", id, strings.Join(cut(r.Lore.Missing, 3), "; ")))
		}
	}
	s := Summarize(rs)
	if s.LoreChars >= s.GrepChars {
		out = append(out, fmt.Sprintf("total chars: lore %d >= grep %d", s.LoreChars, s.GrepChars))
	}
	if s.LoreCorrect < s.GrepCorrect {
		out = append(out, fmt.Sprintf("correct: lore %d < grep %d", s.LoreCorrect, s.GrepCorrect))
	}
	m := byType(rs)
	for _, t := range Types() {
		tt, ok := m[t]
		if !ok || !t.Typed() {
			continue
		}
		if tt.loreChars*BarTypedGain > tt.grepChars {
			out = append(out, fmt.Sprintf("typed answers %q: lore %d chars vs grep %d, the bar is >= %dx lighter", t, tt.loreChars, tt.grepChars, BarTypedGain))
		}
	}
	return out
}

// cut keeps the first n items of xs and says how many more.
func cut(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return append(slices.Clone(xs[:n]), fmt.Sprintf("+%d more", len(xs)-n))
}

// RunInfo describes one run for the report's head.
type RunInfo struct {
	Now      time.Time
	Seed     uint64
	Rows     int
	Lore     string // lore version line
	GrepTool string // rg or grep
	Started  time.Time
	Load     time.Duration // writing the store
}

// Report renders tmp/bench/recall.md: the run, the summary line, the
// missed bars, a table per question type and a table per question.
func Report(info RunInfo, rs []Result) string {
	var b strings.Builder
	s := Summarize(rs)
	misses := Misses(rs)
	fmt.Fprintf(&b, "# lore beats grep: recall gate\n\n")
	fmt.Fprintf(&b, "Run %s; now pinned at %s; seed %d; %d rows in the store (loaded in %s); %s; grep arm tool: %s.\n\n", info.Started.UTC().Format(time.RFC3339), info.Now.Format(time.RFC3339), info.Seed, info.Rows, info.Load.Round(time.Second), info.Lore, info.GrepTool)
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
	b.WriteString("## Per question type\n\n| type | n | correct lore/grep | calls lore/grep | warm max ms | cold max ms | chars lore/grep | ratio lore/grep |\n|---|---|---|---|---|---|---|---|\n")
	m := byType(rs)
	for _, t := range Types() {
		tt, ok := m[t]
		if !ok {
			continue
		}
		ratio := 0.0
		if tt.grepChars > 0 {
			ratio = float64(tt.loreChars) / float64(tt.grepChars)
		}
		fmt.Fprintf(&b, "| %s | %d | %d/%d | %d/%d | %d | %d | %d/%d | %.2f |\n", t, tt.n, tt.loreOK, tt.grepOK, tt.loreCalls, tt.grepCalls, tt.warmMax.Milliseconds(), tt.coldMax.Milliseconds(), tt.loreChars, tt.grepChars, ratio)
	}
	b.WriteString("\n## Per question\n\n| id | type | question | lore call | lore ok | warm ms | cold ms | lore chars | grep calls | grep chars | grep ok | lore misses |\n|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range rs {
		fmt.Fprintf(&b, "| %s | %s | %s | `lore %s` | %s | %d | %d | %d | %d | %d | %s | %s |\n", r.Q.ID, r.Q.Type, cell(r.Q.Text), cell(strings.Join(r.Q.Lore, " ")), yes(r.Lore.Correct), r.Lore.Warm.Milliseconds(), r.Lore.Cold.Milliseconds(), r.Lore.Chars, r.Grep.Calls, r.Grep.Chars, yes(r.Grep.Correct), cell(strings.Join(cut(r.Lore.Missing, 2), "; ")))
	}
	return b.String()
}

// cell makes s safe in a markdown table cell.
func cell(s string) string {
	return strings.NewReplacer("|", `\|`, "\n", " ", "`", "'").Replace(s)
}

// yes renders a bool.
func yes(ok bool) string {
	if ok {
		return "yes"
	}
	return "no"
}
