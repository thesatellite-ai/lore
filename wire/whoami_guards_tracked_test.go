package wire

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
)

// trackedHookWriters are the commands that would write lore's commit-msg
// block into a hooks folder: none of them may be a guard step there
// (D-732: lore writes nothing into a hooks folder the repo commits).
var trackedHookWriters = []string{"lore org link", "lore fix", "lore sync", "lore doctor --fix", "lore init"}

// assertGuardStepIsPaste is the printed-next-step guard for the client
// repo with a committed hooks folder: the guards-off line names `lore
// doctor` (which prints the paste lines) and no command that writes hooks.
func assertGuardStepIsPaste(t *testing.T, shape, line string) {
	t.Helper()
	if !strings.Contains(line, "guards OFF in this clone") || !strings.Contains(line, "`lore doctor` prints the lines to paste into its commit-msg") {
		t.Errorf("%s: the guards-off line does not name doctor's paste lines:\n%s", shape, line)
	}
	for _, m := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(line, -1) {
		for _, bad := range trackedHookWriters {
			if strings.HasPrefix(strings.TrimSpace(m[1]), bad) {
				t.Errorf("%s: the guards-off line names `%s`, which may not write into a committed hooks folder:\n%s", shape, m[1], line)
			}
		}
	}
	if strings.Contains(line, "lore org link") {
		t.Errorf("%s: the guards-off line names lore org link:\n%s", shape, line)
	}
}

// TestGuardsOffTrackedHooksNamesPasteLines (devco D-732): whoami in a
// client repo whose hooks folder is committed said "guards OFF … `lore org
// link …` here installs it", but the link writes nothing there any more.
// Now the line names doctor's paste lines, for a repo linked by its remote
// and for one no link names yet; a plain .git/hooks clone keeps the link.
// Break-verified: without the trackedHooksDir case both tracked shapes
// name lore org link.
func TestGuardsOffTrackedHooksNamesPasteLines(t *testing.T) {
	ctx := context.Background()
	repo := func(tracked bool) string {
		dir := t.TempDir()
		gitT(t, dir, "init", "-q", "-b", "main")
		if tracked {
			if err := os.MkdirAll(filepath.Join(dir, ".githooks"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".githooks", "pre-push"), []byte("#!/bin/sh\necho gate\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			gitT(t, dir, "add", "-A")
			gitT(t, dir, "-c", "user.email=dev@example.test", "-c", "user.name=Dev", "commit", "-qm", "hooks")
			gitT(t, dir, "config", "core.hooksPath", ".githooks")
		}
		return dir
	}
	result := orgresolve.Result{Role: orgresolve.RoleClient, Org: &orgconf.Org{Name: "northwind"},
		Links: []orgresolve.Link{{Entry: "northwind", Folder: "fabrikam"}}}
	line := func(dir, linked string) string {
		s := &orgcfg.Situation{Dir: dir, LinkedByRemote: linked, Result: result}
		return strings.Join(guardsOffRepoLine(ctx, s, []string{"  repo:  client repo  (guards on)"}), "\n")
	}
	assertGuardStepIsPaste(t, "linked by its remote", line(repo(true), "github.com/fabrikam/shop"))
	assertGuardStepIsPaste(t, "no link yet", line(repo(true), ""))
	if got := line(repo(false), "github.com/fabrikam/shop"); !strings.Contains(got, "lore org link --org northwind --project fabrikam --yes") {
		t.Errorf(".git/hooks clone: want the link that installs the guard, got:\n%s", got)
	}
}
