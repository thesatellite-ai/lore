package testtime

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestFactorFor pins the rule (devco:D-410): 1 on the 48-core Linux gate
// server, so its limits are unchanged; larger on fewer cores (capped at 6)
// and 1.5x on macOS; an explicit LORE_TEST_TIME_SCALE wins.
func TestFactorFor(t *testing.T) {
	cases := []struct {
		name  string
		env   string
		nproc int
		goos  string
		want  float64
	}{
		{name: "gate server", nproc: 48, goos: "linux", want: 1},
		{name: "bigger than the gate", nproc: 128, goos: "linux", want: 1},
		{name: "24-core linux", nproc: 24, goos: "linux", want: 2},
		{name: "10-core mac", nproc: 10, goos: "darwin", want: 4.8 * 1.5},
		{name: "48-core mac", nproc: 48, goos: "darwin", want: 1.5},
		{name: "2-core runner is capped", nproc: 2, goos: "linux", want: 6},
		{name: "2-core mac is capped before the mac stretch", nproc: 2, goos: "darwin", want: 9},
		{name: "unknown core count", nproc: 0, goos: "linux", want: 1},
		{name: "explicit wins", env: "3", nproc: 48, goos: "linux", want: 3},
		{name: "explicit fraction", env: "2.5", nproc: 10, goos: "darwin", want: 2.5},
		{name: "explicit 1 on a mac", env: "1", nproc: 10, goos: "darwin", want: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := FactorFor(c.env, c.nproc, c.goos)
			if err != nil {
				t.Fatal(err)
			}
			if diff := got - c.want; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("FactorFor(%q, %d, %q) = %v, want %v", c.env, c.nproc, c.goos, got, c.want)
			}
		})
	}
}

// TestFactorForRefusesBadValues: a setting below 1 or not a number is
// refused with the typed error, never silently replaced.
func TestFactorForRefusesBadValues(t *testing.T) {
	for _, v := range []string{"0.5", "0", "-2", "fast", "NaN", "1x"} {
		_, err := FactorFor(v, 48, "linux")
		var bad *InvalidScaleError
		if !errors.As(err, &bad) || bad.Value != v || !strings.Contains(err.Error(), EnvVar+"=4") {
			t.Errorf("FactorFor(%q): want *InvalidScaleError naming the variable and an example, got %v", v, err)
		}
	}
}

// TestScaleBySampleDeadline: the factor is applied to a sample deadline,
// and factor 1 leaves it exactly as written.
func TestScaleBySampleDeadline(t *testing.T) {
	f, err := FactorFor("", 48, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if got := ScaleBy(40*time.Minute, f); got != 40*time.Minute {
		t.Fatalf("48-core linux: 40m became %v", got)
	}
	f, err = FactorFor("", 10, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ScaleBy(40*time.Minute, f), 288*time.Minute; got != want {
		t.Fatalf("10-core mac: 40m became %v, want %v", got, want)
	}
	if got := ScaleBy(50*time.Millisecond, 1.5); got != 75*time.Millisecond {
		t.Fatalf("sub-second limits keep their precision: %v", got)
	}
}

// TestScaleUsesTheEnvironment: Scale reads LORE_TEST_TIME_SCALE (this
// test runs a fresh process, since Factor computes once per process).
func TestScaleUsesTheEnvironment(t *testing.T) {
	if os.Getenv("TESTTIME_CHILD") == "1" {
		if got := Scale(10 * time.Second); got != 25*time.Second {
			t.Fatalf("Scale(10s) with %s=2.5 = %v", EnvVar, got)
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestScaleUsesTheEnvironment$", "-test.count=1")
	cmd.Env = append(os.Environ(), "TESTTIME_CHILD=1", EnvVar+"=2.5")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
}

// repoRoot is the module root, two folders up from this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// TestShellHelperAgrees: tests/lib/testtime.sh (the Taskfile and script
// side) computes the same factor as FactorFor for the same inputs, and
// scales a sample duration with it.
func TestShellHelperAgrees(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the shell helper runs under bash on Linux and macOS")
	}
	sh := filepath.Join(repoRoot(t), "tests", "lib", "testtime.sh")
	run := func(env []string, arg string) (string, error) {
		cmd := exec.Command("bash", sh, arg)
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	cases := []struct {
		env   string
		nproc int
		goos  string
	}{
		{nproc: 48, goos: "linux"}, {nproc: 64, goos: "linux"}, {nproc: 10, goos: "darwin"},
		{nproc: 16, goos: "linux"}, {nproc: 2, goos: "darwin"}, {env: "2.5", nproc: 48, goos: "linux"},
	}
	for _, c := range cases {
		env := []string{EnvVar + "=" + c.env, "LORE_TEST_TIME_NPROC=" + strconv.Itoa(c.nproc), "LORE_TEST_TIME_OS=" + c.goos}
		want, err := FactorFor(c.env, c.nproc, c.goos)
		if err != nil {
			t.Fatal(err)
		}
		got, err := run(env, "--factor")
		if err != nil {
			t.Fatalf("%v --factor: %v", env, err)
		}
		if g, err := strconv.ParseFloat(got, 64); err != nil || g-want > 1e-6 || want-g > 1e-6 {
			t.Errorf("%v: shell factor %q, Go factor %v", env, got, want)
		}
		secs, err := run(env, "40m")
		if err != nil {
			t.Fatalf("%v 40m: %v", env, err)
		}
		if w := ScaleBy(40*time.Minute, want); secs != strconv.Itoa(int(w.Seconds()))+"s" {
			t.Errorf("%v: shell scaled 40m to %q, Go to %v", env, secs, w)
		}
	}
	for _, bad := range []string{"0.5", "fast"} {
		if _, err := run([]string{EnvVar + "=" + bad}, "10m"); err == nil {
			t.Errorf("%s=%s: the shell helper did not refuse it", EnvVar, bad)
		}
	}
	if _, err := run(nil, "ten minutes"); err == nil {
		t.Error("a duration the helper cannot read was not refused")
	}
}

// gateFiles are the files that run go test or wait on a whole run from
// the shell: the Taskfiles (whose test tasks the gate steps call) and the
// shell scripts under tests/.
func gateFiles(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	files, err := filepath.Glob(filepath.Join(root, "Taskfile*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(filepath.Join(root, "tests"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".sh") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 5 {
		t.Fatalf("found only %d gate files under %s", len(files), root)
	}
	return files
}

var (
	// rawGoTimeout is a go test -timeout given a literal duration.
	rawGoTimeout = regexp.MustCompile(`-timeout[ =]+["']?[0-9]`)
	// rawShellTimeout is coreutils timeout given a literal duration.
	rawShellTimeout = regexp.MustCompile(`(^|[\s;&|('"])timeout\s+["']?[0-9]`)
	// goTestCmd is a go test command (not prose that mentions one).
	goTestCmd = regexp.MustCompile(`(^|[\s;&|("'}])go test( |$)`)
)

// TestNoRawTimeoutInGateScripts (devco:D-410): every go test -timeout and
// every coreutils timeout in the Taskfiles and the test scripts goes
// through tests/lib/testtime.sh, and every go test command of a Taskfile
// names a -timeout (Go's silent default is a fixed 10 minutes). Without
// this, one new step with a raw limit dies on a slower machine again.
func TestNoRawTimeoutInGateScripts(t *testing.T) {
	root := repoRoot(t)
	for _, f := range gateFiles(t) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, f)
		isTaskfile := strings.HasSuffix(f, ".yml")
		for i, line := range strings.Split(string(b), "\n") {
			code := strings.TrimSpace(line)
			if strings.HasPrefix(code, "#") || strings.HasPrefix(code, "desc:") {
				continue
			}
			if rel == filepath.Join("tests", "lib", "gotest-shards.sh") && strings.Contains(code, "-test.timeout=") {
				continue // passes on the -timeout it was given
			}
			if rawGoTimeout.MatchString(code) || rawShellTimeout.MatchString(code) {
				t.Errorf("%s:%d: a raw time limit; scale it with tests/lib/testtime.sh (Taskfile: $({{.TEST_TIME}} 40m)):\n  %s", rel, i+1, code)
			}
			if isTaskfile && goTestCmd.MatchString(code) && !strings.Contains(code, "-timeout") {
				t.Errorf("%s:%d: go test without -timeout gets Go's fixed 10 min; add -timeout $({{.TEST_TIME}} 10m):\n  %s", rel, i+1, code)
			}
		}
	}
}

// TestGuardSeesARawTimeout: the guard's patterns match the shapes it must
// refuse and pass the scaled ones (so a pattern typo cannot make it pass
// everything).
func TestGuardSeesARawTimeout(t *testing.T) {
	for _, raw := range []string{
		`go test -tags x -timeout 40m ./...`, `go test -timeout=15m ./x`, `gotest-shards.sh 6 ./wire -timeout 40m`,
		`timeout 10 $LORE learn-from`, `'timeout 120 lore api --port $PORT'`,
	} {
		if !rawGoTimeout.MatchString(raw) && !rawShellTimeout.MatchString(raw) {
			t.Errorf("not seen as raw: %s", raw)
		}
	}
	for _, ok := range []string{
		`go test -timeout $({{.TEST_TIME}} 40m) ./...`, `timeout "$("$(dirname "$0")/../lib/testtime.sh" 10s)" $LORE`,
		`'timeout $API_LIFE lore api'`, `# -timeout 40m (times the machine factor)`,
	} {
		if strings.HasPrefix(ok, "#") {
			continue // comments are skipped before matching
		}
		if rawGoTimeout.MatchString(ok) || rawShellTimeout.MatchString(ok) {
			t.Errorf("a scaled limit seen as raw: %s", ok)
		}
	}
	if !goTestCmd.MatchString(`- go test -p 8 ./...`) || goTestCmd.MatchString(`desc: "every go testing tier"`) {
		t.Error("goTestCmd does not tell a go test command from prose")
	}
}
