package recall

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

// The speed and size bars of the recall gate (the raised bar of 2026-11-21);
// the accuracy bars, checked first, are in accuracy.go.
const (
	BarWarm      = 50 * time.Millisecond  // every question's warm time (WarmOf its WarmRepeats calls) under this
	BarCold      = 300 * time.Millisecond // every cold lore call under this
	BarCalls     = 1                      // lore calls per question
	BarTypedGain = 10                     // who is X: grep chars / lore chars at least this
)

// WarmRepeats is how many times the bench repeats a question's call once
// it is warm. The warm time the bar judges is their median (WarmOf), so
// two of three calls must be under BarWarm: on a box shared with other
// projects one call's wall time can include waiting for a core, and gate
// runs missed on a single warm call of a different question each time
// while its CPU time was 20-30 ms (bug hunt H-106, devco D-849). The cold
// call stays one call: it is the first after the store changed and cannot
// be repeated.
const WarmRepeats = 3

// Timing is one timing of a question's lore call: the cold call right
// after the store changed and the WarmRepeats warm calls after it, wall
// and CPU time (user + system) each.
type Timing struct {
	Cold, ColdCPU time.Duration
	Warm, WarmCPU []time.Duration // in the order they ran
}

// NeedsRetime says whether a question missed a speed bar (warm or cold)
// and is timed once more after the run's last question.
//
// Why (devco:D-879, bug hunt H-138): the median of three warm calls
// (D-849) covers one call that waited for a core, but the three calls run
// back to back, so a burst of load on a shared box that lasts seconds
// slows all three and the cold call before them. One gate run failed 17
// speed bars on its last ten questions (warm 88-169 ms, cold 400-512 ms)
// while each call's CPU time stayed 22-36 ms, and the same binary on the
// same data answered them in 24-41 ms warm and 101-179 ms cold when timed
// again. Timing a missed question again minutes later, outside that burst,
// tells load from a slow lore: a real slowdown is slow both times and
// still fails, and the report keeps both timings.
func NeedsRetime(r LoreRun) bool {
	return r.Warm >= BarWarm || r.Cold >= BarCold
}

// Retime makes t the timing the bars judge and keeps the one it replaces
// in First, so the report shows both and the summary's raw max still
// counts the first calls.
func (r *LoreRun) Retime(t Timing) {
	first := Timing{Cold: r.Cold, ColdCPU: r.ColdCPU, Warm: r.WarmRuns, WarmCPU: r.warmCPURuns}
	r.First = &first
	r.Cold, r.ColdCPU = t.Cold, t.ColdCPU
	r.WarmRuns, r.Warm = t.Warm, WarmOf(t.Warm)
	r.warmCPURuns, r.WarmCPU = t.WarmCPU, WarmOf(t.WarmCPU)
}

// RetimeMisses times again, with again, every question whose first timing
// missed a speed bar and makes that timing the one the bars judge
// (NeedsRetime, Retime); it returns how many it timed again. Answers and
// accuracy stay graded from the first calls.
func RetimeMisses(rs []Result, again func(r Result, i int) Timing) int {
	n := 0
	for i := range rs {
		if !NeedsRetime(rs[i].Lore) {
			continue
		}
		rs[i].Lore.Retime(again(rs[i], i))
		n++
	}
	return n
}

// SetTiming records a question's (first) timing.
func (r *LoreRun) SetTiming(t Timing) {
	r.Cold, r.ColdCPU = t.Cold, t.ColdCPU
	r.WarmRuns, r.Warm = t.Warm, WarmOf(t.Warm)
	r.warmCPURuns, r.WarmCPU = t.WarmCPU, WarmOf(t.WarmCPU)
}

// WarmOf is the warm time of a question: the median of its warm calls'
// wall times (the middle one of three; the upper middle of an even
// count, so a tie never rounds a slow pair down). Zero for no calls.
func WarmOf(runs []time.Duration) time.Duration {
	if len(runs) == 0 {
		return 0
	}
	s := slices.Clone(runs)
	slices.Sort(s)
	return s[len(s)/2]
}

// LoreRun is the lore arm's result for one question.
type LoreRun struct {
	Calls int
	Warm  time.Duration // the warm time the bar judges: WarmOf(WarmRuns)
	// WarmRuns are the wall times of the WarmRepeats warm calls, in the
	// order they ran; the report prints them next to the median and the
	// summary line keeps their raw max, so a spike is shown, not hidden.
	WarmRuns []time.Duration
	Cold     time.Duration // the first run after the store changed
	// WarmCPU and ColdCPU are the CPU time (user + system) of the same two
	// runs: on a loaded box wall time is mostly waiting for a core, and CPU
	// time shows what the call itself costs (devco D-813). The bars stay
	// on wall time. WarmCPU is the median of the warm calls' CPU times.
	WarmCPU, ColdCPU time.Duration
	// warmCPURuns are the warm calls' CPU times (WarmCPU is their median).
	warmCPURuns []time.Duration
	// First is the question's first timing when it missed a speed bar and
	// was timed again after the run (Retime, NeedsRetime); nil otherwise.
	// The bars judge the second timing; the report prints both.
	First   *Timing
	Chars   int // output of the first warm run (stdout + stderr)
	Correct bool
	Missing []string
	Exit    int
	// Acc is the accuracy grade of the same call with --json (the ranked
	// answer items); AccErr says why there is none (the call failed or
	// its JSON did not read), which fails every accuracy bar.
	Acc    Accuracy
	AccErr string
}

// GrepRun is the grep arm's result for one question.
type GrepRun struct {
	Calls   int
	Wall    time.Duration // all its commands
	Chars   int
	Correct bool
	Missing []string
	// Present and Stale: the accuracy bar as it applies to grep's pile of
	// text (every required fact in it; a forbidden fact without the
	// current value).
	Present bool
	Stale   []string
}

// Result is one question asked of both arms.
type Result struct {
	Q    Question
	Lore LoreRun
	Grep GrepRun
}

// Summary is the one line the coordinator copies into the gate status.
type Summary struct {
	LoreCalls, GrepCalls int
	WarmP50, WarmMax     time.Duration
	// WarmRawMax is the slowest single warm call of the run (WarmMax is
	// the slowest question's median, the one the bar judges).
	WarmRawMax               time.Duration
	ColdMax                  time.Duration
	WarmCPUP50, WarmCPUMax   time.Duration
	LoreChars, GrepChars     int
	LoreCorrect, GrepCorrect int
	Harder, HarderOf         int
	Questions                int
	// Retimed counts the questions timed again after the run because
	// their first timing missed a speed bar (NeedsRetime).
	Retimed int
	// accuracy (lore ask --json): questions with the answer in the top 3,
	// with the first item right, with a stale fact in the top 3, with more
	// than one irrelevant item in the top 3; grep present and stale.
	Recall, Top1, Stale, Noisy int
	GrepPresent, GrepStale     int
}

// Summarize totals results.
func Summarize(rs []Result) Summary {
	var s Summary
	var warm, cpu []time.Duration
	for _, r := range rs {
		s.Questions++
		s.LoreCalls += r.Lore.Calls
		s.GrepCalls += r.Grep.Calls
		s.LoreChars += r.Lore.Chars
		s.GrepChars += r.Grep.Chars
		warm = append(warm, r.Lore.Warm)
		s.WarmMax = max(s.WarmMax, r.Lore.Warm)
		s.WarmRawMax = max(s.WarmRawMax, rawMax(r.Lore))
		s.ColdMax = max(s.ColdMax, r.Lore.Cold)
		if r.Lore.First != nil {
			s.Retimed++
		}
		cpu = append(cpu, r.Lore.WarmCPU)
		s.WarmCPUMax = max(s.WarmCPUMax, r.Lore.WarmCPU)
		if r.Lore.Correct {
			s.LoreCorrect++
		}
		if r.Grep.Correct {
			s.GrepCorrect++
		}
		a := r.Lore.Acc
		if a.Recall {
			s.Recall++
		}
		if a.Top1 {
			s.Top1++
		}
		if len(a.Stale) > 0 {
			s.Stale++
		}
		if a.Noise > BarNoiseTop {
			s.Noisy++
		}
		if r.Grep.Present {
			s.GrepPresent++
		}
		if len(r.Grep.Stale) > 0 {
			s.GrepStale++
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
		slices.Sort(cpu)
		s.WarmCPUP50 = cpu[len(cpu)/2]
	}
	return s
}

// rawMax is the slowest of a question's warm calls, those of a first
// timing it replaced included (its Warm when the single calls were not
// kept).
func rawMax(r LoreRun) time.Duration {
	m := r.Warm
	for _, w := range r.WarmRuns {
		m = max(m, w)
	}
	if r.First != nil {
		for _, w := range r.First.Warm {
			m = max(m, w)
		}
	}
	return m
}

// Line is the summary line, accuracy first: `recall@3 R/N, top-1 T/N,
// stale S, noise X, calls L/G, warm p50/max ms (raw max ms), cold max ms,
// chars L/G, correct L/G of N, harder L/20, warm cpu p50/max ms, retimed
// R`. The warm numbers are the questions' medians; raw max is the slowest
// single call; retimed counts the questions timed again (NeedsRetime).
func (s Summary) Line() string {
	return fmt.Sprintf("recall@3 %d/%d, top-1 %d/%d, stale %d, noise %d, calls %d/%d, warm p50/max %d/%d ms (raw max %d), cold max %d ms, chars %d/%d, correct %d/%d of %d, harder %d/%d, warm cpu p50/max %d/%d ms, retimed %d",
		s.Recall, s.Questions, s.Top1, s.Questions, s.Stale, s.Noisy, s.LoreCalls, s.GrepCalls, s.WarmP50.Milliseconds(), s.WarmMax.Milliseconds(), s.WarmRawMax.Milliseconds(), s.ColdMax.Milliseconds(),
		s.LoreChars, s.GrepChars, s.LoreCorrect, s.GrepCorrect, s.Questions, s.Harder, s.HarderOf,
		s.WarmCPUP50.Milliseconds(), s.WarmCPUMax.Milliseconds(), s.Retimed)
}

// typeTotals is one type's totals.
type typeTotals struct {
	n, loreOK, grepOK    int
	loreChars, grepChars int
	loreCalls, grepCalls int
	warmMax, coldMax     time.Duration
	// accuracy, as in Summary
	recall, top1, stale, noisy int
	grepPresent, grepStale     int
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
		a := r.Lore.Acc
		t.recall += b2i(a.Recall)
		t.top1 += b2i(a.Top1)
		t.stale += b2i(len(a.Stale) > 0)
		t.noisy += b2i(a.Noise > BarNoiseTop)
		t.grepPresent += b2i(r.Grep.Present)
		t.grepStale += b2i(len(r.Grep.Stale) > 0)
	}
	return m
}

// b2i counts a bool.
func b2i(ok bool) int {
	if ok {
		return 1
	}
	return 0
}

// AccuracyMisses lists every accuracy bar the run misses, one line each:
// per question recall@3, stale and noise, then top-1 over all questions.
func AccuracyMisses(rs []Result) []string {
	var out []string
	for _, r := range rs {
		id := r.Q.ID + " (" + string(r.Q.Type) + ")"
		a := r.Lore.Acc
		if r.Lore.AccErr != "" {
			out = append(out, fmt.Sprintf("%s: no graded answer: %s", id, r.Lore.AccErr))
			continue
		}
		if !a.Recall {
			out = append(out, fmt.Sprintf("%s: recall@3 miss, the bar is 100%%: %s", id, strings.Join(cut(notIn(a.Missing), 3), "; ")))
		}
		if len(a.Stale) > 0 {
			out = append(out, fmt.Sprintf("%s: stale fact in the top %d, the bar is 0: %s", id, r.Q.TopK(), strings.Join(cut(a.Stale, 2), "; ")))
		}
		if a.Noise > BarNoiseTop {
			out = append(out, fmt.Sprintf("%s: %d irrelevant items in the top %d, the bar is <= %d", id, a.Noise, BarTopK, BarNoiseTop))
		}
	}
	s := Summarize(rs)
	if s.Questions > 0 && s.Top1*100 < BarTop1Pct*s.Questions {
		var wrong []string
		for _, r := range rs {
			if !r.Lore.Acc.Top1 {
				wrong = append(wrong, r.Q.ID)
			}
		}
		out = append(out, fmt.Sprintf("top-1: %d/%d right (%d%%), the bar is >= %d%%; wrong: %s", s.Top1, s.Questions, s.Top1*100/s.Questions, BarTop1Pct, strings.Join(wrong, " ")))
	}
	return out
}

// notIn keeps the recall lines of an accuracy's Missing.
func notIn(xs []string) []string {
	var out []string
	for _, x := range xs {
		if strings.HasPrefix(x, "not in top") {
			out = append(out, x)
		}
	}
	return out
}

// Misses lists every bar the run misses, one line each, the accuracy bars
// first; empty = pass.
func Misses(rs []Result) []string {
	out := AccuracyMisses(rs)
	for _, r := range rs {
		id := r.Q.ID + " (" + string(r.Q.Type) + ")"
		if r.Lore.Calls != BarCalls {
			out = append(out, fmt.Sprintf("%s: %d lore calls, the bar is %d", id, r.Lore.Calls, BarCalls))
		}
		if r.Lore.Warm >= BarWarm {
			out = append(out, fmt.Sprintf("%s: warm call %d ms (median of %s ms%s), the bar is < %d ms", id, r.Lore.Warm.Milliseconds(), runsMS(r.Lore), firstWarm(r.Lore), BarWarm.Milliseconds()))
		}
		if r.Lore.Cold >= BarCold {
			out = append(out, fmt.Sprintf("%s: cold call %d ms%s, the bar is < %d ms", id, r.Lore.Cold.Milliseconds(), firstCold(r.Lore), BarCold.Milliseconds()))
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
		if !ok {
			continue
		}
		switch t.TypedBar() {
		case BarTenfold:
			if tt.loreChars*BarTypedGain > tt.grepChars {
				out = append(out, fmt.Sprintf("typed answers %q: lore %d chars vs grep %d, the bar is >= %dx lighter", t, tt.loreChars, tt.grepChars, BarTypedGain))
			}
		case BarLighter:
			if tt.loreChars >= tt.grepChars {
				out = append(out, fmt.Sprintf("typed answers %q: lore %d chars vs grep %d, the bar is lighter than grep", t, tt.loreChars, tt.grepChars))
			}
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
	// BoxLoad is the machine's load average (1/5/15 min) at the start
	// and at the end of the questions ("" where it cannot be read), so a
	// report from a loaded box says so next to its wall times.
	BoxLoad [2]string
}

// Report renders tmp/bench/recall.md: the run, the summary line, the
// missed bars, a table per question type and a table per question.
func Report(info RunInfo, rs []Result) string {
	var b strings.Builder
	s := Summarize(rs)
	misses := Misses(rs)
	fmt.Fprintf(&b, "# lore beats grep: recall gate\n\n")
	fmt.Fprintf(&b, "Run %s; now pinned at %s; seed %d; %d rows in the store (loaded in %s); %s; grep arm tool: %s.\n\n", info.Started.UTC().Format(time.RFC3339), info.Now.Format(time.RFC3339), info.Seed, info.Rows, info.Load.Round(time.Second), info.Lore, info.GrepTool)
	if info.BoxLoad[0] != "" || info.BoxLoad[1] != "" {
		fmt.Fprintf(&b, "Box load (1/5/15 min): %s at the first question, %s after the last; wall times on a loaded box are mostly waiting for a core, so each call's CPU time (user + system) is shown next to it.\n\n", orNone(info.BoxLoad[0]), orNone(info.BoxLoad[1]))
	}
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
	b.WriteString("## Accuracy (lore ask --json, first)\n\n")
	fmt.Fprintf(&b, "Bars: recall@3 100%% (top max(3, items the answer needs)), top-1 >= %d%% overall, 0 stale facts, <= %d irrelevant in the top 3. Grep: present = every fact in its output, stale = an old fact without the current one.\n\n", BarTop1Pct, BarNoiseTop)
	b.WriteString("| type | n | recall@3 | top-1 | stale | noise > 1 | grep present | grep stale |\n|---|---|---|---|---|---|---|---|\n")
	m := byType(rs)
	for _, set := range []struct {
		name   string
		harder bool
	}{{"normal", false}, {"harder", true}} {
		var tot typeTotals
		for _, t := range Types() {
			tt, ok := m[t]
			if !ok || t.Harder() != set.harder {
				continue
			}
			fmt.Fprintf(&b, "| %s | %d | %d/%d | %d/%d | %d | %d | %d/%d | %d |\n", t, tt.n, tt.recall, tt.n, tt.top1, tt.n, tt.stale, tt.noisy, tt.grepPresent, tt.n, tt.grepStale)
			tot.n += tt.n
			tot.recall += tt.recall
			tot.top1 += tt.top1
			tot.stale += tt.stale
			tot.noisy += tt.noisy
			tot.grepPresent += tt.grepPresent
			tot.grepStale += tt.grepStale
		}
		fmt.Fprintf(&b, "| **%s set** | %d | %d/%d | %d/%d | %d | %d | %d/%d | %d |\n", set.name, tot.n, tot.recall, tot.n, tot.top1, tot.n, tot.stale, tot.noisy, tot.grepPresent, tot.n, tot.grepStale)
	}
	b.WriteString("\n## Speed and size per question type\n\n| type | n | correct lore/grep | calls lore/grep | warm max ms | cold max ms | chars lore/grep | ratio lore/grep |\n|---|---|---|---|---|---|---|---|\n")
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
	b.WriteString("\n## Per question\n\n| id | type | question | lore call | recall@3 | top-1 | stale | noise | lore ok | warm ms | warm runs ms | warm cpu ms | cold ms | cold cpu ms | lore chars | grep calls | grep chars | grep ok | lore misses |\n|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range rs {
		a := r.Lore.Acc
		misses := append(slices.Clone(a.Missing), r.Lore.Missing...)
		if r.Lore.AccErr != "" {
			misses = append([]string{r.Lore.AccErr}, misses...)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | `lore %s` | %s | %s | %d | %d | %s | %d | %s | %d | %d | %d | %d | %d | %d | %s | %s |\n", r.Q.ID, r.Q.Type, cell(r.Q.Text), cell(strings.Join(r.Q.Lore, " ")), yes(a.Recall), yes(a.Top1), len(a.Stale), a.Noise, yes(r.Lore.Correct), r.Lore.Warm.Milliseconds(), runsCell(r.Lore), r.Lore.WarmCPU.Milliseconds(), r.Lore.Cold.Milliseconds(), r.Lore.ColdCPU.Milliseconds(), r.Lore.Chars, r.Grep.Calls, r.Grep.Chars, yes(r.Grep.Correct), cell(strings.Join(cut(misses, 3), "; ")))
	}
	return b.String()
}

// runsMS prints a question's warm calls in milliseconds, in the order
// they ran ("31/77/29"); "-" when they were not kept.
func runsMS(r LoreRun) string {
	return msList(r.WarmRuns)
}

// msList prints wall times in milliseconds joined by "/" ("-" for none).
func msList(ds []time.Duration) string {
	if len(ds) == 0 {
		return "-"
	}
	ms := make([]string, len(ds))
	for i, w := range ds {
		ms[i] = fmt.Sprint(w.Milliseconds())
	}
	return strings.Join(ms, "/")
}

// firstWarm and firstCold name the first timing of a retimed question in
// a missed-bar line ("" when it was timed once), so a question slow both
// times shows both.
func firstWarm(r LoreRun) string {
	if r.First == nil {
		return ""
	}
	return fmt.Sprintf("; timed again after the run, first %d ms (%s)", WarmOf(r.First.Warm).Milliseconds(), msList(r.First.Warm))
}

func firstCold(r LoreRun) string {
	if r.First == nil {
		return ""
	}
	return fmt.Sprintf(" (timed again after the run, first %d ms)", r.First.Cold.Milliseconds())
}

// runsCell is a question's warm calls for the per-question table: a
// retimed question shows its first calls and cold time before the ones
// the bars judge ("169/226/100 (cold 406) then 31/30/29").
func runsCell(r LoreRun) string {
	if r.First == nil {
		return runsMS(r)
	}
	return fmt.Sprintf("%s (cold %d) then %s", msList(r.First.Warm), r.First.Cold.Milliseconds(), runsMS(r))
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

// orNone prints an unread load average as "unknown".
func orNone(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// BoxLoad reads the 1, 5 and 15 minute load averages from /proc/loadavg
// ("" where there is none, as on macOS).
func BoxLoad() string {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return ""
	}
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return ""
	}
	return strings.Join(f[:3], " ")
}
