//go:build bench

package coding

// bench_test.go — the run against a lore binary (task bench:coding; build
// tag bench, so the plain go test ./... does not run it).

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Environment knobs (README.md lists them).
const (
	envBin    = "LORE_BENCH_BIN"           // the lore binary (default: build ./cmd/lore)
	envReport = "LORE_BENCH_CODING_REPORT" // the report (default <repo>/tmp/bench/coding.md)
	envDir    = "LORE_BENCH_DIR"           // keep the run here (default: a temp folder)
)

// env runs lore and git as the gate's person in the repo.
type env struct {
	t    *testing.T
	bin  string
	repo string
	env  []string
}

// lore runs one lore command in the repo: its output and exit code.
func (e *env) lore(args ...string) (string, int) {
	e.t.Helper()
	cmd := exec.Command(e.bin, args...)
	cmd.Dir, cmd.Env = e.repo, e.env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err == nil {
		return out.String(), 0
	}
	ee, ok := err.(*exec.ExitError)
	if !ok {
		e.t.Fatalf("lore %s: %v", strings.Join(args, " "), err)
	}
	return out.String(), ee.ExitCode()
}

// must runs a setup command that has to work.
func (e *env) must(args ...string) string {
	e.t.Helper()
	out, code := e.lore(args...)
	if code != 0 {
		e.t.Fatalf("setup: lore %s: exit %d\n%s", strings.Join(args, " "), code, out)
	}
	return out
}

// git runs git in the repo.
func (e *env) git(args ...string) {
	e.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = e.repo, e.env
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// addArgs is the lore command that writes r.
func addArgs(r Row) []string {
	a := []string{string(r.Kind), "add", "--body", r.Body, "--json", "--yes"}
	if r.Title != "" {
		a = append(a, "--title", r.Title)
	}
	if r.Severity != "" {
		a = append(a, "--severity", r.Severity)
	}
	switch r.Act {
	case ActGlob:
		g, _ := json.Marshal(r.Globs)
		a = append(a, "--activation", string(ActGlob), "--globs", string(g))
	case ActSemantic:
		a = append(a, "--activation", string(ActSemantic), "--applies-to", r.Applies)
	case ActAlways:
		a = append(a, "--activation", string(ActAlways))
	}
	if r.Scope != "" {
		a = append(a, "--scope", r.Scope)
	}
	return a
}

// writeFiles writes files into dir and commits them in a new git repo.
func writeFiles(t *testing.T, e *env, files map[string]string) {
	t.Helper()
	for f, body := range files {
		p := filepath.Join(e.repo, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e.git("init", "-q", "-b", "main")
	e.git("add", "-A")
	e.git("commit", "-q", "-m", "orbit")
}

// writeRepo makes the repo with files, runs lore init and writes the
// rows; it returns row id → key.
func writeRepo(t *testing.T, e *env, files map[string]string) map[string]string {
	t.Helper()
	return writeRepoFor(t, e, files, "codex")
}

// writeRepoFor is writeRepo with lore init wiring the repo for tool.
func writeRepoFor(t *testing.T, e *env, files map[string]string, tool string) map[string]string {
	t.Helper()
	writeFiles(t, e, files)
	e.must("init", "--name", "orbit", "--target", tool)
	keyOf := map[string]string{}
	for _, r := range Rows() {
		out := e.must(addArgs(r)...)
		var got struct {
			ID string `json:"id"`
		}
		// --json prints the row's JSON last; lines before it are notes
		if i := strings.Index(out, "{"); i >= 0 {
			_ = json.Unmarshal([]byte(out[i:]), &got)
		}
		if got.ID == "" {
			t.Fatalf("lore %s printed no id:\n%s", r.Kind, out)
		}
		keyOf[got.ID] = r.Key
	}
	return keyOf
}

// runDir is the run's folder: LORE_BENCH_DIR emptied, or a temp folder.
func runDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv(envDir)
	if dir == "" {
		return t.TempDir()
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// loreBin is LORE_BENCH_BIN, or ./cmd/lore built into dir/bin.
func loreBin(t *testing.T, root, dir string) string {
	t.Helper()
	bin := os.Getenv(envBin)
	if bin == "" {
		bin = filepath.Join(dir, "bin", "lore")
		build := exec.Command("go", "build", "-o", bin, "./cmd/lore")
		build.Dir, build.Env = root, append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build lore: %v\n%s", err, out)
		}
	}
	bin, _ = filepath.Abs(bin)
	return bin
}

// newEnv makes home (with the gate's person's git identity) and repo, and
// runs lore and git there as that person.
func newEnv(t *testing.T, bin, home, repo string) *env {
	t.Helper()
	for _, d := range []string{home, repo} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = Pia Holm\n\temail = pia@notes.example\n[init]\n\tdefaultBranch = main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmp := os.Getenv("TMPDIR")
	if tmp == "" {
		tmp = os.TempDir()
	}
	return &env{t: t, bin: bin, repo: repo, env: []string{
		"HOME=" + home, "PATH=" + filepath.Dir(bin) + ":/usr/local/bin:/usr/bin:/bin", "TMPDIR=" + tmp, "NO_COLOR=1", "LANG=C.UTF-8",
	}}
}

// repoRoot is the module root (the folder holding go.mod).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			t.Fatal("no go.mod above the bench")
		}
		dir = up
	}
}

func TestBenchCoding(t *testing.T) {
	started := time.Now()
	if err := Validate(); err != nil {
		t.Fatal(err)
	}
	root := repoRoot(t)
	dir := runDir(t)
	bin := loreBin(t, root, dir)
	e := newEnv(t, bin, filepath.Join(dir, "home"), filepath.Join(dir, "orbit"))
	keyOf := writeRepo(t, e, StubFiles())
	version, _ := e.lore("--version")

	// the always-on must-rules reach the agent through the rendered file
	e.must("render")
	rendered, err := os.ReadFile(filepath.Join(e.repo, ".lore", "LORE.md"))
	if err != nil {
		t.Fatal(err)
	}
	info := RunInfo{Lore: strings.TrimSpace(version), Rows: len(Rows()), Started: started}
	for _, r := range Rows() {
		if r.Act == ActAlways && !strings.Contains(string(rendered), r.Body) {
			info.Render = append(info.Render, r.Key)
		}
	}

	ts := Tasks()
	ctxArm := ArmResult{Name: "context", Grades: map[string]Grade{}, Chars: map[string]int{}}
	askArm := ArmResult{Name: "ask", Grades: map[string]Grade{}, Chars: map[string]int{}}
	for _, tk := range ts {
		file, _ := e.lore("context", "--for-file", tk.File)
		prompt, _ := e.lore("context", "--for-prompt", tk.Text)
		ctxArm.Grades[tk.ID] = GradeIDs(tk, IDsIn(file+"\n"+prompt), keyOf)
		ctxArm.Chars[tk.ID] = len(file) + len(prompt)
		ask, _ := e.lore("ask", "--query", tk.AskQuery())
		askArm.Grades[tk.ID] = GradeIDs(tk, IDsIn(ask), keyOf)
		askArm.Chars[tk.ID] = len(ask)
		t.Logf("%s context missing %v noise %v; ask missing %v noise %v", tk.ID, ctxArm.Grades[tk.ID].Missing, ctxArm.Grades[tk.ID].Noise, askArm.Grades[tk.ID].Missing, askArm.Grades[tk.ID].Noise)
	}
	arms := []ArmResult{ctxArm, askArm}
	report := Report(info, ts, arms)
	path := os.Getenv(envReport)
	if path == "" {
		path = filepath.Join(root, "tmp", "bench", "coding.md")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	// the trap run's section (task bench:agent) stays
	old, err := os.ReadFile(filepath.Clean(path))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(KeepTrapSection(string(old), report)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("report: %s\nsummary: %s", path, Summary(ts, arms))
	if misses := AllMisses(info, ts, arms); len(misses) > 0 {
		t.Errorf("coding context gate: %d bar(s) missed (report %s):\n  %s", len(misses), path, strings.Join(misses, "\n  "))
	}
}
