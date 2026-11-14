package repoguard

// Windows-only code (store paths like C:\x, file:///C:/x) was once built and
// vetted by hand only, never by the gate, and one end-to-end suite did not
// even compile on Windows (bug hunt W-1). The full gate's cross-tests step
// type-checks every package with its tests for Windows and macOS; this pins
// that the step stays in the gate and keeps both targets and the tagged
// suites.

import (
	"os"
	"strings"
	"testing"
)

// TestGateCompilesTestsForWindowsAndMacOS fails when the cross-tests step
// leaves GATE_STEPS, drops a target, or stops vetting the module's tests.
func TestGateCompilesTestsForWindowsAndMacOS(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(loreTaskfile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	steps := taskfileBlock(text, "GATE_STEPS")
	step, ok := steps["cross-tests"]
	if !ok || len(step) < 2 || step[1] != "build:cross:tests" {
		t.Fatalf("GATE_STEPS has no `cross-tests <slots> build:cross:tests` line (got %v)", step)
	}
	body := taskBody(text, "build:cross:tests")
	for _, target := range []string{"GOOS: windows", "GOOS: darwin"} {
		if !strings.Contains(body, "{task: build:cross:tests:one, vars: {"+target) {
			t.Errorf("build:cross:tests no longer checks %s", target)
		}
	}
	one := taskBody(text, "build:cross:tests:one")
	for _, want := range []string{"go vet ./...", "teame2e", "setupe2e", "machines", "journeys", "userscen"} {
		if !strings.Contains(one, want) {
			t.Errorf("build:cross:tests:one no longer runs %q", want)
		}
	}
}

// taskBody is the text of one task of Taskfile.lore.yml, from its "  name:"
// line to the next task at the same indent.
func taskBody(text, name string) string {
	start := strings.Index(text, "\n  "+name+":\n")
	if start < 0 {
		return ""
	}
	rest := text[start+1:]
	lines := strings.Split(rest, "\n")
	var b strings.Builder
	for i, line := range lines {
		if i > 0 && strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(strings.TrimSpace(line), ":") {
			break
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
