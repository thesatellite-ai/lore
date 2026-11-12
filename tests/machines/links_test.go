//go:build machines && !windows

package machines

// links_test.go — the commands a person runs on a machine whose files are
// links or read-only (shape_links.go), through the real binary. After
// every command three things are checked:
//
//  1. the result: exit code and output name the actual state (a skill
//     behind a chain of links is installed; a read-only file is an error
//     naming that file and how to fix it);
//  2. the disk: nothing written through a link into a foreign folder (the
//     foreign tree is byte-identical unless lore owns the file and the
//     person asked), every link stays a link, config.yaml stays block
//     style, repo modes 0644/0755 and ~/.lore owner-only (CheckModes);
//  3. doctor --all --json: clean, or naming exactly the issue left, and
//     never a fix line while every check passes.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// linkCase is one machine under test with what must not change on it.
type linkCase struct {
	t *testing.T
	m *Machine
	// links maps each watched link to its target; it must stay that link.
	links map[string]string
	// foreign is the snapshot of Machine.Dir/foreign.
	foreign map[string]string
}

func newLinkCase(t *testing.T, shape string) *linkCase {
	t.Helper()
	m := Build(t, shape, loreBin)
	c := &linkCase{t: t, m: m, links: map[string]string{}}
	c.foreign = snapshot(t, m.foreign(ForeignDir))
	return c
}

// watch records path (under Home, or absolute) as a link that must stay.
func (c *linkCase) watch(rels ...string) {
	c.t.Helper()
	for _, rel := range rels {
		p := c.abs(rel)
		target, err := os.Readlink(p)
		if err != nil {
			c.t.Fatalf("fixture: %s is no link: %v", p, err)
		}
		c.links[p] = target
	}
}

func (c *linkCase) abs(rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(c.m.Home, rel)
}

// snapshot is every entry under root (links not followed) as path → kind,
// mode and content hash.
func snapshot(t testing.TB, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // no foreign folder on this shape
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			out[p] = "link " + target
		case info.IsDir():
			out[p] = "dir " + info.Mode().Perm().String()
		default:
			b, _ := os.ReadFile(p)
			sum := sha256.Sum256(b)
			out[p] = "file " + info.Mode().Perm().String() + " " + hex.EncodeToString(sum[:])
		}
		return nil
	})
	return out
}

// run runs lore in cwd (a Home-relative folder or "") and checks the disk
// afterwards; changed lists the foreign files this command may change
// (lore's own file behind a link, written because the person asked).
func (c *linkCase) run(step, cwd string, changed []string, args ...string) Result {
	c.t.Helper()
	dir := ""
	if cwd != "" {
		dir = c.abs(cwd)
	}
	r := c.m.Lore(c.t, dir, args...)
	if strings.Contains(r.Stderr, "panic") || strings.Contains(r.Stderr, "goroutine ") {
		c.t.Errorf("[%s] lore crashed: %s", step, r)
	}
	c.checkDisk(step, changed)
	return r
}

// checkDisk: links stay links, the foreign tree only changes where
// allowed, config.yaml is block style, modes hold.
func (c *linkCase) checkDisk(step string, changed []string) {
	c.t.Helper()
	for p, target := range c.links {
		got, err := os.Readlink(p)
		if err != nil || got != target {
			c.t.Errorf("[%s] %s is no longer a link to %s (readlink %q, %v)", step, rel(c.m.Home, p), target, got, err)
		}
	}
	now := snapshot(c.t, c.m.foreign(ForeignDir))
	for p, was := range c.foreign {
		if now[p] != was && !slices.Contains(changed, p) {
			c.t.Errorf("[%s] lore changed %s outside its folders: %s → %s", step, p, was, now[p])
		}
	}
	for p, is := range now {
		if _, had := c.foreign[p]; !had && !slices.Contains(changed, p) {
			c.t.Errorf("[%s] lore wrote %s (%s) outside its folders", step, p, is)
		}
	}
	c.foreign = now
	checkBlockConfig(c.t, step, c.abs(LinkConfig))
	c.m.CheckModes(c.t, step)
}

// checkBlockConfig: config.yaml (read through any link) parses and holds
// no flow-style list or mapping lore wrote (the fixtures write none).
func checkBlockConfig(t testing.TB, step, p string) {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		if !os.IsNotExist(err) {
			t.Errorf("[%s] read %s: %v", step, p, err)
		}
		return
	}
	for i, line := range strings.Split(string(b), "\n") {
		v := strings.TrimSpace(line)
		if _, after, ok := strings.Cut(v, ": "); ok && (strings.HasPrefix(after, "[") || strings.HasPrefix(after, "{")) {
			t.Errorf("[%s] config.yaml line %d is flow style: %q\n%s", step, i+1, line, b)
		}
	}
}

// docCheck is one check row of doctor --all --json.
type docCheck struct {
	Check  string `json:"check"`
	Status string `json:"status"`
	Target string `json:"target"`
	Detail string `json:"detail"`
}

type docReport struct {
	Status string `json:"status"`
	Setups []struct {
		Name   string     `json:"name"`
		Status string     `json:"status"`
		Checks []docCheck `json:"checks"`
	} `json:"setups"`
	Machine struct {
		Checks []docCheck `json:"checks"`
	} `json:"machine"`
}

// issueStatuses are the check statuses that are an issue to fix.
var issueStatuses = []string{"warn", "fail", "error"}

// doctor runs doctor --all and wants exactly the issues named in want
// (each a substring of one issue's "check: detail" line; none = clean).
// The text form never prints a fix while every check passes, and setup
// list's Health cell agrees with the doctor afterwards.
func (c *linkCase) doctor(step string, want ...string) {
	c.t.Helper()
	r := c.run(step+"/doctor", "", nil, "doctor", "--all", "--json")
	if !slices.Contains(doctorExits, r.Code) {
		c.t.Errorf("[%s] doctor --all: %s", step, r)
		return
	}
	var d docReport
	if err := json.Unmarshal([]byte(r.Stdout), &d); err != nil {
		c.t.Fatalf("[%s] doctor --all --json: %v\n%s", step, err, r)
	}
	var issues []string
	add := func(cs []docCheck) {
		for _, ch := range cs {
			if slices.Contains(issueStatuses, ch.Status) {
				issues = append(issues, ch.Check+" ["+ch.Target+"]: "+ch.Detail)
			}
		}
	}
	add(d.Machine.Checks)
	for _, s := range d.Setups {
		add(s.Checks)
	}
	for _, w := range want {
		if !slices.ContainsFunc(issues, func(i string) bool { return strings.Contains(i, w) }) {
			c.t.Errorf("[%s] doctor does not name %q; its issues: %q", step, w, issues)
		}
	}
	for _, i := range issues {
		if !slices.ContainsFunc(want, func(w string) bool { return strings.Contains(i, w) }) {
			c.t.Errorf("[%s] doctor names an issue nobody expects: %q", step, i)
		}
	}
	text := c.m.Lore(c.t, "", "doctor", "--all")
	if len(issues) == 0 {
		if d.Status != "healthy" || r.Code != 0 {
			c.t.Errorf("[%s] no issue, yet doctor says %s (exit %d)", step, d.Status, r.Code)
		}
		if strings.Contains(text.Stdout, "fix:") || strings.Contains(text.Stdout, "Repair") {
			c.t.Errorf("[%s] doctor prints a fix while every check passes:\n%s", step, text.Stdout)
		}
	}
	l := c.m.Lore(c.t, "", "setup", "list", "--json")
	var lo struct {
		Setups []struct {
			Name   string `json:"name"`
			Health string `json:"health"`
		} `json:"setups"`
	}
	if err := json.Unmarshal([]byte(l.Stdout), &lo); err != nil {
		c.t.Fatalf("[%s] setup list --json: %v\n%s", step, err, l)
	}
	for _, s := range lo.Setups {
		if len(issues) == 0 && s.Health != "" && !strings.HasPrefix(s.Health, "✓") {
			c.t.Errorf("[%s] doctor is clean, yet setup list shows %s's health as %q", step, s.Name, s.Health)
		}
	}
}

// errOf is the error of a run: the --json envelope, else the
// "ERROR: [E_CODE] message" and "  hint: …" lines of stderr (nil = none).
func errOf(r Result) *csErr {
	if e := csErrOf(r); e != nil {
		return e
	}
	_, after, ok := strings.Cut(r.Stderr, "ERROR: [")
	if !ok {
		return nil
	}
	code, msg, _ := strings.Cut(after, "] ")
	msg, hint, _ := strings.Cut(msg, "\n  hint: ")
	return &csErr{Code: code, Message: strings.TrimSpace(msg), Hint: strings.TrimSpace(hint)}
}

// wantErr: the run failed with code, its message names every one of
// names, and its hint is a command or step to take.
func (c *linkCase) wantErr(step string, r Result, code string, names ...string) {
	c.t.Helper()
	e := errOf(r)
	if r.Code == 0 || e == nil || e.Code != code {
		c.t.Errorf("[%s] want %s, got %s", step, code, r)
		return
	}
	for _, n := range names {
		if !strings.Contains(e.Message+" "+e.Hint, n) {
			c.t.Errorf("[%s] error does not name %s: %s / hint %s", step, n, e.Message, e.Hint)
		}
	}
	if strings.TrimSpace(e.Hint) == "" {
		c.t.Errorf("[%s] error has no hint saying how to fix it: %s", step, e.Message)
	}
}

func (c *linkCase) ok(step string, r Result) {
	c.t.Helper()
	if r.Code != 0 {
		c.t.Errorf("[%s] %s", step, r)
	}
}

// linkSkillRow is one row of skill status --json.
type linkSkillRow struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version"`
	Status    string `json:"status"`
	Detail    string `json:"detail"`
}

// skillRow is claude_code's row of skill status --json.
func (c *linkCase) skillRow(step string) linkSkillRow {
	c.t.Helper()
	r := c.run(step, "", nil, "skill", "status", "--tool", "claude_code", "--json")
	c.ok(step, r)
	var out struct {
		Tools []linkSkillRow `json:"tools"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil || len(out.Tools) != 1 {
		c.t.Fatalf("[%s] skill status --json (%v): %s", step, err, r)
	}
	return out.Tools[0]
}

// sideSetup is the second setup a person adds, edits and removes.
const sideSetup = "side"

// setupCycle adds a second setup, edits both, lists, probes and scans,
// then removes and purges the second one; each must succeed.
func (c *linkCase) setupCycle(step string) {
	c.t.Helper()
	cfg := c.configChange()
	for _, a := range [][]string{
		{"setup", "apply", sideSetup, "--step", "kind", "--answers", `{"kind":"solo","name":"` + sideSetup + `","default":false}`, "--json"},
		{"setup", "apply", sideSetup, "--step", "identity", "--answers", `{"name":"` + PersonName + `","email":"side@example.test"}`, "--json"},
		{"setup", "apply", sideSetup, "--step", "folders", "--answers", `{"folders":["~/code/` + RepoNonGit + `/**"],"default":false}`, "--json"},
		{"setup", "edit", LinkSetup, "--note", "edited on a linked machine", "--json"},
		{"setup", "edit", sideSetup, "--note", "the side setup", "--json"},
	} {
		c.ok(step+"/"+a[1]+" "+a[2], c.run(step+"/"+a[1], "", cfg, a...))
	}
	for _, a := range [][]string{
		{"setup", "list", "--json"},
		{"setup", "show", sideSetup, "--json"},
		{"setup", "discover", "--json"},
		{"setup", "import-scan", "--json"},
		{"setup", "check", LinkSetup, "--step", "tools", "--answers", `{"tools":["claude_code"]}`, "--json"},
		{"setup", "check", LinkSetup, "--step", "folders", "--answers", `{"folders":["` + LinkSetupFolder + `"],"default":true}`, "--json"},
		{"config", "check", "--json"},
	} {
		c.ok(step+"/"+a[1], c.run(step+"/"+a[1], "", nil, a...))
	}
	c.ok(step+"/remove", c.run(step+"/remove", "", cfg, "setup", "remove", sideSetup, "--json"))
	c.ok(step+"/purge", c.run(step+"/purge", "", cfg, "setup", "trash", "purge", sideSetup, "--confirm", "--json"))
	c.m.CheckMachine(c.t, step+"/after the cycle", Gone{Purged: []string{sideSetup}})
}

// configChange is the foreign file a config write may change: the end of
// config.yaml's link when it is one (lore's own file, kept by dotfiles).
func (c *linkCase) configChange() []string {
	end, err := filepath.EvalSymlinks(c.abs(LinkConfig))
	if err != nil || end == c.abs(LinkConfig) {
		return nil
	}
	return []string{end}
}

// repoSession inits, renders and installs the directive in repo, each
// expected to succeed.
func (c *linkCase) repoSession(step, repo string) {
	c.t.Helper()
	dir := filepath.Join("code", repo)
	if r := c.run(step+"/init", dir, nil, "init"); r.Code != 0 && !alreadyInit(r) {
		c.t.Errorf("[%s/init] %s", step, r)
	}
	c.ok(step+"/render", c.run(step+"/render", dir, nil, "render", "--json"))
	c.ok(step+"/directive", c.run(step+"/directive", dir, nil, "directive", "install", "--json"))
	c.ok(step+"/render again", c.run(step+"/render again", dir, nil, "render", "--json"))
}

// TestLinksSkillChain: ~/.claude/skills/lore → ~/.agents/skills/lore → the
// shared-skills tool's real folder. The skill reads as installed at its
// version through both hops, install leaves the chain and the folder
// alone, every setup command works, and the doctor is clean.
func TestLinksSkillChain(t *testing.T) {
	t.Parallel()
	c := newLinkCase(t, ShapeLinksSkillChain)
	c.watch(LinkSkillClaude, LinkSkillAgents)
	row := c.skillRow("status")
	if !row.Installed || row.Version == "" {
		t.Errorf("a skill behind a chain of links reads as %+v, want installed with its version", row)
	}
	c.doctor("as built")
	for _, a := range [][]string{
		{"skill", "install", "--json"},
		{"skill", "install", "--tool", "claude_code", "--json"},
		{"skill", "install", "--all", "--json"},
	} {
		r := c.run(strings.Join(a, " "), "", nil, a...)
		c.ok(strings.Join(a, " "), r)
		var out struct {
			Installed []struct {
				Tool    string `json:"tool"`
				Changed bool   `json:"changed"`
			} `json:"installed"`
		}
		_ = json.Unmarshal([]byte(r.Stdout), &out)
		i := slices.IndexFunc(out.Installed, func(x struct {
			Tool    string `json:"tool"`
			Changed bool   `json:"changed"`
		}) bool {
			return x.Tool == "claude_code"
		})
		if i < 0 || out.Installed[i].Changed {
			t.Errorf("[%s] the linked skill is up to date, yet: %s", strings.Join(a, " "), r.Stdout)
		}
	}
	c.ok("apply tools", c.run("apply tools", "", nil, "setup", "apply", LinkSetup, "--step", "tools", "--answers", `{"tools":["claude_code"]}`, "--json"))
	if row := c.skillRow("status after install"); !row.Installed {
		t.Errorf("after install the linked skill reads as %+v", row)
	}
	c.setupCycle("cycle")
	c.repoSession("plain-git", RepoPlain)
	c.doctor("after the session")
}

// TestLinksSkillDangling: ~/.claude/skills/lore, ~/.claude/CLAUDE.md and
// plain-git's CLAUDE.md point at things that are gone. The skill is "not
// installed" naming the dangling link; a plain install never creates the
// missing target; the doctor names the skill and the repo's dangling
// pointer file as what they are; --link replace fixes the skill.
func TestLinksSkillDangling(t *testing.T) {
	t.Parallel()
	c := newLinkCase(t, ShapeLinksSkillDangling)
	plainClaude := filepath.Join(c.m.Repos[RepoPlain], "CLAUDE.md")
	c.watch(LinkSkillClaude, LinkGlobalClaude, plainClaude)
	row := c.skillRow("status")
	if row.Installed || !strings.Contains(row.Detail, "does not exist") {
		t.Errorf("a dangling skill link reads as %+v, want not installed naming the missing target", row)
	}
	c.doctor("as built", "not installed: "+c.abs(LinkSkillClaude)+" is a symbolic link")
	// --all skips the link; naming the tool is refused with both ways out.
	c.ok("install --all", c.run("install --all", "", nil, "skill", "install", "--all", "--json"))
	r := c.run("install --tool", "", nil, "skill", "install", "--tool", "claude_code", "--json")
	c.wantErr("install --tool", r, "E_SKILL_LINKED", c.abs(LinkSkillClaude), "--link replace")
	c.setupCycle("cycle")
	// plain-git's CLAUDE.md is a dangling link out of the repo: render
	// skips it with the reason and goes on (owner item 23: a pointer file
	// is never a repo failure), creating nothing at the missing end and
	// leaving the link.
	dir := filepath.Join("code", RepoPlain)
	r = c.run("init plain-git", dir, nil, "init")
	if r.Code != 0 && !alreadyInit(r) {
		t.Errorf("[init plain-git] %s", r)
	}
	r = c.run("render plain-git", dir, nil, "render", "--target", "claude_code", "--json")
	c.ok("render plain-git", r)
	if !strings.Contains(r.Stdout+r.Stderr, "pointer skipped") {
		t.Errorf("[render plain-git] the dangling CLAUDE.md link was not named as skipped: %s", r)
	}
	c.doctor("dangling pointer", "not installed: "+c.abs(LinkSkillClaude)+" is a symbolic link", "CLAUDE.md: it is a symbolic link to")
	// The person picks --link replace: lore's own folder replaces the
	// link (the one link the person asked to change).
	delete(c.links, c.abs(LinkSkillClaude))
	c.ok("install --link replace", c.run("install --link replace", "", nil, "skill", "install", "--tool", "claude_code", "--link", "replace", "--json"))
	if row := c.skillRow("status after replace"); !row.Installed {
		t.Errorf("after --link replace the skill reads as %+v", row)
	}
	c.doctor("after replace", "CLAUDE.md: it is a symbolic link to")
}

// TestLinksFiles: config.yaml, ~/.claude/CLAUDE.md and two repos' pointer
// files are links. Config writes land in the dotfiles file and the link
// stays. The repos' pointer files link to files of the same name in a
// shared rules folder: render writes lore's pointer block through them
// (owner item 23) and the links stay; every other write through them (the
// directive) is refused naming the file and the way out.
func TestLinksFiles(t *testing.T) {
	t.Parallel()
	c := newLinkCase(t, ShapeLinksFiles)
	sharedClaude := filepath.Join(c.m.Repos[RepoClaude], "CLAUDE.md")
	sharedAgents := filepath.Join(c.m.Repos[RepoAgents], "AGENTS.md")
	c.watch(LinkConfig, LinkGlobalClaude, sharedClaude, sharedAgents)
	c.doctor("as built")
	c.setupCycle("cycle")
	cfg, err := os.ReadFile(c.m.foreign(filepath.Join(DotfilesDir, "lore", "config.yaml")))
	if err != nil || !strings.Contains(string(cfg), "edited on a linked machine") {
		t.Errorf("the edit did not land in the dotfiles config.yaml (%v):\n%s", err, cfg)
	}
	if info, err := os.Stat(c.abs(LinkConfig)); err != nil || info.Mode().Perm()&privateBits != 0 {
		t.Errorf("config.yaml behind the link is not owner-only: %v %v", info.Mode(), err)
	}
	for _, repo := range []string{RepoClaude, RepoAgents} {
		dir := filepath.Join("code", repo)
		link := filepath.Join(c.m.Repos[repo], "CLAUDE.md")
		if repo == RepoAgents {
			link = filepath.Join(c.m.Repos[repo], "AGENTS.md")
		}
		end, err := filepath.EvalSymlinks(link)
		if err != nil {
			t.Fatalf("fixture: %s does not resolve: %v", link, err)
		}
		if r := c.run(repo+"/init", dir, []string{end}, "init"); r.Code != 0 && !alreadyInit(r) {
			t.Errorf("[%s/init] %s", repo, r)
		}
		target := "claude_code"
		if repo == RepoAgents {
			target = "codex"
		}
		c.ok(repo+"/render", c.run(repo+"/render", dir, []string{end}, "render", "--target", target, "--json"))
		if b, err := os.ReadFile(end); err != nil || !strings.Contains(string(b), ".lore/LORE.md") {
			t.Errorf("[%s/render] the shared %s does not hold lore's pointer block (%v):\n%s", repo, end, err, b)
		}
		c.wantErr(repo+"/directive", c.run(repo+"/directive", dir, nil, "directive", "install", "--target", target, "--json"), "E_REFUSED", link)
	}
	c.repoSession("plain-git", RepoPlain)
	c.doctor("after the session")
}

// TestLinksDirHome: ~/code is a link to another disk. A setup for ~/code/**
// applies in its repos whichever path the shell reports, and init, render
// and the directive write inside the repo on the other disk.
func TestLinksDirHome(t *testing.T) {
	t.Parallel()
	c := newLinkCase(t, ShapeLinksDirHome)
	c.watch("code")
	c.doctor("as built")
	c.setupCycle("cycle")
	for _, repo := range []string{RepoPlain, RepoClaude} {
		c.repoSession(repo, repo)
		for _, pwd := range []string{"", filepath.Join(c.m.Home, "code", repo)} {
			m := *c.m
			if pwd != "" {
				m.ExtraEnv = append(slices.Clone(m.ExtraEnv), "PWD="+pwd)
			}
			lo := decode[listOut](t, m.Lore(t, filepath.Join(c.m.Home, "code", repo), "setup", "list", "--json"))
			if lo.Active.Name != LinkSetup || !strings.Contains(lo.Active.Why, "folder") {
				t.Errorf("[%s PWD=%q] active setup is %+v, want %s by its folder ~/code/**", repo, pwd, lo.Active, LinkSetup)
			}
		}
		if _, err := os.Stat(filepath.Join(c.m.foreign(ElsewhereCode), repo, ".lore", "LORE.md")); err != nil {
			t.Errorf("[%s] render wrote no .lore/LORE.md in the repo on the other disk: %v", repo, err)
		}
	}
	c.doctor("after the session")
}

// TestLinksReadonly: a read-only CLAUDE.md, a read-only config.yaml and a
// read-only repo folder. Every write lore cannot make is an error naming
// the file and how to make it writable; nothing read-only is replaced,
// and the read-only files keep their mode.
func TestLinksReadonly(t *testing.T) {
	t.Parallel()
	c := newLinkCase(t, ShapeLinksReadonly)
	cfgPath := c.abs(LinkConfig)
	cfgBefore, _ := os.ReadFile(cfgPath)
	c.doctor("as built")
	// Reads work on a read-only config.yaml.
	for _, a := range [][]string{{"setup", "list", "--json"}, {"config", "check", "--json"}, {"setup", "show", LinkSetup, "--json"}, {"skill", "status", "--json"}} {
		c.ok(a[0]+" "+a[1], c.run(a[0]+" "+a[1], "", nil, a...))
	}
	// Writes are refused by name, with the chmod that makes them work.
	for _, a := range [][]string{
		{"setup", "edit", LinkSetup, "--note", "x", "--json"},
		{"setup", "apply", sideSetup, "--step", "kind", "--answers", `{"kind":"solo","name":"` + sideSetup + `","default":false}`, "--json"},
		{"config", "set", "live.autopush", "false", "--json"},
		{"setup", "remove", LinkSetup, "--json"},
	} {
		c.wantErr(a[0]+" "+a[1], c.run(a[0]+" "+a[1], "", nil, a...), "E_BAD_PATH", cfgPath, "chmod u+w")
	}
	if b, _ := os.ReadFile(cfgPath); string(b) != string(cfgBefore) {
		t.Errorf("a read-only config.yaml was rewritten:\n%s", b)
	}
	if info, err := os.Stat(cfgPath); err != nil || info.Mode().Perm() != readOnlyFile {
		t.Errorf("config.yaml is no longer read-only: %v %v", info.Mode(), err)
	}
	// A read-only CLAUDE.md: init and render name it; it keeps its text.
	claude := filepath.Join(c.m.Repos[RepoClaude], "CLAUDE.md")
	dir := filepath.Join("code", RepoClaude)
	r := c.run("init has-claude", dir, nil, "init")
	if r.Code != 0 && !alreadyInit(r) {
		t.Errorf("[init has-claude] %s", r)
	}
	c.wantErr("render has-claude", c.run("render has-claude", dir, nil, "render", "--target", "claude_code", "--json"), "E_BAD_PATH", claude, "chmod u+w")
	c.wantErr("directive has-claude", c.run("directive has-claude", dir, nil, "directive", "install", "--json"), "E_BAD_PATH", claude, "chmod u+w")
	if b, _ := os.ReadFile(claude); string(b) != ClaudeBody {
		t.Errorf("a read-only CLAUDE.md was rewritten:\n%s", b)
	}
	if info, _ := os.Stat(claude); info.Mode().Perm() != readOnlyFile {
		t.Errorf("CLAUDE.md is no longer read-only: %v", info.Mode())
	}
	// A read-only repo folder: init names the folder and the chmod.
	locked := c.m.Repos[RepoReadonly]
	c.wantErr("init locked", c.run("init locked", filepath.Join("code", RepoReadonly), nil, "init"), "E_BAD_PATH", locked, "chmod u+w")
	// Once the person makes them writable, the same commands work.
	chmod(t, cfgPath, 0o600)
	chmod(t, claude, 0o644)
	chmod(t, locked, 0o755)
	c.setupCycle("writable again")
	c.repoSession("has-claude", RepoClaude)
	c.repoSession("locked", RepoReadonly)
	c.doctor("after the session")
}
