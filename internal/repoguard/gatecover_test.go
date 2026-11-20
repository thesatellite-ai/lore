package repoguard

// gatecover_test.go — devco D-223: the full gate (lore:check and the
// after-land gate) runs every top-level test of every tagged end-to-end
// suite under tests/. The gate's team step ran go test with a -run prefix
// list, so 138 of 331 tests of tests/teame2e (TestB11*, TestTeA..TeF*,
// TestMerged*, TestMigrate*, TestRoster*, ...) ran in no step, the race
// step (lore:test:team:race) was not a gate step at all, and setupe2e's
// -run list left six tests out; 39 of them broke unseen.
//
// The check reads Taskfile.lore.yml: every GATE_STEPS task is followed
// through the tasks it calls (a `task:` entry with its vars, or `task
// lore:<name>` in a command) to its `go test` commands, each giving a
// selector (suite folder, -tags, -run, -skip). Every test function of a
// file under tests/ whose build constraint needs a tag (journeys,
// teame2e, ...) must be selected by one selector of its folder whose tags
// satisfy that constraint. Files without such a tag run in test-all's
// plain go test ./....

import (
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// gateSelector is one `go test` a gate step runs on a suite folder.
type gateSelector struct {
	step string // the GATE_STEPS name that reaches it
	dir  string // the suite folder, e.g. "tests/teame2e"
	tags map[string]bool
	run  *regexp.Regexp // nil: every test
	skip *regexp.Regexp // nil: none skipped
	// parallel is the go test -parallel the step runs it at (0: none
	// given, go test's GOMAXPROCS default); timeout is its -timeout before
	// the machine factor (0: none given). The budget guard
	// (gatebudget_test.go, devco D-779) reads both.
	parallel int
	timeout  time.Duration
}

// selects reports whether go test runs the top-level test name.
func (s gateSelector) selects(name string) bool {
	return (s.run == nil || s.run.MatchString(name)) && (s.skip == nil || !s.skip.MatchString(name))
}

// taskfileDoc is the part of Taskfile.lore.yml the check reads.
type taskfileDoc struct {
	Vars  map[string]yaml.Node `yaml:"vars"`
	Tasks map[string]struct {
		Cmds []yaml.Node `yaml:"cmds"`
	} `yaml:"tasks"`
}

var (
	goTestSuite = regexp.MustCompile(`\./(tests/[a-z0-9]+)/`)
	goTestTags  = regexp.MustCompile(`-tags (?:'([^']*)'|"([^"]*)"|(\S+))`)
	goTestFlag  = regexp.MustCompile(`-(run|skip) '([^']*)'`)
	callsTask   = regexp.MustCompile(`\btask (?:lore:)?([a-z0-9:-]+)`)
	// templateVar is a Taskfile {{.NAME}} with an optional `| default X`
	// (group 2), which stands in when the step does not set NAME.
	templateVar  = regexp.MustCompile(`\{\{\s*\.([A-Z_]+)(?:\s*\|\s*default\s+"?([^"}\s]*)"?)?[^}]*\}\}`)
	goTestPar    = regexp.MustCompile(`-parallel (\d+)`)
	goTestLimit  = regexp.MustCompile(`-timeout \$\([^)]*?(\S+)\)|-timeout (\S+)`)
	stepVarToken = regexp.MustCompile(`^([A-Z_]+)=(\S*)$`)
)

// expand fills a command's {{.NAME}} from vars, else from its default.
func expand(cmd string, vars map[string]string) string {
	return templateVar.ReplaceAllStringFunc(cmd, func(m string) string {
		sm := templateVar.FindStringSubmatch(m)
		if v := vars[sm[1]]; v != "" {
			return v
		}
		return sm[2]
	})
}

// gateSelectors reads every go test selector the gate's steps reach.
func gateSelectors(t *testing.T, raw []byte) []gateSelector {
	t.Helper()
	var doc taskfileDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("Taskfile.lore.yml: %v", err)
	}
	stepsNode, ok := doc.Vars["GATE_STEPS"]
	if !ok {
		t.Fatal("no GATE_STEPS in Taskfile.lore.yml")
	}
	var out []gateSelector
	for _, line := range strings.Split(stepsNode.Value, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || strings.HasPrefix(f[0], "#") {
			continue
		}
		stepVars := map[string]string{}
		for _, tok := range f[3:] {
			if m := stepVarToken.FindStringSubmatch(tok); m != nil {
				stepVars[m[1]] = m[2]
			}
		}
		seen := map[string]bool{}
		var walk func(task string, vars map[string]string)
		walk = func(task string, vars map[string]string) {
			if seen[task] {
				return
			}
			seen[task] = true
			body, ok := doc.Tasks[task]
			if !ok {
				t.Errorf("gate step %s: task %s is not in Taskfile.lore.yml", f[0], task)
				return
			}
			for _, c := range body.Cmds {
				if c.Kind == yaml.MappingNode {
					var call struct {
						Task string            `yaml:"task"`
						Vars map[string]string `yaml:"vars"`
					}
					if err := c.Decode(&call); err != nil || call.Task == "" {
						continue
					}
					// The step line's vars (PARALLEL=2) reach every task the
					// step calls, as task's command-line vars do; a call's own
					// vars win and may name the caller's ({{.PARALLEL}}).
					next := maps.Clone(stepVars)
					for k, v := range call.Vars {
						next[k] = expand(v, vars)
					}
					walk(call.Task, next)
					continue
				}
				cmd := expand(c.Value, vars)
				if !strings.Contains(cmd, "go test") {
					for _, m := range callsTask.FindAllStringSubmatch(cmd, -1) {
						if _, ok := doc.Tasks[m[1]]; ok {
							walk(m[1], maps.Clone(stepVars))
						}
					}
					continue
				}
				m := goTestSuite.FindStringSubmatch(cmd)
				if m == nil {
					continue // a unit run (./..., a package list): test-all's
				}
				s := gateSelector{step: f[0], dir: m[1], tags: map[string]bool{}}
				if tm := goTestTags.FindStringSubmatch(cmd); tm != nil {
					for _, tag := range strings.FieldsFunc(tm[1]+tm[2]+tm[3], func(r rune) bool { return r == ' ' || r == ',' }) {
						s.tags[tag] = true
					}
				}
				if pm := goTestPar.FindStringSubmatch(cmd); pm != nil {
					s.parallel, _ = strconv.Atoi(pm[1])
				}
				if lm := goTestLimit.FindStringSubmatch(cmd); lm != nil {
					d, err := time.ParseDuration(lm[1] + lm[2])
					if err != nil {
						t.Errorf("gate step %s: bad -timeout in %q: %v", f[0], cmd, err)
					}
					s.timeout = d
				}
				for _, fl := range goTestFlag.FindAllStringSubmatch(cmd, -1) {
					re, err := regexp.Compile(fl[2])
					if err != nil {
						t.Errorf("gate step %s: bad -%s %q: %v", f[0], fl[1], fl[2], err)
						continue
					}
					if fl[1] == "run" {
						s.run = re
					} else {
						s.skip = re
					}
				}
				out = append(out, s)
			}
		}
		walk(f[2], stepVars)
	}
	return out
}

// osTags are the platform words a build constraint may name besides a
// suite tag; the gate runs on Linux.
var osTags = map[string]bool{"linux": true, "unix": true, "windows": false, "darwin": false, "amd64": true, "arm64": false}

// suiteTest is one top-level test of a tagged suite file.
type suiteTest struct {
	dir, file, name string
	expr            constraint.Expr
}

// taggedSuiteTests lists every TestXxx of every file under tests/ whose
// build constraint needs a non-platform tag and holds on Linux with it.
func taggedSuiteTests(t *testing.T) []suiteTest {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot, "tests", "*", "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	var out []suiteTest
	fset := token.NewFileSet()
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		expr := buildConstraint(f)
		if expr == nil || !needsSuiteTag(expr) {
			continue // runs in test-all (go test ./...)
		}
		rel, _ := filepath.Rel(repoRoot, filepath.Dir(path))
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Name.Name == "TestMain" {
				continue
			}
			out = append(out, suiteTest{dir: filepath.ToSlash(rel), file: filepath.Base(path), name: fn.Name.Name, expr: expr})
		}
	}
	return out
}

// buildConstraint is the file's //go:build line, nil when it has none.
func buildConstraint(f *ast.File) constraint.Expr {
	for _, cg := range f.Comments {
		if cg.Pos() >= f.Package {
			break
		}
		for _, c := range cg.List {
			if constraint.IsGoBuild(c.Text) {
				if e, err := constraint.Parse(c.Text); err == nil {
					return e
				}
			}
		}
	}
	return nil
}

// needsSuiteTag reports whether expr is false on Linux without any
// non-platform tag (so the plain go test ./... never compiles the file).
func needsSuiteTag(expr constraint.Expr) bool {
	return !expr.Eval(func(tag string) bool { return osTags[tag] })
}

// uncovered lists the tagged suite tests no selector runs, as
// "<dir>/<file>: <Test>".
func uncovered(tests []suiteTest, sels []gateSelector) []string {
	var out []string
	for _, tc := range tests {
		ok := slices.ContainsFunc(sels, func(s gateSelector) bool {
			if s.dir != tc.dir || !s.selects(tc.name) {
				return false
			}
			return tc.expr.Eval(func(tag string) bool {
				if v, isOS := osTags[tag]; isOS {
					return v
				}
				return s.tags[tag]
			})
		})
		if !ok {
			out = append(out, tc.dir+"/"+tc.file+": "+tc.name)
		}
	}
	return out
}

// TestGateRunsEveryTaggedSuiteTest is the D-223 guard on the real Taskfile.
func TestGateRunsEveryTaggedSuiteTest(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(loreTaskfile)
	if err != nil {
		t.Fatal(err)
	}
	sels := gateSelectors(t, raw)
	tests := taggedSuiteTests(t)
	if len(tests) < 500 {
		t.Fatalf("found only %d tagged suite tests: the file scan is broken", len(tests))
	}
	for _, dir := range []string{"tests/teame2e", "tests/setupe2e", "tests/machines", "tests/journeys", "tests/stories", "tests/userscen"} {
		if !slices.ContainsFunc(sels, func(s gateSelector) bool { return s.dir == dir }) {
			t.Errorf("no gate step runs go test on %s", dir)
		}
	}
	if !slices.ContainsFunc(sels, func(s gateSelector) bool {
		return s.dir == "tests/teame2e" && s.run != nil && s.run.String() == "Race"
	}) {
		t.Error("the gate has no teame2e race step (lore:test:team:race:run, -race -run 'Race')")
	}
	if miss := uncovered(tests, sels); len(miss) > 0 {
		t.Errorf("%d test(s) of the tagged suites run in no gate step (GATE_STEPS in Taskfile.lore.yml; add them to a part's -run, or let a catch-all part's -skip leave them in):\n  %s", len(miss), strings.Join(miss, "\n  "))
	}
}

// TestGateCoverBites: the check flags a test that a filtered step leaves
// out, and passes it once a catch-all part runs it.
func TestGateCoverBites(t *testing.T) {
	t.Parallel()
	expr, err := constraint.Parse("//go:build teame2e")
	if err != nil {
		t.Fatal(err)
	}
	tests := []suiteTest{{dir: "tests/teame2e", file: "a_test.go", name: "TestF01_Kept", expr: expr}, {dir: "tests/teame2e", file: "b_test.go", name: "TestTeC_Lost", expr: expr}}
	filtered := []byte(`vars:
  GATE_STEPS: |
    teame2e  7  test:team:run
tasks:
  test:team:run:
    cmds:
      - go test -tags teame2e -run '^TestF[0-9]' ./tests/teame2e/
`)
	if miss := uncovered(tests, gateSelectors(t, filtered)); len(miss) != 1 || !strings.HasSuffix(miss[0], "TestTeC_Lost") {
		t.Fatalf("a -run filter leaving a test out must be flagged: %v", miss)
	}
	parts := []byte(`vars:
  GATE_STEPS: |
    teame2e       5  test:team:core
    teame2e-rest  4  test:team:rest
tasks:
  test:team:part:
    cmds:
      - go test -tags teame2e -count=1 {{.FILTER}} ./tests/teame2e/
  test:team:core:
    cmds:
      - task: test:team:part
        vars: {PART: core, FILTER: "-run '^TestF[0-9]'"}
  test:team:rest:
    cmds:
      - task: test:team:part
        vars: {PART: rest, FILTER: "-skip '^TestF[0-9]'"}
`)
	if miss := uncovered(tests, gateSelectors(t, parts)); len(miss) != 0 {
		t.Fatalf("a catch-all part covers the rest: %v", miss)
	}
	untagged := []byte(`vars:
  GATE_STEPS: |
    teame2e  7  test:team:run
tasks:
  test:team:run:
    cmds:
      - go test -count=1 ./tests/teame2e/
`)
	if miss := uncovered(tests, gateSelectors(t, untagged)); len(miss) != 2 {
		t.Fatalf("a go test without the suite's tag compiles none of its files: %v", miss)
	}
}
