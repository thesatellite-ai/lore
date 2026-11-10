//go:build journeys && !windows

package journeys

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// envLore is a prebuilt lore binary built with -tags loretest (the Taskfile
// sets it); unset = TestMain builds ./cmd/lore that way once.
const envLore = "JOURNEYS_LORE"

// userUmask is the usual login umask: the modes the invariants assert are
// the ones a user with it gets.
const userUmask = 0o022

var loreBin string

func TestMain(m *testing.M) { os.Exit(runMain(m)) }

func runMain(m *testing.M) int {
	syscall.Umask(userUmask)
	if loreBin = os.Getenv(envLore); loreBin != "" {
		return m.Run()
	}
	dir, err := os.MkdirTemp("", "journeys-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "journeys:", err)
		return 1
	}
	defer os.RemoveAll(dir)
	loreBin = filepath.Join(dir, "lore")
	c := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-tags", "loretest", "-o", loreBin, "./cmd/lore")
	c.Dir = filepath.Join("..", "..")
	c.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := c.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "journeys: cannot build lore: %v\n%s", err, out)
		return 1
	}
	return m.Run()
}
