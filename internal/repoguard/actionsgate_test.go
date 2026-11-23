package repoguard

// actionsgate_test.go — devco D-919: the full gate runs on GitHub Actions
// (.github/workflows/gate.yml), one job per GATE_STEPS step, and releases wait
// for that run's verdict. The check pins the shape that makes the run a real
// gate: the matrix is read from GATE_STEPS (tests/lib/gate-matrix.sh), never a
// hand list that a new step can miss; every GATE_STEPS step is in that matrix;
// the gate job runs each entry through tests/lib/ci-step.sh without stopping
// the others on the first failure; and a final job named `summary` runs after
// every other job, even failed ones, and turns the results into the verdict.
// The release tooling finds the run by its run-name (the commit) and reads its
// `summary` job, so both names are pinned too. No network: it parses the file.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// actionsWorkflow is the part of a GitHub Actions workflow file the guard reads.
type actionsWorkflow struct {
	RunName string                `yaml:"run-name"`
	Jobs    map[string]actionsJob `yaml:"jobs"`
}

// actionsJob is one job of an actionsWorkflow.
type actionsJob struct {
	Name            string    `yaml:"name"`
	Needs           yaml.Node `yaml:"needs"` // a job name or a list of them
	If              string    `yaml:"if"`
	ContinueOnError bool      `yaml:"continue-on-error"`
	Strategy        struct {
		FailFast *bool     `yaml:"fail-fast"`
		Matrix   yaml.Node `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []struct {
		Run   string `yaml:"run"`
		Uses  string `yaml:"uses"`
		Shell string `yaml:"shell"`
	} `yaml:"steps"`
}

// needs lists the jobs a job waits for.
func (j actionsJob) needs() []string {
	switch j.Needs.Kind {
	case yaml.ScalarNode:
		return []string{j.Needs.Value}
	case yaml.SequenceNode:
		out := make([]string, 0, len(j.Needs.Content))
		for _, n := range j.Needs.Content {
			out = append(out, n.Value)
		}
		return out
	}
	return nil
}

// runs reports whether one of the job's steps runs a command containing want.
func (j actionsJob) runs(want string) bool {
	for _, s := range j.Steps {
		if strings.Contains(s.Run, want) {
			return true
		}
	}
	return false
}

// actionsGateProblems lists what is wrong with the gate workflow raw, given
// the step names gate-matrix.sh produced and the names GATE_STEPS lists.
func actionsGateProblems(raw []byte, matrixSteps, gateSteps []string) []string {
	var wf actionsWorkflow
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		return []string{"gate.yml does not parse: " + err.Error()}
	}
	var out []string
	if !strings.Contains(wf.RunName, "inputs.ref") || !strings.Contains(wf.RunName, "github.sha") {
		out = append(out, "run-name must name the commit tested (inputs.ref || github.sha): the release tooling matches runs by it")
	}
	steps, ok := wf.Jobs["steps"]
	if !ok || !steps.runs("tests/lib/gate-matrix.sh") {
		out = append(out, "no `steps` job running tests/lib/gate-matrix.sh: the matrix must be read from GATE_STEPS")
	}
	gate, ok := wf.Jobs["gate"]
	switch {
	case !ok:
		out = append(out, "no `gate` job")
	default:
		if m := strings.ReplaceAll(gate.Strategy.Matrix.Value, " ", ""); m != "${{fromJSON(needs.steps.outputs.matrix)}}" {
			out = append(out, "gate job's matrix is not ${{ fromJSON(needs.steps.outputs.matrix) }} (a hand-written matrix misses new GATE_STEPS)")
		}
		if gate.Strategy.FailFast == nil || *gate.Strategy.FailFast {
			out = append(out, "gate job needs fail-fast: false (one failed step must not cancel the others)")
		}
		if !gate.runs("tests/lib/ci-step.sh") {
			out = append(out, "gate job does not run tests/lib/ci-step.sh (log, clean-tree check, result for the summary)")
		}
		if gate.ContinueOnError {
			out = append(out, "gate job has continue-on-error: a failed step would not fail the run")
		}
	}
	summary, ok := wf.Jobs["summary"]
	switch {
	case !ok:
		out = append(out, "no job `summary`: the release tooling reads its conclusion as the gate verdict")
	default:
		if summary.Name != "" && summary.Name != "summary" {
			out = append(out, "job summary is named "+summary.Name+", the release tooling looks for `summary`")
		}
		if !strings.Contains(summary.If, "always()") {
			out = append(out, "summary job needs if: always() (it must run when a step failed)")
		}
		need := summary.needs()
		for name := range wf.Jobs {
			if name != "summary" && !slices.Contains(need, name) {
				out = append(out, "summary job does not wait for job "+name)
			}
		}
		if !summary.runs("tests/lib/ci-summary.sh") {
			out = append(out, "summary job does not run tests/lib/ci-summary.sh")
		}
		// A step without shell: runs `bash -e` with no pipefail, so `ci-summary.sh | tee`
		// exits 0 whatever the verdict (run 45146110701 printed FAIL and passed).
		for _, s := range summary.Steps {
			if strings.Contains(s.Run, "tests/lib/ci-summary.sh") && strings.Contains(s.Run, "|") && s.Shell != "bash" && !strings.Contains(s.Run, "pipefail") {
				out = append(out, "summary job pipes ci-summary.sh without pipefail (add shell: bash): its verdict would never fail the run")
			}
		}
	}
	for _, s := range gateSteps {
		if !slices.Contains(matrixSteps, s) {
			out = append(out, "GATE_STEPS step "+s+" has no job in the Actions matrix")
		}
	}
	for _, s := range matrixSteps {
		if !slices.Contains(gateSteps, s) {
			out = append(out, "matrix job "+s+" is not a GATE_STEPS step")
		}
	}
	return out
}

// gateMatrixSteps runs tests/lib/gate-matrix.sh on taskfile and returns the
// step names of the matrix it prints.
func gateMatrixSteps(t *testing.T, taskfile string) []string {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	out, err := exec.Command("bash", filepath.Join(repoRoot, "tests", "lib", "gate-matrix.sh"), taskfile).Output()
	if err != nil {
		t.Fatalf("gate-matrix.sh %s: %v", taskfile, err)
	}
	var m struct {
		Include []struct {
			Step string `yaml:"step"`
			Task string `yaml:"task"`
		} `yaml:"include"`
	}
	if err := yaml.Unmarshal(out, &m); err != nil {
		t.Fatalf("gate-matrix.sh printed no JSON matrix: %v\n%s", err, out)
	}
	names := make([]string, 0, len(m.Include))
	for _, e := range m.Include {
		if e.Task == "" {
			t.Errorf("matrix entry %s has no task", e.Step)
		}
		names = append(names, e.Step)
	}
	return names
}

// TestActionsGateRunsEveryGateStep fails when gate.yml stops taking its matrix
// from GATE_STEPS, a GATE_STEPS step has no Actions job, or the summary job
// that gives the verdict is missing (devco D-919).
func TestActionsGateRunsEveryGateStep(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(loreTaskfile)
	if err != nil {
		t.Fatal(err)
	}
	var gateSteps []string
	for name := range taskfileBlock(string(raw), "GATE_STEPS") {
		gateSteps = append(gateSteps, name)
	}
	sort.Strings(gateSteps)
	wf, err := os.ReadFile(filepath.Join(repoRoot, ".github", "workflows", "gate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range actionsGateProblems(wf, gateMatrixSteps(t, loreTaskfile), gateSteps) {
		t.Error(p)
	}
}

// TestActionsGateGuardBites breaks gate.yml in each way the guard must catch.
func TestActionsGateGuardBites(t *testing.T) {
	t.Parallel()
	wf, err := os.ReadFile(filepath.Join(repoRoot, ".github", "workflows", "gate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	good := string(wf)
	steps := []string{"a", "b"}
	if p := actionsGateProblems(wf, steps, steps); len(p) != 0 {
		t.Fatalf("the real gate.yml should pass: %v", p)
	}
	cases := map[string]struct {
		yml            string
		matrix, listed []string
		want           string
	}{
		"hand matrix":     {strings.Replace(good, "${{ fromJSON(needs.steps.outputs.matrix) }}", "{step: [a, b]}", 1), steps, steps, "matrix is not"},
		"no matrix read":  {strings.Replace(good, "m=$(tests/lib/gate-matrix.sh", "m=$(echo", 1), steps, steps, "gate-matrix.sh"},
		"fail fast":       {strings.Replace(good, "fail-fast: false", "fail-fast: true", 1), steps, steps, "fail-fast"},
		"no summary":      {strings.Replace(good, "\n  summary:\n", "\n  verdict:\n", 1), steps, steps, "no job `summary`"},
		"summary renamed": {strings.Replace(good, "\n    name: summary\n", "\n    name: verdict\n", 1), steps, steps, "named verdict"},
		"summary skipped": {strings.Replace(good, "\n    if: always()\n", "\n", 1), steps, steps, "always()"},
		"run-name":        {strings.Replace(good, "run-name: Gate ${{ inputs.ref || github.sha }}", "run-name: Gate", 1), steps, steps, "run-name"},
		"step missing":    {good, []string{"a"}, steps, "step b has no job"},
		"no pipefail":     {strings.Replace(good, "        shell: bash\n        env:\n          STEPS_MATRIX", "        env:\n          STEPS_MATRIX", 1), steps, steps, "pipefail"},
	}
	for name, c := range cases {
		if c.yml == good && name != "step missing" {
			t.Errorf("%s: the mutation did not apply; update the case to the current gate.yml", name)
			continue
		}
		p := actionsGateProblems([]byte(c.yml), c.matrix, c.listed)
		if !slices.ContainsFunc(p, func(s string) bool { return strings.Contains(s, c.want) }) {
			t.Errorf("%s: guard missed it (want %q in %v)", name, c.want, p)
		}
	}
	// gate-matrix.sh refuses a malformed line instead of dropping the step.
	dir := t.TempDir()
	bad := filepath.Join(dir, "Taskfile.lore.yml")
	if err := os.WriteFile(bad, []byte("vars:\n  GATE_STEPS: |\n    ok 1 lint:vet\n    broken lint:fmt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = exec.Command("bash", filepath.Join(repoRoot, "tests", "lib", "gate-matrix.sh"), bad).Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 {
		t.Errorf("gate-matrix.sh on a malformed GATE_STEPS line: want exit 2, got %v", err)
	}
}
