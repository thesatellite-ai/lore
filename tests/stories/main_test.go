//go:build stories && !windows

package stories

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

// envLore is a prebuilt lore binary built with -tags loretest (the Taskfile
// sets it); unset = TestMain builds ./cmd/lore that way once.
const envLore = "STORIES_LORE"

// userUmask is the usual login umask: the file modes the shared invariants
// assert are the ones a person with it gets.
const userUmask = 0o022

func TestMain(m *testing.M) { os.Exit(runMain(m)) }

func runMain(m *testing.M) int {
	syscall.Umask(userUmask)
	if bin := os.Getenv(envLore); bin != "" {
		journeys.UseBinary(bin)
		return m.Run()
	}
	dir, err := os.MkdirTemp("", "stories-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "stories:", err)
		return 1
	}
	defer os.RemoveAll(dir)
	bin := filepath.Join(dir, "lore")
	c := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-tags", "loretest", "-o", bin, "./cmd/lore")
	c.Dir = filepath.Join("..", "..")
	c.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := c.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "stories: cannot build lore: %v\n%s", err, out)
		return 1
	}
	journeys.UseBinary(bin)
	return m.Run()
}
