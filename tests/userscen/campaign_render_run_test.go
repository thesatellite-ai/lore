//go:build userscen

package userscen

// campaign_render_run_test.go — `lore render` (render.run, CONTEXT_BUDGET
// 6.1-6.11) beyond the render_*_test.go scenarios:
//   - --json of every mode: {"output": …} for a run and --dry-run, the
//     report / trend object for --report / --trend, the E_REFUSED envelope
//     for --check over the warn level;
//   - one read mode at a time: --check with --report (a CI gate that must
//     not pass by printing a report), --trend with --dry-run, … refused;
//     every read mode leaves the whole work tree as it was;
//   - a pointer target that cannot be a file (a folder, a name too long
//     for the file system) is the person's input, never E_INTERNAL;
//   - target names with spaces and unicode; REST answers as the CLI;
//   - two renders at once end with the same files as one render.

import (
	"encoding/json"
	"strings"
	"testing"
)

// campRenderOut is render.run's --json for a run or --dry-run.
type campRenderOut struct {
	Output string `json:"output"`
}

// campReport is the --report --json object (the fields a CI script reads).
type campReport struct {
	File           string `json:"file"`
	Profile        string `json:"profile"`
	Budget         int    `json:"budget"`
	WarnAtPercent  int    `json:"warn_at_percent"`
	After          int    `json:"after"`
	BudgetExceeded bool   `json:"budget_exceeded"`
	OverWarnLevel  bool   `json:"over_warn_level"`
}

// campTrend is the --trend --json object.
type campTrend struct {
	Target  string `json:"target"`
	Budget  int    `json:"budget"`
	Profile string `json:"profile"`
	Renders []struct {
		At    string `json:"at"`
		Bytes int    `json:"bytes"`
	} `json:"renders"`
}

func TestCampaignRender_RunJSONShapes(t *testing.T) {
	c := renderSolo(t)
	c.MustLore("rule", "add", "--body", "money is stored in minor units", "--severity", "must")
	run := campJSON[campRenderOut](t, c.Lore("render", "--json"))
	if !strings.Contains(run.Output, "✓ .lore/LORE.md (") {
		t.Errorf("render --json: {output} carries the lines, got %+v", run)
	}
	md := renderRead(t, c, renderLoreMD)
	before := campTree(t, c.Dir)
	dry := campJSON[campRenderOut](t, c.Lore("render", "--dry-run", "--json"))
	// The body comes first; the private render follows under its own
	// "==> <path>" header when the person has a personal store.
	if !strings.HasPrefix(dry.Output, md) {
		i := 0
		for i < min(len(dry.Output), len(md)) && dry.Output[i] == md[i] {
			i++
		}
		t.Errorf("render --dry-run --json: {output} is the body render writes; first difference at byte %d:\n%q\nwant\n%q", i, dry.Output[max(0, i-80):min(len(dry.Output), i+120)], md[max(0, i-80):min(len(md), i+120)])
	}
	// --report --json is enveloped in "data" (v0.1.17's report object);
	// --trend --json is the trend object itself.
	rep := campJSON[struct {
		Data campReport `json:"data"`
	}](t, c.Lore("render", "--report", "--json")).Data
	if rep.File != renderLoreMD || rep.Profile != "codex" || rep.Budget != 16000 || rep.After != len(md) || rep.WarnAtPercent != 80 || rep.BudgetExceeded || rep.OverWarnLevel {
		t.Errorf("render --report --json: the report object, got %+v (LORE.md is %d bytes)", rep, len(md))
	}
	tr := campJSON[campTrend](t, c.Lore("render", "--trend", "--json"))
	if tr.Target != renderLoreMD || tr.Budget != 16000 || len(tr.Renders) == 0 || tr.Renders[len(tr.Renders)-1].Bytes != len(md) {
		t.Errorf("render --trend --json: the trend object with the last render, got %+v", tr)
	}
	c.MustLore("render", "--check").Says(t, "✓ .lore/LORE.md would be")
	campSameTree(t, "the read modes", before, campTree(t, c.Dir))
	// Over the warn level --check refuses, text and JSON alike.
	c.MustLore("policy", "set", "context.warn_at", "1", "--yes")
	before = campTree(t, c.Dir)
	campRefusal(t, c, campExitRefused, "E_REFUSED", "lore render --report", "render", "--check")
	campSameTree(t, "a refused --check", before, campTree(t, c.Dir))
}

func TestCampaignRender_RunOneReadModeAtATime(t *testing.T) {
	c := renderSolo(t)
	c.MustLore("render")
	// warn_at 1: a --check that ran would refuse, so a combination that
	// exits 0 would be a CI gate that never fails.
	c.MustLore("policy", "set", "context.warn_at", "1", "--yes")
	before := campTree(t, c.Dir)
	for _, combo := range [][]string{
		{"--check", "--report"}, {"--check", "--trend"}, {"--check", "--dry-run"},
		{"--report", "--trend"}, {"--report", "--dry-run"}, {"--trend", "--dry-run"},
		{"--check", "--report", "--trend", "--dry-run"},
	} {
		t.Run(strings.Join(combo, "_"), func(t *testing.T) {
			campRefusal(t, c, campExitBadInput, "E_INVALID_INPUT", "lore render --check", append([]string{"render"}, combo...)...)
		})
	}
	// --scopes refines --report: still one mode.
	c.MustLore("render", "--report", "--scopes")
	campSameTree(t, "refused combinations", before, campTree(t, c.Dir))
}

func TestCampaignRender_RunTargetsThatCannotBeFiles(t *testing.T) {
	c := renderSolo(t)
	c.WriteFile("docs/README.md", "docs\n")
	before := campTree(t, c.Dir)
	for _, tc := range []struct{ name, target, reason string }{
		{"a folder", "./docs/", "is a directory"},
		{"a name too long", "./" + strings.Repeat("y", 300) + ".md", "file name too long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			campRefusal(t, c, campExitBadInput, "E_INVALID_INPUT", "--target", "render", "--target", tc.target)
			c.Lore("render", "--target", tc.target).Says(t, tc.reason).Never(t, "E_INTERNAL")
		})
	}
	// The refused renders write no pointer file and touch no tool file.
	after := campTree(t, c.Dir)
	for _, f := range []string{"AGENTS.md", "docs/README.md"} {
		if before[f] != after[f] {
			t.Errorf("a refused --target leaves %s as it was", f)
		}
	}
	// Names with spaces and unicode are files like any other.
	for _, f := range []string{"./notes for ai.md", "./ünïcode-指示.md"} {
		c.MustLore("render", "--target", f).Says(t, "(pointer → .lore/LORE.md)")
		if got := renderRead(t, c, strings.TrimPrefix(f, "./")); got != renderPtrAt {
			t.Errorf("%s holds lore's pointer: %q", f, got)
		}
		c.MustLore("render", "--target", f).Says(t, "(pointer present)")
	}
}

func TestCampaignRender_RunOverREST(t *testing.T) {
	c := renderSolo(t)
	c.MustLore("rule", "add", "--body", "money is stored in minor units", "--severity", "must")
	c.MustLore("render")
	srv := renderStartAPI(t, c)
	cliDry := campJSON[campRenderOut](t, c.Lore("render", "--dry-run", "--json"))
	r := srv.post(t, "/v1/render/run", []byte(`{"dry_run":true}`))
	var restDry campRenderOut
	if r.status != 200 || json.Unmarshal(r.body, &restDry) != nil || restDry != cliDry {
		t.Errorf("REST dry_run answers the CLI's body: %d %.300s", r.status, r.body)
	}
	run := srv.post(t, "/v1/render/run", []byte(`{}`))
	var restRun campRenderOut
	if run.status != 200 || json.Unmarshal(run.body, &restRun) != nil || !strings.Contains(restRun.Output, "· .lore/LORE.md (unchanged)") {
		t.Errorf("REST run: the lines of an unchanged render, got %d %s", run.status, run.body)
	}
	for _, body := range []string{`{"check":true,"report":true}`, `{"trend":true,"dry_run":true}`, `{"target":["nope"]}`} {
		rr := srv.post(t, "/v1/render/run", []byte(body))
		if rr.status != 400 || rr.code != "E_INVALID_INPUT" {
			t.Errorf("REST %s: 400 E_INVALID_INPUT as the CLI, got %d %s", body, rr.status, rr.body)
		}
	}
}

func TestCampaignRender_RunTwoAtOnce(t *testing.T) {
	w := newWorld(t)
	p := w.Human("person", "person@example.com")
	p.WriteConfig(renderSoloConfig())
	c := p.Clone(w.Remote(renderSlugOwn), "tool")
	c.MustLore("init", "--name=tool")
	for i := range 5 {
		c.MustLore("rule", "add", "--body", "rule number "+string(rune('a'+i)), "--severity", "must")
	}
	res := w.Simultaneously(c.Prepare("render"), c.Prepare("render"))
	for _, r := range res {
		r.OK(t)
	}
	got := renderSnapshot(c, renderLoreMD, renderFullMD, "AGENTS.md")
	c.MustLore("render").Says(t, "· .lore/LORE.md (unchanged)")
	renderSameFiles(t, "a render after two at once", got, renderSnapshot(c, renderLoreMD, renderFullMD, "AGENTS.md"))
}
