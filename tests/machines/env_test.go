//go:build machines && !windows

package machines

// env_test.go — a user's commands on the env shapes (shape_env.go): add a
// solo setup for a spaced repo folder, list, edit, init, render, doctor,
// the setup probe, config check, store list, identity show, remove. After
// each run: the result is right, config.yaml is block style, nothing was
// written outside home but where asked, repo files are 0644/0755 and
// ~/.lore owner-only (CheckMachine), and doctor names any remaining issue.

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// envSetup is the solo setup each flow adds.
const envSetup = "solo"

// envFolderGlob is the setup's folder for a repo under home.
func envFolderGlob(rel string) string { return "~/" + rel + "/**" }

// outsideHome lists every path under the machine dir but outside Home
// (and the fakes' own data), relative to the dir: a lore run may change
// none of it unless the test asked for it.
func outsideHome(t *testing.T, m *Machine) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(m.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p == m.Home {
			return filepath.SkipDir
		}
		r, _ := filepath.Rel(m.Dir, p)
		if r == "old-lore-calls" { // the fake old lore's own log
			return nil
		}
		out = append(out, r)
		return nil
	})
	slices.Sort(out)
	return out
}

// envConfigBlockStyle: config.yaml, when present, holds no flow list or
// mapping lore wrote (a `key: [` or `key: {` line).
func envConfigBlockStyle(t *testing.T, m *Machine, step string) {
	t.Helper()
	cfg := csConfig(t, m)
	for i, line := range strings.Split(cfg, "\n") {
		trim := strings.TrimSpace(line)
		if _, v, ok := strings.Cut(trim, ": "); ok && (strings.HasPrefix(v, "[") || strings.HasPrefix(v, "{")) {
			t.Errorf("[%s] config.yaml line %d is flow style: %q\n%s", step, i+1, line, cfg)
		}
	}
}

// envUserFlow runs the commands a user runs on a working machine, on the
// spaced repo EnvRepo, and checks result, disk and doctor after each.
func envUserFlow(t *testing.T, m *Machine) {
	t.Helper()
	repo := m.Repos[EnvRepo]
	before := outsideHome(t, m)
	step := func(name string) {
		t.Helper()
		envConfigBlockStyle(t, m, m.Shape+"/"+name)
		m.CheckMachine(t, m.Shape+"/"+name, Gone{})
		if after := outsideHome(t, m); !slices.Equal(after, before) {
			t.Errorf("[%s/%s] lore wrote outside home: before %v, after %v", m.Shape, name, before, after)
		}
	}
	// setup add solo, for the spaced folder.
	csNewSetup(t, m, envSetup, PersonEmail)
	ans, _ := json.Marshal(map[string]any{"folders": []string{envFolderGlob(EnvRepo)}, "default": false})
	csOK(t, m.Lore(t, "", "setup", "apply", envSetup, "--step", "folders", "--answers", string(ans), "--json"))
	step("setup add")
	// The setup applies in the spaced repo (the folder glob matched).
	if here := m.List(t, repo).Active; here.Name != envSetup {
		t.Errorf("in %q the active setup is %q (%s), want %s", EnvRepo, here.Name, here.Why, envSetup)
	}
	if s := csOK(t, m.Lore(t, "", "setup", "show", envSetup, "--json")); !strings.Contains(s, envFolderGlob(EnvRepo)) {
		t.Errorf("setup show lacks the spaced folder: %s", s)
	}
	csOK(t, m.Lore(t, "", "setup", "edit", envSetup, "--note", "edited on "+m.Shape, "--json"))
	step("setup edit")
	// init again in the spaced repo says it is a lore repo; render writes
	// the pointer files there.
	if r := m.Lore(t, repo, "init"); !alreadyInit(r) {
		t.Errorf("lore init again in %q: %s", EnvRepo, r)
	}
	// The repo was made before the setup, from the git fallback: its actor
	// copy holds git's user.name, which is the setup's name, so nothing is
	// stale (SOURCE_OF_TRUTH rows 2-3).
	envActorCopiesCurrent(t, m, repo, "setup add")
	csOK(t, m.Lore(t, repo, "render"))
	if _, err := os.Stat(filepath.Join(repo, ".lore", "LORE.md")); err != nil {
		t.Errorf("render wrote no .lore/LORE.md in %q: %v", EnvRepo, err)
	}
	step("render")
	// Read-only commands answer.
	csOK(t, m.Lore(t, "", "setup", "repair", envSetup, "--dry-run", "--json"))
	csOK(t, m.Lore(t, repo, "store", "list", "--json"))
	id := csOK(t, m.Lore(t, repo, "identity", "show", "--json"))
	if !strings.Contains(id, PersonEmail) {
		t.Errorf("identity show in %q does not name the setup's identity %s: %s", EnvRepo, PersonEmail, id)
	}
	envDoctorClean(t, m, repo, "after render")
	step("probe")
	// remove: the setup goes to the trash.
	csOK(t, m.Lore(t, "", "setup", "remove", envSetup, "--json"))
	if tr := m.Trash(t); !slices.Contains(tr, envSetup) {
		t.Errorf("trash lacks %s: %v", envSetup, tr)
	}
	m.CheckMachine(t, m.Shape+"/setup remove", Gone{Trashed: []string{envSetup}})
}

// envDoctor is the part of doctor --json the env tests read.
type envDoctor struct {
	Status   string   `json:"status"`
	Warnings []string `json:"warnings"`
	Errors   []string `json:"errors"`
}

// envDoctorRun is `lore doctor --json` in cwd.
func envDoctorRun(t *testing.T, m *Machine, cwd string) (envDoctor, Result) {
	t.Helper()
	r := m.Lore(t, cwd, "doctor", "--json")
	var d envDoctor
	if err := json.Unmarshal([]byte(r.Stdout), &d); err != nil || d.Status == "" {
		t.Fatalf("doctor printed no report (%v): %s", err, r)
	}
	return d, r
}

// envDoctorClean: doctor in the repo is healthy with no warning.
func envDoctorClean(t *testing.T, m *Machine, cwd, step string) {
	t.Helper()
	d, r := envDoctorRun(t, m, cwd)
	if d.Status != "healthy" || len(d.Warnings) > 0 || len(d.Errors) > 0 {
		t.Errorf("[%s/%s] doctor is not clean: %s", m.Shape, step, r)
	}
}

// envActorCopiesCurrent: the repo at cwd was initialised before its setup
// named the person, so its actor copy was written from the git fallback.
// That fallback shows git's user.name (PersonName in gitconfig), the same
// name the setup gives, so the copy already agrees with its source
// (SOURCE_OF_TRUTH rows 2-3): actor show marks the repo copy current with
// PersonName, and doctor has no actor_copies warning to fix.
func envActorCopiesCurrent(t *testing.T, m *Machine, cwd, step string) {
	t.Helper()
	d, r := envDoctorRun(t, m, cwd)
	for _, w := range d.Warnings {
		if strings.HasPrefix(w, "actor_copies:") {
			t.Errorf("[%s/%s] doctor flags an actor copy that agrees with its source: %s", m.Shape, step, r)
		}
	}
	act := sotActorID(t, m, cwd)
	var s sotActorShow
	got := lsRun(t, m, cwd, "actor", "show", act)
	if err := json.Unmarshal([]byte(got), &s); err != nil {
		t.Fatalf("[%s/%s] actor show: %v: %s", m.Shape, step, err, got)
	}
	repoCopy := false
	for _, cp := range s.Data.Copies {
		if cp.Store != "repo" {
			continue
		}
		repoCopy = true
		if cp.DisplayName != PersonName || cp.State != sotCopyCurrent {
			t.Errorf("[%s/%s] the repo's actor copy is %q (%s), want %q (%s): git's user.name is the setup's name", m.Shape, step, cp.DisplayName, cp.State, PersonName, sotCopyCurrent)
		}
	}
	if !repoCopy || s.Data.DisplayName != PersonName {
		t.Errorf("[%s/%s] actor show names %q with no repo copy check (%v), want %q:\n%s", m.Shape, step, s.Data.DisplayName, repoCopy, PersonName, got)
	}
}

// envDoctorOnly: doctor in cwd has no warning line, and every setup check
// that is not ok is one of allowed (named by check id).
func envDoctorOnly(t *testing.T, m *Machine, cwd, step string, allowed ...string) {
	t.Helper()
	r := m.Lore(t, cwd, "doctor", "--json")
	var d struct {
		Warnings []string `json:"warnings"`
		Setups   []struct {
			Checks []struct {
				Check  string `json:"check"`
				Status string `json:"status"`
				Detail string `json:"detail"`
			} `json:"checks"`
		} `json:"setups"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &d); err != nil {
		t.Fatalf("[%s/%s] doctor printed no report (%v): %s", m.Shape, step, err, r)
	}
	if len(d.Warnings) > 0 {
		t.Errorf("[%s/%s] doctor warns: %v", m.Shape, step, d.Warnings)
	}
	for _, s := range d.Setups {
		for _, c := range s.Checks {
			if c.Status != "ok" && c.Status != "skip" && !slices.Contains(allowed, c.Check) {
				t.Errorf("[%s/%s] doctor check %s is %s: %s", m.Shape, step, c.Check, c.Status, c.Detail)
			}
		}
	}
}

// TestEnvUserFlow: the working env shapes (odd paths, odd TMPDIR) take the
// whole flow.
func TestEnvUserFlow(t *testing.T) {
	for _, shape := range []string{ShapeEnvNonASCIIHome, ShapeEnvSpaces, ShapeEnvNoLoreHome, ShapeEnvTmpdirUnset, ShapeEnvTmpdirMissing} {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			m := Build(t, shape, loreBin)
			envUserFlow(t, m)
			if shape == ShapeEnvTmpdirUnset || shape == ShapeEnvTmpdirMissing {
				// XDG_CONFIG_HOME is not lore's: nothing lands there.
				if ents, _ := os.ReadDir(filepath.Join(m.Dir, xdgDir)); len(ents) > 0 {
					t.Errorf("lore wrote into XDG_CONFIG_HOME: %v", ents)
				}
			}
			if shape == ShapeEnvTmpdirMissing {
				if _, err := os.Stat(filepath.Join(m.Dir, missingTmp)); err == nil {
					t.Errorf("lore made the missing TMPDIR %s", missingTmp)
				}
			}
		})
	}
}

// TestEnvSpacedStore: on env_spaces the personal store is a folder in a
// spaced path (file:// url with spaces, also checked as %20). A bare git
// repository named by file:// is refused before config.yaml changes (lore
// would write a folder store into git's own folder). The folder is created
// as a store with its marker in the same step, so setup show names it, store
// list in both spaced repos answers and doctor finds nothing at all.
func TestEnvSpacedStore(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeEnvSpaces, loreBin)
	csToGitHub(t, m, envSetup)
	ans, _ := json.Marshal(map[string]any{"folders": []string{envFolderGlob(EnvRepo), envFolderGlob(EnvSpacedRepo)}, "default": true})
	csOK(t, m.Lore(t, "", "setup", "apply", envSetup, "--step", "folders", "--answers", string(ans), "--json"))
	bareRepo := filepath.Join(m.Home, EnvStoreDir, EnvBareStore)
	before := csConfig(t, m)
	ans, _ = json.Marshal(map[string]string{"personal_store": "create", "store_url": "file://" + bareRepo})
	if e := csRefused(t, m.Lore(t, "", "setup", "apply", envSetup, "--step", "personal_store", "--answers", string(ans), "--json"), "E_INVALID_INPUT"); e != nil && !strings.Contains(e.Message, "is a git repository") {
		t.Errorf("the bare repo refusal does not say why: %+v", e)
	}
	if csConfig(t, m) != before {
		t.Errorf("a refused bare-repo store wrote config.yaml")
	}
	if _, err := os.Stat(filepath.Join(bareRepo, "lore-store.yaml")); err == nil {
		t.Errorf("lore wrote lore-store.yaml into the bare git repository")
	}
	bare := filepath.Join(m.Home, EnvStoreDir, EnvFolderStore)
	// The url as a person types it (with the spaces) is taken; the same
	// folder written with %20 checks the same.
	ans, _ = json.Marshal(map[string]string{"personal_store": "create", "store_url": "file://" + bare})
	csOK(t, m.Lore(t, "", "setup", "apply", envSetup, "--step", "personal_store", "--answers", string(ans), "--json"))
	if _, err := os.Stat(filepath.Join(bare, "lore-store.yaml")); err != nil {
		t.Errorf("create wrote no lore-store.yaml in the new store: %v", err)
	}
	enc, _ := json.Marshal(map[string]string{"personal_store": "existing", "store_url": "file://" + strings.ReplaceAll(bare, " ", "%20")})
	csOK(t, m.Lore(t, "", "setup", "check", envSetup, "--step", "personal_store", "--answers", string(enc), "--json"))
	// config.yaml keeps the url in its canonical form (%20).
	if s := csOK(t, m.Lore(t, "", "setup", "show", envSetup, "--json")); !strings.Contains(s, "file://"+strings.ReplaceAll(bare, " ", "%20")) {
		t.Errorf("setup show lacks the spaced store url: %s", s)
	}
	for _, rel := range []string{EnvRepo, EnvSpacedRepo} {
		csOK(t, m.Lore(t, m.Repos[rel], "store", "list", "--json"))
		csOK(t, m.Lore(t, m.Repos[rel], "render"))
		envActorCopiesCurrent(t, m, m.Repos[rel], "store in "+rel)
		envDoctorOnly(t, m, m.Repos[rel], "store in "+rel)
	}
	envConfigBlockStyle(t, m, "spaced store")
	m.CheckMachine(t, "env_spaces/store", Gone{})
}

// TestEnvBrokenLoreHome: ~/.lore is a file or a 0500 folder. Every command
// that needs to write there refuses with an error naming ~/.lore (never a
// panic, never a write elsewhere instead); read-only commands still answer;
// doctor names the problem.
func TestEnvBrokenLoreHome(t *testing.T) {
	for _, shape := range []string{ShapeEnvLoreHomeFile, ShapeEnvLoreHomeReadOnly} {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			m := Build(t, shape, loreBin)
			before := outsideHome(t, m)
			r := m.Lore(t, "", "setup", "apply", envSetup, "--step", "kind", "--answers", csKindAnswers(envSetup), "--json")
			if r.Code == 0 {
				t.Errorf("setup add on a broken ~/.lore succeeded: %s", r)
			} else if e := csErrOf(r); e == nil || e.Code == "E_INTERNAL" || !strings.Contains(e.Message+e.Hint, ".lore") || strings.Contains(r.Stderr, "panic") {
				t.Errorf("setup add on a broken ~/.lore: want an error naming ~/.lore and what to do: %s", r)
			}
			for _, args := range [][]string{{"setup", "list", "--json"}, {"config", "check", "--json"}, {"identity", "show", "--json"}} {
				if r := m.Lore(t, m.Repos[EnvRepo], args...); strings.Contains(r.Stderr, "panic") || (r.Code != 0 && csErrOf(r) == nil) {
					t.Errorf("%v on a broken ~/.lore: %s", args, r)
				}
			}
			// doctor --all reports the broken home as its finding (a JSON
			// report, never an error envelope) and names the fix.
			d := m.Lore(t, m.Repos[EnvRepo], "doctor", "--all", "--json")
			var rep doctorOut
			if d.Code == 0 || csErrOf(d) != nil || json.Unmarshal([]byte(d.Stdout), &rep) != nil || rep.Status == "" || !strings.Contains(d.Stdout, ".lore") {
				t.Errorf("doctor --all does not report the broken ~/.lore: %s", d)
			}
			if shape == ShapeEnvLoreHomeFile && (!strings.Contains(d.Stdout, "a file is in the way of a folder") || !strings.Contains(d.Stdout, "move it aside")) {
				t.Errorf("doctor --all does not say ~/.lore is a file and how to fix it: %s", d)
			}
			if after := outsideHome(t, m); !slices.Equal(after, before) {
				t.Errorf("lore wrote outside home: before %v, after %v", before, after)
			}
			// ~/.lore is still what the person left there.
			info, err := os.Lstat(filepath.Join(m.Home, ".lore"))
			switch {
			case err != nil:
				t.Errorf("~/.lore is gone: %v", err)
			case shape == ShapeEnvLoreHomeFile && !info.Mode().IsRegular():
				t.Errorf("lore replaced the ~/.lore file: %v", info.Mode())
			case shape == ShapeEnvLoreHomeReadOnly && info.Mode().Perm() != loreHomeReadOnly:
				t.Errorf("lore changed ~/.lore's mode to %#o", info.Mode().Perm())
			}
		})
	}
}

// TestEnvTwoLores: an old lore (0.1.17) earlier on PATH than this one.
// doctor names which binary a bare `lore` runs, its version, what it
// shadows and that it is not this binary. The git hooks run `lore` from PATH
// (what the person and their agents run); they only probe the old one and,
// as it cannot read 0.1.18 rows, refuse the commit saying so, never run an
// old command on the new data.
func TestEnvTwoLores(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeEnvTwoLores, loreBin)
	repo := m.Repos[EnvRepo]
	old := filepath.Join(m.Dir, oldLoreDir, "lore")
	real := filepath.Join(m.Dir, realLoreDir, "lore")
	d, r := envDoctorRun(t, m, repo)
	named := false
	for _, w := range d.Warnings {
		named = named || (strings.Contains(w, "`lore` runs "+old+" (lore "+OldLoreVersion+")") &&
			strings.Contains(w, "shadows "+real+" (this binary)") && strings.Contains(w, "not this binary"))
	}
	if !named {
		t.Errorf("doctor does not say `lore` runs %s (%s) and shadows this binary %s: %s", old, OldLoreVersion, real, r)
	}
	csOK(t, m.Lore(t, repo, "render"))
	csOK(t, m.Lore(t, repo, "memory", "add", "--body", "two lores on PATH"))
	git := func(args ...string) (string, error) {
		c := exec.Command("/usr/bin/git", args...)
		c.Dir, c.Env = repo, m.env()
		out, err := c.CombinedOutput()
		return string(out), err
	}
	if out, err := git("add", "-A"); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	out, err := git("commit", "-q", "-m", "first")
	if err == nil || !strings.Contains(out, "on PATH is older; install lore 0.1.18") {
		t.Errorf("a commit of 0.1.18 rows with an old lore first on PATH: want a refusal naming the old lore, got err=%v:\n%s", err, out)
	}
	calls, _ := os.ReadFile(filepath.Join(m.Dir, "old-lore-calls"))
	for _, line := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
		if line != "" && !strings.HasSuffix(line, " --help") && !strings.HasSuffix(line, " version") {
			t.Errorf("a hook or directive ran an old-lore command (only probes are allowed): %q", line)
		}
	}
	for _, f := range []string{"post-commit", "pre-push", "pre-commit", "commit-msg"} {
		body, err := os.ReadFile(filepath.Join(repo, ".git", "hooks", f))
		if err != nil {
			t.Errorf("hook %s missing: %v", f, err)
			continue
		}
		if strings.Contains(string(body), filepath.Join(m.Dir, oldLoreDir)) {
			t.Errorf("hook %s names the old lore by path: %s", f, body)
		}
	}
}

// TestEnvCreateMissingStoreFolder: `create` on a local folder that does not
// exist yet makes it (0755, with its marker) when its nearest existing
// parent is writable, and doctor right after is clean; `existing` on a
// missing folder is refused and makes nothing; `create` under a parent lore
// cannot write in is refused and makes nothing.
func TestEnvCreateMissingStoreFolder(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeEnvSpaces, loreBin)
	csToGitHub(t, m, envSetup)
	ans, _ := json.Marshal(map[string]any{"folders": []string{envFolderGlob(EnvRepo)}, "default": true})
	csOK(t, m.Lore(t, "", "setup", "apply", envSetup, "--step", "personal_store", "--answers", `{"personal_store":"none"}`, "--json"))
	csOK(t, m.Lore(t, "", "setup", "apply", envSetup, "--step", "folders", "--answers", string(ans), "--json"))
	store := func(choice, dir string) Result {
		a, _ := json.Marshal(map[string]string{"personal_store": choice, "store_url": "file://" + dir})
		return m.Lore(t, "", "setup", "apply", envSetup, "--step", "personal_store", "--answers", string(a), "--json")
	}
	missing := filepath.Join(m.Home, "new stores", "not yet", "me")
	csRefused(t, store("existing", missing), "E_SETUP_STEP_FAILED")
	if _, err := os.Stat(filepath.Join(m.Home, "new stores")); err == nil {
		t.Errorf("existing on a missing folder made it")
	}
	locked := filepath.Join(m.Home, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	csRefused(t, store("create", filepath.Join(locked, "me")), "E_SETUP_STEP_FAILED")
	if _, err := os.Stat(filepath.Join(locked, "me")); err == nil {
		t.Errorf("create under an unwritable parent made the folder")
	}
	csOK(t, store("create", missing))
	for _, d := range []string{filepath.Join(m.Home, "new stores"), filepath.Join(m.Home, "new stores", "not yet"), missing} {
		info, err := os.Stat(d)
		if err != nil || info.Mode().Perm() != 0o755 {
			t.Errorf("create made %s as %v (%v), want a 0755 folder", rel(m.Home, d), info, err)
		}
	}
	if _, err := os.Stat(filepath.Join(missing, "lore-store.yaml")); err != nil {
		t.Errorf("create wrote no marker: %v", err)
	}
	envActorCopiesCurrent(t, m, m.Repos[EnvRepo], "created store")
	envDoctorOnly(t, m, m.Repos[EnvRepo], "created store")
	m.CheckMachine(t, "env_spaces/created store", Gone{})
}
