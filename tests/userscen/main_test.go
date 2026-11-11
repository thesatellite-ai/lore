//go:build userscen

// Package userscen is the user-scenario suite: every user-facing lore
// feature driven through the real binary the way a person or an agent uses
// it (temp HOME, real git, bare remotes, `lore api` for REST parity).
// It reuses the team harness (tests/teame2e/teamharness): one World per
// test, actors with their own HOME, fake GitHub remotes, a pinned clock.
//
// Run: task lore:test:scen   (or: go test -tags userscen ./tests/userscen/...)
// Files are owned per area: <area>_test.go, one area per file prefix.
package userscen

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// envScenBin names a prebuilt lore binary (built with -tags loretest); when
// unset TestMain builds cmd/lore once. Under -short the suite skips.
const envScenBin = "LORE_SCEN_BIN"

// scenBin is the binary under test ("" under -short: every World skips).
var scenBin string

func TestMain(m *testing.M) { os.Exit(runMain(m)) }

func runMain(m *testing.M) int {
	flag.Parse()
	if b := os.Getenv(envScenBin); b != "" {
		scenBin = b
		return m.Run()
	}
	if testing.Short() {
		return m.Run()
	}
	tmp, err := os.MkdirTemp("", "lore-userscen-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	scenBin = filepath.Join(tmp, "lore")
	if runtime.GOOS == "windows" {
		scenBin += ".exe" // Windows starts only programs named .exe
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		fmt.Fprintln(os.Stderr, "build lore for userscen:", err)
		return 1
	}
	build := &exec.Cmd{
		Path: goBin,
		Args: []string{goBin, "build", "-trimpath", "-buildvcs=false", "-tags", "loretest", "-o", scenBin, "github.com/thesatellite-ai/lore/cmd/lore"},
		Env:  append(os.Environ(), "CGO_ENABLED=0"),
	}
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build lore for userscen: %v\n%s", err, out)
		return 1
	}
	return m.Run()
}

// newWorld is the one entry point every scenario uses: an isolated World
// driving the binary under test. It marks the test parallel: a World owns
// its whole universe (t.TempDir root, per-actor HOME and config, its own
// git shim and log, environment passed per process, never os.Setenv), so
// no two scenarios share anything but the read-only binary. The suite's
// wall time is therefore bound by its longest scenario, not by their sum.
func newWorld(t *testing.T) *teamharness.World {
	t.Helper()
	scenParallel(t)
	return teamharness.New(t, scenBin)
}

// parallelMarked records the tests that already called t.Parallel through
// scenParallel. testing panics on a second t.Parallel for one test, and a
// scenario may build two Worlds (or call a fixture helper that builds one
// after marking itself), so the mark is made once per *testing.T. Entries
// are removed in the test's cleanup, so the map only holds running tests.
var parallelMarked sync.Map // *testing.T → struct{}

// scenParallel is t.Parallel made idempotent for one test (see
// parallelMarked). Every scenario goes through it instead of calling
// t.Parallel directly.
func scenParallel(t *testing.T) {
	t.Helper()
	if _, done := parallelMarked.LoadOrStore(t, struct{}{}); done {
		return
	}
	t.Cleanup(func() { parallelMarked.Delete(t) })
	t.Parallel()
}
