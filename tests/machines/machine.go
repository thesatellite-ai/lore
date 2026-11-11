//go:build machines && !windows

// Package machines builds realistic machines (a HOME with ~/.ssh,
// ~/.gitconfig, ~/.lore and repo folders, plus fake gh and ssh programs)
// from named shapes, and runs the real lore binary in them. The shapes are
// taken from what real users' machines look like, not from what a feature
// needs: backup files next to the files lore reads (*.migrated,
// config.bak-*), keys without a .pub, an IdentityFile that is gone, several
// gh logins with the wrong one active, ssh aliases per account written with
// mixed case and indentation, a socket in ~/.ssh, a config.yaml in flow
// style, repo folders that already hold CLAUDE.md or AGENTS.md.
//
// Every name, login, email and key here is a placeholder; keys are made
// fresh with ssh-keygen per machine and never committed.
//
// Contract with the tests: Build returns a Machine whose Home is a fresh
// directory owned by the test; Lore runs the binary with HOME pointing at
// it and only the fake gh/ssh, /usr/bin and /bin on PATH.
package machines

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/e2eenv"
)

// Shape names, the closed set Build accepts.
const (
	// ShapeFresh is a machine that never ran lore: git, one key, one gh login.
	ShapeFresh = "fresh"
	// ShapeV0117 is a v0.1.17 install before `lore migrate run`:
	// ~/.lore/identity.yaml, identity.toml and lore.db, no setups.
	ShapeV0117 = "v0117"
	// ShapeMigrated is after the migration: a flow-style config.yaml with
	// one setup and empty identity:/personal: keys, the identity
	// *.migrated backups (naming a person nobody else names), machine.json
	// and the state/cache folders, all owner-only as a real install has them.
	ShapeMigrated = "migrated"
	// ShapeOwnerLike is a developer with three GitHub accounts: gh holds
	// all three with the wrong one active, ~/.ssh/config has one alias per
	// account (mixed indentation, "Hostname" spelled two ways), one key has
	// no .pub, one alias points at a key that is gone, plus a non-GitHub
	// host, config backups, known_hosts.old and a control socket.
	ShapeOwnerLike = "owner_like"
	// ShapeOrgMember is a member of one org with a member setup and a solo one.
	ShapeOrgMember = "org_member"
	// ShapeGraveyard holds setups in every after-life: one disabled, one
	// trashed, one purged with its trash.purged tombstone, and an identity
	// left at the top of config.yaml by a purge from before tombstones.
	ShapeGraveyard = "graveyard"
)

// AllShapes lists every shape, in run order.
func AllShapes() []string {
	return []string{ShapeFresh, ShapeV0117, ShapeMigrated, ShapeOwnerLike, ShapeOrgMember, ShapeGraveyard}
}

// EveryShape is the six built-in shapes first, then
// every shape a shape_*.go file registered, in name order.
func EveryShape() []string { return append(AllShapes(), extraShapeNames()...) }

// Placeholder people and accounts. MigratedOnlyEmail appears only in
// identity.toml.migrated, so any lore output naming it read a backup.
const (
	PersonName        = "Pat Example"
	PersonEmail       = "pat@example.test"
	MigratedOnlyEmail = "migrated-only@example.test"
	OldEmail          = "old-install@example.test"
	WorkEmail         = "pat@acme.example"

	LoginAlpha   = "alpha-dev"   // personal account, key id_ed25519
	LoginBravo   = "bravo-work"  // work account, key bravo_key (gh's active login)
	LoginCharlie = "charlie-oss" // account whose key has no .pub
)

// Repo folder names under ~/code (Machine.Repos keys).
const (
	RepoLore    = "has-lore"   // a git repo where lore init already ran
	RepoClaude  = "has-claude" // a git repo with a hand-written CLAUDE.md
	RepoAgents  = "has-agents" // a git repo with a hand-written AGENTS.md
	RepoPlain   = "plain-git"  // a git repo lore never saw
	RepoNonGit  = "not-git"    // a folder that is no git repo
	RepoWorkApp = "work/app"   // the lifecycle setup's folder (a lore repo)
)

// Hand-written pointer-file bodies; lore must keep them.
const (
	ClaudeBody = "# Team rules\n\nAlways run the linter.\n"
	AgentsBody = "# Agents\n\nUse tabs.\n"
)

// Machine is one built machine.
type Machine struct {
	Shape string
	// Dir holds Home, fakebin, tmp and the fakes' data.
	Dir  string
	Home string
	// Repos maps a Repo* name to its absolute folder.
	Repos map[string]string
	bin   string
	// PathPrefix holds folders put before fakebin on PATH (a shape with two
	// lore binaries on PATH sets it); ExtraEnv holds KEY=value pairs added
	// after the base environment (later entries win).
	PathPrefix []string
	ExtraEnv   []string
	// MacCaseFold is set by env_macos when the machine dir's file system
	// is case-insensitive (its other-case repo path is then no symlink).
	MacCaseFold bool
}

// Result is one lore run.
type Result struct {
	Args   []string
	Stdout string
	Stderr string
	Code   int
}

func (r Result) String() string {
	return fmt.Sprintf("lore %s → exit %d\nstdout: %s\nstderr: %s", strings.Join(r.Args, " "), r.Code, r.Stdout, r.Stderr)
}

// runTimeout bounds one lore run.
const runTimeout = 90 * time.Second

// Lore runs the binary in cwd ("" = Home).
func (m *Machine) Lore(t testing.TB, cwd string, args ...string) Result {
	t.Helper()
	if cwd == "" {
		cwd = m.Home
	}
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, m.bin, args...)
	c.Dir = cwd
	c.Env = m.env()
	var so, se strings.Builder
	c.Stdout, c.Stderr = &so, &se
	err := c.Run()
	r := Result{Args: args, Stdout: so.String(), Stderr: se.String()}
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("lore %s: %v", strings.Join(args, " "), err)
		}
		r.Code = ee.ExitCode()
	}
	return r
}

// deadAPI is a GitHub API address nothing listens on: a lookup fails at
// once instead of reaching the network.
const deadAPI = "http://127.0.0.1:9"

func (m *Machine) env() []string {
	path := filepath.Join(m.Dir, "fakebin") + ":/usr/bin:/bin"
	if len(m.PathPrefix) > 0 {
		path = strings.Join(m.PathPrefix, ":") + ":" + path
	}
	// e2eenv: the gate's Go runtime settings for lore processes (LORE_E2E_GOENV).
	env := append([]string{
		"PATH=" + path,
		"HOME=" + m.Home, "TERM=dumb", "LANG=C.UTF-8", "GIT_CONFIG_NOSYSTEM=1",
		"LORE_NO_UPDATE_CHECK=1", "LORE_HUMAN=1", "LORE_GITHUB_API=" + deadAPI,
		"TMPDIR=" + filepath.Join(m.Dir, "tmp"), "FAKE_W=" + m.Dir,
	}, e2eenv.LoreGoEnv()...)
	return append(env, m.ExtraEnv...)
}

// Build makes a machine of the shape under a fresh short directory
// (unix socket paths are limited to ~100 bytes) removed when t ends.
func Build(t testing.TB, shape, bin string) *Machine {
	t.Helper()
	dir, err := os.MkdirTemp("", "lm-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	// The real path: on macOS the temp folder /var/... is /private/var/...,
	// and lore and PlacesBefore name files by their resolved paths, so a
	// /var root made every write of a linked file look foreign.
	if real, rerr := filepath.EvalSymlinks(dir); rerr == nil {
		dir = real
	}
	extra, isExtra := extraShapes[shape]
	homeName := "h"
	if isExtra && extra.HomeName != "" {
		homeName = extra.HomeName
	}
	m := &Machine{Shape: shape, Dir: dir, Home: filepath.Join(dir, homeName), Repos: map[string]string{}, bin: bin}
	b := &builder{t: t, m: m}
	b.dir(m.Home, 0o755)
	b.dir(filepath.Join(dir, "tmp"), 0o700)
	b.fakes()
	b.gitconfig(shape == ShapeOwnerLike)
	b.repos()
	switch shape {
	case ShapeFresh:
		b.ssh(false)
		b.gh([]string{LoginAlpha}, LoginAlpha)
	case ShapeV0117:
		b.ssh(false)
		b.gh([]string{LoginAlpha}, LoginAlpha)
		b.loreHome()
		b.file(".lore/identity.yaml", "stable_key: human:"+OldEmail+"\ndisplay_name: Old Name\n", 0o644)
		b.file(".lore/identity.toml", "stable_key = \"human:"+OldEmail+"\"\ndisplay_name = \"Old Name\"\n", 0o644)
		b.file(".lore/lore.db", "", 0o644)
	case ShapeMigrated:
		b.ssh(false)
		b.gh([]string{LoginAlpha}, LoginAlpha)
		b.migratedHome("main")
	case ShapeOwnerLike:
		b.ssh(true)
		// gh holds all three; bravo is active although the personal
		// setup wants alpha.
		b.gh([]string{LoginAlpha, LoginBravo, LoginCharlie}, LoginBravo)
		b.migratedHome("main")
	case ShapeOrgMember:
		b.ssh(true)
		b.gh([]string{LoginAlpha, LoginBravo}, LoginAlpha)
		b.loreHome()
		b.file(".lore/config.yaml", orgConfig, 0o600)
	case ShapeGraveyard:
		b.ssh(false)
		b.gh([]string{LoginAlpha}, LoginAlpha)
		b.graveyard()
	default:
		if !isExtra {
			t.Fatalf("machines: unknown shape %q", shape)
		}
		extra.Build(b)
	}
	return m
}

// orgConfig is a member of acme with a member setup and a solo one.
var orgConfig = `identity:
    key: human:` + PersonEmail + `
    name: ` + PersonName + `
orgs:
    - name: acme
      identity: {key: "human:` + WorkEmail + `"}
setups:
    - name: personal
      kind: solo
      default: true
      folders: [~/code/**]
    - name: acme
      kind: member
      org: acme
      folders: [~/acme/**]
`

type builder struct {
	t testing.TB
	m *Machine
}

func (b *builder) dir(p string, mode os.FileMode) {
	b.t.Helper()
	if err := os.MkdirAll(p, mode); err != nil {
		b.t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		b.t.Fatal(err)
	}
}

// file writes rel (under Home, or absolute) with mode, making its folder.
func (b *builder) file(rel, body string, mode os.FileMode) {
	b.t.Helper()
	p := rel
	if !filepath.IsAbs(p) {
		p = filepath.Join(b.m.Home, rel)
	}
	// The folder keeps its mode when it exists (~/.lore is 0700).
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		b.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		b.t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		b.t.Fatal(err)
	}
}

func (b *builder) run(cwd string, name string, args ...string) {
	b.t.Helper()
	c := exec.Command(name, args...)
	c.Dir = cwd
	c.Env = b.m.env()
	if out, err := c.CombinedOutput(); err != nil {
		b.t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func (b *builder) gitconfig(includeIf bool) {
	body := "[user]\n\tname = " + PersonName + "\n\temail = " + PersonEmail + "\n[init]\n\tdefaultBranch = main\n"
	if includeIf {
		body += "[includeIf \"gitdir:~/work/\"]\n\tpath = ~/.gitconfig-work\n[includeIf \"gitdir:~/oss/\"]\n\tpath = ~/.gitconfig-missing\n"
		b.file(".gitconfig-work", "[user]\n\temail = "+WorkEmail+"\n", 0o644)
	}
	b.file(".gitconfig", body, 0o644)
}

func (b *builder) repos() {
	code := filepath.Join(b.m.Home, "code")
	for _, r := range []string{RepoLore, RepoClaude, RepoAgents, RepoPlain, RepoWorkApp} {
		p := filepath.Join(code, r)
		b.dir(p, 0o755)
		b.run(p, "/usr/bin/git", "init", "-q")
		b.m.Repos[r] = p
	}
	nongit := filepath.Join(code, RepoNonGit)
	b.dir(nongit, 0o755)
	b.file(filepath.Join(nongit, "notes.txt"), "plain notes\n", 0o644)
	b.m.Repos[RepoNonGit] = nongit
	b.file(filepath.Join(b.m.Repos[RepoClaude], "CLAUDE.md"), ClaudeBody, 0o644)
	b.file(filepath.Join(b.m.Repos[RepoAgents], "AGENTS.md"), AgentsBody, 0o644)
	// has-lore: lore init on a bare ~/.lore, which is then wiped so the
	// shape decides ~/.lore (setupe2e does the same).
	b.lore(b.m.Repos[RepoLore], "init", "--name", "has-lore")
	// work/app is a lore repo too: a pin needs its repo id.
	b.lore(b.m.Repos[RepoWorkApp], "init", "--name", "work-app")
	if err := os.RemoveAll(filepath.Join(b.m.Home, ".lore")); err != nil {
		b.t.Fatal(err)
	}
}

func (b *builder) lore(cwd string, args ...string) {
	b.t.Helper()
	if r := b.m.Lore(b.t, cwd, args...); r.Code != 0 {
		b.t.Fatalf("fixture: %s", r)
	}
}

// loreHome makes ~/.lore with the folders a real install has.
func (b *builder) loreHome() {
	for _, d := range []string{".lore", ".lore/cache", ".lore/clones", ".lore/render", ".lore/tmp", ".lore/state"} {
		b.dir(filepath.Join(b.m.Home, d), 0o700)
	}
}

// migratedHome is ~/.lore after `lore migrate run`, shaped like a real
// one: flow-style setups line, empty identity:/personal: keys, backups.
func (b *builder) migratedHome(setup string) {
	b.loreHome()
	cfg := "setups: [{name: " + setup + ", kind: solo, default: true, identity: {key: 'human:" + PersonEmail + "', name: " + PersonName + ", handle: " + LoginAlpha + "}, github: {user: " + LoginAlpha + ", host_alias: github.com, route: ssh}, personal_store: inherit, tools: [claude_code], folders: [~/Downloads/scratch/**]}]\nidentity:\npersonal:\n"
	b.file(".lore/config.yaml", cfg, 0o600)
	// identity.yaml.migrated is the same person as config.yaml (what the
	// migration copied); the older identity.toml names someone else.
	b.file(".lore/identity.yaml.migrated", "stable_key: human:"+PersonEmail+"\ndisplay_name: "+PersonName+"\n", 0o600)
	b.file(".lore/identity.toml.migrated", "stable_key = \"human:"+MigratedOnlyEmail+"\"\ndisplay_name = \"Migrated Only\"\n", 0o600)
	b.file(".lore/machine.json", "{}\n", 0o600)
	b.file(".lore/state/setup-debug.log", "", 0o600)
	b.file(".lore/state/upgrade-check.json", "{}\n", 0o600)
}

// GraveSetups are ShapeGraveyard's setups by after-life.
const (
	GraveDisabled = "dormant"
	GraveTrashed  = "binned"
	GravePurged   = "gone"
	GraveLive     = "main"
)

func (b *builder) graveyard() {
	b.migratedHome(GraveLive)
	for _, n := range []string{GraveDisabled, GraveTrashed, GravePurged} {
		b.lore(b.m.Home, "setup", "apply", n, "--step", "kind", "--answers", `{"kind":"solo","name":"`+n+`","default":false}`)
		b.lore(b.m.Home, "setup", "apply", n, "--step", "identity", "--answers", `{"name":"`+PersonName+`","email":"`+n+`@example.test"}`)
	}
	b.lore(b.m.Home, "setup", "disable", GraveDisabled)
	b.lore(b.m.Home, "setup", "remove", GraveTrashed)
	b.lore(b.m.Home, "setup", "remove", GravePurged)
	b.lore(b.m.Home, "setup", "trash", "purge", GravePurged, "--confirm")
	// A purge from before tombstones left its identity at the top and
	// wrote no trash.purged entry.
	b.lore(b.m.Home, "config", "set", "identity.key", "human:"+LegacyPurgedEmail)
}

// LegacyPurgedEmail is the identity an old, tombstone-less purge left.
const LegacyPurgedEmail = "legacy-purged@example.test"

// keyAccounts maps a key file name to the GitHub login it logs in as.
// sshKeys lists the keys each ssh shape makes; withPub false = no .pub.
type sshKey struct {
	file    string
	login   string
	withPub bool
}

func (b *builder) ssh(ownerLike bool) {
	sshDir := filepath.Join(b.m.Home, ".ssh")
	b.dir(sshDir, 0o700)
	keys := []sshKey{{"id_ed25519", LoginAlpha, true}}
	if ownerLike {
		keys = append(keys, sshKey{"bravo_key", LoginBravo, true}, sshKey{"charlie_nopub", LoginCharlie, false}, sshKey{"cloud_key", "", true})
	}
	var keymap strings.Builder
	for _, k := range keys {
		p := filepath.Join(sshDir, k.file)
		b.run(sshDir, "/usr/bin/ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", k.file+"@placeholder", "-f", p)
		if !k.withPub {
			if err := os.Remove(p + ".pub"); err != nil {
				b.t.Fatal(err)
			}
		}
		if k.login != "" {
			fmt.Fprintf(&keymap, "%s %s\n", k.file, k.login)
		}
	}
	b.file(filepath.Join(b.m.Dir, "keymap"), keymap.String(), 0o644)
	b.file(".ssh/known_hosts", "github.com ssh-ed25519 AAAAplaceholder\n", 0o600)
	if !ownerLike {
		return
	}
	b.file(".ssh/config", ownerSSHConfig, 0o600)
	b.file(".ssh/config.bak-2026-01-01", ownerSSHConfig, 0o600)
	b.file(".ssh/known_hosts.old", "", 0o600)
	b.dir(filepath.Join(sshDir, "agent"), 0o700)
	// A ControlMaster socket, as ssh leaves one behind.
	ln, err := net.Listen("unix", filepath.Join(sshDir, "cm-0a1b2c"))
	if err != nil {
		b.t.Fatalf("control socket: %v", err)
	}
	b.t.Cleanup(func() { _ = ln.Close() })
}

// ownerSSHConfig mixes tabs and spaces and both spellings of HostName.
const ownerSSHConfig = `Host *
	ServerAliveInterval 60
	AddKeysToAgent yes

Host github-alpha
	HostName github.com
	User git
	IdentityFile ~/.ssh/id_ed25519
	IdentitiesOnly yes

Host github-bravo
    Hostname github.com
    User git
    IdentityFile ~/.ssh/bravo_key
    IdentitiesOnly yes

Host github-charlie
	HostName github.com
	User git
	IdentityFile ~/.ssh/charlie_nopub
	IdentitiesOnly yes

Host github-gone
	HostName github.com
	User git
	IdentityFile ~/.ssh/deleted_key

Host build-box
	HostName 10.0.0.5
	User deploy
	IdentityFile ~/.ssh/cloud_key
`

func (b *builder) gh(logins []string, active string) {
	var s strings.Builder
	for _, l := range logins {
		mark := "inactive"
		if l == active {
			mark = "active"
		}
		fmt.Fprintf(&s, "%s %s\n", l, mark)
	}
	b.file(filepath.Join(b.m.Dir, "gh-accounts"), s.String(), 0o644)
}
