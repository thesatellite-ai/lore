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
}

// fixCases covers every runnable, non-outward fix row (see
// TestEveryRunnableFixHasACase for the rows that cannot run unattended).
var fixCases = []fixCase{
	{
		name: "pointer file missing: tool not in context.targets", tools: []string{"claude_code"},
		step: onboard.StepTools, check: onboard.CheckToolsPointerFile,
		prepare: func(w *fixWorld) { w.run("lore", "init", "--target", "codex") },
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
		prepare: func(w *fixWorld) { w.run("lore", "init", "--target", "codex") },
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
			before := w.check(c.step, c.check)
			if before.Status != onboard.StatusFail && before.Status != onboard.StatusWarn {
				t.Fatalf("prepared world: %s is %s (%s), want it failing", c.check, before.Status, before.Detail)
			}
			if !before.Fix.Runnable || len(before.Fix.Command) == 0 {
				t.Fatalf("%s offers no runnable fix: %+v", c.check, before.Fix)
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
			if run.StillFailing != "" || run.Recheck != onboard.StatusOK {
				t.Fatalf("fix %v ran but setup.fix's re-check is %q: %s\noutput:\n%s", run.Argv, run.Recheck, run.StillFailing, run.OutputTail)
			}
			after := w.check(c.step, c.check)
			if after.Status != onboard.StatusOK {
				t.Fatalf("after %v: %s is %s (%s), want ok\nfix output:\n%s", run.Argv, c.check, after.Status, after.Detail, run.OutputTail)
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
