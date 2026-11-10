package wire_test

// setuptools_regress_test.go — five picked tools read as one group per
// tool, and a tool that reads lore through a pointer file adds no warning.

import (
	"slices"
	"strings"
	"testing"
)

// Finding d01a1fab→891b57d9/e9d78239 (realmac): picking five coding tools
// filled the Tools step with a wall of warnings — rows that merged every
// tool into one line ("claude_code: …; codex: …; cursor: …"), and Cursor,
// Gemini CLI and Copilot (which read lore through a pointer file and have
// no skill folder) warned "skill not installed" under every skill check.
// Each row must belong to exactly one picked tool, and those three tools
// may add no warning at all; the skill tools warn once (not installed).
func TestRegress_FiveToolsNoWallOfWarnings(t *testing.T) {
	w := newRegressWorld(t, rwShape{GHAccounts: 1, SSH: "default_only"})
	a := rwAllAccounts[0]
	w.WriteConfig(rwPersonalConfig(a.Login, a.Email))
	tools := []string{"claude_code", "codex", "cursor", "gemini_cli", "copilot"}
	pointerOnly := []string{"cursor", "gemini_cli", "copilot"}
	out := w.Check("personal", "tools", rwAnswers{"tools": list(tools...)})
	if len(out.FieldErrors) > 0 {
		t.Fatalf("field errors for five known tools: %+v", out.FieldErrors)
	}
	warns := map[string]int{}
	seen := map[string]bool{}
	for _, c := range out.Checks {
		if !slices.Contains(tools, c.Target) {
			t.Errorf("row %s is not one tool's (target %q): %s", c.Check, c.Target, c.Detail)
			continue
		}
		seen[c.Target] = true
		if c.Status != "warn" && c.Status != "fail" {
			continue
		}
		warns[c.Target]++
		if slices.Contains(pointerOnly, c.Target) {
			t.Errorf("%s (pointer file, no skill) warns under %s: %s", c.Target, c.Check, c.Detail)
		}
	}
	for _, tool := range tools {
		if !seen[tool] {
			t.Errorf("no row for %s", tool)
		}
		if warns[tool] > 1 {
			t.Errorf("%s warns %d times; only its not-installed skill is worth a warning", tool, warns[tool])
		}
	}
	if t.Failed() {
		var rows []string
		for _, c := range out.Checks {
			rows = append(rows, c.Check+" "+c.Target+" "+c.Status+": "+c.Detail)
		}
		t.Log(strings.Join(rows, "\n"))
	}
}
