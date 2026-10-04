package main

// Use-case tests for the bench evaluation engine (`lore bench …`), the skill
// compiler (`lore skill compile`) and the TUI's tables screen. The bench run
// and the skill compiler call a model through llmcall.Auto(), which — with
// ANTHROPIC_API_KEY unset and no "ollama:" model prefix — shells out to a
// `claude` CLI found on PATH; the end-to-end tests put a stand-in script
// there, so the real commands run with canned model output and no network.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"dbent/gen/ent"
	entBenchEval "dbent/gen/ent/bencheval"
	entBenchResult "dbent/gen/ent/benchresult"
	entBenchRun "dbent/gen/ent/benchrun"

	enttuirt "github.com/khanakia/entx/enttui/runtime"
)

// floatsClose compares floats computed through sqrt/division.
func floatsClose(a, b float64) bool { return math.Abs(a-b) < 1e-3 }

// ── pure statistics (bench_report.go) ───────────────────────────────────────

func TestUC_BenchComputePairedStats(t *testing.T) {
	t.Parallel()
	if got := computePairedStats(nil); got != (pairedStats{}) {
		t.Fatalf("empty: %+v", got)
	}
	if got := computePairedStats([]float64{2}); got.N != 1 || got.Mean != 2 || got.SD != 0 || got.T != 0 {
		t.Fatalf("single sample has no spread: %+v", got)
	}
	// Textbook paired sample: deltas 1..5 → mean 3, SD √2.5, SE √0.5,
	// t = 3/√0.5 ≈ 4.243 (df 4, beyond the 99% cut 4.032 → p 0.01),
	// Cohen's d = 3/√2.5 ≈ 1.897, 95% CI 3 ± 2.571·√0.5.
	got := computePairedStats([]float64{1, 2, 3, 4, 5})
	se := math.Sqrt(0.5)
	want := pairedStats{N: 5, Mean: 3, SD: math.Sqrt(2.5), T: 3 / se, P: 0.01, D: 3 / math.Sqrt(2.5), CILo: 3 - 2.571*se, CIHi: 3 + 2.571*se}
	for name, pair := range map[string][2]float64{
		"mean": {got.Mean, want.Mean}, "sd": {got.SD, want.SD}, "t": {got.T, want.T}, "p": {got.P, want.P},
		"d": {got.D, want.D}, "ci-lo": {got.CILo, want.CILo}, "ci-hi": {got.CIHi, want.CIHi},
	} {
		if !floatsClose(pair[0], pair[1]) {
			t.Fatalf("%s = %v, want %v (%+v)", name, pair[0], pair[1], got)
		}
	}
	// No spread: t and d stay 0 (no division by zero), p is the weakest bucket.
	if flat := computePairedStats([]float64{2, 2, 2}); flat.T != 0 || flat.D != 0 || flat.P != 0.20 {
		t.Fatalf("zero variance: %+v", flat)
	}
	// n ≥ 30 switches to the normal approximation and the 1.96 critical value.
	big := make([]float64, 30)
	for i := range big {
		big[i] = float64(i % 2)
	}
	b := computePairedStats(big)
	bse := b.SD / math.Sqrt(30)
	if !floatsClose(b.CIHi-b.Mean, 1.96*bse) || !floatsClose(b.P, math.Erfc(math.Abs(b.T)/math.Sqrt2)) {
		t.Fatalf("large sample: %+v", b)
	}
}

func TestUC_BenchApproxTwoTailedP(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		t    float64
		df   int
		want float64
	}{
		{5, 0, 1.0},
		{1.96, 30, math.Erfc(1.96 / math.Sqrt2)}, // ≈ 0.05, normal approximation
		{3.2, 10, 0.01},                          // ≥ t99(10) = 3.169
		{-3.2, 10, 0.01},                         // two-tailed: sign ignored
		{2.3, 10, 0.05},                          // ≥ t95(10) = 2.228
		{1.8, 10, 0.10},                          // within 0.5 of t95
		{1.0, 10, 0.20},
		{4.1, 5, 0.01},
		{2.6, 25, 0.01},                        // ≥ t99(25) = 2.787? no → 2.6 ≥ 2.060 → 0.05
		{2.0, 27, math.Erfc(2.0 / math.Sqrt2)}, // df 26..29 is past the table
	} {
		want := tc.want
		if tc.t == 2.6 && tc.df == 25 {
			want = 0.05
		}
		if got := approxTwoTailedP(tc.t, tc.df); !floatsClose(got, want) {
			t.Fatalf("p(t=%v, df=%d) = %v, want %v", tc.t, tc.df, got, want)
		}
	}
	if p := approxTwoTailedP(1.96, 30); math.Abs(p-0.05) > 0.001 {
		t.Fatalf("t=1.96 at large df must be p≈0.05, got %v", p)
	}
}

func TestUC_BenchTCrit95(t *testing.T) {
	t.Parallel()
	for df, want := range map[int]float64{0: 12.706, 1: 12.706, 3: 2.571, 5: 2.571, 7: 2.228, 12: 2.131, 18: 2.086, 24: 2.060, 25: 2.060, 26: 2.045, 40: 2.045} {
		if got := tCrit95(df); got != want {
			t.Fatalf("tCrit95(%d) = %v, want %v", df, got, want)
		}
	}
}

func TestUC_BenchEffectSizeLabel(t *testing.T) {
	t.Parallel()
	for d, want := range map[float64]string{
		0: "(negligible)", 0.19: "(negligible)", -0.19: "(negligible)",
		0.2: "(small)", -0.49: "(small)",
		0.5: "(medium)", 0.79: "(medium)",
		0.8: "(large)", -2: "(large)",
	} {
		if got := effectSizeLabel(d); got != want {
			t.Fatalf("effectSizeLabel(%v) = %q, want %q", d, got, want)
		}
	}
}

// ── small helpers ───────────────────────────────────────────────────────────

func TestUC_BenchSmallHelpers(t *testing.T) {
	t.Parallel()
	if strPtrOrNil("") != nil {
		t.Fatal("empty string must become nil")
	}
	if p := strPtrOrNil("x"); p == nil || *p != "x" {
		t.Fatal("non-empty string must round-trip")
	}
	if errString(nil) != "" || errString(errors.New("boom")) != "boom" {
		t.Fatal("errString")
	}
	n := 7
	if derefIntZero(nil) != 0 || derefIntZero(&n) != 7 {
		t.Fatal("derefIntZero")
	}
	for _, tc := range []struct{ in, want []string }{
		{nil, nil},
		{[]string{"b"}, []string{"b"}},
		{[]string{"c", "a", "b", "a"}, []string{"a", "a", "b", "c"}},
		{[]string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{[]string{"z", "y", "x"}, []string{"x", "y", "z"}},
	} {
		got := append([]string(nil), tc.in...)
		sortStrings(got)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("sortStrings(%v) = %v", tc.in, got)
		}
	}
}

func TestUC_BenchIOReadAll(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("abcdefghij", 1000) // spans several 4 KiB reads
	for name, r := range map[string]interface {
		Read(p []byte) (int, error)
	}{
		"whole":     strings.NewReader(long),
		"byte-wise": iotest.OneByteReader(strings.NewReader(long)),
		"data+EOF":  iotest.DataErrReader(strings.NewReader(long)),
	} {
		got, err := io_ReadAll(r)
		if err != nil || string(got) != long {
			t.Fatalf("%s: %d bytes, %v", name, len(got), err)
		}
	}
	boom := errors.New("boom")
	got, err := io_ReadAll(iotest.TimeoutReader(strings.NewReader("ab")))
	if err == nil || len(got) == 0 {
		t.Fatalf("a read error after data must be returned with the data: %q %v", got, err)
	}
	if _, err := io_ReadAll(iotest.ErrReader(boom)); !errors.Is(err, boom) {
		t.Fatalf("read error lost: %v", err)
	}
	if got, err := io_ReadAll(strings.NewReader("")); err != nil || len(got) != 0 {
		t.Fatalf("empty input: %q %v", got, err)
	}
}

func TestUC_BenchAutoRunCode(t *testing.T) {
	t.Parallel()
	day := time.Now().Format("20060102")
	for model, want := range map[string]string{
		"claude-haiku-4-5-20251001": "RUN-" + day + "-claude-haiku",
		"gpt-4":                     "RUN-" + day + "-gpt-4",
		"ollama:qwen3":              "RUN-" + day + "-ollama:qwen3",
	} {
		// A run spanning midnight would differ by a day; recompute once.
		if got := autoRunCode(model); got != want && got != strings.Replace(want, day, time.Now().Format("20060102"), 1) {
			t.Fatalf("autoRunCode(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestUC_BenchParseArms(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"baseline,with_skill":    "baseline,with_skill",
		" with_skill , bogus, ,": "with_skill",
		"":                       "",
		"baseline,baseline":      "baseline,baseline",
		"BASELINE":               "",
	} {
		var got []string
		for _, a := range parseArms(in) {
			got = append(got, string(a))
		}
		if strings.Join(got, ",") != want {
			t.Fatalf("parseArms(%q) = %v, want %q", in, got, want)
		}
	}
}

func TestUC_BenchBuildArmPrompt(t *testing.T) {
	t.Parallel()
	if got := buildArmPrompt(entBenchResult.ArmBaseline, "PRE ", "SKILL", "TASK"); got != "PRE TASK" {
		t.Fatalf("baseline must never see the skill text: %q", got)
	}
	if got := buildArmPrompt(entBenchResult.ArmWithSkill, "PRE ", "SKILL", "TASK"); got != "PRE SKILL\n\n---\n\nTASK" {
		t.Fatalf("with_skill prompt: %q", got)
	}
}

func TestUC_BenchLoadClaudeMd(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	sha := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	if _, _, _, err := loadClaudeMd(""); err == nil || !strings.Contains(err.Error(), "bench-CLAUDE.md") {
		t.Fatalf("nothing to load must name what was tried: %v", err)
	}
	if err := os.WriteFile("CLAUDE.md", []byte("plain"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, text, s, err := loadClaudeMd(""); err != nil || p != "CLAUDE.md" || text != "plain" || s != sha("plain") {
		t.Fatalf("fallback CLAUDE.md: %q %q %q %v", p, text, s, err)
	}
	if err := os.WriteFile("bench-CLAUDE.md", []byte("bench"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, text, _, err := loadClaudeMd(""); err != nil || p != "bench-CLAUDE.md" || text != "bench" {
		t.Fatalf("bench-CLAUDE.md wins over CLAUDE.md: %q %q %v", p, text, err)
	}
	explicit := filepath.Join(dir, "x.md")
	if err := os.WriteFile(explicit, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, text, s, err := loadClaudeMd(explicit); err != nil || p != explicit || text != "mine" || s != sha("mine") {
		t.Fatalf("explicit path wins: %q %q %v", p, text, err)
	}
	if p, _, _, err := loadClaudeMd(filepath.Join(dir, "missing.md")); err != nil || p != "bench-CLAUDE.md" {
		t.Fatalf("a missing explicit path falls back: %q %v", p, err)
	}
}

func TestUC_BenchSummaryDeltaAndParseSince(t *testing.T) {
	t.Parallel()
	if s, err := parseRunSummary(map[string]any{"delta_pp": 12.5}); err != nil || s.DeltaPP != 12.5 {
		t.Fatalf("stored delta: %+v %v", s, err)
	}
	if s, err := parseRunSummary(map[string]any{}); err != nil || s.DeltaPP != 0 || len(s.Arms) != 0 {
		t.Fatalf("a not-yet-finished run stores {}: %+v %v", s, err)
	}
	// A malformed stored summary is reported, never shown as zeros.
	if _, err := parseRunSummary(map[string]any{"delta_pp": "12"}); err == nil {
		t.Fatal("a wrongly typed stored summary must be an error")
	}
	got, err := parseSince("7d")
	if err != nil || math.Abs(time.Since(got).Hours()-7*24) > 1 {
		t.Fatalf("7d = %v %v", got, err)
	}
	if got, err := parseSince("2026-01-31"); err != nil || got.Format(time.DateOnly) != "2026-01-31" {
		t.Fatalf("date = %v %v", got, err)
	}
	for _, bad := range []string{"xd", "yesterday", "31/01/2026"} {
		if _, err := parseSince(bad); err == nil {
			t.Fatalf("%q must fail", bad)
		}
	}
}

// A summary as stored in the DB (JSON numbers decode to float64).
func storedSummary(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	raw := `{"arms":{"baseline":{"n":4,"pass":1,"pass_rate":0.25},"with_skill":{"n":4,"pass":3,"pass_rate":0.75}},
		"by_category":{"with_skill":{"rule-trigger":{"n":4,"pass":3,"pass_rate":0.75}},"baseline":{"rule-trigger":{"n":4,"pass":1,"pass_rate":0.25}}},
		"delta_pp":50}`
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// parsedStoredSummary is storedSummary read the way the commands read it.
func parsedStoredSummary(t *testing.T) benchRunSummary {
	t.Helper()
	s, err := parseRunSummary(storedSummary(t))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestUC_BenchPrintRunSummary(t *testing.T) {
	out := captureStdout(t, func() { printRunSummary("RUN-1", parsedStoredSummary(t), 8, 0.0123, 1500*time.Millisecond) })
	for _, want := range []string{
		"=== summary: RUN-1 ===",
		"baseline     1/4    (25.0%)",
		"with_skill   3/4    (75.0%)",
		"Δ:           +50.0 pp",
		"calls:       8   cost: $0.0123   elapsed: 2s",
		"baseline     rule-trigger         25.0%",
		"with_skill   rule-trigger         75.0%",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if i, j := strings.Index(out, "baseline     rule"), strings.Index(out, "with_skill   rule"); i > j {
		t.Fatal("by-category arms must print in sorted order")
	}
	neg := parsedStoredSummary(t)
	neg.DeltaPP = -12.5
	out = captureStdout(t, func() { printRunSummary("RUN-2", neg, 0, 0, 0) })
	if !strings.Contains(out, "Δ:           -12.5 pp") || strings.Contains(out, "elapsed") {
		t.Fatalf("negative delta / no elapsed:\n%s", out)
	}
}

// ── DB-backed helpers ───────────────────────────────────────────────────────

// saved wraps a Save result so a test can fail on its error before using
// the row: saved(q.Save(ctx)).get(t).
type savedRow[T any] struct {
	row T
	err error
}

func saved[T any](row T, err error) savedRow[T] { return savedRow[T]{row, err} }

func (s savedRow[T]) get(t *testing.T) T {
	t.Helper()
	if s.err != nil {
		t.Fatal(s.err)
	}
	return s.row
}

// benchFixture is a project with one row of each linkable kind plus bench
// evals in several categories (and one archived, one in another project).
type benchFixture struct {
	client                           *ent.Client
	project, other                   string
	hotfix, decision, memory         string
	evalRule, evalHotfix, evalCustom string
}

func newBenchFixture(t *testing.T) benchFixture {
	t.Helper()
	ctx := context.Background()
	c := openTestClient(t)
	f := benchFixture{client: c}
	f.project = saved(c.Project.Create().SetName("bench").Save(ctx)).get(t).ID
	f.other = saved(c.Project.Create().SetName("other").Save(ctx)).get(t).ID
	f.hotfix = saved(c.Hotfix.Create().SetProjectID(f.project).SetTitle("ent regen").SetBody("ent regen wipes resolver helpers").SetSourceKind("manual").Save(ctx)).get(t).ID
	f.decision = saved(c.Decision.Create().SetProjectID(f.project).SetTitle("Use JWT").SetBody("sessions do not scale").SetSourceKind("manual").Save(ctx)).get(t).ID
	f.memory = saved(c.Memory.Create().SetProjectID(f.project).SetBody("cache for 5 minutes").SetSourceKind("manual").Save(ctx)).get(t).ID
	addEval := func(project, code string, cat entBenchEval.Category, archived bool) string {
		t.Helper()
		q := c.BenchEval.Create().SetProjectID(project).SetCode(code).SetCategory(cat).SetPrompt("p " + code).
			SetGraderKind(entBenchEval.GraderKindProgrammatic).SetGraderSpec(map[string]any{"cmd": "true"})
		if archived {
			q.SetArchivedAt(time.Now())
		}
		return saved(q.Save(ctx)).get(t).ID
	}
	f.evalRule = addEval(f.project, "E1-001", entBenchEval.CategoryRuleTrigger, false)
	f.evalHotfix = addEval(f.project, "E2-001", entBenchEval.CategoryHotfixAvoid, false)
	f.evalCustom = addEval(f.project, "X-001", entBenchEval.CategoryCustom, false)
	addEval(f.project, "E1-999", entBenchEval.CategoryRuleTrigger, true)
	addEval(f.other, "E1-001", entBenchEval.CategoryRuleTrigger, false)
	return f
}

func TestUC_BenchLookupBodies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newBenchFixture(t)
	if body, id := lookupHotfixBody(ctx, f.client, f.project, f.hotfix); body != "ent regen wipes resolver helpers" || id != f.hotfix {
		t.Fatalf("hotfix: %q %q", body, id)
	}
	if body, id := lookupDecisionBody(ctx, f.client, f.project, f.decision); body != "Use JWT\n\nsessions do not scale" || id != f.decision {
		t.Fatalf("decision = title + body: %q %q", body, id)
	}
	// A decision always has a body (ent rejects an empty one), so the
	// title-only branch of lookupDecisionBody cannot be reached.
	if _, err := f.client.Decision.Create().SetProjectID(f.project).SetTitle("t").SetBody("").SetSourceKind("manual").Save(ctx); err == nil {
		t.Fatal("ent now accepts an empty decision body: cover lookupDecisionBody's title-only branch")
	}
	if body, id := lookupMemoryBody(ctx, f.client, f.project, f.memory); body != "cache for 5 minutes" || id != f.memory {
		t.Fatalf("memory: %q %q", body, id)
	}
	// Another project's row, or an unknown id, resolves to nothing.
	for name, got := range map[string][2]string{
		"hotfix other project":   pair(lookupHotfixBody(ctx, f.client, f.other, f.hotfix)),
		"decision other project": pair(lookupDecisionBody(ctx, f.client, f.other, f.decision)),
		"memory other project":   pair(lookupMemoryBody(ctx, f.client, f.other, f.memory)),
		"unknown hotfix":         pair(lookupHotfixBody(ctx, f.client, f.project, "hfx_00000000000070008000000000000000")),
		"unknown memory":         pair(lookupMemoryBody(ctx, f.client, f.project, "nope")),
	} {
		if got != [2]string{"", ""} {
			t.Fatalf("%s must resolve to nothing: %v", name, got)
		}
	}
}

func pair(a, b string) [2]string { return [2]string{a, b} }

func TestUC_BenchPickEvals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newBenchFixture(t)
	codes := func(set string) string {
		t.Helper()
		evs, err := pickEvals(ctx, f.client, f.project, set)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range evs {
			out = append(out, e.Code)
		}
		return strings.Join(out, ",")
	}
	for set, want := range map[string]string{
		"all":           "E1-001,E2-001,X-001", // archived + other project's evals excluded, sorted by code
		"":              "E1-001,E2-001,X-001",
		"  all ":        "E1-001,E2-001,X-001",
		"rule-trigger":  "E1-001",
		"custom":        "X-001",
		"E2":            "E2-001",
		"e2":            "E2-001",
		"E3":            "",
		"X-001, E1-001": "E1-001,X-001",
		"E1-999":        "", // archived
		",":             "",
	} {
		if got := codes(set); got != want {
			t.Fatalf("pickEvals(%q) = %q, want %q", set, got, want)
		}
	}
}

// seedBenchResults records a run: baseline passes 1 of 2, with_skill 2 of 2,
// across two evals.
func seedBenchResults(t *testing.T, f benchFixture) string {
	t.Helper()
	ctx := context.Background()
	c := f.client
	run, err := c.BenchRun.Create().SetProjectID(f.project).SetCode("RUN-T").SetModel("m").
		SetEvalCodes([]string{"E1-001", "E2-001"}).SetArms([]string{"baseline", "with_skill"}).
		SetStartedAt(time.Now()).SetStatus(entBenchRun.StatusRunning).SetSummary(map[string]any{}).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	add := func(eval string, arm entBenchResult.Arm, grade entBenchResult.Grade) {
		t.Helper()
		if _, err := c.BenchResult.Create().SetProjectID(f.project).SetBenchRunID(run.ID).SetBenchEvalID(eval).
			SetArm(arm).SetAttempt(1).SetPromptSent("p").SetOutputReceived("o").SetGrade(grade).
			SetGraderTrace(map[string]any{}).Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	add(f.evalRule, entBenchResult.ArmBaseline, entBenchResult.GradePass)
	add(f.evalHotfix, entBenchResult.ArmBaseline, entBenchResult.GradeFail)
	add(f.evalRule, entBenchResult.ArmWithSkill, entBenchResult.GradePass)
	add(f.evalHotfix, entBenchResult.ArmWithSkill, entBenchResult.GradePass)
	return run.ID
}

func TestUC_BenchComputeRunSummary(t *testing.T) {
	t.Parallel()
	f := newBenchFixture(t)
	runID := seedBenchResults(t, f)
	s, err := computeRunSummary(context.Background(), f.client, runID)
	if err != nil {
		t.Fatal(err)
	}
	base, with := s.Arms["baseline"], s.Arms["with_skill"]
	if base != (benchArmStats{N: 2, Pass: 1, PassRate: 0.5}) || with != (benchArmStats{N: 2, Pass: 2, PassRate: 1}) {
		t.Fatalf("arms: %+v", s.Arms)
	}
	if s.DeltaPP != 50 {
		t.Fatalf("delta: %v", s.DeltaPP)
	}
	cats := s.ByCategory["baseline"]
	if cats["hotfix-avoid"].PassRate != 0 || cats["rule-trigger"].PassRate != 1 {
		t.Fatalf("by category: %+v", cats)
	}
	// Stored and read back: the same values (the round trip the
	// commands do through ent's JSON column).
	stored, err := s.toStored()
	if err != nil {
		t.Fatal(err)
	}
	back, err := parseRunSummary(stored)
	if err != nil || back.Arms["baseline"] != base || back.DeltaPP != s.DeltaPP || back.ByCategory["baseline"]["rule-trigger"] != cats["rule-trigger"] {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	empty, err := computeRunSummary(context.Background(), f.client, "brn_none")
	if err != nil || empty.DeltaPP != 0 || len(empty.Arms) != 0 {
		t.Fatalf("a run without results: %+v %v", empty, err)
	}
}

func TestUC_BenchRunEvalRates(t *testing.T) {
	t.Parallel()
	f := newBenchFixture(t)
	rates, err := runEvalRates(context.Background(), f.client, seedBenchResults(t, f))
	if err != nil {
		t.Fatal(err)
	}
	if r := rates["E1-001"]; r.baseline != 1 || r.withSkill != 1 {
		t.Fatalf("E1-001: %+v", r)
	}
	if r := rates["E2-001"]; r.baseline != 0 || r.withSkill != 1 {
		t.Fatalf("E2-001: %+v", r)
	}
	if len(rates) != 2 {
		t.Fatalf("rates: %v", rates)
	}
}

// Regression: `bench run start` printed its end-of-run summary from an
// in-memory map whose counts were ints, while the printer read float64 (the
// shape after a JSON round trip), so every count printed as 0/0. The summary
// is a typed struct now (bench_summary.go); this pins the printed counts.
func TestUC_BenchPrintRunSummaryFromComputedSummary(t *testing.T) {
	f := newBenchFixture(t)
	s, err := computeRunSummary(context.Background(), f.client, seedBenchResults(t, f))
	if err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { printRunSummary("RUN-T", s, 4, 0, 0) })
	if !strings.Contains(out, "baseline     1/2") || !strings.Contains(out, "with_skill   2/2") {
		t.Fatalf("bench run start must print the real pass counts:\n%s", out)
	}
}

// ── skill compiler helpers ──────────────────────────────────────────────────

func TestUC_BenchSkillCompilerHelpers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("SKILL.md", "skill")
	write("ZETA.md", "zeta")
	write("ALPHA.md", "alpha")
	write("examples/one.md", "ex")
	write("notes.txt", "not markdown")
	if err := os.MkdirAll(filepath.Join(dir, "dir.md"), 0o755); err != nil { // a directory matching *.md
		t.Fatal(err)
	}
	files, total, err := collectSkillSources(dir, []string{"*.md", "examples/*.md", "*.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 || total != len("skill")+len("zeta")+len("alpha")+len("ex") || files["examples/one.md"] != "ex" {
		t.Fatalf("collected %v (%d bytes)", files, total)
	}
	if _, _, err := collectSkillSources(dir, []string{"["}); err == nil {
		t.Fatal("a malformed glob must error")
	}
	if f, n, err := collectSkillSources(filepath.Join(dir, "missing"), []string{"*.md"}); err != nil || len(f) != 0 || n != 0 {
		t.Fatalf("missing dir: %v %d %v", f, n, err)
	}

	prompt := buildSkillCompressPrompt(files, 9000)
	if !strings.HasPrefix(prompt, compressionRules(9000)) {
		t.Fatal("the prompt must open with the compression rules")
	}
	order := []string{"## FILE: SKILL.md", "## FILE: ALPHA.md", "## FILE: ZETA.md", "## FILE: examples/one.md"}
	last := -1
	for _, h := range order {
		i := strings.Index(prompt, h)
		if i <= last {
			t.Fatalf("SKILL.md first, then alphabetical; %q out of order", h)
		}
		last = i
	}
	if !strings.Contains(prompt, "approximately 9000 bytes") {
		t.Fatal("budget not stated")
	}
	if rules := compressionRules(1234); strings.Count(rules, "1234") != 2 || !strings.Contains(rules, "OUTPUT CONTRACT") {
		t.Fatal("compressionRules must state the budget at the top and the end")
	}
}

func TestUC_BenchExtractCompressedOutput(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"---\nname: x\n---\nbody":                                  "---\nname: x\n---\nbody\n",
		"  ---\nbody  \n\n":                                        "---\nbody\n",
		"```markdown\n---\nbody\n```":                              "---\nbody\n",
		"Here is the compressed version:\n---\nname: x\n---\nbody": "---\nname: x\n---\nbody\n",
		"```\nSure! Below:\n---\nbody\n```":                        "---\nbody\n",
		"ok\n---\nbody":                                            "ok\n---\nbody\n", // too little prose to strip
		"no frontmatter at all":                                    "no frontmatter at all\n",
	} {
		if got := extractCompressedOutput(in); got != want {
			t.Fatalf("extractCompressedOutput(%q) = %q, want %q", in, got, want)
		}
	}
}

// ── TUI tables screen ───────────────────────────────────────────────────────

func TestUC_BenchTUITablesHelpers(t *testing.T) {
	t.Parallel()
	if tablesSortField("rows") != "count" || tablesSortField("table") != "name" || tablesSortField("") != "name" {
		t.Fatal("tablesSortField")
	}
	if tablesDir(enttuirt.Desc) != "desc" || tablesDir(enttuirt.Asc) != "asc" {
		t.Fatal("tablesDir")
	}
	// Registration validates the spec (Kind, Fetch) and panics on a bad
	// one; the Fetch closure itself is private to the runtime.
	app := enttuirt.New()
	registerTablesScreen(app, filepath.Join(t.TempDir(), "lore.db"))
}

// ── end to end, with a stand-in model ───────────────────────────────────────

// fakeClaude installs a `claude` that answers with script (a POSIX sh body
// that may read the prompt on stdin) and returns the env for running lore
// against it.
func fakeClaude(t *testing.T, script string) []string {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\n" + script + "\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{
		"PATH=" + dir + string(os.PathListSeparator) + filepath.Dir(e2eBin) + string(os.PathListSeparator) + os.Getenv("PATH"),
		"ANTHROPIC_API_KEY=", // empty = unset for llmcall's router
	}
}

func TestUC_BenchRunStartEndToEnd(t *testing.T) {
	w := newWorld(t)
	a := w.newClone("alice")
	a.lore("init", "--non-interactive", "--name=app")
	hfx := idPattern.FindString(a.lore("hotfix", "add", "--title=ent regen", "--body=ent regen wipes resolver helpers").stdout)
	a.lore("bench", "eval", "add", "--code=E2-001", "--category=hotfix-avoid", "--prompt=change the resolver",
		"--grader-kind=programmatic", `--grader-cmd=grep -q SAFE "$OUTPUT_FILE"`, "--link=hotfix:"+hfx)
	a.lore("bench", "eval", "add", "--code=E1-001", "--category=rule-trigger", "--prompt=hello",
		"--grader-kind=programmatic", `--grader-cmd=grep -q NEVER "$OUTPUT_FILE"`)
	if err := os.WriteFile(filepath.Join(a.dir, "bench.md"), []byte("SKILLMARKER rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The stand-in model answers SAFE only when the skill text is in the prompt.
	env := fakeClaude(t, `if grep -q SKILLMARKER; then echo SAFE; else echo whatever; fi`)

	// The hotfix link snapshots the hotfix body (lookupHotfixBody).
	var show struct {
		Data struct {
			LinkedID   string `json:"linked_id"`
			LinkedBody string `json:"linked_body_snapshot"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(a.lore("bench", "eval", "show", "E2-001", "--json").stdout), &show); err != nil ||
		show.Data.LinkedID != hfx || show.Data.LinkedBody != "ent regen wipes resolver helpers" {
		t.Fatalf("linked snapshot: %+v %v", show.Data, err)
	}

	r := w.run(a.dir, env, e2eBin, "bench", "run", "start", "--code=RUN-T", "--claude-md=bench.md", "--parallel=1", "--json")
	if r.code != 0 {
		t.Fatalf("bench run start: %d %s %s", r.code, r.stdout, r.stderr)
	}
	var res struct {
		Data struct {
			Code    string         `json:"code"`
			Calls   int            `json:"calls"`
			Summary map[string]any `json:"summary"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &res); err != nil {
		t.Fatalf("json: %v %s", err, r.stdout)
	}
	arms := res.Data.Summary["arms"].(map[string]any)
	base, with := arms["baseline"].(map[string]any), arms["with_skill"].(map[string]any)
	if res.Data.Code != "RUN-T" || res.Data.Calls != 4 || base["pass"] != 0.0 || base["n"] != 2.0 || with["pass"] != 1.0 || res.Data.Summary["delta_pp"] != 50.0 {
		t.Fatalf("summary: %+v", res.Data)
	}
	// The stored run reports the same numbers.
	rep := a.lore("bench", "report", "summary", "RUN-T").stdout
	if !strings.Contains(rep, "with_skill   1/2") || !strings.Contains(rep, "+50.0 pp") {
		t.Fatalf("report summary:\n%s", rep)
	}

	// Guards before any model call.
	for name, args := range map[string][]string{
		"no evals match": {"bench", "run", "start", "--eval-set=NOPE-1", "--claude-md=bench.md"},
		"no valid arm":   {"bench", "run", "start", "--arms=bogus", "--claude-md=bench.md"},
		"no CLAUDE.md":   {"bench", "run", "start", "--claude-md=missing.md"},
	} {
		if r := w.run(a.dir, env, e2eBin, args...); r.code == 0 {
			t.Fatalf("%s: must fail", name)
		}
	}
	// A model that errors grades the result "error", the run still completes.
	broken := fakeClaude(t, `cat >/dev/null; echo down >&2; exit 1`)
	if r := w.run(a.dir, broken, e2eBin, "bench", "run", "start", "--code=RUN-ERR", "--claude-md=bench.md", "--parallel=1", "--json"); r.code != 0 {
		t.Fatalf("a failing model must not fail the run: %s", r.stderr)
	}
	list := a.lore("bench", "result", "list", "--run=RUN-ERR", "--json").stdout
	if strings.Count(list, `"grade": "error"`) != 4 {
		t.Fatalf("4 results graded error expected:\n%s", list)
	}
	// A budget cap stops the run after the first call and marks it aborted.
	if r := w.run(a.dir, env, e2eBin, "bench", "run", "start", "--code=RUN-CAP", "--claude-md=bench.md", "--parallel=1", "--budget-cap=0.0000001"); r.code == 0 || !strings.Contains(r.stdout+r.stderr, "budget cap") {
		t.Fatalf("budget cap: %d %s %s", r.code, r.stdout, r.stderr)
	}
	runs := a.lore("bench", "run", "list", "--json").stdout
	if !strings.Contains(runs, `"aborted"`) {
		t.Fatalf("capped run must be stored as aborted:\n%s", runs)
	}
}

// User-visible half of TestUC_BenchPrintRunSummaryFromComputedSummary: the
// human summary at the end of `bench run start` shows the real counts.
func TestUC_BenchRunStartPrintsRealCounts(t *testing.T) {
	w := newWorld(t)
	a := w.newClone("alice")
	a.lore("init", "--non-interactive", "--name=app")
	a.lore("bench", "eval", "add", "--code=E1-001", "--category=rule-trigger", "--prompt=hi",
		"--grader-kind=programmatic", `--grader-cmd=grep -q YES "$OUTPUT_FILE"`)
	if err := os.WriteFile(filepath.Join(a.dir, "bench.md"), []byte("ctx"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := fakeClaude(t, `cat >/dev/null; echo YES`)
	r := w.run(a.dir, env, e2eBin, "bench", "run", "start", "--code=RUN-H", "--claude-md=bench.md", "--parallel=1")
	if r.code != 0 {
		t.Fatalf("%s %s", r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "baseline     1/1") || !strings.Contains(r.stdout, "with_skill   1/1") {
		t.Fatalf("summary must show real pass counts:\n%s", r.stdout)
	}
}

func TestUC_BenchSkillCompileEndToEnd(t *testing.T) {
	w := newWorld(t)
	a := w.newClone("alice")
	src := filepath.Join(a.dir, "skill")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte(strings.Repeat("long skill text ", 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "args")
	env := fakeClaude(t, `echo "$@" > `+argsFile+`; grep -q "COMPRESSION RULES" || exit 3; printf 'Here is the compressed version:\n---\nname: lore\n---\nmini body\n'`)

	out := filepath.Join(a.dir, "mini.md")
	r := w.run(a.dir, env, e2eBin, "skill", "compile", "--source-dir="+src, "--output="+out, "--model=claude-test", "--json")
	if r.code != 0 {
		t.Fatalf("compile: %d %s %s", r.code, r.stdout, r.stderr)
	}
	if got := readFileE2E(t, out); got != "---\nname: lore\n---\nmini body\n" {
		t.Fatalf("written output must have the preamble stripped: %q", got)
	}
	if args := readFileE2E(t, argsFile); !strings.Contains(args, "--model claude-test") {
		t.Fatalf("model not passed to the provider: %q", args)
	}
	var env2 struct {
		Data struct {
			OutputBytes int     `json:"output_bytes"`
			SourceBytes int     `json:"source_bytes"`
			Ratio       float64 `json:"compression_x"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &env2); err != nil || env2.Data.SourceBytes != 1600 || env2.Data.OutputBytes != len("---\nname: lore\n---\nmini body\n") || env2.Data.Ratio <= 1 {
		t.Fatalf("json: %+v %v", env2.Data, err)
	}

	// --dry-run prints and writes nothing; the default output is a DRAFT path.
	r = w.run(a.dir, env, e2eBin, "skill", "compile", "--source-dir="+src, "--dry-run")
	if r.code != 0 || !strings.Contains(r.stdout, "mini body") {
		t.Fatalf("dry run: %s %s", r.stdout, r.stderr)
	}
	if _, err := os.Stat(filepath.Join(src, "SKILL-mini-draft.md")); !os.IsNotExist(err) {
		t.Fatal("--dry-run wrote a file")
	}
	r = w.run(a.dir, env, e2eBin, "skill", "compile", "--source-dir="+src)
	if r.code != 0 || readFileE2E(t, filepath.Join(src, "SKILL-mini-draft.md")) != "---\nname: lore\n---\nmini body\n" {
		t.Fatalf("default output must be the draft path: %s", r.stderr)
	}

	for name, args := range map[string][]string{
		"unsupported target": {"skill", "compile", "--source-dir=" + src, "--target=full"},
		"no sources":         {"skill", "compile", "--source-dir=" + t.TempDir()},
	} {
		if r := w.run(a.dir, env, e2eBin, args...); r.code == 0 {
			t.Fatalf("%s: must fail", name)
		}
	}
	failing := fakeClaude(t, `cat >/dev/null; exit 1`)
	if r := w.run(a.dir, failing, e2eBin, "skill", "compile", "--source-dir="+src, "--output="+out); r.code == 0 {
		t.Fatal("a failing model must fail the compile")
	}
}

// runTUI builds the whole TUI (project scope, every generated screen, the
// tables screen) and then opens /dev/tty. Without a controlling terminal
// that open fails, so the run returns an error instead of taking over the
// screen; everything before it is exercised. With a terminal attached the
// TUI would open for real, so the test only runs headless.
func TestUC_BenchRunTUIHeadless(t *testing.T) {
	if tty, err := os.Open("/dev/tty"); err == nil {
		_ = tty.Close()
		t.Skip("a controlling terminal is attached; `lore tui` would open for real")
	}
	w := newWorld(t)
	a := w.newClone("alice")
	a.lore("init", "--non-interactive", "--name=app")
	r := a.loreAny("tui", "--kind=task", "--view=list")
	if r.code == 0 || !strings.Contains(r.stderr, "tty") {
		t.Fatalf("headless tui must fail on the terminal, after building every screen: %d %s", r.code, r.stderr)
	}
	if strings.Contains(r.stderr, "panic") {
		t.Fatalf("building the screens panicked: %s", r.stderr)
	}
	// Outside a project it fails before touching the terminal.
	r = w.run(t.TempDir(), nil, e2eBin, "tui")
	if r.code == 0 || strings.Contains(r.stderr, "tty") {
		t.Fatalf("outside a project: %d %s", r.code, r.stderr)
	}
}
