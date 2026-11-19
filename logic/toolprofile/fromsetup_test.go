package toolprofile

import (
	"strings"
	"testing"
)

// TestFromSetup: a repo with no context.targets row and a guided setup.
// D-711: a pointer goes only to a tool the repo uses or one picked for
// it. In a repo that uses no tool yet the setup's tools are the pick
// (A-9); in a repo that already uses tools, those stay the targets, a
// setup tool whose file is in the repo joins them, and every other setup
// tool is Skipped (named with the command that adds it), never written.
func TestFromSetup(t *testing.T) {
	reg, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	all := []string{"claude_code", "codex", "cursor", "gemini_cli", "copilot", "nosuch"}

	// the owner's shape: only CLAUDE.md (wired, as after init or a link)
	wired := mapProbe{"CLAUDE.md": FileWired}
	sel := reg.FromSetup(all, reg.Resolve(wired), wired)
	if got, sk := names(sel.Profiles), names(sel.Skipped); got != "claude_code" || sk != "codex,cursor,gemini_cli,copilot" || sel.Reason != ReasonWired {
		t.Errorf("CLAUDE.md wired: profiles %s skipped %s reason %s (want claude_code only, the rest skipped)", got, sk, sel.Reason)
	}

	// same with CLAUDE.md not wired yet (a repo before its first render)
	present := mapProbe{"CLAUDE.md": FilePresent}
	sel = reg.FromSetup(all, reg.Resolve(present), present)
	if got, sk := names(sel.Profiles), names(sel.Skipped); got != "claude_code" || sk != "codex,cursor,gemini_cli,copilot" || sel.Reason != ReasonDetected {
		t.Errorf("CLAUDE.md present: profiles %s skipped %s reason %s", got, sk, sel.Reason)
	}

	// a detected tool the setup does not list is still the repo's tool
	sel = reg.FromSetup([]string{"codex"}, reg.Resolve(present), present)
	if got, sk := names(sel.Profiles), names(sel.Skipped); got != "claude_code" || sk != "codex" || len(sel.Unwired) != 0 {
		t.Errorf("setup codex, repo CLAUDE.md: profiles %s skipped %s unwired %s", got, sk, names(sel.Unwired))
	}

	// a setup tool whose file the repo has (hand-written AGENTS.md next to
	// a wired CLAUDE.md) is used and picked: wired; a hand-written file of
	// a tool the setup does not list stays only named
	mixed := mapProbe{"CLAUDE.md": FileWired, "AGENTS.md": FilePresent, "GEMINI.md": FilePresent}
	sel = reg.FromSetup([]string{"codex", "cursor"}, reg.Resolve(mixed), mixed)
	if got, sk, un := names(sel.Profiles), names(sel.Skipped), names(sel.Unwired); got != "claude_code,codex" || sk != "cursor" || un != "gemini_cli" || sel.Reason != ReasonSetup {
		t.Errorf("mixed: profiles %s skipped %s unwired %s reason %s", got, sk, un, sel.Reason)
	}

	// a repo that uses no tool: the setup is the pick (A-9), nothing skipped
	fresh := mapProbe{}
	sel = reg.FromSetup([]string{"codex", "cursor"}, reg.Resolve(fresh), fresh)
	if got := names(sel.Profiles); got != "codex,cursor" || len(sel.Skipped) != 0 || sel.Reason != ReasonSetup {
		t.Errorf("fresh repo: profiles %s skipped %s reason %s", got, names(sel.Skipped), sel.Reason)
	}

	// a setup with no known tool changes nothing
	sel = reg.FromSetup([]string{"nosuch"}, reg.Resolve(present), present)
	if got := names(sel.Profiles); got != "claude_code" || sel.Reason != ReasonDetected || len(sel.Skipped) != 0 {
		t.Errorf("unknown setup tool: profiles %s reason %s", got, sel.Reason)
	}
}

// TestRepoTargets: the one choice render, init and directive install
// share. A context.targets row wins over the setup (no Skipped: the row
// is the repo's own pick); with no row the setup applies by FromSetup.
func TestRepoTargets(t *testing.T) {
	reg, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	probe := mapProbe{"CLAUDE.md": FileWired}
	setup := []string{"claude_code", "codex", "cursor"}
	sel, err := reg.RepoTargets(RepoTargetsIn{Row: []string{"claude_code", "cursor"}, RowSet: true, Setup: setup, Probe: probe})
	if err != nil || names(sel.Profiles) != "claude_code,cursor" || sel.Reason != ReasonSetting || len(sel.Skipped) != 0 {
		t.Fatalf("row: %s %s skipped %s err %v", names(sel.Profiles), sel.Reason, names(sel.Skipped), err)
	}
	sel, err = reg.RepoTargets(RepoTargetsIn{Setup: setup, Probe: probe})
	if err != nil || names(sel.Profiles) != "claude_code" || names(sel.Skipped) != "codex,cursor" {
		t.Fatalf("no row: %s skipped %s err %v", names(sel.Profiles), names(sel.Skipped), err)
	}
	if _, err = reg.RepoTargets(RepoTargetsIn{Row: []string{"nosuch"}, RowSet: true, Probe: probe}); err == nil {
		t.Fatal("an unknown profile in the row must be refused")
	}
	codex, _ := reg.Lookup("codex")
	if n := codex.SkippedNote("lore render --add-target"); !strings.Contains(n, "no AGENTS.md was written") || !strings.HasSuffix(n, "lore render --add-target codex") {
		t.Errorf("note = %q", n)
	}
}
