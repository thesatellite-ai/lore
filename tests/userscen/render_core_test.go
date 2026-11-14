//go:build userscen

package userscen

// render_core_test.go — `lore render` as a person runs it in their own repo
// (CLI_REFERENCE 2.14, CONTEXT_BUDGET 6.1-6.11):
//   - an empty store renders a usable file and wires AGENTS.md (never
//     CLAUDE.md by default, TOOL NEUTRAL);
//   - every knowledge kind lands in its tier: critical hotfixes and must
//     rules in full, high hotfixes one line with their fetch command,
//     should/may rules, medium/low hotfixes, memories, decisions, patterns
//     search-only (counted in the index); LORE.full.md has every pinned entry;
//   - render twice is byte-identical and says "unchanged";
//   - a row edit, delete, restore and archive each show in the next render;
//   - the read-only modes (--dry-run, --check, --report, --trend) write
//     nothing; --no-pointer and --out; --scopes without --report refused;
//   - a very large rule set stays inside the budget (line forms) and
//     `--check` warns over the warn level and refuses over the budget with E_REFUSED and its hint;
//   - unicode bodies survive byte for byte and the budget counts bytes.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// renderCanaryLine is the first line of every LORE.md (content-derived
// canary, schema v2).
const renderCanaryPrefix = "<!-- lore render stamp rnd_"

func TestRenderEmptyStoreWiresAgentsMD(t *testing.T) {
	c := renderSolo(t)
	r := c.MustLore("render")
	r.Says(t, "✓ .lore/LORE.md (", "budget 16000 codex", "0 full + 0 line rules, 0 + 0 hotfixes, 0 topics", "✓ .lore/LORE.full.md")

	md := renderRead(t, c, renderLoreMD)
	if !strings.HasPrefix(md, renderCanaryPrefix) {
		t.Fatalf("LORE.md starts with the canary line: %q", md[:min(len(md), 80)])
	}
	for _, want := range []string{"# Project knowledge (summary)", "<!-- lore:directive:start -->", "## Not in this file"} {
		if !strings.Contains(md, want) {
			t.Errorf("an empty LORE.md still carries %q", want)
		}
	}
	for _, never := range []string{"## Critical hotfixes", "## Must rules", "## High hotfixes", "<rule ", "<hotfix "} {
		if strings.Contains(md, never) {
			t.Errorf("an empty store renders no %q section: %s", never, md)
		}
	}
	// The empty-state hint is its own paragraph, not glued onto the last
	// bullet of "Not in this file" (markdown would read it as that
	// bullet's continuation).
	hint := "_No pinned knowledge yet. Run `lore rule add` or `lore memory add` to add some._"
	if !strings.Contains(md, "\n\n"+hint+"\n") {
		t.Errorf("the empty-state hint is a paragraph of its own: %q", md[max(0, len(md)-300):])
	}
	full := renderRead(t, c, renderFullMD)
	if !strings.Contains(full, "# Project knowledge (full)") || strings.Contains(full, "<rule ") {
		t.Errorf("LORE.full.md of an empty store has its header and no entry: %q", full)
	}
	if got := renderRead(t, c, "AGENTS.md"); got != renderPtrRead {
		t.Errorf("AGENTS.md is exactly lore's read pointer:\n%q", got)
	}
	if _, ok := c.ReadFile("CLAUDE.md"); ok {
		t.Error("CLAUDE.md is never created by default (TOOL NEUTRAL)")
	}
}

// renderKinds adds one row of every kind and severity; it returns the ids
// by name.
func renderKinds(t *testing.T, c *th.Clone) map[string]string {
	t.Helper()
	return map[string]string{
		"must":     renderID(t, c.MustLore("rule", "add", "--body", "money is stored in minor units", "--severity", "must"), "rul"),
		"should":   renderID(t, c.MustLore("rule", "add", "--body", "prefer table tests", "--severity", "should"), "rul"),
		"may":      renderID(t, c.MustLore("rule", "add", "--body", "you may inline tiny helpers", "--severity", "may"), "rul"),
		"critical": renderID(t, c.MustLore("hotfix", "add", "--title", "Login 500", "--body", "clear the session cache", "--severity", "critical"), "hfx"),
		"high":     renderID(t, c.MustLore("hotfix", "add", "--title", "Flaky CI", "--body", "rerun the job once", "--severity", "high"), "hfx"),
		"medium":   renderID(t, c.MustLore("hotfix", "add", "--title", "Slow lint", "--body", "lint changed files", "--severity", "medium"), "hfx"),
		"low":      renderID(t, c.MustLore("hotfix", "add", "--title", "Noisy log", "--body", "ignore the gc line", "--severity", "low"), "hfx"),
		"memory":   renderID(t, c.MustLore("memory", "add", "--body", "the parser lives in internal/parse"), "mem"),
		"decision": renderID(t, c.MustLore("decision", "add", "--title", "Postgres over MySQL", "--body", "we need jsonb"), "dec"),
		"pattern":  renderID(t, c.MustLore("pattern", "add", "--title", "Retry wrapper", "--body", "wrap calls in retry.Do"), "pat"),
	}
}

func TestRenderEveryKindLandsInItsTier(t *testing.T) {
	c := renderSolo(t)
	ids := renderKinds(t, c)
	c.MustLore("render").Says(t, "1 full + 0 line rules, 1 + 1 hotfixes")
	md := renderRead(t, c, renderLoreMD)

	crit := renderSection(md, "## Critical hotfixes")
	if !strings.Contains(crit, `<hotfix id="`+ids["critical"]+`" severity="critical">`) || !strings.Contains(crit, "clear the session cache") {
		t.Errorf("the critical hotfix is in full: %q", crit)
	}
	must := renderSection(md, "## Must rules")
	if !strings.Contains(must, `<rule id="`+ids["must"]+`" severity="must"`) || !strings.Contains(must, "money is stored in minor units") {
		t.Errorf("the must rule is in full: %q", must)
	}
	high := renderSection(md, "## High hotfixes")
	// G-6: one line, no body; the fetch command is said once, in the
	// footer, for every one-line entry (not repeated per line)
	if !strings.Contains(high, "`"+ids["high"]+"` Flaky CI") || strings.Contains(high, "rerun the job once") {
		t.Errorf("the high hotfix is one line, no body: %q", high)
	}
	if foot := renderSection(md, "## Not in this file"); !strings.Contains(foot, "1 pinned entry is shown here only as one line: `lore show --id <id>` prints the full text") {
		t.Errorf("the footer names the fetch command for the one-line entry: %q", foot)
	}
	// Search-only kinds: neither id nor text in the summary.
	for _, k := range []string{"should", "may", "medium", "low", "memory", "decision", "pattern"} {
		if strings.Contains(md, ids[k]) {
			t.Errorf("%s (%s) is search-only, not in LORE.md", k, ids[k])
		}
	}
	for _, text := range []string{"prefer table tests", "inline tiny helpers", "lint changed files", "ignore the gc line", "internal/parse", "we need jsonb", "retry.Do"} {
		if strings.Contains(md, text) {
			t.Errorf("search-only text %q is not in LORE.md", text)
		}
	}
	idx := renderSection(md, "## Index: fetch on demand")
	if !strings.Contains(idx, "| (no topic) | 3 rules, 4 hotfixes, 1 memory, 1 decision") {
		t.Errorf("the index counts every rule, hotfix, memory and decision: %q", idx)
	}
	// LORE.full.md: every pinned entry in full, nothing search-only.
	full := renderRead(t, c, renderFullMD)
	for _, k := range []string{"must", "critical", "high"} {
		if !strings.Contains(full, `id="`+ids[k]+`"`) {
			t.Errorf("LORE.full.md holds the pinned %s entry in full", k)
		}
	}
	if !strings.Contains(full, "rerun the job once") {
		t.Error("LORE.full.md has the high hotfix's body")
	}
	for _, k := range []string{"should", "may", "medium", "low", "memory", "decision", "pattern"} {
		if strings.Contains(full, ids[k]) {
			t.Errorf("LORE.full.md holds pinned entries only, not %s", k)
		}
	}
}

func TestRenderTwiceIsByteIdentical(t *testing.T) {
	c := renderSolo(t)
	renderKinds(t, c)
	c.MustLore("render", "--target", "claude_code,codex,cursor,gemini_cli,copilot")
	files := []string{renderLoreMD, renderFullMD, "CLAUDE.md", "AGENTS.md", ".cursor/rules/lore.mdc", "GEMINI.md", ".github/copilot-instructions.md"}
	first := renderSnapshot(c, files...)
	r := c.MustLore("render", "--target", "claude_code,codex,cursor,gemini_cli,copilot")
	r.Says(t, "· .lore/LORE.md (unchanged)", "CLAUDE.md (pointer present)", "AGENTS.md (pointer present)")
	renderSameFiles(t, "a second render", first, renderSnapshot(c, files...))
	// A third run, after a commit, still changes nothing in the work tree.
	c.CommitAll("render")
	c.MustLore("render", "--target", "claude_code,codex,cursor,gemini_cli,copilot")
	if st := c.Status(); st != "" {
		t.Fatalf("a re-render of the same rows leaves nothing to commit: %q", st)
	}
}

func TestRenderFollowsRowEditDeleteRestoreArchive(t *testing.T) {
	c := renderSolo(t)
	id := renderID(t, c.MustLore("rule", "add", "--body", "use the v1 client", "--severity", "must"), "rul")
	c.MustLore("render")
	if md := renderRead(t, c, renderLoreMD); !strings.Contains(md, "use the v1 client") {
		t.Fatalf("the new rule is rendered: %s", md)
	}

	steps := []struct {
		name      string
		args      []string
		has, nope string
	}{
		{"edit body", []string{"rule", "edit", id, "--body", "use the v2 client"}, "use the v2 client", "use the v1 client"},
		{"demote to should", []string{"rule", "edit", id, "--severity", "should"}, "", id},
		{"back to must", []string{"rule", "edit", id, "--severity", "must"}, id, ""},
		{"delete", []string{"rule", "delete", id, "--yes"}, "", id},
		{"restore", []string{"trash", "restore", id, "--yes"}, "use the v2 client", ""},
		{"archive", []string{"rule", "archive", id}, "", id},
		{"unarchive", []string{"rule", "unarchive", id}, id, ""},
	}
	for _, s := range steps {
		c.MustLore(s.args...)
		c.MustLore("render")
		md := renderRead(t, c, renderLoreMD)
		full := renderRead(t, c, renderFullMD)
		if s.has != "" && !strings.Contains(md, s.has) {
			t.Errorf("after %s, LORE.md has %q:\n%s", s.name, s.has, md)
		}
		if s.nope != "" && (strings.Contains(md, s.nope) || strings.Contains(full, s.nope)) {
			t.Errorf("after %s, neither LORE.md nor LORE.full.md has %q", s.name, s.nope)
		}
	}
}

func TestRenderReadOnlyModesWriteNothing(t *testing.T) {
	c := renderSolo(t)
	renderKinds(t, c)
	files := []string{renderLoreMD, renderFullMD, "AGENTS.md", "CLAUDE.md"}

	// Before any render: the read-only modes create no file.
	for _, args := range [][]string{{"--dry-run"}, {"--check"}, {"--report"}, {"--report", "--scopes"}, {"--trend"}} {
		r := c.MustLore(append([]string{"render"}, args...)...)
		if _, ok := c.ReadFile(renderLoreMD); ok {
			t.Fatalf("render %v writes no LORE.md: %s", args, r)
		}
	}
	dry := c.MustLore("render", "--dry-run")
	if !strings.HasPrefix(dry.Stdout, renderCanaryPrefix) || !strings.Contains(dry.Stdout, "money is stored in minor units") {
		t.Errorf("--dry-run prints the generated body: %s", dry)
	}

	// After a render: each read-only mode leaves every file as it is.
	c.MustLore("render")
	c.WriteFile("CLAUDE.md", "# my own notes\n")
	before := renderSnapshot(c, files...)
	c.MustLore("rule", "add", "--body", "a rule added after the render", "--severity", "must")
	for _, args := range [][]string{{"--dry-run"}, {"--report"}, {"--report", "--scopes"}, {"--trend"}, {"--dry-run", "--target", "claude_code"}} {
		c.MustLore(append([]string{"render"}, args...)...)
		renderSameFiles(t, fmt.Sprintf("render %v", args), before, renderSnapshot(c, files...))
	}
	// --check fails on the stale LORE.md (SOURCE_OF_TRUTH row 10: a rule
	// added since the render) and still writes nothing.
	c.Lore("render", "--check").Fails(t, th.ExitRefused, "[E_REFUSED]", "LORE.md is stale", "lore render")
	renderSameFiles(t, "render --check", before, renderSnapshot(c, files...))
	// The dry run reflects the new rule all the same.
	c.MustLore("render", "--dry-run").Says(t, "a rule added after the render")
}

func TestRenderNoPointerAndOut(t *testing.T) {
	c := renderSolo(t)
	c.MustLore("rule", "add", "--body", "the out rule", "--severity", "must")
	c.Git("rm", "-q", "--cached", "--ignore-unmatch", "AGENTS.md")
	c.WriteFile("AGENTS.md", "# hand only\n")
	c.MustLore("render", "--no-pointer")
	if got := renderRead(t, c, "AGENTS.md"); got != "# hand only\n" {
		t.Errorf("--no-pointer leaves the pointer file as it is: %q", got)
	}
	if md := renderRead(t, c, renderLoreMD); !strings.Contains(md, "the out rule") {
		t.Errorf("--no-pointer still writes LORE.md: %s", md)
	}

	c.MustLore("render", "--out", "docs/AI.md", "--target", "claude_code")
	if md := renderRead(t, c, "docs/AI.md"); !strings.Contains(md, "the out rule") {
		t.Errorf("--out writes the knowledge file there: %q", md)
	}
	if got := renderRead(t, c, "CLAUDE.md"); !strings.Contains(got, "@docs/AI.md") {
		t.Errorf("the pointer follows --out: %q", got)
	}
}

func TestRenderFlagRefusals(t *testing.T) {
	c := renderSolo(t)
	r := c.Lore("render", "--scopes")
	r.Fails(t, 20, "[E_INVALID_INPUT] --scopes needs --report", "lore render --report --scopes")
	j := c.Lore("render", "--scopes", "--json")
	var env struct {
		Error struct {
			Code string `json:"code"`
			Exit int    `json:"exit"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(j.Stdout), &env); err != nil || env.Error.Code != "E_INVALID_INPUT" || env.Error.Exit != 20 {
		t.Errorf("--json refusal is {error:{code,exit}}: %v %s", err, j)
	}
	if _, ok := c.ReadFile(renderLoreMD); ok {
		t.Error("a refused render writes nothing")
	}
}

// renderLongBody is a must rule body of about n bytes.
func renderLongBody(i, n int) string {
	head := fmt.Sprintf("must rule %03d: ", i)
	return head + strings.Repeat("keep the invariant ", (n-len(head))/19+1)[:n-len(head)]
}

func TestRenderLargeRuleSetStaysInBudget(t *testing.T) {
	c := renderSolo(t)
	const rules, size = 30, 700 // 21000 bytes of must rules: over codex's 16000
	ids := make([]string, 0, rules)
	for i := range rules {
		ids = append(ids, renderID(t, c.MustLore("rule", "add", "--body", renderLongBody(i, size), "--severity", "must"), "rul"))
	}
	r := c.MustLore("render")
	r.Says(t, "budget 16000 codex")
	md := renderRead(t, c, renderLoreMD)
	if len(md) > 16000 {
		t.Fatalf("LORE.md is %d bytes, over its 16000 budget", len(md))
	}
	// Every must rule is still reachable: in full, or as a line with its id.
	for _, id := range ids {
		if !strings.Contains(md, id) {
			t.Errorf("must rule %s is never lost from LORE.md (full or line)", id)
		}
	}
	full := renderRead(t, c, renderFullMD)
	for _, id := range ids {
		if !strings.Contains(full, `<rule id="`+id+`"`) {
			t.Errorf("LORE.full.md has %s in full", id)
		}
	}
	type report struct {
		Data struct {
			Budget   int  `json:"budget"`
			After    int  `json:"after"`
			MustFull int  `json:"must_full"`
			MustLine int  `json:"must_line"`
			Exceeded bool `json:"budget_exceeded"`
			Lines    []struct {
				ID string `json:"id"`
			} `json:"line_forms"`
		} `json:"data"`
	}
	rep := th.DecodeJSON[report](t, c.Lore("render", "--report", "--json"))
	if rep.Data.Budget != 16000 || rep.Data.After != len(md) || rep.Data.Exceeded {
		t.Errorf("report: budget 16000, after = LORE.md's size %d, not exceeded: %+v", len(md), rep.Data)
	}
	if rep.Data.MustFull+rep.Data.MustLine != rules || rep.Data.MustLine == 0 || len(rep.Data.Lines) != rep.Data.MustLine {
		t.Errorf("report: %d must rules split into full and line forms, one line_forms entry per line: %+v", rules, rep.Data)
	}
	// Over the warn level (80%) but within the budget: --check warns and
	// passes (C-8); over the budget it refuses, CI fails.
	if len(md)*100 > 16000*80 {
		c.MustLore("render", "--check").Says(t, "over the warn level (80% of 16000, profile codex)", "lore render --report")
		c.MustLore("policy", "set", "context.budget.codex", "1000", "--yes")
		c.Lore("render", "--check").Fails(t, th.ExitRefused, "[E_REFUSED]", "over the budget (1000, profile codex)", "lore render --report")
		c.MustLore("policy", "set", "context.budget.codex", "16000", "--yes")
	} else {
		t.Fatalf("fixture: %d bytes should be over the 80%% warn level", len(md))
	}
	// The inverse: deleting most rules brings LORE.md back under the warn
	// level, and --check passes.
	for _, id := range ids[8:] {
		c.MustLore("rule", "delete", id, "--yes")
	}
	// The deletes made the rendered LORE.md stale (SOURCE_OF_TRUTH row
	// 10): --check fails for that, not for the budget, until a render.
	c.Lore("render", "--check").Fails(t, th.ExitRefused, "[E_REFUSED]", "LORE.md is stale", "lore render")
	c.MustLore("render")
	c.MustLore("render", "--check")
	if md := renderRead(t, c, renderLoreMD); strings.Contains(md, ids[len(ids)-1]) || !strings.Contains(md, ids[0]) {
		t.Errorf("after the deletes LORE.md keeps the first rules and drops the deleted ones")
	}
}

func TestRenderUnicodeBodiesByteForByte(t *testing.T) {
	c := renderSolo(t)
	bodies := []string{
		"日本語のルール: 金額は最小単位で保存する",
		"emoji 🚀 and accents: café, naïve, Zürich",
		"right-to-left: القاعدة الذهبية",
		"combining: e\u0301 vs \u00e9, zero-width\u200bjoiner",
		"markup-like <rule> & \"quotes\" stay text",
	}
	ids := make([]string, len(bodies))
	for i, b := range bodies {
		ids[i] = renderID(t, c.MustLore("rule", "add", "--body", b, "--severity", "must"), "rul")
	}
	c.MustLore("hotfix", "add", "--title", "Ünïcödé 🔥 title", "--body", "修正: キャッシュを消す", "--severity", "critical")
	c.MustLore("render", "--target", "claude_code")
	md := renderRead(t, c, renderLoreMD)
	// Text is normalised when written (logic/textnorm): NFC, and the
	// zero-width and bidi control characters are dropped.
	want := append([]string(nil), bodies...)
	want[3] = "combining: \u00e9 vs \u00e9, zero-widthjoiner"
	if strings.Contains(md, "\u200b") || strings.Contains(md, "e\u0301") {
		t.Error("LORE.md carries no zero-width character and no decomposed accent")
	}
	for i, b := range want {
		if !strings.Contains(md, b) {
			t.Errorf("rule %s body %q is in LORE.md byte for byte", ids[i], b)
		}
	}
	for _, s := range []string{"**Ünïcödé 🔥 title**", "修正: キャッシュを消す"} {
		if !strings.Contains(md, s) {
			t.Errorf("hotfix text %q is in LORE.md", s)
		}
	}
	// The command's byte count is the file's UTF-8 size (budgets are bytes).
	c.MustLore("render", "--target", "claude_code").Says(t, "· .lore/LORE.md (unchanged)")
	rep := th.DecodeJSON[struct {
		Data struct {
			After int `json:"after"`
		} `json:"data"`
	}](t, c.Lore("render", "--report", "--json", "--target", "claude_code"))
	if rep.Data.After != len(md) {
		t.Errorf("the report's size %d is LORE.md's byte length %d", rep.Data.After, len(md))
	}
}
