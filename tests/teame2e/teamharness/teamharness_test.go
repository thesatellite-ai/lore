package teamharness

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeLore stands in for the lore binary in the harness's own tests: it
// prints cobra-style help for the commands listed in FAKE_LORE_CMDS and, for
// `fake-git <args>`, runs git (through the wrapper on PATH) so crash injection
// can be observed killing its parent.
const fakeLore = `#!/bin/sh
if [ "$1" = "fake-git" ]; then shift; git "$@"; echo "survived"; exit 0; fi
if [ "$1" = "grp" ] && [ "$2" != "built" ] && [ "$2" != "--help" ]; then printf 'A group\n\nUsage:\n  lore grp [command]\n\nAvailable Commands:\n  built  a built one\n'; exit 0; fi
last=""; for a in "$@"; do last="$a"; done
if [ "$last" = "--help" ]; then
  path=""; for a in "$@"; do [ "$a" = "--help" ] || path="$path $a"; done
  path="${path# }"
  case "|$FAKE_LORE_CMDS|" in
    *"|$path|"*) printf 'Usage:\n  lore %s [flags]\n\nFlags:\n      --live   share now\n  -h, --help\n' "$path"; exit 0;;
  esac
  printf 'Usage:\n  lore [command]\n'; exit 0
fi
echo "args: $*"; echo "actor=$LORE_ACTOR session=$LORE_SESSION now=$LORE_NOW"
`

func newFakeWorld(t *testing.T, cmds string) *World {
	t.Helper()
	// Named like the real team build (Taskfile TEAM_BIN), not "lore", so
	// every harness test runs with the name lore's hooks do not call.
	bin := WriteProgram(t, filepath.Join(t.TempDir(), "lore-team"), fakeLore) // lore-team.exe on Windows
	t.Setenv("FAKE_LORE_CMDS", cmds)
	return New(t, bin)
}

func TestHarness_URLsReachTheFakeHost(t *testing.T) {
	w := newFakeWorld(t, "")
	app := w.Remote("github.com/acme/app")
	dev := w.Human("dev1", "dev1@acme.example")
	c := dev.Clone(app, "app")
	if !app.Has("refs/heads/main") {
		t.Fatal("first clone of an empty remote must push main")
	}
	if got := c.Git("config", "--get", "remote.origin.url"); got != "https://github.com/acme/app" {
		t.Fatalf("clone must keep the real-looking URL lore resolves orgs from, got %q", got)
	}
	ssh := dev.CloneURL(app, app.SSHURL(), "app-ssh")
	ssh.WriteFile("x.txt", "x")
	ssh.CommitAll("x")
	ssh.Git("push", "-q", "origin", "main")
	if app.CommitCount("refs/heads/main") != 2 {
		t.Fatal("ssh URL form must reach the same bare repo")
	}
	noSuffix := dev.CloneURL(app, "git@github.com:acme/app", "app-nosuffix")
	if noSuffix.Head() != app.Rev("refs/heads/main") {
		t.Fatal("URL without .git must reach the same bare repo")
	}
}

func TestHarness_ProtectedMainRefusesExceptAdmin(t *testing.T) {
	w := newFakeWorld(t, "")
	app := w.Remote("github.com/acme/app")
	dev := w.Human("dev1", "dev1@acme.example")
	c := dev.Clone(app, "app")
	app.ProtectMain()
	c.WriteFile("a.txt", "a")
	c.CommitAll("a")
	r := c.TryGit("push", "origin", "main")
	if r.Code == 0 || !strings.Contains(r.Stderr, "GH006") {
		t.Fatalf("protected main must refuse with GH006: %+v", r)
	}
	c.Git("push", "-q", "origin", "HEAD:refs/heads/feat")
	c.Git("push", "-q", "origin", "HEAD:refs/lore/live")
	if !app.Has("refs/heads/feat") || !app.Has("refs/lore/live") {
		t.Fatal("other refs must still be accepted")
	}
	c.AdminPush("origin", "main")
	if app.Rev("refs/heads/main") != c.Head() {
		t.Fatal("admin push (merge button) must update main")
	}
}

func TestHarness_OfflineIsPerMachine(t *testing.T) {
	w := newFakeWorld(t, "")
	app := w.Remote("github.com/acme/app")
	dev1 := w.Human("dev1", "dev1@acme.example")
	dev2 := w.Human("dev2", "dev2@acme.example")
	c1 := dev1.Clone(app, "app")
	c2 := dev2.Clone(app, "app")
	agent := dev1.Agent("backend-1")
	dev1.Offline()
	if r := c1.TryGit("fetch", "origin"); r.Code == 0 {
		t.Fatal("offline machine must not reach the remote")
	}
	if r := c1.As(agent).TryGit("fetch", "origin"); r.Code == 0 {
		t.Fatal("an agent on the offline machine is offline too")
	}
	if r := c2.TryGit("fetch", "origin"); r.Code != 0 {
		t.Fatalf("other machines stay online: %s", r.Stderr)
	}
	dev1.Online()
	if r := c1.TryGit("fetch", "origin"); r.Code != 0 {
		t.Fatalf("back online: %s", r.Stderr)
	}
}

func TestHarness_ActorEnvAndClock(t *testing.T) {
	w := newFakeWorld(t, "")
	dev := w.Human("alice", "alice@northwind.example")
	agent := dev.Agent("frontend-2")
	agent.SetNow(mustTime(t, "2026-11-02T10:00:00Z"))
	agent.Advance(21 * time.Minute)
	r := agent.Lore("", "whoami").OK(t)
	r.Says(t, "actor=agent:frontend-2", "session=s-frontend-2", "now=2026-11-02T10:21:00Z")
	if strings.Contains(dev.Lore("", "whoami").Stdout, "agent:") {
		t.Fatal("the owner must not inherit the agent's env")
	}
	other := agent.Session("s-two")
	other.Lore("", "x").Says(t, "actor=agent:frontend-2", "session=s-two")
}

func TestHarness_CrashInjectionKillsLore(t *testing.T) {
	w := newFakeWorld(t, "")
	app := w.Remote("github.com/acme/app")
	dev := w.Human("dev1", "dev1@acme.example")
	c := dev.Clone(app, "app")
	counter := filepath.Join(w.Root, "kill-counter")
	before := c.With(EnvKillOn+"=status", EnvKillWhen+"="+KillBefore, EnvKillCounter+"="+counter).Lore("fake-git", "status")
	// killed: 137 or -1 from SIGKILL; taskkill /F leaves exit code 1 on Windows
	if before.Code != 137 && before.Code != -1 && (runtime.GOOS != "windows" || before.Code != 1) {
		t.Fatalf("lore must die at the injected step: %s", before)
	}
	// On Windows the kill ends the fake lore's shim (its stand-in for the
	// lore process); the shell script under it is not lore and may still
	// print, so only the exit code is checked there.
	if runtime.GOOS != "windows" && strings.Contains(before.Out(), "survived") {
		t.Fatal("lore kept running after the kill")
	}
	ok := c.Lore("fake-git", "status")
	if !strings.Contains(ok.Out(), "survived") {
		t.Fatalf("without injection lore runs normally: %s", ok)
	}
	if n := len(w.GitCalls("dev1", "status")); n != 2 {
		t.Fatalf("wrapper must log every git call of lore, got %d", n)
	}
}

func TestHarness_SimultaneouslyRunsAll(t *testing.T) {
	w := newFakeWorld(t, "")
	app := w.Remote("github.com/acme/app")
	var ps []*Pending
	for _, n := range []string{"a1", "a2", "a3", "a4"} {
		c := w.Human(n, n+"@x.example").Clone(app, "app")
		ps = append(ps, c.Prepare("claim", n))
	}
	res := w.Simultaneously(ps...)
	for i, r := range res {
		if r.Code != 0 || !strings.Contains(r.Stdout, "args: claim a") {
			t.Fatalf("run %d: %s", i, r)
		}
	}
}

func TestHarness_RequireDetectsCommandsAndFlags(t *testing.T) {
	w := newFakeWorld(t, "task claim|task add")
	if !w.Has(Cmd("task", "claim")) || !w.Has(Flag("live", "task", "add")) {
		t.Fatal("present command/flag not detected")
	}
	if w.Has(Cmd("land")) || w.Has(Flag("nosuch", "task", "add")) || w.Has(Cmd("task")) {
		t.Fatal("absent command/flag detected as present")
	}
}

func TestHarness_LeakScanSeesIgnoredFilesAndObjects(t *testing.T) {
	w := newFakeWorld(t, "")
	app := w.Remote("github.com/acme/app")
	c := w.Human("dev1", "dev1@acme.example").Clone(app, "app")
	c.WriteFile(".gitignore", "secret.local\n")
	c.WriteFile("secret.local", "client is slow to pay")
	c.CommitAll("ignore")
	if f := c.LeakScan("client is slow to pay"); len(f) != 1 || f[0].Where != "secret.local" {
		t.Fatalf("ignored file must be scanned: %+v", f)
	}
	c.WriteFile("blob.txt", "only in history")
	c.CommitAll("add")
	if err := os.Remove(filepath.Join(c.Dir, "blob.txt")); err != nil {
		t.Fatal(err)
	}
	c.CommitAll("rm")
	c.Git("gc", "-q")
	if f := c.LeakScan("only in history"); len(f) != 1 || f[0].Where != "git objects: blob blob.txt" {
		t.Fatalf("packed objects must be scanned and the finding names the file: %+v", f)
	}
	// A needle in a commit message names the commit, so the owner can find
	// which writer put it there.
	c.WriteFile("x.txt", "x")
	c.CommitAll("agent:backend-1 did this")
	// (The same text is also in .git/COMMIT_EDITMSG and the reflogs, which
	// the scan reports as files.)
	var objs []string
	for _, f := range c.LeakScan("agent:backend-1") {
		if strings.HasPrefix(f.Where, "git objects: ") {
			objs = append(objs, f.Where)
		}
	}
	if len(objs) != 1 || !strings.HasPrefix(objs[0], "git objects: commit ") || !strings.HasSuffix(objs[0], " agent:backend-1 did this") {
		t.Fatalf("a commit message hit names the commit and its subject: %q", objs)
	}
}

func mustTime(t *testing.T, s string) (v time.Time) {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Only the push subcommand is a push: lore's remote and config reads that
// mention "push" must never be counted (lore-live report, F02 autopush and
// Recipe01 failed on them).
func TestGitSubcommand(t *testing.T) {
	for in, want := range map[string]string{
		"push --porcelain origin refs/lore/live":                  "push",
		"-C /tmp/ab push -q origin main":                          "push",
		"-c core.hooksPath=/x -C /r push origin":                  "push",
		"--git-dir /r/.git fetch origin":                          "fetch",
		"--git-dir=/r/.git fetch origin":                          "fetch",
		"-C /r remote get-url --push --all origin":                "remote",
		"-C /r config --get-regexp ^remote\\..*\\.(url|pushurl)$": "config",
		"--version": "",
		"":          "",
	} {
		if got := GitSubcommand(in); got != want {
			t.Errorf("GitSubcommand(%q) = %q want %q", in, got, want)
		}
	}
}

// lore's git hooks call plain `lore` from PATH; with the binary under test
// named lore-team they must still reach it, never a lore installed on the
// machine (lore-live report: F6 ride-along hooks never fired).
func TestHarness_GitHooksReachTheBinaryUnderTest(t *testing.T) {
	w := newFakeWorld(t, "")
	dev := w.Human("dev1", "dev1@acme.example")
	c := dev.Clone(w.Remote("github.com/acme/app"), "app")
	hook := filepath.Join(c.Dir, ".git", "hooks", "post-commit")
	writeExec(t, hook, "#!/bin/sh\nlore from-hook > .git/hook.out\n")
	c.WriteFile("x.txt", "x")
	c.CommitAll("x")
	out, err := os.ReadFile(filepath.Join(c.Dir, ".git", "hook.out"))
	if err != nil || !strings.Contains(string(out), "args: from-hook") {
		t.Fatalf("the hook's `lore` must be the binary under test: %q %v", out, err)
	}
}

// recTB records a Skip or Fatal and stops the calling goroutine, so a test
// can watch the harness end another test as PENDING.
type recTB struct {
	testing.TB
	skipped, failed string
}

func (r *recTB) Helper()                      {}
func (r *recTB) Skip(args ...any)             { r.skipped = fmt.Sprint(args...); runtime.Goexit() }
func (r *recTB) Fatal(args ...any)            { r.failed = fmt.Sprint(args...); runtime.Goexit() }
func (r *recTB) Fatalf(f string, args ...any) { r.failed = fmt.Sprintf(f, args...); runtime.Goexit() }

// run calls fn with a recTB on its own goroutine and returns it.
func runRec(t *testing.T, fn func(tb testing.TB)) *recTB {
	rec := &recTB{TB: t}
	done := make(chan struct{})
	go func() { defer close(done); fn(rec) }()
	<-done
	return rec
}

// cobra exits 0 and prints the group help for an unknown subcommand of an
// existing group; the harness must call that PENDING, never a pass
// (lore-signals report: `MustLore("actor", "register")` passed before the
// command existed).
func TestUnbuiltCommand(t *testing.T) {
	group := "Inspect actors\n\nUsage:\n  lore actor [command]\n\nAvailable Commands:\n  list  List actors\n"
	nested := "Usage:\n  lore org store [command]\n\nAvailable Commands:\n  x  y\n"
	for _, c := range []struct {
		args   []string
		stdout string
		want   string
	}{
		{[]string{"actor", "register"}, group, "lore actor register"},
		{[]string{"actor", "register", "--kind", "agent"}, group, "lore actor register"},
		{[]string{"actor"}, group, "lore actor"},
		{[]string{"actor", "--help"}, group, ""},
		{[]string{"actor", "register", "-h"}, group, ""},
		{[]string{"help", "actor"}, group, ""},
		{[]string{"org", "store", "frob"}, nested, "lore org store frob"},
		{[]string{"actor", "list"}, "ID  NAME\nact_1  Alice\n", ""},
		{[]string{"memory", "add", "--body", "Available Commands: is in my note"}, "✓ mem_1\n", ""},
	} {
		if got := unbuiltCommand(c.args, c.stdout); got != c.want {
			t.Errorf("unbuiltCommand(%q) = %q want %q", c.args, got, c.want)
		}
	}
}

func TestHarness_UnbuiltSubcommandIsPendingNotAPass(t *testing.T) {
	w := newFakeWorld(t, "")
	c := w.Human("dev1", "dev1@acme.example").Clone(w.Remote("github.com/acme/app"), "app")
	rec := runRec(t, func(tb testing.TB) { c.Lore("grp", "missing").OK(tb) })
	if !strings.HasPrefix(rec.skipped, PendingPrefix) || !strings.Contains(rec.skipped, "lore grp missing") || rec.failed != "" {
		t.Fatalf("an unbuilt subcommand must end the test as PENDING: skipped=%q failed=%q", rec.skipped, rec.failed)
	}
	t.Setenv(EnvStrict, "1")
	rec = runRec(t, func(tb testing.TB) { c.Lore("grp", "missing").Says(tb, "anything") })
	if !strings.HasPrefix(rec.failed, PendingPrefix) {
		t.Fatalf("LORE_TEAM_STRICT=1 turns it into a failure: %+v", rec)
	}
	rec = runRec(t, func(tb testing.TB) { c.Lore("grp", "built").OK(tb) })
	if rec.skipped != "" || rec.failed != "" {
		t.Fatalf("a built subcommand runs normally: %+v", rec)
	}
}

// A known open bug makes its regression test PENDING only where the defect
// shows; a closed bug, an absent symptom, or an empty symptom list never
// skips, and LORE_TEAM_STRICT=1 turns the pending into a failure.
func TestBug_PendingOnlyWhileOpenAndSeen(t *testing.T) {
	t.Setenv(EnvStrict, "")
	open := Bug{ID: "BUG-X1", Owner: "lore-x", Open: true}
	refused := Result{Stderr: "ERROR: [E_LEAK_BLOCKED] xref: pointers flow inward only"}
	for _, c := range []struct {
		name    string
		bug     Bug
		symptom []string
		skip    bool
	}{
		{"open and seen", open, []string{"E_LEAK_BLOCKED", "inward only"}, true},
		{"open, one symptom missing", open, []string{"E_LEAK_BLOCKED", "E_CYCLE"}, false},
		{"open, no symptom given", open, nil, false},
		{"closed and seen", Bug{ID: "BUG-X1", Owner: "lore-x"}, []string{"E_LEAK_BLOCKED"}, false},
	} {
		rec := runRec(t, func(tb testing.TB) { c.bug.PendingOn(tb, refused, c.symptom...) })
		if got := rec.skipped != ""; got != c.skip || rec.failed != "" {
			t.Fatalf("%s: skipped=%q failed=%q", c.name, rec.skipped, rec.failed)
		}
		if c.skip && !strings.HasPrefix(rec.skipped, PendingPrefix+" BUG-X1 open (lore-x)") {
			t.Fatalf("%s: the pending reason must name the bug and its owner: %q", c.name, rec.skipped)
		}
	}
	t.Setenv(EnvStrict, "1")
	if rec := runRec(t, func(tb testing.TB) { open.PendingIf(tb, true, "seen") }); rec.failed == "" {
		t.Fatal("strict mode must fail an open bug's test, not skip it")
	}
}
