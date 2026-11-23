//go:build setupe2e

package setupe2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Env knobs of the suite (Taskfile lore:test:setupe2e sets none of them;
// they are for a developer narrowing a run).
const (
	// envLore is a prebuilt lore binary; unset = TestMain builds ./cmd/lore once.
	envLore = "SETUPE2E_LORE"
	// envOnly runs only the shapes whose Name contains this text.
	envOnly = "SETUPE2E_ONLY"
	// envShard runs one part of TestAPIMatrix's shapes, "<i>/<n>" (1 <= i <= n): the shapes
	// whose place in AllShapes is i-1 modulo n, plus the team push case in part 1. The gate
	// splits the matrix into parts (lore:test:setupe2e:shard) so each fits one CI runner's
	// time limit; unset runs every shape.
	envShard = "SETUPE2E_SHARD"
)

// loreBin is the binary every world runs.
var loreBin string

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	loreBin = os.Getenv(envLore)
	if loreBin == "" {
		dir, err := os.MkdirTemp("", "setupe2e-bin-")
		if err != nil {
			fmt.Fprintln(os.Stderr, "setupe2e:", err)
			return 1
		}
		defer os.RemoveAll(dir)
		if loreBin, err = BuildLore(context.Background(), dir); err != nil {
			fmt.Fprintln(os.Stderr, "setupe2e: cannot build lore:", err)
			return 1
		}
	} else if abs, err := filepath.Abs(loreBin); err == nil {
		loreBin = abs
	}
	defer CleanupKeys()
	defer removeVersionedLore()
	return m.Run()
}
