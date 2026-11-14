// Package regressguard keeps tests/stories/REGRESSIONS.md honest: every
// scenario, incident and finding listed there names Go tests (or scripts)
// that really exist in the tree, so a fixed bug cannot silently lose its
// guard when a test is renamed or deleted. It is a plain go test with no
// build tag, so it runs in every `go test ./...` (check:fast included); it
// only reads files, so it costs well under a second.
package regressguard

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// indexRel is the index's path from the module root.
const indexRel = "tests/stories/REGRESSIONS.md"

// tableHeader is the one header every index table carries; a table with
// another header is not part of the index and is not read.
const tableHeader = "| id | kind | title | state | guarding test(s) | notes |"

// strictEnv makes a fixed row with no test (NONE) a failure instead of a
// reported count. It stays opt-in until the backfill gives every fixed row
// a test; then the default flips (see the index's "Needs a test").
const strictEnv = "LORE_REGRESSIONS_STRICT"

// Kind is what a row records.
type Kind string

// The kinds a row may have.
const (
	KindScenario Kind = "scenario"
	KindFinding  Kind = "finding"
	KindIncident Kind = "incident"
)

// State is where a row's item stands.
type State string

// The states a row may have. Only StateFixed needs a guarding test; the
// "no test" states need a reason in the notes; StateOpen rows live only in
// the Open section.
const (
	StateFixed         State = "fixed"
	StateOpen          State = "open"
	StateNotReproduced State = "not-reproduced"
	StateNotABug       State = "not-a-bug"
	StateDocsOnly      State = "docs-only"
	StateProcess       State = "process"
)

var kinds = map[Kind]bool{KindScenario: true, KindFinding: true, KindIncident: true}

var states = map[State]bool{
	StateFixed: true, StateOpen: true, StateNotReproduced: true,
	StateNotABug: true, StateDocsOnly: true, StateProcess: true,
}

// Cell values with a fixed meaning in the test column.
const (
	cellNone = "NONE" // a fixed item still waiting for its test
	cellDash = "-"    // no test needed (the notes say why)
)

const sectionOpen = "Open"

var (
	idRe       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*:[A-Za-z0-9][A-Za-z0-9._/:-]*$`)
	testNameRe = regexp.MustCompile(`^Test[A-Za-z0-9_]*$`)
	testFuncRe = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)
)

// row is one parsed table row of the index.
type row struct {
	line    int
	section string
	id      string
	kind    Kind
	title   string
	state   State
	tests   string
	notes   string
}

// TestRegressionIndex is the guard: it fails on a malformed row, a repeated
// id, a named test or script that does not exist, a no-test state without a
// reason, or an open row outside the Open section; it reports how many fixed
// rows still wait for a test (failing on them only under strictEnv).
func TestRegressionIndex(t *testing.T) {
	root := moduleRoot(t)
	rows, malformed := readIndex(t, filepath.Join(root, indexRel))
	for _, m := range malformed {
		t.Errorf("%s: %s", indexRel, m)
	}
	if len(rows) == 0 {
		t.Fatalf("%s: no rows read; the tables need the header %q", indexRel, tableHeader)
	}
	tests := testNames(t, root)

	seen := map[string]int{}
	var waiting []string
	for _, r := range rows {
		at := indexRel + ":" + strconv.Itoa(r.line) + " " + r.id
		if prev, dup := seen[r.id]; dup {
			t.Errorf("%s: id already used on line %d", at, prev)
		}
		seen[r.id] = r.line
		if !idRe.MatchString(r.id) {
			t.Errorf("%s: id must be <source>:<id>", at)
		}
		if !kinds[r.kind] {
			t.Errorf("%s: kind %q is not one of scenario, finding, incident", at, r.kind)
		}
		if !states[r.state] {
			t.Errorf("%s: state %q is not one of fixed, open, not-reproduced, not-a-bug, docs-only, process", at, r.state)
		}
		if strings.TrimSpace(r.title) == "" {
			t.Errorf("%s: empty title", at)
		}
		if (r.state == StateOpen) != (r.section == sectionOpen) {
			t.Errorf("%s: state %q in section %q: open rows belong in the Open section and only there", at, r.state, r.section)
		}
		switch r.tests {
		case cellNone:
			if r.state != StateFixed {
				t.Errorf("%s: NONE is only for a fixed row; a %s row uses -", at, r.state)
			}
			waiting = append(waiting, r.id)
			continue
		case cellDash:
			if r.state == StateFixed {
				t.Errorf("%s: a fixed row names its test (or NONE while it waits for one)", at)
			}
			if r.state != StateOpen && strings.TrimSpace(r.notes) == "" {
				t.Errorf("%s: a %s row with no test says why in the notes", at, r.state)
			}
			continue
		}
		for _, name := range strings.Split(r.tests, ",") {
			name = strings.TrimSpace(name)
			switch {
			case name == "":
				t.Errorf("%s: empty entry in the test cell", at)
			case testNameRe.MatchString(name):
				if !tests[name] {
					t.Errorf("%s: test %s does not exist in any _test.go file", at, name)
				}
			default:
				if filepath.IsAbs(name) || strings.Contains(name, "..") {
					t.Errorf("%s: %q must be a repo-relative path", at, name)
				} else if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
					t.Errorf("%s: script %s does not exist", at, name)
				}
			}
		}
	}
	checkNeedsList(t, root, waiting)
	if len(waiting) > 0 {
		if os.Getenv(strictEnv) == "1" {
			t.Errorf("%d fixed rows have no guarding test (NONE): %s", len(waiting), strings.Join(waiting, ", "))
		} else {
			t.Logf("%d fixed rows still need a test (NONE); %s=1 fails on them", len(waiting), strictEnv)
		}
	}
}

// TestIndexGuardCatchesBadRows proves the parser and the checks bite: a
// made-up index with one problem of each kind yields one problem each, so a
// broken parser cannot pass the real index by reading nothing.
func TestIndexGuardCatchesBadRows(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "R.md")
	body := "# x\n\n## alpha\n\n" + tableHeader + "\n|---|---|---|---|---|---|\n" +
		"| alpha:1 | finding | ok | fixed | TestIndexGuardCatchesBadRows |  |\n" +
		"| alpha:2 | finding | short row | fixed |\n" +
		"| alpha:3 | finding | open outside | open | - |  |\n\n" +
		"## Open\n\n" + tableHeader + "\n|---|---|---|---|---|---|\n" +
		"| alpha:4 | finding | still open | open | - |  |\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, malformed := readIndex(t, p)
	if len(rows) != 3 || len(malformed) != 1 {
		t.Fatalf("read %d rows and %d malformed, want 3 and 1: %v", len(rows), len(malformed), malformed)
	}
	if rows[1].section != "alpha" || rows[1].state != StateOpen || rows[2].section != sectionOpen {
		t.Fatalf("sections/states read wrong: %+v", rows)
	}
}

// readIndex returns every row of every index table with its section (the
// nearest "## " heading) and one message per row that does not split into
// the six cells.
func readIndex(t *testing.T, path string) ([]row, []string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open the regression index: %v", err)
	}
	defer f.Close()
	var (
		rows      []row
		malformed []string
		section   string
		inTable   bool
		n         int
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		n++
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "## "):
			section = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			inTable = false
			continue
		case line == tableHeader:
			inTable = true
			continue
		case !strings.HasPrefix(line, "|"):
			inTable = false
			continue
		case !inTable || strings.HasPrefix(line, "|---"):
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), " | ")
		if len(cells) != 6 {
			malformed = append(malformed, "line "+strconv.Itoa(n)+": want 6 cells split by \" | \", got "+strconv.Itoa(len(cells)))
			continue
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		rows = append(rows, row{
			line: n, section: section, id: cells[0], kind: Kind(cells[1]), title: cells[2],
			state: State(cells[3]), tests: cells[4], notes: cells[5],
		})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read the regression index: %v", err)
	}
	return rows, malformed
}

// checkNeedsList keeps the "Needs a test" paragraph in step with the NONE
// rows, so the list a reader sees is the list the guard counts.
func checkNeedsList(t *testing.T, root string, waiting []string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, indexRel))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	i := strings.Index(text, "\n## Needs a test\n")
	if i < 0 {
		t.Errorf("%s: no \"## Needs a test\" section", indexRel)
		return
	}
	listed := map[string]bool{}
	for _, f := range strings.FieldsFunc(text[i:], func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
		f = strings.TrimSuffix(f, ".")
		if idRe.MatchString(f) {
			listed[f] = true
		}
	}
	want := map[string]bool{}
	for _, id := range waiting {
		want[id] = true
		if !listed[id] {
			t.Errorf("%s: %s has NONE but is missing from \"Needs a test\"", indexRel, id)
		}
	}
	var extra []string
	for id := range listed {
		if !want[id] {
			extra = append(extra, id)
		}
	}
	sort.Strings(extra)
	for _, id := range extra {
		t.Errorf("%s: %s is listed under \"Needs a test\" but its row names a test (or is not fixed)", indexRel, id)
	}
}

// testNames returns every top-level Test function declared in any _test.go
// file of the module, whatever its build tags, so journeys and other tagged
// suites count.
func testNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "tmp", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range testFuncRe.FindAllSubmatch(b, -1) {
			names[string(m[1])] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module for tests: %v", err)
	}
	return names
}

// moduleRoot walks up from the package folder to the folder holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test folder")
		}
		dir = parent
	}
}
