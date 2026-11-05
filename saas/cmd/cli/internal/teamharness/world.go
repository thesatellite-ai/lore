// Package teamharness drives the real lore binary the way a team of people
// and agents would, for the LORE_TEAM_SPEC.md end-to-end suite
// (saas/cmd/cli/team_e2e_*_test.go, build tag teame2e).
//
// One World is one isolated universe:
//
//   - a fake "GitHub": bare repositories under <root>/hosts/<host>/<owner>/<repo>.git,
//     reached through real URLs (https://github.com/acme/app, git@github.com:acme/app.git)
//     because every actor's ~/.gitconfig rewrites those prefixes with url.<path>.insteadOf.
//     Lore therefore sees the same remote URLs a real user has, which matters for org
//     resolution (F10 normalises remotes to host/owner/repo);
//   - actors (people and agent sessions), each with its own HOME, ~/.gitconfig,
//     ~/.lore/config.yaml, clock (LORE_NOW) and network state (Offline/Online);
//   - a git wrapper placed first on lore's PATH that logs every git command lore runs
//     (to count pushes) and can SIGKILL lore at a chosen git step (crash injection).
//
// Nothing here is hidden state: every path lives under t.TempDir(), the binary
// path is passed in, and no package-level variable is mutated.
package teamharness

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Environment variables the git wrapper reads. They are set per lore run by
// Clone.With / Actor env; the wrapper is a POSIX sh script (see wrapperScript).
const (
	// EnvGitLog is the file the wrapper appends "<actor>\t<args>" to for every git call.
	EnvGitLog = "LORE_TEAM_GITLOG"
	// EnvActorName labels wrapper log lines so tests can count one actor's pushes.
	EnvActorName = "LORE_TEAM_ACTOR"
	// EnvKillOn is a substring of a git command line; when matched the wrapper
	// SIGKILLs its parent (lore) to simulate a crash between two steps.
	EnvKillOn = "LORE_TEAM_KILL_ON"
	// EnvKillWhen is "before" (kill instead of running git) or "after" (run git, then kill).
	EnvKillWhen = "LORE_TEAM_KILL_WHEN"
	// EnvKillNth makes the kill fire on the nth match only (default 1).
	EnvKillNth = "LORE_TEAM_KILL_NTH"
	// EnvKillCounter is the file the wrapper uses to count matches across git calls.
	EnvKillCounter = "LORE_TEAM_KILL_COUNTER"
	// EnvRealGit is the absolute path of the real git the wrapper execs.
	EnvRealGit = "LORE_TEAM_REAL_GIT"
	// EnvAdminPush lets a push through the protected-main pre-receive hook,
	// the way a repo admin (or GitHub's merge button) can update main.
	EnvAdminPush = "LORE_TEAM_ADMIN_PUSH"
	// EnvStrict turns PENDING skips (feature not built yet) into failures.
	EnvStrict = "LORE_TEAM_STRICT"
	// EnvForce skips the capability probe and runs every test as if its
	// commands existed: for a feature session whose command is only partly
	// wired (help text not final yet), and for checking fixtures end to end.
	EnvForce = "LORE_TEAM_FORCE"
)

// Kill modes for EnvKillWhen.
const (
	KillBefore = "before"
	KillAfter  = "after"
)

// fakeHost is the only host the fake GitHub serves; every remote slug starts with it.
const fakeHost = "github.com"

// wrapperScript is the git shim lore finds first on PATH. It must stay POSIX
// sh (dash on Linux, bash on macOS) and must never alter git's behaviour other
// than the documented kill: arguments pass through unchanged via "$@".
const wrapperScript = `#!/bin/sh
if [ -n "$LORE_TEAM_GITLOG" ]; then
  printf '%s\t%s\n' "${LORE_TEAM_ACTOR:-?}" "$*" >> "$LORE_TEAM_GITLOG"
fi
if [ -n "$LORE_TEAM_KILL_ON" ]; then
  case " $* " in
    *"$LORE_TEAM_KILL_ON"*)
      n=0
      if [ -f "$LORE_TEAM_KILL_COUNTER" ]; then n=$(cat "$LORE_TEAM_KILL_COUNTER"); fi
      n=$((n + 1))
      echo "$n" > "$LORE_TEAM_KILL_COUNTER"
      if [ "$n" -eq "${LORE_TEAM_KILL_NTH:-1}" ]; then
        if [ "$LORE_TEAM_KILL_WHEN" = "after" ]; then
          "$LORE_TEAM_REAL_GIT" "$@"
        fi
        kill -9 "$PPID"
        exit 137
      fi
      ;;
  esac
fi
exec "$LORE_TEAM_REAL_GIT" "$@"
`

// protectedMainHook rejects pushes to refs/heads/main with GitHub's GH006
// wording (F5 treats that text as a protection refusal, not a race), unless
// EnvAdminPush is set by the pusher (the "merge button").
const protectedMainHook = `#!/bin/sh
if [ -n "$` + EnvAdminPush + `" ]; then exit 0; fi
while read old new ref; do
  if [ "$ref" = "refs/heads/main" ]; then
    echo "error: GH006: Protected branch update failed for refs/heads/main." >&2
    echo "error: Changes must be made through a pull request." >&2
    exit 1
  fi
done
exit 0
`

// World is one isolated team universe. Create it with New.
type World struct {
	T       testing.TB
	Root    string // everything lives under here
	Bin     string // the lore binary under test
	hosts   string // <Root>/hosts: the fake GitHub
	shimDir string // holds the git wrapper named "git"
	realGit string // absolute path of the real git
	gitLog  string // wrapper log file
}

// New creates a World for one test. bin is the lore binary to drive; an empty
// bin (the e2e binary is not built under -short) skips the test.
func New(t testing.TB, bin string) *World {
	t.Helper()
	if bin == "" {
		t.Skip("team e2e: no lore binary (skipped with -short)")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("team e2e needs git on PATH: %v", err)
	}
	if realGit, err = filepath.Abs(realGit); err != nil {
		t.Fatal(err)
	}
	if bin, err = filepath.Abs(bin); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	w := &World{
		T:       t,
		Root:    root,
		Bin:     bin,
		hosts:   filepath.Join(root, "hosts"),
		shimDir: filepath.Join(root, "shim"),
		realGit: realGit,
		gitLog:  filepath.Join(root, "git-calls.log"),
	}
	mustMkdir(t, w.hosts)
	mustMkdir(t, w.shimDir)
	writeExec(t, filepath.Join(w.shimDir, "git"), wrapperScript)
	// Git hooks lore installs (pre-commit, post-commit, pre-push for the F6
	// ride-along) call plain `lore` from PATH; the binary under test is
	// built under another name (lore-team), so a `lore` link to it sits
	// first on PATH or hooks would run whatever lore the machine has.
	if err := os.Symlink(bin, filepath.Join(w.shimDir, loreName)); err != nil {
		t.Fatalf("link lore into the shim dir: %v", err)
	}
	return w
}

// baseEnv is the environment every process in the World starts from: the
// parent's environment minus anything that would leak the developer's own
// lore or git state into the test (LORE_*, GIT_*, CI), plus fixed settings.
func (w *World) baseEnv(home, actorName string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "LORE_") || strings.HasPrefix(k, "GIT_") || isCIMarker(k) || k == "HOME" || k == "PATH" {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"HOME="+home,
		"PATH="+w.shimDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"NO_COLOR=1",
		"LORE_SYNC_QUIET=0",
		// Every simulated person is a human at a terminal unless the test
		// says otherwise (LORE_ACTOR, which wins over this, or an actor env
		// override, which comes later). Without it, a suite started from a
		// coding-agent session inherits that agent through agent detection
		// (the ancestor-process walk, AGENT_IDENTITY 6.1) and every write is
		// attributed to "alice via <tool>" instead of alice.
		"LORE_HUMAN=1",
		// LORE_NO_DETECT=1 (loretest builds only): agent detection sees
		// nothing, so whoami's agent line does not depend on where the run
		// starts either.
		"LORE_NO_DETECT=1",
		EnvRealGit+"="+w.realGit,
		EnvGitLog+"="+w.gitLog,
		EnvActorName+"="+actorName,
	)
}

// Git runs the real git (not the wrapper) for harness bookkeeping, with an
// isolated environment, and fails the test on error.
func (w *World) Git(dir string, args ...string) string {
	w.T.Helper()
	r := w.run(dir, w.baseEnv(w.Root, "harness"), w.realGit, args...)
	if r.Code != 0 {
		w.T.Fatalf("git %v in %s: exit %d\n%s%s", args, dir, r.Code, r.Stdout, r.Stderr)
	}
	return strings.TrimSpace(r.Stdout)
}

// TryGit runs the real git and returns its result without failing.
func (w *World) TryGit(dir string, args ...string) Result {
	w.T.Helper()
	return w.run(dir, w.baseEnv(w.Root, "harness"), w.realGit, args...)
}

// run executes one process and captures everything. A failure to start is a
// harness bug and fails the test; a non-zero exit is data.
func (w *World) run(dir string, env []string, name string, args ...string) Result {
	w.T.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return w.finish(name, args, err, out.String(), errb.String())
}

// finish turns a process outcome into a Result.
func (w *World) finish(name string, args []string, err error, stdout, stderr string) Result {
	w.T.Helper()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			w.T.Fatalf("start %s %v: %v", name, args, err)
		}
		code = ee.ExitCode()
	}
	r := Result{Args: args, Stdout: stdout, Stderr: stderr, Code: code}
	if name == w.Bin && code == 0 {
		r.Unbuilt = unbuiltCommand(args, stdout)
	}
	return r
}

// GitCalls returns the git command lines the wrapper saw from lore, optionally
// filtered to one actor ("" = all) and to lines containing every substring.
func (w *World) GitCalls(actor string, contains ...string) []string {
	w.T.Helper()
	b, err := os.ReadFile(w.gitLog)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		w.T.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		who, cmdline, ok := strings.Cut(line, "\t")
		if !ok || (actor != "" && who != actor) {
			continue
		}
		match := true
		for _, c := range contains {
			if !strings.Contains(cmdline, c) {
				match = false
				break
			}
		}
		if match {
			out = append(out, cmdline)
		}
	}
	return out
}

// CIMarkers are the environment variables that mark a CI run (coordinator
// answer 15:05, Q-stores-3): lore hides internal ids, store names and folders
// in its output when ANY of them is set. The harness strips them all from the
// parent environment so a suite run on a CI host behaves like a developer's
// terminal unless a test sets one on purpose.
var CIMarkers = []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "CIRCLECI", "JENKINS_URL", "TF_BUILD", "BITBUCKET_BUILD_NUMBER"}

func isCIMarker(k string) bool {
	for _, m := range CIMarkers {
		if k == m {
			return true
		}
	}
	return false
}

// loreName is the command name lore's git hooks call through PATH.
const loreName = "lore"

// gitOptsWithValue are git's global options that take their value as the
// next word; the subcommand is the first word after them that is not an
// option. Kept to the ones lore and git itself pass.
var gitOptsWithValue = map[string]bool{
	"-C": true, "-c": true, "--git-dir": true, "--work-tree": true,
	"--namespace": true, "--config-env": true, "--exec-path": true,
}

// GitSubcommand returns the git subcommand of a logged command line
// ("-C /x push --porcelain origin" → "push"), or "" when there is none.
// Matching the subcommand, not a substring, keeps `remote get-url --push`
// and `config --get-regexp …pushurl` from counting as pushes.
func GitSubcommand(cmdline string) string {
	f := strings.Fields(cmdline)
	for i := 0; i < len(f); i++ {
		switch {
		case gitOptsWithValue[f[i]]:
			i++
		case strings.HasPrefix(f[i], "-"):
		default:
			return f[i]
		}
	}
	return ""
}

// GitSubcommandCalls returns actor's logged git command lines whose
// subcommand is sub ("push", "fetch", …); actor "" means every actor.
func (w *World) GitSubcommandCalls(actor, sub string) []string {
	w.T.Helper()
	var out []string
	for _, c := range w.GitCalls(actor) {
		if GitSubcommand(c) == sub {
			out = append(out, c)
		}
	}
	return out
}

// GitPushes is GitSubcommandCalls(actor, "push"): real pushes only.
func (w *World) GitPushes(actor string) []string {
	w.T.Helper()
	return w.GitSubcommandCalls(actor, "push")
}

func mustMkdir(t testing.TB, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeExec(t testing.TB, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t testing.TB, path, body string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
