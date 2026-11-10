//go:build journeys && !windows

package journeys

// harness_oldlore.go — a real lore 0.1.17 beside the lore under test, for
// the upgrader journey: one machine keeps running the old binary while
// another runs the new one, both writing to the same repo.
//
//	JOURNEYS_LORE_OLD set ──▶ that binary (must answer "lore version 0.1.17")
//	else ──▶ built once from the v0.1.17 tag (git archive + go build, the
//	         release's own main package) into $TMPDIR/lore-journeys-v0117/,
//	         reused by later runs; no tag here ──▶ the test is skipped
//
// An old machine has the old lore first on its PATH, so the git hooks 0.1.17
// installs (which run whatever `lore` is on PATH) run the old binary too. It
// is put to Sleep: the shared invariants run the NEW lore (doctor in every
// clone), which would rewrite the old machine's hooks and state, and a
// machine that only has 0.1.17 never runs the new lore. Its own checks are
// the old binary's, made by the journey. Upgrading it (OldMachine.Upgrade)
// puts the clean PATH back and wakes it.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const (
	// envLoreOld names a prebuilt lore 0.1.17.
	envLoreOld = "JOURNEYS_LORE_OLD"
	// oldLoreTag is the release the upgrader comes from.
	oldLoreTag = "v0.1.17"
	// oldLoreVersion is what `lore --version` of that release prints.
	oldLoreVersion = "lore version 0.1.17"
	// oldLoreMain is the release's main package (a go.work workspace then).
	oldLoreMain = "./saas/cmd/cli"
)

var (
	oldLoreOnce sync.Once
	oldLorePath string
	oldLoreSkip string // why there is no old binary (the test skips)
	oldLoreErr  string // the build failed (the test fails)
)

// oldLoreBin returns a lore 0.1.17 binary, building it on first use.
func oldLoreBin(t testing.TB) string {
	t.Helper()
	oldLoreOnce.Do(provideOldLore)
	switch {
	case oldLoreErr != "":
		t.Fatalf("lore %s: %s", oldLoreTag, oldLoreErr)
	case oldLoreSkip != "":
		t.Skipf("lore %s: %s", oldLoreTag, oldLoreSkip)
	}
	return oldLorePath
}

func provideOldLore() {
	if p := os.Getenv(envLoreOld); p != "" {
		oldLorePath = p
		checkOldVersion()
		return
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		oldLoreErr = err.Error()
		return
	}
	commit, err := exec.Command("git", "-C", repo, "rev-parse", "--verify", "-q", oldLoreTag+"^{commit}").Output()
	if err != nil {
		oldLoreSkip = "the tag is not in this clone and " + envLoreOld + " is unset"
		return
	}
	cache := filepath.Join(os.TempDir(), "lore-journeys-v0117", strings.TrimSpace(string(commit))[:12])
	oldLorePath = filepath.Join(cache, "lore")
	if _, err := os.Stat(oldLorePath); err == nil {
		checkOldVersion()
		return
	}
	src, err := os.MkdirTemp("", "lore-v0117-src-")
	if err != nil {
		oldLoreErr = err.Error()
		return
	}
	defer os.RemoveAll(src)
	archive := exec.Command("sh", "-c", `git -C "$1" archive "$2" | tar -x -C "$3"`, "sh", repo, oldLoreTag, src)
	if out, err := archive.CombinedOutput(); err != nil {
		oldLoreErr = "git archive: " + err.Error() + ": " + string(out)
		return
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		oldLoreErr = err.Error()
		return
	}
	tmp := oldLorePath + ".tmp"
	build := exec.Command("go", "build", "-trimpath", "-ldflags", "-X main.version=0.1.17", "-o", tmp, oldLoreMain)
	build.Dir = src
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK="+filepath.Join(src, "go.work"))
	if out, err := build.CombinedOutput(); err != nil {
		oldLoreErr = "go build: " + err.Error() + ": " + string(out)
		return
	}
	if err := os.Rename(tmp, oldLorePath); err != nil {
		oldLoreErr = err.Error()
		return
	}
	checkOldVersion()
}

func checkOldVersion() {
	out, err := exec.Command(oldLorePath, "--version").Output()
	if err != nil || !strings.Contains(string(out), oldLoreVersion) {
		oldLoreErr = oldLorePath + " is not lore 0.1.17: " + strings.TrimSpace(string(out))
	}
}

// OldMachine is a machine that runs lore 0.1.17.
type OldMachine struct {
	*Machine
	bin    string
	oldDir string // holds `lore` → the old binary, first on PATH
}

// UseOldLore turns m into a machine whose lore is 0.1.17 (see the file
// comment): the old binary first on PATH, and asleep for the invariants.
func UseOldLore(t testing.TB, m *Machine) *OldMachine {
	t.Helper()
	bin := oldLoreBin(t)
	dir := filepath.Join(m.w.Root, "oldlore-"+m.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, filepath.Join(dir, "lore")); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	o := &OldMachine{Machine: m, bin: bin, oldDir: dir}
	m.SetEnv("PATH", dir+":"+filepath.Join(m.w.Root, "shim")+":/usr/bin:/bin")
	m.Sleep()
	return o
}

// Old runs lore 0.1.17 in clone c (nil: HOME) with the machine's environment.
func (o *OldMachine) Old(c *teamharness.Clone, args ...string) teamharness.Result {
	o.w.T.Helper()
	if c == nil {
		c = &teamharness.Clone{A: o.Actor, Dir: o.Home}
	}
	cmd := c.Command(args...)
	cmd.Path = o.bin
	cmd.Args[0] = o.bin
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	r := teamharness.Result{Args: args, Stdout: so.String(), Stderr: se.String()}
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			o.w.T.Fatalf("lore 0.1.17 %s: %v", strings.Join(args, " "), err)
		}
		r.Code = ee.ExitCode()
	}
	return r
}

// Upgrade is the person installing the new lore on this machine: the clean
// PATH (the lore under test) comes back and the invariants check it again.
func (o *OldMachine) Upgrade() *Machine {
	o.SetEnv("PATH", filepath.Join(o.w.Root, "shim")+":/usr/bin:/bin")
	o.Wake()
	return o.Machine
}
