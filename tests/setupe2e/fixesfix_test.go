//go:build setupe2e

package setupe2e

// fixesfix_test.go — every runnable fix keeps its promise: build a world
// where the check fails, run the fix exactly as the page's "Run it" does
// (setup.fix with --yes: the same op, the same FixRunner, the serving lore
// binary, in the check's target repo), run the check again and require it
// to pass. Origin: the tools check offered `lore render` for a CLAUDE.md
// that render only writes while claude_code is in context.targets; Run it
// "finished" and the warning stayed (owner, 2026-11-08).
//
// TestEveryRunnableFixHasACase holds the catalog to this table: a new
// runnable, non-outward fix row fails it until it gets a case here or a
// stated reason why it cannot run unattended.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// fixWorld is a hermetic HOME with one solo setup and one git repo in it.
type fixWorld struct {
	t    *testing.T
	bin  string // the lore binary (loreBin, or the versioned one)
	home string
	repo string
	env  []string
}

// fixSetup is the setup name every fix case uses.
const fixSetup = "personal"

// newFixWorld writes config.yaml (setup personal, tools, folder ~/work/**)
// and a git repo with one commit at ~/work/repo.
func newFixWorld(t *testing.T, bin string, tools []string) *fixWorld {
	t.Helper()
	dir := t.TempDir()
	w := &fixWorld{t: t, bin: bin, home: filepath.Join(dir, "home")}
	w.repo = filepath.Join(w.home, "work", "repo")
	if err := os.MkdirAll(filepath.Join(w.home, ".lore"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(w.repo, 0o700); err != nil {
		t.Fatal(err)
	}
	w.env = []string{
		"HOME=" + w.home, "PATH=" + filepath.Dir(bin) + ":/usr/bin:/bin", "TERM=dumb", "LANG=C.UTF-8",
		"GIT_CONFIG_NOSYSTEM=1", "LORE_NO_UPDATE_CHECK=1", "TMPDIR=" + os.TempDir(),
		"GIT_AUTHOR_NAME=e2e", "GIT_AUTHOR_EMAIL=e2e@example.com", "GIT_COMMITTER_NAME=e2e", "GIT_COMMITTER_EMAIL=e2e@example.com",
	}
	cfg := "setups:\n  - name: " + fixSetup + "\n    kind: solo\n    default: true\n    folders: [\"" + filepath.Join(w.home, "work") + "/**\"]\n    identity:\n      emails: [e2e@example.com]\n"
	if len(tools) > 0 {
		cfg += "    tools: [" + strings.Join(tools, ", ") + "]\n"
	}
	if err := os.WriteFile(filepath.Join(w.home, ".lore", "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	w.run("git", "init", "-q")
	w.run("git", "commit", "-q", "--allow-empty", "-m", "init")
	return w
}

// run runs a program in the repo; a failure fails the test.
func (w *fixWorld) run(prog string, args ...string) string {
	w.t.Helper()
	out, err := w.cmd(prog, args...)
	if err != nil {
		w.t.Fatalf("%s %s: %v\n%s", prog, strings.Join(args, " "), err, out)
	}
	return out
}

func (w *fixWorld) cmd(prog string, args ...string) (string, error) {
	if prog == "lore" {
		prog = w.bin
	}
	c := exec.Command(prog, args...)
	c.Dir, c.Env = w.repo, w.env
	var b bytes.Buffer
	c.Stdout, c.Stderr = &b, &b
	err := c.Run()
	return b.String(), err
}

// check runs setup.check for step and returns the result of id.
func (w *fixWorld) check(step onboard.StepID, id onboard.CheckID) onboard.CheckResult {
	w.t.Helper()
	out, err := w.cmd("lore", "setup", "check", fixSetup, "--step", string(step), "--json")
	if err != nil {
		w.t.Fatalf("setup check %s: %v\n%s", step, err, out)
	}
	var co opssetup.CheckOut
	if err := json.Unmarshal([]byte(out), &co); err != nil {
		w.t.Fatalf("setup check %s: %v\n%s", step, err, out)
	}
	for _, r := range co.Checks {
		if r.Check == id {
			return r
		}
	}
	w.t.Fatalf("setup check %s: no %s row\n%s", step, id, out)
	return onboard.CheckResult{}
}

// skillFile is the claude_code skill's SKILL.md in the world's HOME.
func (w *fixWorld) skillFile() string {
	return filepath.Join(w.home, ".claude", "skills", "lore", "SKILL.md")
}

// editFile rewrites path with edit; a missing file fails the test.
func (w *fixWorld) editFile(path string, edit func(string) string) {
	w.t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(edit(string(b))), 0o600); err != nil {
		w.t.Fatal(err)
	}
}

// versionLine is the skill's lore_version frontmatter line.
var versionLine = regexp.MustCompile(`(?m)^\s*lore_version:.*\n`)

// testVersion stamps the versioned binary: tools.skill_version is skipped
// for a development build, so its fixes need a release-shaped version.
const testVersion = "0.1.18"

var (
	versionedOnce sync.Once
	versionedBin  string
	versionedErr  error
)

// versionedLore builds lore once with main.version = testVersion.
func versionedLore(t *testing.T) string {
	t.Helper()
	versionedOnce.Do(func() {
		root, err := RepoRoot()
		if err != nil {
			versionedErr = err
			return
		}
		versionedBin = filepath.Join(os.TempDir(), fmt.Sprintf("setupe2e-lore-versioned-%d", os.Getpid()), "lore")
		c := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags", "-X main.version="+testVersion, "-o", versionedBin, "./cmd/lore")
		c.Dir, c.Env = root, append(os.Environ(), "CGO_ENABLED=0")
		if out, err := c.CombinedOutput(); err != nil {
			versionedErr = fmt.Errorf("build versioned lore: %w\n%s", err, out)
		}
	})
	if versionedErr != nil {
		t.Fatal(versionedErr)
	}
	return versionedBin
}

// removeVersionedLore deletes the versioned binary's folder (TestMain).
func removeVersionedLore() {
	if versionedBin != "" {
		_ = os.RemoveAll(filepath.Dir(versionedBin))
	}
}

// fixCase is one check whose runnable fix must make it pass.
type fixCase struct {
	name    string
	tools   []string
	step    onboard.StepID
	check   onboard.CheckID
	variant onboard.FixVariant // the catalog row the case covers
	// versioned runs the case on a release-stamped binary.
	versioned bool
	prepare   func(w *fixWorld) // makes the check fail
	// doctor marks a check no setup step runs (onboard.DoctorOnly): the
	// world is read the way the setup page's Doctor and `lore doctor
	// --all` read it, and the offered fix must be the variant's row.
	doctor bool
	// settled is the check's status once the problem is gone (ok when
	// zero). identity.top_level_keep only exists while the two identities
	// disagree: after its fix it is skipped with "do not disagree".
	settled onboard.Status
	// verify checks what the fix left behind beyond the check's own word.
	verify func(w *fixWorld)
}

// fixCases covers every runnable, non-outward fix row (see
// TestEveryRunnableFixHasACase for the rows that cannot run unattended).
var fixCases = []fixCase{
	{
		// D-711: a setup tool the repo does not target and has no file of
		// is skipped, not failed; the failing shape is a target whose file
		// went missing.
		name: "pointer file missing: target's file removed", tools: []string{"claude_code"},
		step: onboard.StepTools, check: onboard.CheckToolsPointerFile,
		prepare: func(w *fixWorld) {
			w.run("lore", "init", "--target", "claude_code")
			if err := os.Remove(filepath.Join(w.repo, "CLAUDE.md")); err != nil {
				w.t.Fatal(err)
			}
		},
	},
	{
		name: "pointer file does not import LORE.md", tools: []string{"claude_code"},
		step: onboard.StepTools, check: onboard.CheckToolsPointerFile,
		prepare: func(w *fixWorld) {
			w.run("lore", "init", "--target", "codex")
			if err := os.WriteFile(filepath.Join(w.repo, "CLAUDE.md"), []byte("# notes\n"), 0o600); err != nil {
				w.t.Fatal(err)
			}
		},
	},
	{
		name: "skill missing", tools: []string{"claude_code"},
		step: onboard.StepTools, check: onboard.CheckToolsSkillInstalled,
		prepare: func(w *fixWorld) { w.run("lore", "init", "--target", "claude_code") },
	},
	{
		name: "tool with no skill folder: pointer file missing", tools: []string{"cursor"},
		step: onboard.StepTools, check: onboard.CheckToolsPointerFile,
		prepare: func(w *fixWorld) {
			w.run("lore", "init", "--target", "cursor")
			if err := os.Remove(filepath.Join(w.repo, ".cursor", "rules", "lore.mdc")); err != nil {
				w.t.Fatal(err)
			}
		},
	},
	{
		// No version line AND other files: an older or hand-edited skill.
		// (No line with this binary's files is current: an ok row.)
		name: "skill has no version line", tools: []string{"claude_code"},
		step: onboard.StepTools, check: onboard.CheckToolsSkillVersion, variant: onboard.FixNoVersionLine, versioned: true,
		prepare: func(w *fixWorld) {
			w.run("lore", "init", "--target", "claude_code")
			w.run("lore", "skill", "install", "--tool", "claude_code")
			w.editFile(w.skillFile(), func(s string) string { return versionLine.ReplaceAllString(s, "") + "\nexample note\n" })
		},
	},
	{
		name: "skill from another lore version", tools: []string{"claude_code"},
		step: onboard.StepTools, check: onboard.CheckToolsSkillVersion, versioned: true,
		prepare: func(w *fixWorld) {
			w.run("lore", "init", "--target", "claude_code")
			w.run("lore", "skill", "install", "--tool", "claude_code")
			w.editFile(w.skillFile(), func(s string) string { return versionLine.ReplaceAllString(s, "lore_version: 0.0.1\n") })
		},
	},
	{
		name: "skill edited by hand", tools: []string{"claude_code"},
		step: onboard.StepTools, check: onboard.CheckToolsSkillModified,
		prepare: func(w *fixWorld) {
			w.run("lore", "init", "--target", "claude_code")
			w.run("lore", "skill", "install", "--tool", "claude_code")
			w.editFile(w.skillFile(), func(s string) string { return s + "\nmy own note\n" })
		},
	},
	{
		name: "session hooks not installed", tools: []string{"claude_code"},
		step: onboard.StepTools, check: onboard.CheckToolsHooks,
		prepare: func(w *fixWorld) {
			w.run("lore", "init", "--target", "claude_code")
			if err := os.MkdirAll(filepath.Join(w.repo, ".claude"), 0o700); err != nil {
				w.t.Fatal(err)
			}
		},
	},
	{
		name: "repo not set up, setup has tools", tools: []string{"claude_code"},
		step: onboard.StepThisFolder, check: onboard.CheckRepoInit, variant: onboard.FixInitTargets,
		prepare: func(*fixWorld) {},
	},
	{
		name: "repo not set up, setup has no tools",
		step: onboard.StepThisFolder, check: onboard.CheckRepoInit,
		prepare: func(*fixWorld) {},
	},
	{
		// The setup's identity lacks the name the old top-level block holds.
		name: "top-level identity is an old copy beside the setup's",
		step: onboard.StepIdentity, check: onboard.CheckIdentityTopLevel, doctor: true,
		prepare: func(w *fixWorld) {
			w.setIdentity(topIdentityOld, "      key: "+fixKey+"\n      emails: [e2e@example.com]\n")
		},
		verify: func(w *fixWorld) {
			w.configSays("name: Old Name", "the setup took the name only the top-level block held")
		},
	},
	{
		name: "top-level identity disagrees with the setup's: keep the setup's",
		step: onboard.StepIdentity, check: onboard.CheckIdentityTopLevel, variant: onboard.FixIdentityConflict, doctor: true,
		prepare: func(w *fixWorld) { w.setIdentity(topIdentityOld, setupIdentityNew) },
		verify:  func(w *fixWorld) { w.configSays("name: New Name", "the setup kept its own name") },
	},
	{
		name: "top-level identity disagrees with the setup's: keep the top-level one",
		step: onboard.StepIdentity, check: onboard.CheckIdentityTopLevelKeep, doctor: true, settled: onboard.StatusSkip,
		prepare: func(w *fixWorld) { w.setIdentity(topIdentityOld, setupIdentityNew) },
		verify: func(w *fixWorld) {
			w.configSays("name: Old Name", "the setup took the top-level name")
			if r := w.doctorCheck(onboard.CheckIdentityTopLevel); r.Status != onboard.StatusOK {
				w.t.Errorf("after keeping the top-level identity, identity.top_level is %s (%s), want ok", r.Status, r.Detail)
			}
		},
	},
	{
		// The repo's copy of the actor was written under the old name; the
		// setup (the source) is renamed by hand afterwards. With agent
		// detection on (a run under an AI tool) the actor is "<name> via
		// <tool>" and is stale the same way.
		name: "a store holds an old copy of an actor",
		step: onboard.StepIdentity, check: onboard.CheckIdentityActorCopies, doctor: true,
		prepare: func(w *fixWorld) {
			w.setIdentity("", "      key: "+fixKey+"\n      name: Pat One\n      emails: [e2e@example.com]\n")
			w.run("lore", "init")
			w.run("lore", "memory", "add", "first memory")
			w.editFile(w.configFile(), func(s string) string { return strings.Replace(s, "name: Pat One", "name: Pat Two", 1) })
		},
	},
	{
		// personal.store and the setup's personal_store reach one store
		// (one bare repo, two paths): lore writes only to one of them.
		name: "personal store reached at two addresses",
		step: onboard.StepPersonalStore, check: onboard.CheckStoreAddress, doctor: true,
		prepare: func(w *fixWorld) {
			bare, alias := w.bareRepo("me.git"), filepath.Join(filepath.Dir(w.home), "me-alias.git")
			if err := os.Symlink(bare, alias); err != nil {
				w.t.Fatal(err)
			}
			w.run("lore", "init")
			w.run("lore", "store", "init", "--name", "me", "--url", bare, "--yes")
			w.run("lore", "memory", "add", "a personal note", "--to", "me")
			w.run("lore", "sync")
			w.editFile(w.configFile(), func(s string) string {
				return strings.Replace(s, "    kind: solo\n", "    kind: solo\n    personal_store: "+alias+"\n", 1)
			})
			w.run("lore", "sync") // the alias gets its own clone: two addresses of one store id
		},
		verify: func(w *fixWorld) { w.run("lore", "memory", "add", "a note after set-url", "--to", "me") },
	},
	{
		// A member setup whose org entry was changed by hand (no source
		// stamp, an extra repo pattern) against the store's lore-org.yaml.
		name: "org entry differs from the store's lore-org.yaml",
		step: onboard.StepJoin, check: onboard.CheckOrgSource, doctor: true,
		prepare: func(w *fixWorld) {
			bare := w.bareRepo("team.git")
			w.run("lore", "init")
			w.run("lore", "store", "init", "--name", "acme_team", "--url", bare, "--yes")
			w.run("lore", "org", "create", "--org", "acme", "--store", "acme_team", "--key", fixKey, "--name", "E2E", "--yes")
			w.run("lore", "sync")
			w.editFile(w.configFile(), func(s string) string {
				s = strings.Replace(s, "    kind: solo\n", "    kind: member\n    org: acme\n", 1)
				s = strings.Replace(s, "    identity:\n      emails: [e2e@example.com]\n", "", 1) // a member's identity is its org entry's
				return orgSourceStamp.ReplaceAllString(s, "    repos:\n      - github.com/elsewhere/*\n")
			})
		},
		verify: func(w *fixWorld) {
			if c := w.readFile(w.configFile()); strings.Contains(c, "github.com/elsewhere/*") || !strings.Contains(c, "version:") {
				w.t.Errorf("the org entry after lore org refresh:\n%s", c)
			}
		},
	},
}

// Identities and patterns of the doctor-only cases.
const (
	fixKey           = "human:e2e@example.com"
	topIdentityOld   = "identity:\n  key: " + fixKey + "\n  name: Old Name\n"
	setupIdentityNew = "      key: " + fixKey + "\n      name: New Name\n      emails: [e2e@example.com]\n"
)

// orgSourceStamp is the source block lore org create stamps on an entry.
var orgSourceStamp = regexp.MustCompile(`    source:\n      store: .*\n      version: .*\n`)

// configFile is the world's ~/.lore/config.yaml.
func (w *fixWorld) configFile() string { return filepath.Join(w.home, ".lore", "config.yaml") }

// readFile is path's content; a missing file fails the test.
func (w *fixWorld) readFile(path string) string {
	w.t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		w.t.Fatal(err)
	}
	return string(b)
}

// setIdentity puts top (a top-level identity: block, "" for none) in front
// of the config and replaces the setup's identity body with setup.
func (w *fixWorld) setIdentity(top, setup string) {
	w.t.Helper()
	w.editFile(w.configFile(), func(s string) string {
		return top + strings.Replace(s, "      emails: [e2e@example.com]\n", setup, 1)
	})
}

// configSays fails the test when config.yaml lacks want or still holds a
// top-level identity: block.
func (w *fixWorld) configSays(want, why string) {
	w.t.Helper()
	c := w.readFile(w.configFile())
	if !strings.Contains(c, want) || strings.HasPrefix(c, "identity:") || strings.Contains(c, "\nidentity:") {
		w.t.Errorf("%s: want %q and no top-level identity: block in config.yaml:\n%s", why, want, c)
	}
}

// bareRepo makes an empty bare git repo next to HOME and returns its path.
func (w *fixWorld) bareRepo(name string) string {
	w.t.Helper()
	p := filepath.Join(filepath.Dir(w.home), name)
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", p).CombinedOutput(); err != nil {
		w.t.Fatalf("git init --bare %s: %v\n%s", p, err, out)
	}
	return p
}

// doctorCheck is check id's row of the setup in `lore doctor --all
// --json` (the setup page's Doctor runs the same checks). The command
// exits non-zero while a check warns; its JSON is still the answer.
func (w *fixWorld) doctorCheck(id onboard.CheckID) onboard.CheckResult {
	w.t.Helper()
	out, _ := w.cmd("lore", "doctor", "--all", "--json")
	var d struct {
		Setups []struct {
			Name   string                `json:"name"`
			Checks []onboard.CheckResult `json:"checks"`
		} `json:"setups"`
	}
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		w.t.Fatalf("doctor --all: %v\n%s", err, out)
	}
	for _, s := range d.Setups {
		if s.Name != fixSetup {
			continue
		}
		for _, r := range s.Checks {
			if r.Check == id {
				return r
			}
		}
	}
	w.t.Fatalf("doctor --all: no %s row for setup %s\n%s", id, fixSetup, out)
	return onboard.CheckResult{}
}

// probe is c's check as the page shows it: the step's check, or the
// Doctor's row for a doctor-only check.
func (w *fixWorld) probe(c fixCase) onboard.CheckResult {
	w.t.Helper()
	if c.doctor {
		return w.doctorCheck(c.check)
	}
	return w.check(c.step, c.check)
}

// placeholder is a {var} of a fix row's text.
var placeholder = regexp.MustCompile(`\\\{[a-z_]+\\\}`)

// offersRow fails the test unless text is row (check, variant)'s fix
// text with its {vars} filled in: the case covers the row it names.
func offersRow(t *testing.T, check onboard.CheckID, v onboard.FixVariant, text string) {
	t.Helper()
	for _, r := range onboard.FixRows() {
		if r.Check != check || r.Variant != v {
			continue
		}
		re := regexp.MustCompile("^" + placeholder.ReplaceAllString(regexp.QuoteMeta(r.Fix.Text), ".+") + "$")
		if !re.MatchString(text) {
			t.Fatalf("%s offers %q, not the %q row's text %q", check, text, v, r.Fix.Text)
		}
		return
	}
	t.Fatalf("no fix row %s / %q", check, v)
}

// TestEveryFixFixesItsCheck: fail → Run it → re-check passes, and
// setup.fix itself reports the passing re-check.
func TestEveryFixFixesItsCheck(t *testing.T) {
	for _, c := range fixCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			bin := loreBin
			if c.versioned {
				bin = versionedLore(t)
			}
			w := newFixWorld(t, bin, c.tools)
			c.prepare(w)
			before := w.probe(c)
			if before.Status != onboard.StatusFail && before.Status != onboard.StatusWarn {
				t.Fatalf("prepared world: %s is %s (%s), want it failing", c.check, before.Status, before.Detail)
			}
			if !before.Fix.Runnable || len(before.Fix.Command) == 0 {
				t.Fatalf("%s offers no runnable fix: %+v", c.check, before.Fix)
			}
			if c.doctor {
				offersRow(t, c.check, c.variant, before.Fix.Text)
			}
			settled := c.settled
			if settled == "" {
				settled = onboard.StatusOK
			}
			out, err := w.cmd("lore", "setup", "fix", fixSetup, "--check", string(c.check), "--target", before.Target, "--yes", "--json")
			if err != nil {
				t.Fatalf("setup fix %s (%s): %v\n%s", c.check, strings.Join(before.Fix.Command, " "), err, out)
			}
			var run opssetup.FixOut
			if err := json.Unmarshal([]byte(out), &run); err != nil {
				t.Fatalf("setup fix output: %v\n%s", err, out)
			}
			if run.Exit != 0 {
				t.Fatalf("fix %v exited %d:\n%s", run.Argv, run.Exit, run.OutputTail)
			}
			if run.StillFailing != "" || run.Recheck != settled {
				t.Fatalf("fix %v ran but setup.fix's re-check is %q: %s\noutput:\n%s", run.Argv, run.Recheck, run.StillFailing, run.OutputTail)
			}
			after := w.probe(c)
			if after.Status != settled {
				t.Fatalf("after %v: %s is %s (%s), want %s\nfix output:\n%s", run.Argv, c.check, after.Status, after.Detail, settled, run.OutputTail)
			}
			if c.verify != nil {
				c.verify(w)
			}
		})
	}
}

// fixExempt are the runnable, non-outward rows no case runs, each with
// the reason it cannot run unattended here.
var fixExempt = map[string]string{
	rowKey(onboard.CheckGitHubKeyMatches, "", onboard.FixKeyPair):      "its probe offers it only after an ssh alias logged in to GitHub as the account; needs a real ssh login",
	rowKey(onboard.CheckGitHubKeyMatches, "", onboard.FixKeyAliasOnly): "its probe offers it only after an ssh alias logged in to GitHub as the account; needs a real ssh login",
	rowKey(onboard.CheckInvitePending, "", onboard.FixDefault):         "needs a team store holding an invite (lore team invite resend writes to it)",
}

func rowKey(c onboard.CheckID, s onboard.StepID, v onboard.FixVariant) string {
	return fmt.Sprintf("%s|%s|%s", c, s, v)
}

// TestEveryRunnableFixHasACase: every runnable fix row that is not
// outward (an outward one needs a login to a remote service and is always
// confirmed) is run by a case above or named in fixExempt with a reason.
func TestEveryRunnableFixHasACase(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range fixCases {
		covered[rowKey(c.check, "", c.variant)] = true
	}
	for _, r := range onboard.FixRows() {
		if !r.Fix.Runnable || r.Fix.Outward {
			continue
		}
		k := rowKey(r.Check, r.Step, r.Variant)
		if !covered[k] && fixExempt[k] == "" {
			t.Errorf("runnable fix %s (%s) has no case in fixCases and no reason in fixExempt", k, strings.Join(r.Fix.Command, " "))
		}
	}
}
