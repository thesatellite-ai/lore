//go:build machines && !windows

package machines

// repos_test.go — lore in every kind of repo a developer has (hooks that
// call lore, bare, worktree, submodule, detached HEAD, empty, a subfolder),
// setups whose folders nest or tie, and setups with a history (trashed and
// re-added, purged and re-added, renamed twice, a default moved to a setup
// then trashed). Each case checks the answer, the files on disk afterwards
// (nothing written into a bare repo or a submodule's parent, config.yaml in
// block style after every write, modes) and what doctor says.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// gitIn runs git in dir with the machine's environment.
func (m *Machine) gitIn(t testing.TB, dir string, args ...string) (string, error) {
	t.Helper()
	c := exec.Command("/usr/bin/git", args...)
	c.Dir = dir
	c.Env = m.env()
	out, err := c.CombinedOutput()
	return string(out), err
}

func (m *Machine) mustGit(t testing.TB, dir string, args ...string) string {
	t.Helper()
	out, err := m.gitIn(t, dir, args...)
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), rel(m.Home, dir), err, out)
	}
	return out
}

// treeSnap is every path under root (minus skip, relative names) with its
// mode and content hash: equal snapshots mean nothing was written.
func treeSnap(t testing.TB, root string, skip ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		r, _ := filepath.Rel(root, p)
		for _, s := range skip {
			if r == s || strings.HasPrefix(r, s+string(filepath.Separator)) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		v := info.Mode().String()
		if info.Mode().IsRegular() {
			b, _ := os.ReadFile(p)
			h := sha256.Sum256(b)
			v += " " + hex.EncodeToString(h[:8])
		}
		out[r] = v
		return nil
	})
	return out
}

func diffSnap(a, b map[string]string) []string {
	var d []string
	for k, v := range b {
		if a[k] != v {
			d = append(d, k+" ("+a[k]+" → "+v+")")
		}
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			d = append(d, k+" (removed)")
		}
	}
	slices.Sort(d)
	return d
}

// flowYAML matches a flow list or mapping with content ("key: [a]",
// "- {a: b}"); "[]" and "{}" are fine.
var flowYAML = regexp.MustCompile(`(:\s+|^\s*-\s+)[\[{]\s*[^\]}\s]`)

// checkBlockConfig: ~/.lore/config.yaml is block style (the owner's
// flow-list bug) and lore config check reads it without a warning.
func (m *Machine) checkBlockConfig(t testing.TB, step string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(m.Home, ".lore", "config.yaml"))
	if err != nil {
		t.Errorf("[%s] config.yaml: %v", step, err)
		return
	}
	for i, line := range strings.Split(string(b), "\n") {
		if flowYAML.MatchString(line) {
			t.Errorf("[%s] config.yaml line %d is flow style: %q\n%s", step, i+1, line, b)
		}
	}
	r := m.Lore(t, "", "config", "check", "--json")
	if r.Code != 0 {
		t.Errorf("[%s] config check: %s", step, r)
		return
	}
	for _, w := range decode[checkOut](t, r).Warnings {
		t.Errorf("[%s] config check warns: %s", step, w)
	}
}

func loreOK(t testing.TB, r Result) Result {
	t.Helper()
	if r.Code != 0 {
		t.Fatalf("%s", r)
	}
	return r
}

type whoamiOut struct {
	Data struct {
		Org       string `json:"org"`
		RepoRole  string `json:"repo_role"`
		ProjectID string `json:"project_id"`
	} `json:"data"`
}

func parseWhoami(t testing.TB, s string) whoamiOut {
	t.Helper()
	var w whoamiOut
	if err := json.Unmarshal([]byte(s), &w); err != nil {
		t.Fatalf("whoami --json (%v): %s", err, s)
	}
	return w
}

// projectIDOf reads dir/.lore/data/_meta.json's project id.
func projectIDOf(t testing.TB, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".lore", "data", "_meta.json"))
	if err != nil {
		t.Fatalf("_meta.json: %v", err)
	}
	var v struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(b, &v); err != nil || v.ProjectID == "" {
		t.Fatalf("_meta.json has no project id (%v): %s", err, b)
	}
	return v.ProjectID
}

// memoryFiles lists the memory row files under dir/.lore/data whose text
// holds needle.
func memoryFiles(dir, needle string) []string {
	var out []string
	_ = filepath.WalkDir(filepath.Join(dir, ".lore", "data", "memories"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(b), needle) {
				out = append(out, p)
			}
		}
		return nil
	})
	return out
}

// activeIn is `lore setup list --json`'s active setup in dir, asked twice
// (the answer must be stable).
func (m *Machine) activeIn(t testing.TB, dir string) (name, why, detail string) {
	t.Helper()
	var first string
	for i := range 2 {
		l := m.List(t, dir)
		got := l.Active.Name + "|" + l.Active.Why
		if i == 1 && got != first {
			t.Errorf("active setup in %s changed between two runs: %s then %s", rel(m.Home, dir), first, got)
		}
		first = got
		name, why = l.Active.Name, l.Active.Why
	}
	r := m.Lore(t, dir, "setup", "list", "--json")
	var v struct {
		Active struct {
			Detail string `json:"detail"`
		} `json:"active"`
	}
	_ = json.Unmarshal([]byte(r.Stdout), &v)
	return name, why, v.Active.Detail
}

func TestReposKinds(t *testing.T) {
	t.Run("bare", func(t *testing.T) {
		t.Parallel()
		m := Build(t, ShapeReposKinds, loreBin)
		bare := m.Repos[RepoBare]
		before := treeSnap(t, bare)
		// Every repo command refuses with what the folder is and the way
		// out (a clone), never "run git init here" in a git repository.
		for _, args := range [][]string{{"init"}, {"render"}, {"sync", "status"}, {"memory", "list"}, {"memory", "add", "a note"}} {
			r := m.Lore(t, bare, args...)
			if r.Code == 0 || !strings.Contains(r.Stderr, "E_NOT_A_STORE") || !strings.Contains(r.Stderr, "bare repository") ||
				!strings.Contains(r.Stderr, "git clone") || strings.Contains(r.Stderr, "git init here") {
				t.Errorf("in a bare repo: %s", r)
			}
		}
		loreOK(t, m.Lore(t, bare, "setup", "list"))
		r := m.Lore(t, bare, "doctor")
		if !slices.Contains(doctorExits, r.Code) || !strings.Contains(r.Stdout, "in a bare git repository") {
			t.Errorf("doctor in a bare repo does not say so: %s", r)
		}
		for _, wrong := range []string{"not inside a git repo", "v0.1.17 settings not checked", "not rendered yet"} {
			if strings.Contains(r.Stdout, wrong) {
				t.Errorf("doctor in a bare repo says %q: %s", wrong, r.Stdout)
			}
		}
		if d := diffSnap(before, treeSnap(t, bare)); len(d) > 0 {
			t.Errorf("lore wrote into the bare repo: %v", d)
		}
		// Inside a checkout's .git folder: named as such.
		gitDir := filepath.Join(m.Repos[RepoLore], ".git")
		if r := m.Lore(t, gitDir, "memory", "list"); r.Code == 0 || !strings.Contains(r.Stderr, ".git folder") || strings.Contains(r.Stderr, "git init here") {
			t.Errorf("in a .git folder: %s", r)
		}
		m.CheckMachine(t, "repos_kinds/bare", Gone{})
	})

	t.Run("hook", func(t *testing.T) {
		t.Parallel()
		m := Build(t, ShapeReposKinds, loreBin)
		hooked := m.Repos[RepoHooked]
		if err := os.WriteFile(filepath.Join(hooked, "change.txt"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		m.mustGit(t, hooked, "add", "change.txt")
		m.mustGit(t, hooked, "commit", "-q", "-m", "change")
		log, _ := os.ReadFile(filepath.Join(m.Dir, HookLog))
		short := strings.TrimSpace(m.mustGit(t, hooked, "rev-parse", "--short", "HEAD"))
		// The note of this commit landed in this repo only.
		if got := memoryFiles(hooked, "hook-note "+short); len(got) != 1 {
			t.Errorf("post-commit's lore memory add: %d files in %s (want 1); hook log:\n%s", len(got), RepoHooked, log)
		}
		if got := memoryFiles(m.Repos[RepoLore], "hook-note "); len(got) != 0 {
			t.Errorf("the hook's note went into %s too: %v", RepoLore, got)
		}
		// cd into another repo inside the hook (GIT_DIR still exported): the
		// note is that repo's.
		if got := memoryFiles(m.Repos[RepoLore], "cross-hook-note"); len(got) != 1 {
			t.Errorf("lore run in %s from a hook of %s: %d notes there (want 1); hook log:\n%s", RepoLore, RepoHooked, len(got), log)
		}
		if got := memoryFiles(hooked, "cross-hook-note"); len(got) != 0 {
			t.Errorf("lore run in %s from a hook wrote into the hook's repo: %v", RepoLore, got)
		}
		// whoami from the repo's subfolder, inside the hook: this project.
		wb, err := os.ReadFile(filepath.Join(m.Dir, "hook-whoami.json"))
		if err != nil {
			t.Fatalf("hook whoami: %v\n%s", err, log)
		}
		if w := parseWhoami(t, string(wb)); w.Data.ProjectID != projectIDOf(t, hooked) {
			t.Errorf("whoami in %s/sub from a hook names project %q, want %s", RepoHooked, w.Data.ProjectID, projectIDOf(t, hooked))
		}
		// pre-push runs lore too, and the push goes through.
		remote := filepath.Join(m.Home, HookedRemote)
		remoteBefore := treeSnap(t, remote, "objects", "refs", "packed-refs", "logs")
		if out, err := m.gitIn(t, hooked, "push", "-q", "origin", "HEAD:refs/heads/main"); err != nil {
			t.Fatalf("push with lore's pre-push: %v\n%s", err, out)
		}
		pp, _ := os.ReadFile(filepath.Join(m.Dir, "prepush.txt"))
		if !strings.Contains(string(pp), "live rows") {
			t.Errorf("lore sync status in pre-push printed:\n%s", pp)
		}
		if d := diffSnap(remoteBefore, treeSnap(t, remote, "objects", "refs", "packed-refs", "logs")); len(d) > 0 {
			t.Errorf("lore wrote into the bare remote beyond the pushed refs: %v", d)
		}
		m.CheckMachine(t, "repos_kinds/hook", Gone{})
	})

	t.Run("worktree", func(t *testing.T) {
		t.Parallel()
		m := Build(t, ShapeReposKinds, loreBin)
		main, wt := m.Repos[RepoLore], m.Repos[RepoWorktree]
		loreOK(t, m.Lore(t, main, "memory", "add", "main-note"))
		m.mustGit(t, main, "add", "-A")
		m.mustGit(t, main, "commit", "-q", "-m", "note")
		m.mustGit(t, wt, "merge", "-q", "main")
		if r := loreOK(t, m.Lore(t, wt, "memory", "list")); !strings.Contains(r.Stdout, "main-note") {
			t.Errorf("the worktree does not see the main checkout's committed note: %s", r)
		}
		w := parseWhoami(t, loreOK(t, m.Lore(t, wt, "whoami", "--json")).Stdout)
		if w.Data.ProjectID != projectIDOf(t, main) {
			t.Errorf("worktree project %q, main %s", w.Data.ProjectID, projectIDOf(t, main))
		}
		loreOK(t, m.Lore(t, wt, "memory", "add", "wt-note"))
		if len(memoryFiles(wt, "wt-note")) != 1 || len(memoryFiles(main, "wt-note")) != 0 {
			t.Errorf("a note written in the worktree: %v in it, %v in the main checkout", memoryFiles(wt, "wt-note"), memoryFiles(main, "wt-note"))
		}
		loreOK(t, m.Lore(t, wt, "render"))
		loreOK(t, m.Lore(t, wt, "sync", "status"))
		// an upgraded machine: the old top-level identity: is a leftover
		// the doctor flags until its fix runs (SOURCE_OF_TRUTH row 1)
		sotRetireTopIdentity(t, m, wt, "personal")
		if r := m.Lore(t, wt, "doctor"); r.Code != 0 {
			t.Errorf("doctor in a worktree: %s", r)
		}
		m.CheckMachine(t, "repos_kinds/worktree", Gone{})
	})

	t.Run("submodule", func(t *testing.T) {
		t.Parallel()
		m := Build(t, ShapeReposKinds, loreBin)
		parent := m.Repos[RepoParent]
		sub := filepath.Join(parent, SubmodulePath)
		gitModules := filepath.Join(".git", "modules")
		before := treeSnap(t, parent, SubmodulePath, gitModules)
		loreOK(t, m.Lore(t, sub, "init", "--name", "lib"))
		loreOK(t, m.Lore(t, sub, "memory", "add", "sub-note"))
		loreOK(t, m.Lore(t, sub, "render"))
		loreOK(t, m.Lore(t, sub, "sync", "status"))
		if len(memoryFiles(sub, "sub-note")) != 1 {
			t.Errorf("the submodule's note is not in the submodule")
		}
		w := parseWhoami(t, loreOK(t, m.Lore(t, sub, "whoami", "--json")).Stdout)
		if w.Data.ProjectID != projectIDOf(t, sub) {
			t.Errorf("whoami in the submodule names %q, want its own project", w.Data.ProjectID)
		}
		// The hooks went to the submodule's git folder, not the parent's.
		hook, err := os.ReadFile(filepath.Join(parent, gitModules, SubmodulePath, "hooks", "pre-commit"))
		if err != nil || !strings.Contains(string(hook), "lore") {
			t.Errorf("the submodule's pre-commit hook (%v): %s", err, hook)
		}
		// The parent: read commands only (see the report on writes).
		loreOK(t, m.Lore(t, parent, "setup", "list"))
		if r := m.Lore(t, parent, "doctor"); !slices.Contains(doctorExits, r.Code) || !strings.Contains(r.Stdout, "no lore project") {
			t.Errorf("doctor in the parent (no lore project) says: %s", r)
		}
		if d := diffSnap(before, treeSnap(t, parent, SubmodulePath, gitModules)); len(d) > 0 {
			t.Errorf("lore in the submodule wrote into the parent: %v", d)
		}
		m.CheckMachine(t, "repos_kinds/submodule", Gone{})
	})

	t.Run("detached", func(t *testing.T) {
		t.Parallel()
		m := Build(t, ShapeReposKinds, loreBin)
		det := m.Repos[RepoDetached]
		head := m.mustGit(t, det, "rev-parse", "HEAD")
		for _, args := range [][]string{{"memory", "add", "detached-note"}, {"render"}, {"sync", "status"}, {"memory", "list"}} {
			loreOK(t, m.Lore(t, det, args...))
		}
		// an upgraded machine: the old top-level identity: is a leftover
		// the doctor flags until its fix runs (SOURCE_OF_TRUTH row 1)
		sotRetireTopIdentity(t, m, det, "personal")
		if r := m.Lore(t, det, "doctor"); r.Code != 0 {
			t.Errorf("doctor at a detached HEAD: %s", r)
		}
		if _, err := m.gitIn(t, det, "symbolic-ref", "-q", "HEAD"); err == nil {
			t.Errorf("lore checked out a branch at a detached HEAD")
		}
		if got := m.mustGit(t, det, "rev-parse", "HEAD"); got != head {
			t.Errorf("HEAD moved from %s to %s", head, got)
		}
		if out := m.mustGit(t, det, "branch", "--list"); strings.Count(out, "\n") != 2 { // main and "(HEAD detached …)"
			t.Errorf("branches after lore at a detached HEAD:\n%s", out)
		}
		m.CheckMachine(t, "repos_kinds/detached", Gone{})
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		m := Build(t, ShapeReposKinds, loreBin)
		empty := m.Repos[RepoPlain] // git init, no commit
		for _, args := range [][]string{{"init"}, {"render"}, {"sync", "status"}, {"memory", "add", "first-note"}, {"memory", "list"}} {
			loreOK(t, m.Lore(t, empty, args...))
		}
		// the memory added after the render made LORE.md stale: the doctor
		// says so with its fix, and a render clears it (SOURCE_OF_TRUTH row 10)
		if r := m.Lore(t, empty, "doctor"); !strings.Contains(r.Stdout, "rendered_copy: .lore/LORE.md is stale") || !strings.Contains(r.Stdout, "fix: lore render") {
			t.Errorf("doctor after a memory added since the render does not flag LORE.md stale: %s", r)
		}
		loreOK(t, m.Lore(t, empty, "render"))
		// an upgraded machine: the old top-level identity: is a leftover
		// the doctor flags until its fix runs (SOURCE_OF_TRUTH row 1)
		sotRetireTopIdentity(t, m, empty, "personal")
		if r := m.Lore(t, empty, "doctor"); r.Code != 0 {
			t.Errorf("doctor in a repo with no commit: %s", r)
		}
		if _, err := m.gitIn(t, empty, "rev-parse", "-q", "--verify", "HEAD"); err == nil {
			t.Errorf("lore made a commit in a repo with none")
		}
		m.CheckMachine(t, "repos_kinds/empty", Gone{})
	})

	// A command run in a subfolder of a repo resolves the same org, role
	// and project as at the repo's top (an agent's cwd is often src/).
	t.Run("subfolder", func(t *testing.T) {
		t.Parallel()
		m := Build(t, ShapeReposKinds, loreBin)
		top := m.Repos[RepoLore]
		deep := filepath.Join(top, "src", "deep")
		if err := os.MkdirAll(deep, 0o755); err != nil {
			t.Fatal(err)
		}
		loreOK(t, m.Lore(t, top, "org", "use", ReposOrg))
		atTop := parseWhoami(t, loreOK(t, m.Lore(t, top, "whoami", "--json")).Stdout)
		if atTop.Data.Org != ReposOrg || atTop.Data.ProjectID != projectIDOf(t, top) {
			t.Fatalf("whoami at the top: %+v", atTop.Data)
		}
		for _, dir := range []string{filepath.Join(top, "src"), deep} {
			got := parseWhoami(t, loreOK(t, m.Lore(t, dir, "whoami", "--json")).Stdout)
			if got.Data != atTop.Data {
				t.Errorf("whoami in %s: %+v; at the top: %+v", rel(m.Home, dir), got.Data, atTop.Data)
			}
		}
		loreOK(t, m.Lore(t, top, "render"))
		if r := m.Lore(t, deep, "doctor"); strings.Contains(r.Stdout, "not rendered yet") {
			t.Errorf("doctor in a subfolder looks for LORE.md there: %s", r)
		}
		m.CheckMachine(t, "repos_kinds/subfolder", Gone{})
	})
}

// TestReposOverlap: which setup applies where, with nested folders, a
// folder only one setup has, a repo pin and a default; an edit that would
// make two setups tie is refused and writes nothing; a tie written by hand
// is named by setup list and doctor until it is fixed.
func TestReposOverlap(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeReposOverlap, loreBin)
	shared := filepath.Join(m.Home, OverlapShared)
	elsewhere := filepath.Join(m.Home, "elsewhere")
	loreOK(t, m.Lore(t, m.Repos[RepoLore], "setup", "use", OverlapNarrow, "--here"))
	m.checkBlockConfig(t, "pin")
	type want struct{ dir, name, why string }
	expect := func(step string, ws []want) {
		t.Helper()
		for _, w := range ws {
			if name, why, detail := m.activeIn(t, w.dir); name != w.name || why != w.why {
				t.Errorf("[%s] in %s: setup %q (%s: %s), want %q (%s)", step, rel(m.Home, w.dir), name, why, detail, w.name, w.why)
			}
		}
	}
	base := []want{
		{m.Repos[RepoPlain], OverlapWide, "folder"},     // ~/code/** only
		{m.Repos[RepoWorkApp], OverlapNarrow, "folder"}, // ~/code/work/** is more specific
		{m.Repos[RepoLore], OverlapNarrow, "repo"},      // the pin beats wide's ~/code/**
		{shared, OverlapWide, "folder"},                 // only wide has ~/shared/**
		{elsewhere, OverlapWide, "default"},             // no folder matches
	}
	expect("as built", base)

	// An exact duplicate folder through setup edit: refused, nothing saved.
	cfgPath := filepath.Join(m.Home, ".lore", "config.yaml")
	cfgBefore, _ := os.ReadFile(cfgPath)
	r := m.Lore(t, "", "setup", "edit", OverlapNarrow, "--folder", "~/code/work/**", "--folder", "~/shared/**")
	if r.Code == 0 || !strings.Contains(r.Stderr, "E_SETUP_AMBIGUOUS") || !strings.Contains(r.Stderr, "~/shared/**") || !strings.Contains(r.Stderr, "nothing was saved") {
		t.Errorf("an edit that ties two setups on ~/shared/**: %s", r)
	}
	if after, _ := os.ReadFile(cfgPath); string(after) != string(cfgBefore) {
		t.Errorf("the refused edit changed config.yaml:\n%s", after)
	}
	expect("refused tie", base)
	// A folder that nests (no tie) is accepted.
	loreOK(t, m.Lore(t, "", "setup", "edit", OverlapNarrow, "--folder", "~/code/work/**", "--folder", "~/shared/x/**"))
	m.checkBlockConfig(t, "nested edit")
	expect("nested edit", []want{{shared, OverlapNarrow, "folder"}, {m.Repos[RepoPlain], OverlapWide, "folder"}})
	loreOK(t, m.Lore(t, "", "setup", "edit", OverlapNarrow, "--folder", "~/code/work/**"))
	m.CheckMachine(t, "repos_overlap/after edits", Gone{})

	// The same tie written by hand: setup list names it, doctor --all is
	// broken and names the check and the folder.
	hand := strings.Replace(string(cfgBefore), "      - ~/code/work/**\n", "      - ~/code/work/**\n      - ~/shared/**\n", 1)
	if err := os.WriteFile(cfgPath, []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	if name, _, detail := m.activeIn(t, shared); name != "" || !strings.Contains(detail, "E_SETUP_AMBIGUOUS") {
		t.Errorf("a folder two setups tie on: setup %q (%s)", name, detail)
	}
	expect("hand tie, elsewhere", base[:3])
	d := m.Lore(t, "", "doctor", "--all")
	if d.Code != 2 || !strings.Contains(d.Stdout, "folders.overlap") || !strings.Contains(d.Stdout, "~/shared/**") {
		t.Errorf("doctor --all with a hand-written tie: %s", d)
	}
	// Fixing it with setup edit clears doctor.
	loreOK(t, m.Lore(t, "", "setup", "edit", OverlapNarrow, "--folder", "~/code/work/**"))
	m.checkBlockConfig(t, "tie fixed")
	expect("tie fixed", base)
	if d := m.Lore(t, "", "doctor", "--all"); d.Code == 2 || strings.Contains(d.Stdout, "folders.overlap") {
		t.Errorf("doctor --all after the tie is fixed: %s", d)
	}
	m.CheckMachine(t, "repos_overlap/fixed", Gone{})
}

// TestReposHistory walks a setup history past the graveyard shape:
// trashed then re-added under the same name, purged then re-added, renamed
// twice through the trash (its repo pin follows), and the default moved to
// a setup that is then trashed and restored.
func TestReposHistory(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeReposHistory, loreBin)
	mk := func(name string, dflt bool) {
		t.Helper()
		d := "false"
		if dflt {
			d = "true"
		}
		loreOK(t, m.Lore(t, "", "setup", "apply", name, "--step", "kind", "--answers", `{"kind":"solo","name":"`+name+`","default":`+d+`}`))
		loreOK(t, m.Lore(t, "", "setup", "apply", name, "--step", "identity", "--answers", `{"name":"`+PersonName+`","email":"`+name+`@example.test"}`))
		m.checkBlockConfig(t, "add "+name)
	}
	names := func() []string {
		var out []string
		for _, s := range m.List(t, "").Setups {
			out = append(out, s.Name)
		}
		return out
	}

	// 1. trashed, then re-added with the same name: both exist; the
	// trashed one comes back only under another name.
	mk("side", false)
	loreOK(t, m.Lore(t, "", "setup", "remove", "side"))
	m.checkBlockConfig(t, "trash side")
	mk("side", false)
	if r := m.Lore(t, "", "setup", "trash", "restore", "side"); r.Code == 0 || !strings.Contains(r.Stderr, "--as") {
		t.Errorf("restore over a live setup of the same name: %s", r)
	}
	loreOK(t, m.Lore(t, "", "setup", "trash", "restore", "side", "--as", "side-old"))
	m.checkBlockConfig(t, "restore side-old")
	if got := names(); !slices.Contains(got, "side") || !slices.Contains(got, "side-old") {
		t.Errorf("after re-add and restore --as: %v", got)
	}
	m.CheckMachine(t, "history/trash re-add", Gone{})

	// 2. purged, then re-added: config.yaml stays block style and readable
	// (the owner's flow-list report), and the new one is a fresh setup.
	loreOK(t, m.Lore(t, "", "setup", "remove", "side-old"))
	loreOK(t, m.Lore(t, "", "setup", "trash", "purge", "side-old", "--confirm"))
	m.checkBlockConfig(t, "purge side-old")
	m.CheckMachine(t, "history/purge", Gone{Purged: []string{"side-old"}})
	mk("side-old", false)
	if r := loreOK(t, m.Lore(t, "", "setup", "show", "side-old")); !strings.Contains(r.Stdout, "side-old@example.test") {
		t.Errorf("re-added side-old: %s", r)
	}
	m.CheckMachine(t, "history/purge re-add", Gone{})

	// 3. renamed twice through the trash; the repo pin follows.
	mk("r1", false)
	loreOK(t, m.Lore(t, "", "setup", "apply", "r1", "--step", "folders", "--answers", `{"folders":["~/code/**"],"default":false}`))
	loreOK(t, m.Lore(t, m.Repos[RepoLore], "setup", "use", "r1", "--here"))
	for _, step := range [][2]string{{"r1", "r2"}, {"r2", "r3"}} {
		loreOK(t, m.Lore(t, "", "setup", "remove", step[0]))
		loreOK(t, m.Lore(t, "", "setup", "trash", "restore", step[0], "--as", step[1]))
		m.checkBlockConfig(t, "rename "+step[0]+"→"+step[1])
	}
	if name, why, _ := m.activeIn(t, m.Repos[RepoLore]); name != "r3" || why != "repo" {
		t.Errorf("the pin of a setup renamed twice: %s (%s), want r3 (repo)", name, why)
	}
	if name, why, _ := m.activeIn(t, m.Repos[RepoPlain]); name != "r3" || why != "folder" {
		t.Errorf("the folders of a setup renamed twice: %s (%s), want r3 (folder)", name, why)
	}
	for _, old := range []string{"r1", "r2"} {
		if r := m.Lore(t, "", "setup", "show", old); r.Code == 0 || !strings.Contains(r.Stderr, "E_SETUP_NOT_FOUND") {
			t.Errorf("the old name %s still answers: %s", old, r)
		}
	}
	if tr := m.Trash(t); len(tr) != 0 {
		t.Errorf("the trash after two renames: %v", tr)
	}
	m.CheckMachine(t, "history/renamed twice", Gone{})

	// 4. the default moved to a setup that is then trashed: no setup is
	// the default (lore never promotes one by itself), doctor names the
	// setups lore can no longer pick with the --default fix, and the
	// restore brings the default back.
	mk("dflt", false)
	loreOK(t, m.Lore(t, "", "setup", "edit", "dflt", "--default"))
	m.checkBlockConfig(t, "default to dflt")
	if name, why, _ := m.activeIn(t, ""); name != "dflt" || why != "default" {
		t.Errorf("after the default moved: %s (%s)", name, why)
	}
	loreOK(t, m.Lore(t, "", "setup", "remove", "dflt"))
	m.checkBlockConfig(t, "trash dflt")
	if name, why, _ := m.activeIn(t, ""); name != "" || why != "none" {
		t.Errorf("with the default setup in the trash: %s (%s), want none", name, why)
	}
	d := m.Lore(t, "", "doctor", "--all")
	if d.Code != 1 || !strings.Contains(d.Stdout, "folders.reachable") || !strings.Contains(d.Stdout, "fix: lore setup edit --name side --default") {
		t.Errorf("doctor --all with the default trashed: %s", d)
	}
	m.CheckMachine(t, "history/default trashed", Gone{Trashed: []string{"dflt"}})
	loreOK(t, m.Lore(t, "", "setup", "trash", "restore", "dflt"))
	m.checkBlockConfig(t, "restore dflt")
	if name, why, _ := m.activeIn(t, ""); name != "dflt" || why != "default" {
		t.Errorf("after the default setup is restored: %s (%s)", name, why)
	}
	m.CheckMachine(t, "history/default restored", Gone{})
}
