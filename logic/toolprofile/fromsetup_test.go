package toolprofile

import "testing"

// TestFromSetup: A-9, a repo with no context.targets row takes the tools
// its guided setup chose, keeps every file lore already wires, and names
// (never wires) a tool file the setup did not choose.
func TestFromSetup(t *testing.T) {
	reg, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	// wired for Claude Code only; GEMINI.md is a hand-written file
	probe := mapProbe{"CLAUDE.md": FileWired, "GEMINI.md": FilePresent}
	sel := reg.FromSetup([]string{"codex", "cursor", "nosuch"}, reg.Resolve(probe), probe)
	if got := names(sel.Profiles); got != "claude_code,codex,cursor" || sel.Reason != ReasonSetup {
		t.Errorf("wired repo: profiles %s reason %s", got, sel.Reason)
	}
	if got := names(sel.Unwired); got != "gemini_cli" {
		t.Errorf("wired repo: unwired %s (want gemini named, not wired)", got)
	}
	// a fresh repo whose only tool file is CLAUDE.md: the setup's tools
	// are wired; CLAUDE.md, detected but not chosen, is named
	fresh := mapProbe{"CLAUDE.md": FilePresent}
	sel = reg.FromSetup([]string{"codex"}, reg.Resolve(fresh), fresh)
	if got, un := names(sel.Profiles), names(sel.Unwired); got != "codex" || un != "claude_code" {
		t.Errorf("fresh repo: profiles %s unwired %s", got, un)
	}
}
