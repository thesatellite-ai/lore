package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/schema"
)

// privBody is a private store body with the given core and one section of
// n bullets of the same length.
func privBody(core string, pinned, n int) privateBody {
	sec := privateSection{heading: "Memories"}
	for i := range n {
		sec.lines = append(sec.lines, fmt.Sprintf("- note %02d `mem_%02d`\n", i, i))
	}
	b := privateBody{core: core, pinned: pinned}
	if n > 0 {
		b.sections = []privateSection{sec}
	}
	return b
}

// TestFitPrivate (CONTEXT_BUDGET 6.1, P3-CTX-2): the private files of one
// repo share context.budget.internal. Cores are never cut; bullets fill
// what is left in file order and stop at the first that does not fit, for
// every later file too; a file that lost bullets ends with one pointer
// line naming the key; an empty store says so; budget 0 keeps everything.
func TestFitPrivate(t *testing.T) {
	line := len("- note 00 `mem_00`\n")
	head := len(privateNotesHead) + len("\n### Memories\n\n")
	cases := []struct {
		name           string
		bodies         []privateBody
		budget         int
		shown, leftOut int
		pointers       []bool // per file: ends with the left-out pointer
	}{
		{name: "no budget keeps all", bodies: []privateBody{privBody("C", 1, 5)}, budget: 0, shown: 6, pointers: []bool{false}},
		{name: "roomy budget keeps all", bodies: []privateBody{privBody("C", 1, 5)}, budget: 10000, shown: 6, pointers: []bool{false}},
		{name: "exact fit keeps all", bodies: []privateBody{privBody("C", 0, 3)}, budget: 1 + head + 3*line, shown: 3, pointers: []bool{false}},
		{name: "one byte short drops the last", bodies: []privateBody{privBody("C", 0, 3)}, budget: head + 3*line, shown: 2, leftOut: 1, pointers: []bool{true}},
		{name: "core over budget: bullets out, core kept", bodies: []privateBody{privBody(strings.Repeat("x", 50), 2, 4)}, budget: 10, shown: 2, leftOut: 4, pointers: []bool{true}},
		{name: "first file fills, second gets none", bodies: []privateBody{privBody("A", 0, 3), privBody("B", 0, 3)}, budget: 2 + head + 3*line, shown: 3, leftOut: 3, pointers: []bool{false, true}},
		{name: "empty store", bodies: []privateBody{privBody("E", 0, 0)}, budget: 100, pointers: []bool{false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := fitPrivate(c.bodies, c.budget)
			if got.shown != c.shown || got.leftOut != c.leftOut {
				t.Fatalf("shown %d left out %d, want %d %d", got.shown, got.leftOut, c.shown, c.leftOut)
			}
			total := 0
			for i, txt := range got.texts {
				if !strings.HasPrefix(txt, c.bodies[i].core) {
					t.Fatalf("file %d lost its core: %q", i, txt)
				}
				ptr := strings.Contains(txt, "more notes left out by context.budget.internal")
				if ptr != c.pointers[i] {
					t.Fatalf("file %d pointer=%v, want %v: %q", i, ptr, c.pointers[i], txt)
				}
				if len(c.bodies[i].sections) == 0 && !strings.HasSuffix(txt, privateEmpty) {
					t.Fatalf("empty store without its line: %q", txt)
				}
				if !ptr {
					total += len(txt)
				}
			}
			if c.budget > 0 && got.coreBytes <= c.budget && total > c.budget {
				t.Fatalf("files without a pointer are %d bytes, over %d", total, c.budget)
			}
		})
	}
}

// TestDirCodexBudget (CONTEXT_BUDGET 6.1, P3-CTX-1): a folder's Codex
// AGENTS.md block is fitted to context.budget.dir.codex, apart from the
// folder LORE.md (context.budget.dir): with a roomy LORE.md budget and a
// tight Codex one, the file keeps every rule in full while the block
// turns some into lines; DirInline targets get the block, DirAt the file.
func TestDirCodexBudget(t *testing.T) {
	g := dirGroup{Path: "src/api"}
	for i := range 6 {
		g.Rules = append(g.Rules, pinnedRule{ID: fmt.Sprintf("rul_%02d", i), Severity: renderRuleSeverityMust,
			Body: strings.Repeat("keep handlers small and typed. ", 20), Summary: fmt.Sprintf("rule %d", i), Activation: schema.RuleActivationAlways})
	}
	plan := dirPlan{Settings: dirSettings{On: true, Budget: 20000, CodexBudget: 2000, MinEntries: 1}, Groups: []dirGroup{g}, Overrides: map[string]string{}}
	bodies, reps := buildDirFiles(plan, fallbackEntryMax)
	file, block := bodies.File[g.Path], bodies.Inline[g.Path]
	if reps[0].MustLine != 0 || reps[0].MustFull != 6 || reps[0].Budget != 20000 {
		t.Fatalf("folder LORE.md report %+v, want 6 full under 20000", reps[0])
	}
	ir := bodies.InlineReps[g.Path]
	if ir.MustLine == 0 || ir.Budget != 2000 || len(block) > 2000 {
		t.Fatalf("codex block report %+v (%d bytes), want line forms within 2000", ir, len(block))
	}
	if !strings.Contains(block, "budget 2000 bytes") || !strings.Contains(file, "budget 20000 bytes") {
		t.Fatal("each text names its own budget in the header")
	}
	if bodies.of(toolprofile.DirInline, g.Path) != block || bodies.of(toolprofile.DirAt, g.Path) != file || bodies.of(toolprofile.DirGlobRule, g.Path) != file {
		t.Fatal("of: DirInline must get the Codex block, every other kind the folder file")
	}
}
