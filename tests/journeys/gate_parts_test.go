package journeys

// gate_parts_test.go — the journey gate is complete by construction: every
// test of this package runs in exactly one of the gate's journey parts
// (Taskfile.lore.yml, test:journeys:<part>), and every part is a step of
// the gate (GATE_STEPS) that FAST_MAP brings in wherever it brings in any
// journey part. Sixteen journeys once sat in no part, so neither
// lore:check nor lore:check:fast ran them and one broke unseen for a day.
//
// No build tag on purpose: the plain unit run (lore:test:all, which both
// gate tiers run) compiles this file alone and checks the Taskfile against
// the test names of every file here, whatever their tags.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// taskfilePath is the gate's Taskfile, from this package's folder.
const taskfilePath = "../../Taskfile.lore.yml"

// restStep is the catch-all journey part's gate step (test:journeys:rest).
const restStep = "journey-rest"

// journeyPart is one test:journeys:<name> task: its go test -run and -skip.
type journeyPart struct {
	name      string
	run, skip *regexp.Regexp
}

// has reports whether go test runs the top-level test name in the part.
func (p journeyPart) has(name string) bool {
	return (p.run == nil || p.run.MatchString(name)) && (p.skip == nil || !p.skip.MatchString(name))
}

var (
	partTaskLine = regexp.MustCompile(`^  test:journeys:([a-z-]+):\s*$`)
	partVarsLine = regexp.MustCompile(`vars: \{PART: ([a-z-]+), FILTER: "(.*)"\}`)
	filterFlag   = regexp.MustCompile(`-(run|skip) '([^']*)'`)
)

// journeyParts reads the gate's journey parts from the Taskfile: every
// test:journeys:<name> task whose vars name a PART and a fixed FILTER (the
// manual whole-suite task passes CLI_ARGS and is not a gate part).
func journeyParts(t *testing.T, text string) []journeyPart {
	t.Helper()
	var parts []journeyPart
	task := ""
	for _, line := range strings.Split(text, "\n") {
		if m := partTaskLine.FindStringSubmatch(line); m != nil {
			task = m[1]
			continue
		}
		m := partVarsLine.FindStringSubmatch(line)
		if m == nil || task == "" || strings.Contains(m[2], "CLI_ARGS") {
			continue
		}
		p := journeyPart{name: task}
		for _, f := range filterFlag.FindAllStringSubmatch(m[2], -1) {
			re, err := regexp.Compile(f[2])
			if err != nil {
				t.Fatalf("test:journeys:%s: bad -%s pattern %q: %v", task, f[1], f[2], err)
			}
			if f[1] == "run" {
				p.run = re
			} else {
				p.skip = re
			}
		}
		parts = append(parts, p)
		task = ""
	}
	return parts
}

// packageTests is every top-level Test function declared in this folder.
func packageTests(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	fset := token.NewFileSet()
	for _, f := range files {
		file, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Name.Name == "TestMain" {
				continue
			}
			names = append(names, fn.Name.Name)
		}
	}
	return names
}

// stepNames is the first word of every line of the block var name (a
// "NAME: |" block of Taskfile vars).
func stepNames(text, name string) map[string][]string {
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

func TestGateRunsEveryJourneyOnce(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(taskfilePath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	parts := journeyParts(t, text)
	if len(parts) < 2 {
		t.Fatalf("found %d journey parts in %s; the parser no longer reads the Taskfile", len(parts), taskfilePath)
	}
	tests := packageTests(t)
	if len(tests) < 10 {
		t.Fatalf("found %d tests in tests/journeys; the parser no longer reads the files", len(tests))
	}
	for _, name := range tests {
		var in []string
		for _, p := range parts {
			if p.has(name) {
				in = append(in, p.name)
			}
		}
		switch len(in) {
		case 0:
			t.Errorf("%s runs in no journey part of the gate: widen a part's -run in %s or fix journey-rest's -skip", name, taskfilePath)
		case 1:
		default:
			t.Errorf("%s runs in %d journey parts (%s): each journey runs in exactly one; narrow journey-rest's -skip or a part's -run", name, len(in), strings.Join(in, ", "))
		}
	}

	gate := stepNames(text, "GATE_STEPS")
	var steps []string
	for _, p := range parts {
		step := "journey-" + p.name
		steps = append(steps, step)
		if args, ok := gate[step]; !ok || !slices.Contains(args, "test:journeys:"+p.name) {
			t.Errorf("part test:journeys:%s is no step of GATE_STEPS (want a line %q running test:journeys:%s)", p.name, step, p.name)
		}
	}
	// FAST_MAP: a rule that brings in any journey part brings in the
	// catch-all (journey-rest holds the journeys no part names, whatever
	// they walk through), and a rule that brings in all parts but one was
	// left behind when that part was added.
	for glob, named := range stepNames(text, "FAST_MAP") {
		if !slices.ContainsFunc(named, func(s string) bool { return strings.HasPrefix(s, "journey-") }) {
			continue
		}
		if !slices.Contains(named, restStep) {
			t.Errorf("FAST_MAP rule %s runs journey parts but not the catch-all %s", glob, restStep)
		}
		missing := slices.DeleteFunc(slices.Clone(steps), func(s string) bool { return slices.Contains(named, s) })
		if len(missing) == 1 {
			t.Errorf("FAST_MAP rule %s runs every journey part but %s", glob, missing[0])
		}
	}
}
