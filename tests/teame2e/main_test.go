//go:build teame2e

// Package teame2e is the team scenario suite: it drives a built lore binary
// (cmd/lore, or LORE_TEAM_BIN) through real git clones. It was moved here
// from saas/cmd/cli when the old modules were deleted (SPEC 16 phase 10);
// the helpers below replace the ones it used to borrow from package main.
package teame2e

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire"
)

// e2eBin is the lore binary TestMain builds from ./cmd/lore when
// LORE_TEAM_BIN is unset ("" under -short: the suite then skips).
var e2eBin string

// TestMain builds cmd/lore once into a temp dir unless LORE_TEAM_BIN names
// a binary already, then runs the suite.
func TestMain(m *testing.M) { os.Exit(runMain(m)) }

func runMain(m *testing.M) int {
	flag.Parse()
	defer removeOldLore() // the v0.1.17 build some tests make (fin_compat)
	if os.Getenv(envTeamBin) != "" || testing.Short() {
		return m.Run()
	}
	tmp, err := os.MkdirTemp("", "lore-teame2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	e2eBin = filepath.Join(tmp, "lore")
	goBin, err := exec.LookPath("go")
	if err != nil {
		fmt.Fprintln(os.Stderr, "build lore for team e2e:", err)
		return 1
	}
	// Built as a Cmd directly: the program is the absolute path LookPath
	// resolved and every argument is its own argv entry (no shell).
	build := &exec.Cmd{
		Path: goBin,
		Args: []string{goBin, "build", "-trimpath", "-buildvcs=false", "-tags", "loretest", "-o", e2eBin, "github.com/thesatellite-ai/lore/cmd/lore"},
		Env:  append(os.Environ(), "CGO_ENABLED=0"),
	}
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build lore for team e2e: %v\n%s", err, out)
		return 1
	}
	return m.Run()
}

// actorIDFor is the v0.1.17 natural actor id of a stable key (what
// `lore whoami --json` prints), "" when the key is empty or invalid.
func actorIDFor(key string) string {
	if key == "" {
		return ""
	}
	id, err := identity.ActorID(key)
	if err != nil {
		return ""
	}
	return id.String()
}

// coreTableOf returns the core-owned table whose ids carry id's prefix.
func coreTableOf(id string) (row.Table, bool) {
	p, _, ok := strings.Cut(id, "_")
	if !ok {
		return "", false
	}
	for _, d := range schema.Builtin() {
		if string(d.Prefix) == p && slices.Contains(wire.CoreTables(), d.Name) {
			return d.Name, true
		}
	}
	return "", false
}
