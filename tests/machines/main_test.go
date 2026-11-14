//go:build machines && !windows

package machines

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/thesatellite-ai/lore/tests/tmpworld"
)

// envLore is a prebuilt lore binary (Taskfile lore:test:machines sets it);
// unset = TestMain builds ./cmd/lore once.
const envLore = "MACHINES_LORE"

// userUmask is the default umask of macOS and most Linux logins: the modes
// the tests assert are the modes a user with the usual umask gets.
const userUmask = 0o022

var loreBin string

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	syscall.Umask(userUmask)
	if n := tmpworld.SweepStale(worldPrefix, worldMaxAge); n > 0 {
		fmt.Fprintf(os.Stderr, "machines: removed %d machine folders a killed run left in %s\n", n, os.TempDir())
	}
	loreBin = os.Getenv(envLore)
	if loreBin == "" {
		dir, err := os.MkdirTemp("", "machines-bin-")
		if err != nil {
			fmt.Fprintln(os.Stderr, "machines:", err)
			return 1
		}
		defer os.RemoveAll(dir)
		loreBin = filepath.Join(dir, "lore")
		c := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", loreBin, "./cmd/lore")
		c.Dir = filepath.Join("..", "..")
		c.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := c.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "machines: cannot build lore: %v\n%s", err, out)
			return 1
		}
	} else if abs, err := filepath.Abs(loreBin); err == nil {
		loreBin = abs
	}
	return m.Run()
}
