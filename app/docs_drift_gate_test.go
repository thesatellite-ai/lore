package app

// The docs-vs-CLI drift gate itself (TestDocsMatchCLI): every `lore …`
// invocation in the docs people and agents copy from, resolved against
// cmd/lore's command tree (driftRoot, docs_drift_test.go). Ported from
// saas/cmd/cli/docs_drift_test.go (B11).

import (
	"path/filepath"
	"sort"
	"testing"
)

// docsRoot is the repository root, relative to this package.
const docsRoot = ".."

// driftDocGlobs are the documents people and agents copy commands from.
var driftDocGlobs = []string{
	"README.md",
	"CONTRIBUTING.md",
	"LORE_SYNC_SPEC.md",
	"docs/*.md",
	"docs/team/*.md",
	"docs/team/use-cases/*.md",
	"skills/*.md",
	"skills/examples/*.md",
	"tests/README.md",
}

// driftOptionalDocGlobs are doc sections that may be empty or missing while
// they are being written: they are checked when present but do not count
// toward the "the docs set exists" guard below, which covers only
// driftDocGlobs. Move a glob into driftDocGlobs once its folder ships.
var driftOptionalDocGlobs = []string{
	"docs/reference/*.md",
	"docs/knowledge/*.md",
	"docs/tasks/*.md",
	"docs/sync/*.md",
	"docs/operations/*.md",
	"docs/choosing/*.md",
}

// driftAllowed lists invocations that are wrong on purpose, keyed by the
// exact code text: examples of what NOT to run, or a table row documenting
// that a command does not exist. Each needs its reason.
var driftAllowed = map[string]string{
	"lore task block <tsk_id>": "SKILL.md table row documenting that this verb does not exist",
	"lore add":                 "capture-discipline rules: shorthand for any `lore <entity> add` (\"NO `lore add`\")",
	"lore sync install-git":    "docs/operations/ci.md: says this v0.1.17 command is gone",
	"lore sync export":         "LORE_SYNC_SPEC.md: says this v0.1.17 command is dropped",
	"lore sync dupes":          "LORE_SYNC_SPEC.md: a planned command (E48), not built",
}

// minDocInvocations guards the extractor itself: the docs hold several
// hundred invocations, so finding fewer means the parser broke and the test
// would pass vacuously.
const minDocInvocations = 200

// driftFinding is one problem in one document.
type driftFinding struct {
	file, code, problem string
	line                int
}

func TestDocsMatchCLI(t *testing.T) {
	rootCmd := driftRoot(t)
	var files []string
	for _, g := range driftDocGlobs {
		m, err := filepath.Glob(filepath.Join(docsRoot, g))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	if len(files) < len(driftDocGlobs) {
		t.Fatalf("expected the docs set to exist, found %v", files)
	}
	for _, g := range driftOptionalDocGlobs {
		m, err := filepath.Glob(filepath.Join(docsRoot, g))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...) // no match is fine: the section is not written yet
	}
	var findings []driftFinding
	checked := 0
	for _, f := range files {
		for _, inv := range docInvocations(t, f) {
			if _, ok := driftAllowed[inv.code]; ok {
				continue
			}
			checked++
			if problem := checkInvocation(rootCmd, inv.code); problem != "" {
				rel, _ := filepath.Rel(docsRoot, f)
				findings = append(findings, driftFinding{file: rel, line: inv.line, code: inv.code, problem: problem})
			}
		}
	}
	if checked < minDocInvocations {
		t.Fatalf("only %d invocations found; the extractor is broken", checked)
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].file != findings[j].file {
			return findings[i].file < findings[j].file
		}
		return findings[i].line < findings[j].line
	})
	for _, d := range findings {
		t.Errorf("%s:%d: %s\n    %s", d.file, d.line, d.problem, d.code)
	}
}
