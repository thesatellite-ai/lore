package main

// later.md 52 (fa-oldcode): the core part of `lore init` on the release
// binary, its short words, and the host printing an error once, run in
// process through run. The org half of that row (no org, own repo, client
// repo, LORE_ORG unknown) is wire.TestStandaloneOrgHooks.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// repoMetaFile is the repo's _meta.json.
func repoMetaFile(root string) string { return filepath.Join(root, ".lore", "data", "_meta.json") }

// rowFiles lists a table's row files in the repo store.
func rowFiles(t *testing.T, root, table string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, ".lore", "data", table, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// readMeta reads the repo's _meta.json ids.
func readMeta(t *testing.T, root string) (raw []byte, projectID, repoID string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(repoMetaFile(root)))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		ProjectID string `json:"project_id"`
		RepoID    string `json:"repo_id"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("_meta.json: %v\n%s", err, raw)
	}
	return raw, m.ProjectID, m.RepoID
}

// TestInitCore: a fresh init writes one project row, _meta.json naming it
// and the repo id, and that repo's row, and prints both ids; a second init
// on the same checkout is E_ALREADY_INITIALIZED (exit 23) and writes
// nothing; a fresh clone carrying .lore/data (no local cache) adopts the
// project: same ids, _meta.json byte for byte kept, no second project row.
func TestInitCore(t *testing.T) {
	asPerson(t)
	root := envScopeRepo(t) // runs `lore init`
	projects := rowFiles(t, root, "projects")
	if len(projects) != 1 {
		t.Fatalf("project rows after init: %v", projects)
	}
	meta, pid, rid := readMeta(t, root)
	if !strings.HasPrefix(pid, "prj_") || !strings.HasPrefix(rid, "rep_") {
		t.Fatalf("_meta.json ids: project %q repo %q", pid, rid)
	}
	if filepath.Base(projects[0]) != pid+".json" {
		t.Fatalf("project row %s is not _meta.json's %s", projects[0], pid)
	}
	if repos := rowFiles(t, root, "repos"); len(repos) != 1 || filepath.Base(repos[0]) != rid+".json" {
		t.Fatalf("repo rows %v, want the one for %s", repos, rid)
	}

	code, out, errOut := runCaptured(t, "init")
	if code != 23 || !strings.Contains(errOut, "E_ALREADY_INITIALIZED") || !strings.Contains(errOut, pid) {
		t.Fatalf("second init: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if again, _, _ := readMeta(t, root); string(again) != string(meta) {
		t.Fatalf("second init changed _meta.json:\n%s\nwas\n%s", again, meta)
	}
	if n := len(rowFiles(t, root, "projects")); n != 1 {
		t.Fatalf("second init left %d project rows", n)
	}

	// A fresh clone: .lore/data only, no cache or lore.db on this machine.
	for _, f := range localStateFiles {
		if err := os.RemoveAll(filepath.Join(root, ".lore", f)); err != nil {
			t.Fatal(err)
		}
	}
	code, out, errOut = runCaptured(t, "init")
	if code != 0 || !strings.Contains(out, "adopted the shared lore project") || !strings.Contains(out, pid) {
		t.Fatalf("init of a clone carrying .lore/data: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if again, _, _ := readMeta(t, root); string(again) != string(meta) {
		t.Fatalf("adopting changed _meta.json:\n%s\nwas\n%s", again, meta)
	}
	if n := len(rowFiles(t, root, "projects")); n != 1 {
		t.Fatalf("adopting left %d project rows", n)
	}
}

// TestInitName: --name wins (lower-cased); without it the folder name.
func TestInitName(t *testing.T) {
	asPerson(t)
	for _, c := range []struct{ flag, want string }{{"", ""}, {"My-App", "my-app"}} {
		t.Run("name="+c.flag, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			dir := gitRepo(t)
			args := []string{"init"}
			want := c.want
			if c.flag != "" {
				args = append(args, "--name", c.flag)
			} else {
				want = strings.ToLower(filepath.Base(dir))
			}
			code, out, errOut := runCaptured(t, args...)
			if code != 0 || !strings.Contains(out, "name:       "+want+"\n") {
				t.Fatalf("init %v: exit %d, want name %q\nstdout: %s\nstderr: %s", args, code, want, out, errOut)
			}
		})
	}
}

// gitRepo is an empty git work tree (no lore yet), now the working folder.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	t.Chdir(dir)
	return dir
}

// gitIn runs git in dir and fails the test on an error.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is required: %v", err)
	}
	cmd := &exec.Cmd{Path: git, Args: append([]string{git, "-C", dir}, args...)}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// TestShortcutsRunTheirCommand: every short word answers exactly what the
// command it stands for answers (one implementation, CLI_REFERENCE 2.7,
// 2.13, 3.6, 3.8), and `lore plan apply` is refused naming `lore mission
// apply` with the same arguments (2.15).
func TestShortcutsRunTheirCommand(t *testing.T) {
	asPerson(t)
	root := envScopeRepo(t)
	if code, _, errOut := runCaptured(t, "memory", "add", "--body", "shortcut note"); code != 0 {
		t.Fatalf("memory add: %d %s", code, errOut)
	}
	mem := strings.TrimSuffix(filepath.Base(rowFiles(t, root, "memories")[0]), ".json")
	store := "file://" + filepath.Join(t.TempDir(), "scratch")
	if code, _, errOut := runCaptured(t, "store", "init", "scratch", store); code != 0 {
		t.Fatalf("store init: %d %s", code, errOut)
	}
	pairs := []struct{ short, long []string }{
		{[]string{"show", mem}, []string{"row", "show", mem}},
		{[]string{"show", mem, "--origin"}, []string{"row", "show", mem, "--origin"}},
		{[]string{"sql", "select count(*) as n from memories"}, []string{"sql", "run", "select count(*) as n from memories"}},
		{[]string{"lint"}, []string{"lint", "run"}},
		{[]string{"trash"}, []string{"trash", "list"}},
		{[]string{"machines"}, []string{"machine", "list"}},
		{[]string{"machine"}, []string{"machine", "list"}},
		{[]string{"move", mem, "--to", "scratch"}, []string{"row", "move", mem, "--to", "scratch"}}, // asks first: both E_NEEDS_CONFIRM
		{[]string{"show", "mem_00000000000000000000000000000000"}, []string{"row", "show", "mem_00000000000000000000000000000000"}},
	}
	for _, p := range pairs {
		sc, sOut, sErr := runCaptured(t, p.short...)
		lc, lOut, lErr := runCaptured(t, p.long...)
		if sc != lc || sOut != lOut || sErr != lErr {
			t.Errorf("lore %s != lore %s:\n exit %d vs %d\n stdout %q\n     vs %q\n stderr %q\n     vs %q",
				strings.Join(p.short, " "), strings.Join(p.long, " "), sc, lc, sOut, lOut, sErr, lErr)
		}
	}
	// The confirmed move goes through the shortcut: the row leaves the repo store.
	if code, out, errOut := runCaptured(t, "move", mem, "--to", "scratch", "--yes"); code != 0 {
		t.Fatalf("move --yes: exit %d\n%s\n%s", code, out, errOut)
	}
	left := rowFiles(t, root, "memories")
	if len(left) != 1 {
		t.Fatalf("repo store memory files after the move: %v", left)
	}
	if b, err := os.ReadFile(filepath.Clean(left[0])); err != nil || !strings.Contains(string(b), `"_deleted": true`) {
		t.Fatalf("the repo copy is not tombstoned after the move (%v):\n%s", err, b)
	}

	code, _, errOut := runCaptured(t, "plan", "apply", "plan.md", "--yes")
	if code != 20 || !strings.Contains(errOut, `unknown command "apply" for "lore plan"`) || !strings.Contains(errOut, "lore mission apply plan.md --yes") {
		t.Fatalf("plan apply: exit %d %s", code, errOut)
	}
}

// TestHostErrorPrintedOnce: whatever fails (flag parsing, an operation, a
// host hook before the operation, a shortcut), the error is printed once,
// with its code, and the exit code is the code's.
func TestHostErrorPrintedOnce(t *testing.T) {
	asPerson(t)
	envScopeRepo(t)
	cases := []struct {
		name string
		env  string
		args []string
		code int
		want string
	}{
		{"unknown flag", "", []string{"memory", "list", "--bogus"}, 20, "E_INVALID_INPUT"},
		{"bad value", "", []string{"task", "list", "--status", "bogus"}, 20, "E_INVALID_INPUT"},
		{"not found", "", []string{"memory", "show", "mem_00000000000000000000000000000000"}, 27, "E_NOT_FOUND"},
		{"shortcut not found", "", []string{"show", "mem_00000000000000000000000000000000"}, 27, "E_NOT_FOUND"},
		{"host hook refusal", "nosuchrepo", []string{"memory", "add", "--body", "x"}, -1, "nosuchrepo"},
		{"refused verb", "", []string{"actor", "delete", "act_00000000000000000000000000000000"}, 23, "E_REFUSED"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(hosthooks.EnvRepo, c.env)
			code, _, errOut := runCaptured(t, c.args...)
			if c.code >= 0 && code != c.code || code == 0 {
				t.Fatalf("exit %d, want %d\n%s", code, c.code, errOut)
			}
			if n := strings.Count(errOut, "ERROR"); n != 1 || !strings.Contains(errOut, c.want) {
				t.Fatalf("stderr has %d ERROR lines (want 1) naming %q:\n%s", n, c.want, errOut)
			}
		})
	}
}
