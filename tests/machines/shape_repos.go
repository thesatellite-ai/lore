//go:build machines && !windows

package machines

// shape_repos.go — machines whose repos are not the plain "git init, lore
// init" kind, and whose setups have a history: what a developer's ~/code
// and ~/.lore look like after a few months.
//
//	repos_kinds    a repo whose hooks call lore (with a bare remote to push
//	               to), a bare repo, a linked worktree, a submodule inside a
//	               parent lore never ran in, a detached HEAD, an empty repo
//	repos_overlap  two setups with nested folders (~/code/** and
//	               ~/code/work/**), a folder only one of them has, a repo pin
//	repos_history  the v0.1.17 migration's flow-style config.yaml, before
//	               any trash, purge, rename or default move (the test makes
//	               the history step by step and checks after each one)
//
// Every name is a placeholder; the shape folders are under ~/code so the
// mode invariants (CheckModes) walk them.

import (
	"os"
	"path/filepath"
)

// Shape names of this file.
const (
	ShapeReposKinds   = "repos_kinds"
	ShapeReposOverlap = "repos_overlap"
	ShapeReposHistory = "repos_history"
)

// Repo folders of ShapeReposKinds (Machine.Repos keys and paths under ~/code).
const (
	RepoHooked    = "hooked"        // a lore repo whose post-commit / pre-push also call lore
	RepoBare      = "bare.git"      // a bare repository (no work tree)
	RepoWorktree  = "has-lore-wt"   // a linked worktree of has-lore (branch wt)
	RepoLib       = "lib"           // the submodule's upstream
	RepoParent    = "parent"        // a repo with lib as submodule vendor/lib; lore never ran in it
	RepoDetached  = "detached"      // a lore repo checked out at a commit, no branch
	SubmodulePath = "vendor/lib"    // the submodule inside RepoParent
	HookedRemote  = "remotes/h.git" // RepoHooked's origin, under Home (not ~/code)
	// HookLog collects what the hook lines print (under Machine.Dir).
	HookLog = "hook.log"
)

// Setups and folders of ShapeReposOverlap.
const (
	OverlapWide   = "wide"   // ~/code/** and ~/shared/**, the default
	OverlapNarrow = "narrow" // ~/code/work/**
	OverlapShared = "shared/x"
)

// Org of the subfolder check: has-lore is pinned to it with lore org use.
const ReposOrg = "acme"

func init() {
	RegisterShape(ExtraShape{Name: ShapeReposKinds, Build: func(b *builder) { b.reposKinds() }})
	RegisterShape(ExtraShape{Name: ShapeReposOverlap, Build: func(b *builder) { b.reposOverlap() }})
	RegisterShape(ExtraShape{Name: ShapeReposHistory, Build: func(b *builder) {
		b.ssh(false)
		b.gh([]string{LoginAlpha}, LoginAlpha)
		b.migratedHome("main")
	}})
}

// reposKindsConfig is one solo default setup over ~/code plus an org the
// subfolder check pins has-lore to.
var reposKindsConfig = `identity:
  key: human:` + PersonEmail + `
  name: ` + PersonName + `
orgs:
  - name: ` + ReposOrg + `
    identity:
      key: human:` + WorkEmail + `
      name: ` + PersonName + `
setups:
  - name: personal
    kind: solo
    default: true
    identity:
      key: human:` + PersonEmail + `
      name: ` + PersonName + `
    folders:
      - ~/code/**
`

// hookLines are what a person appends to lore's own hook files: lore run
// from inside the hook, so with git's GIT_DIR / GIT_INDEX_FILE exported,
// in the repo, in a subfolder of it, and in another repo.
const postCommitLines = `
# --- added by hand: notes from every commit ---
lore memory add "hook-note $(git rev-parse --short HEAD)" >> "$FAKE_W/hook.log" 2>&1
( cd sub && lore whoami --json > "$FAKE_W/hook-whoami.json" 2>> "$FAKE_W/hook.log" )
( cd "$HOME/code/has-lore" && lore memory add "cross-hook-note" >> "$FAKE_W/hook.log" 2>&1 )
`

// reposLoreDir is the folder under the machine's Dir that puts the lore
// under test on PATH as `lore` for the repos_kinds hooks.
const reposLoreDir = "repos-bin"

const prePushLines = `
# --- added by hand ---
lore sync status > "$FAKE_W/prepush.txt" 2>&1 || exit 1
`

func (b *builder) git(dir string, args ...string) {
	b.t.Helper()
	b.run(dir, "/usr/bin/git", args...)
}

func (b *builder) commitAll(dir, msg string) {
	b.t.Helper()
	b.git(dir, "add", "-A")
	b.git(dir, "commit", "-q", "-m", msg)
}

func (b *builder) appendFile(p, body string) {
	b.t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		b.t.Fatal(err)
	}
	if _, err := f.WriteString(body); err != nil {
		b.t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		b.t.Fatal(err)
	}
}

func (b *builder) reposKinds() {
	m := b.m
	b.ssh(false)
	b.gh([]string{LoginAlpha}, LoginAlpha)
	b.loreHome()
	b.file(".lore/config.yaml", reposKindsConfig, 0o600)
	// The hooks call `lore`: it is on PATH as it is on a real machine,
	// under that name. The binary under test need not be called lore (the
	// task parts build lore-machines-<part>), so a folder of the machine's
	// own holds a lore link to it; with the binary's own folder on PATH a
	// hook found no lore and the pre-push line refused the shape's push.
	onPath := filepath.Join(m.Dir, reposLoreDir)
	b.dir(onPath, 0o755)
	if err := os.Symlink(m.bin, filepath.Join(onPath, "lore")); err != nil {
		b.t.Fatal(err)
	}
	m.PathPrefix = []string{onPath}
	code := filepath.Join(m.Home, "code")
	add := func(name string) string {
		p := filepath.Join(code, name)
		m.Repos[name] = p
		return p
	}

	// hooked: lore repo with a sub folder, a bare origin, hand-added
	// lines in lore's post-commit and pre-push.
	hooked := add(RepoHooked)
	b.dir(hooked, 0o755)
	b.git(hooked, "init", "-q")
	b.lore(hooked, "init", "--name", RepoHooked)
	b.file(filepath.Join(hooked, "sub", "readme.txt"), "sub folder\n", 0o644)
	b.commitAll(hooked, "start")
	remote := filepath.Join(m.Home, HookedRemote)
	b.dir(remote, 0o755)
	b.git(remote, "init", "-q", "--bare")
	b.git(hooked, "remote", "add", "origin", remote)
	b.appendFile(filepath.Join(hooked, ".git", "hooks", "post-commit"), postCommitLines)
	b.appendFile(filepath.Join(hooked, ".git", "hooks", "pre-push"), prePushLines)

	// bare.git: a bare repository with one pushed commit.
	bare := add(RepoBare)
	b.dir(bare, 0o755)
	b.git(bare, "init", "-q", "--bare")
	b.git(hooked, "push", "-q", bare, "HEAD:refs/heads/main")

	// has-lore: committed, with a linked worktree on branch wt.
	b.commitAll(m.Repos[RepoLore], "start")
	b.git(m.Repos[RepoLore], "worktree", "add", "-q", add(RepoWorktree), "-b", "wt")

	// parent with lib as a submodule; lore never ran in either.
	lib := add(RepoLib)
	b.dir(lib, 0o755)
	b.git(lib, "init", "-q")
	b.file(filepath.Join(lib, "lib.txt"), "lib\n", 0o644)
	b.commitAll(lib, "lib")
	parent := add(RepoParent)
	b.dir(parent, 0o755)
	b.git(parent, "init", "-q")
	b.file(filepath.Join(parent, "main.txt"), "parent\n", 0o644)
	b.commitAll(parent, "parent")
	b.git(parent, "-c", "protocol.file.allow=always", "submodule", "add", "-q", lib, SubmodulePath)
	b.git(parent, "commit", "-q", "-m", "add lib")

	// detached: a lore repo at its first commit, no branch.
	det := add(RepoDetached)
	b.dir(det, 0o755)
	b.git(det, "init", "-q")
	b.lore(det, "init", "--name", RepoDetached)
	b.commitAll(det, "one")
	b.file(filepath.Join(det, "two.txt"), "two\n", 0o644)
	b.commitAll(det, "two")
	b.git(det, "checkout", "-q", "--detach", "HEAD~1")
}

// reposOverlapConfig: wide (default) over ~/code/** and ~/shared/**,
// narrow over ~/code/work/** only.
var reposOverlapConfig = `setups:
  - name: ` + OverlapWide + `
    kind: solo
    default: true
    identity:
      key: human:wide@example.test
      name: ` + PersonName + `
    folders:
      - ~/code/**
      - ~/shared/**
  - name: ` + OverlapNarrow + `
    kind: solo
    identity:
      key: human:narrow@example.test
      name: ` + PersonName + `
    folders:
      - ~/code/work/**
`

func (b *builder) reposOverlap() {
	b.ssh(false)
	b.gh([]string{LoginAlpha}, LoginAlpha)
	b.loreHome()
	b.file(".lore/config.yaml", reposOverlapConfig, 0o600)
	shared := filepath.Join(b.m.Home, OverlapShared)
	b.dir(shared, 0o755)
	b.git(shared, "init", "-q")
	b.dir(filepath.Join(b.m.Home, "elsewhere"), 0o755)
}
