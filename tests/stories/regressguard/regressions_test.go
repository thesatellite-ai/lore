// Package regressguard keeps tests/stories/REGRESSIONS.md honest: every
// scenario, incident and finding listed there names Go tests (or scripts)
// that really exist in the tree, so a fixed bug cannot silently lose its
// guard when a test is renamed or deleted. It is a plain go test with no
// build tag, so it runs in every `go test ./...` (check:fast included); it
// only reads files, so it costs well under a second.
package regressguard

import (
	"bufio"
	"fmt"
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
// "no test" states need a reason in the notes; StateOpen, StateOnHold and
// StateOwner rows each live only in their own section (sectionOf).
const (
	StateFixed         State = "fixed"
	StateOpen          State = "open"
	StateNotReproduced State = "not-reproduced"
	StateNotABug       State = "not-a-bug"
	StateDocsOnly      State = "docs-only"
	StateProcess       State = "process"
	// StateOnHold is a feature or design the owner put on hold for a later
	// release: not built, not counted as open, its notes say why.
	StateOnHold State = "on-hold"
	// StateOwner is an item only the owner can do (a real machine, a real
	// account, a release token); lore keeps its current defaults meanwhile.
	StateOwner State = "owner"
)

var kinds = map[Kind]bool{KindScenario: true, KindFinding: true, KindIncident: true}

var states = map[State]bool{
	StateFixed: true, StateOpen: true, StateNotReproduced: true,
	StateNotABug: true, StateDocsOnly: true, StateProcess: true,
	StateOnHold: true, StateOwner: true,
}

// stateList is the states in the order the Summary table lists them; the
// guard checks each count there against the rows.
var stateList = []State{
	StateFixed, StateOpen, StateOnHold, StateOwner,
	StateNotReproduced, StateNotABug, StateDocsOnly, StateProcess,
}

// Cell values with a fixed meaning in the test column.
const (
	cellNone = "NONE" // a fixed item still waiting for its test
	cellDash = "-"    // no test needed (the notes say why)
)

// The sections that hold exactly the rows of one state.
const (
	sectionOpen   = "Open"
	sectionOnHold = "On hold (owner, not built in 0.1.18)"
	sectionOwner  = "Waiting on the owner"
)

// sectionOf maps each state that owns a section to it: a row of that state
// sits there and nowhere else, and that section holds no other state.
var sectionOf = map[State]string{
	StateOpen:   sectionOpen,
	StateOnHold: sectionOnHold,
	StateOwner:  sectionOwner,
}

// summaryNeeds is the Summary row counting the NONE cells.
const summaryNeeds = "needs a test"

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
// reason, an open, on-hold or owner row outside its own section, or a
// Summary count that differs from the rows; it reports how many fixed rows
// still wait for a test (failing on them only under strictEnv).
func TestRegressionIndex(t *testing.T) {
	root := moduleRoot(t)
	path := filepath.Join(root, indexRel)
	rows, malformed := readIndex(t, path)
	for _, m := range malformed {
		t.Errorf("%s: %s", indexRel, m)
	}
	if len(rows) == 0 {
		t.Fatalf("%s: no rows read; the tables need the header %q", indexRel, tableHeader)
	}
	problems, waiting := checkRows(rows, testNames(t, root), root)
	for _, p := range problems {
		t.Error(p)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range checkSummary(string(b), rows) {
		t.Error(p)
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

// checkRows returns one message per broken rule over the rows, and the ids
// of the fixed rows still waiting for a test (NONE). tests is the set of
// Test functions in the tree; root resolves script paths. It is split out
// of the guard so a made-up index can prove each rule bites.
func checkRows(rows []row, tests map[string]bool, root string) (problems, waiting []string) {
	bad := func(at, msg string) {
		problems = append(problems, at+": "+msg)
	}
	seen := map[string]int{}
	for _, r := range rows {
		at := indexRel + ":" + strconv.Itoa(r.line) + " " + r.id
		if prev, dup := seen[r.id]; dup {
			bad(at, fmt.Sprintf("id already used on line %d", prev))
		}
		seen[r.id] = r.line
		if !idRe.MatchString(r.id) {
			bad(at, "id must be <source>:<id>")
		}
		if !kinds[r.kind] {
			bad(at, fmt.Sprintf("kind %q is not one of scenario, finding, incident", r.kind))
		}
		if !states[r.state] {
			bad(at, fmt.Sprintf("state %q is not one of fixed, open, on-hold, owner, not-reproduced, not-a-bug, docs-only, process", r.state))
		}
		if strings.TrimSpace(r.title) == "" {
			bad(at, "empty title")
		}
		for _, st := range stateList {
			sec, owns := sectionOf[st]
			if owns && (r.state == st) != (r.section == sec) {
				bad(at, fmt.Sprintf("state %q in section %q: %s rows belong in the %q section and only there", r.state, r.section, st, sec))
			}
		}
		// An on-hold or owner row is parked by the owner's call, so the call's
		// reason must be written down whatever the test cell holds.
		if (r.state == StateOnHold || r.state == StateOwner) && strings.TrimSpace(r.notes) == "" {
			bad(at, fmt.Sprintf("%s row with no reason: the notes say why it is parked", r.state))
		}
		switch r.tests {
		case cellNone:
			if r.state != StateFixed {
				bad(at, fmt.Sprintf("NONE is only for a fixed row; a %s row uses -", r.state))
			}
			waiting = append(waiting, r.id)
			continue
		case cellDash:
			if r.state == StateFixed {
				bad(at, "a fixed row names its test (or NONE while it waits for one)")
			}
			// Open rows may wait without a word; on-hold and owner rows were
			// already held to a reason above.
			if r.state != StateOpen && r.state != StateOnHold && r.state != StateOwner && strings.TrimSpace(r.notes) == "" {
				bad(at, fmt.Sprintf("a %s row with no test says why in the notes", r.state))
			}
			continue
		}
		for _, name := range strings.Split(r.tests, ",") {
			name = strings.TrimSpace(name)
			switch {
			case name == "":
				bad(at, "empty entry in the test cell")
			case testNameRe.MatchString(name):
				if !tests[name] {
					bad(at, fmt.Sprintf("test %s does not exist in any _test.go file", name))
				}
			default:
				if filepath.IsAbs(name) || strings.Contains(name, "..") {
					bad(at, fmt.Sprintf("%q must be a repo-relative path", name))
				} else if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
					bad(at, fmt.Sprintf("script %s does not exist", name))
				}
			}
		}
	}
	return problems, waiting
}

// checkSummary keeps the Summary table in step with the rows: every state
// (on-hold and owner included) and "needs a test" is listed once with the
// count the rows give, so the numbers a reader sees are the guard's.
func checkSummary(text string, rows []row) []string {
	want := map[string]int{summaryNeeds: 0}
	for _, st := range stateList {
		want[string(st)] = 0
	}
	for _, r := range rows {
		want[string(r.state)]++
		if r.tests == cellNone {
			want[summaryNeeds]++
		}
	}
	i := strings.Index(text, "\n## Summary\n")
	if i < 0 {
		return []string{indexRel + ": no \"## Summary\" section"}
	}
	got := map[string]int{}
	var problems []string
	for _, line := range strings.Split(text[i+len("\n## Summary\n"):], "\n") {
		if strings.HasPrefix(line, "## ") {
			break
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) != 2 {
			continue
		}
		name := strings.TrimSpace(cells[0])
		n, err := strconv.Atoi(strings.TrimSpace(cells[1]))
		if err != nil {
			continue // the header and the --- line
		}
		if _, dup := got[name]; dup {
			problems = append(problems, fmt.Sprintf("%s: Summary lists %q twice", indexRel, name))
		}
		got[name] = n
	}
	names := make([]string, 0, len(want)+len(got))
	for name := range want {
		names = append(names, name)
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		w, known := want[name]
		g, listed := got[name]
		switch {
		case !known:
			problems = append(problems, fmt.Sprintf("%s: Summary lists %q, which is not a state", indexRel, name))
		case !listed:
			problems = append(problems, fmt.Sprintf("%s: Summary has no %q line (want %d)", indexRel, name, w))
		case g != w:
			problems = append(problems, fmt.Sprintf("%s: Summary says %s %d, the rows give %d", indexRel, name, g, w))
		}
	}
	return problems
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

// TestGuardOnHoldAndOwnerRules proves the on-hold and owner rules bite: a
// row of either state outside its section, another state inside one, a
// parked row with no reason, and a Summary that leaves them out or miscounts
// each give a problem, while well-placed rows with reasons give none and do
// not count as open.
func TestGuardOnHoldAndOwnerRules(t *testing.T) {
	good := []row{
		{line: 1, section: sectionOnHold, id: "alpha:1", kind: KindFinding, title: "t", state: StateOnHold, tests: cellDash, notes: "later release"},
		{line: 2, section: sectionOwner, id: "alpha:2", kind: KindFinding, title: "t", state: StateOwner, tests: cellDash, notes: "needs the owner's machine"},
		{line: 3, section: sectionOpen, id: "alpha:3", kind: KindFinding, title: "t", state: StateOpen, tests: cellDash},
	}
	if problems, _ := checkRows(good, nil, t.TempDir()); len(problems) != 0 {
		t.Fatalf("well-placed rows gave problems: %v", problems)
	}
	cases := []struct {
		name string
		r    row
		want string
	}{
		{"on-hold outside its section", row{section: sectionOpen, state: StateOnHold, notes: "x"}, "belong in the \"" + sectionOnHold + "\" section"},
		{"owner outside its section", row{section: "alpha", state: StateOwner, notes: "x"}, "belong in the \"" + sectionOwner + "\" section"},
		{"open inside on-hold", row{section: sectionOnHold, state: StateOpen}, "on-hold rows belong"},
		{"fixed inside owner", row{section: sectionOwner, state: StateFixed, tests: "TestGuardOnHoldAndOwnerRules"}, "owner rows belong"},
		{"on-hold without reason", row{section: sectionOnHold, state: StateOnHold}, "on-hold row with no reason"},
		{"owner without reason but a test", row{section: sectionOwner, state: StateOwner, tests: "TestGuardOnHoldAndOwnerRules"}, "owner row with no reason"},
	}
	tests := map[string]bool{"TestGuardOnHoldAndOwnerRules": true}
	for _, c := range cases {
		c.r.line, c.r.id, c.r.kind, c.r.title = 9, "alpha:9", KindFinding, "t"
		if c.r.tests == "" {
			c.r.tests = cellDash
		}
		problems, _ := checkRows([]row{c.r}, tests, t.TempDir())
		if !strings.Contains(strings.Join(problems, "\n"), c.want) {
			t.Errorf("%s: want a problem with %q, got %v", c.name, c.want, problems)
		}
	}

	summary := func(onHold, owner string) string {
		s := "# x\n\n## Summary\n\n| state | rows |\n|---|---|\n| fixed | 0 |\n| open | 1 |\n"
		if onHold != "" {
			s += "| on-hold | " + onHold + " |\n"
		}
		if owner != "" {
			s += "| owner | " + owner + " |\n"
		}
		return s + "| not-reproduced | 0 |\n| not-a-bug | 0 |\n| docs-only | 0 |\n| process | 0 |\n| needs a test | 0 |\n\n## alpha\n"
	}
	if p := checkSummary(summary("1", "1"), good); len(p) != 0 {
		t.Fatalf("a right Summary gave problems: %v", p)
	}
	for _, c := range []struct{ onHold, owner, want string }{
		{"", "1", `no "on-hold" line`},
		{"1", "", `no "owner" line`},
		{"2", "1", "on-hold 2, the rows give 1"},
		{"1", "0", "owner 0, the rows give 1"},
	} {
		p := checkSummary(summary(c.onHold, c.owner), good)
		if !strings.Contains(strings.Join(p, "\n"), c.want) {
			t.Errorf("Summary on-hold=%q owner=%q: want %q, got %v", c.onHold, c.owner, c.want, p)
		}
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
