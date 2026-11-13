package repoguard

// The fast tier (`task check:fast`) is a quick sanity check that fits in
// about 5 minutes on the shared server; the full gate after landing is the
// real gate. With the end-to-end suites and the docs steps in it, it took
// 22 minutes (1340 s, 2026-11-13). This pins its step list: only the
// build-and-unit steps, whatever FAST_MAP adds for a changed path, and
// every one of them still a step of the full gate.

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// fastTierSteps are the only steps check:fast may run.
var fastTierSteps = []string{"test-all", "race-changed", "cross-builds", "staticcheck", "vet", "gofmt", "tidy"}

// taskfileBlock is the first word of every non-comment line of the
// "NAME: |" block of Taskfile.lore.yml's vars, mapped to the rest.
func taskfileBlock(text, name string) map[string][]string {
	out := map[string][]string{}
	in := false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.TrimSpace(line) == name+": |":
			in = true
		case in && strings.HasPrefix(line, "    ") && !strings.HasPrefix(strings.TrimSpace(line), "#"):
			f := strings.Fields(line)
			out[f[0]] = f[1:]
		case in:
			in = false
		}
	}
	return out
}

func TestFastTierRunsNoSuite(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(loreTaskfile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	var unit []string
	for _, line := range strings.Split(text, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "FAST_UNIT_STEPS: "); ok {
			unit = strings.Fields(strings.Trim(v, `"`))
		}
	}
	if len(unit) == 0 {
		t.Fatal("no FAST_UNIT_STEPS line in Taskfile.lore.yml")
	}
	fastMap := taskfileBlock(text, "FAST_MAP")
	if len(fastMap) == 0 {
		t.Fatal("no FAST_MAP block in Taskfile.lore.yml")
	}
	steps := slices.Clone(unit)
	for _, named := range fastMap {
		for _, s := range named {
			if s != "-" {
				steps = append(steps, s)
			}
		}
	}
	for s := range taskfileBlock(text, "FAST_EXTRA_STEPS") {
		steps = append(steps, s)
	}
	gate := taskfileBlock(text, "GATE_STEPS")
	for _, s := range steps {
		if !slices.Contains(fastTierSteps, s) {
			t.Errorf("check:fast runs %s: the fast tier runs only %v (end-to-end and docs steps run in the full gate after landing)", s, fastTierSteps)
		}
		if _, ok := gate[s]; !ok && s != "race-changed" {
			t.Errorf("fast step %s is no step of the full gate (GATE_STEPS)", s)
		}
	}
	for _, s := range []string{"test-all", "vet", "gofmt", "staticcheck", "tidy", "cross-builds"} {
		if !slices.Contains(unit, s) {
			t.Errorf("FAST_UNIT_STEPS lost %s", s)
		}
	}
}
