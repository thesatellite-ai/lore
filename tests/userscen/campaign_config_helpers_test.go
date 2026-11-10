//go:build userscen

package userscen

// campaign_config_helpers_test.go — shared pieces of the config campaign
// (campaign_config_*_test.go): the leaf commands of the tree, the shape of
// a typed refusal on stderr, and a content hash of a folder for "wrote
// nothing" checks.

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// ccMinLeaves is a floor on the leaf count: the tree had 486 leaf
// commands when the campaign started, so far fewer means the help parser
// broke, not that commands went away.
const ccMinLeaves = 450

// ccStackMarks are what a Go panic or runtime crash prints; none may ever
// reach a user.
var ccStackMarks = []string{"panic:", "goroutine ", "runtime error", "[recovered]"}

// ccHintPrefix starts the next-step line every refusal prints after its
// ERROR line (errors.md "Reading an error").
const ccHintPrefix = "\n  hint: "

// ccLeaves are the commands that run something: no subcommands of their
// own (the root and `help` left out).
func ccLeaves(t *testing.T, nodes []cliNode) []cliNode {
	t.Helper()
	var out []cliNode
	for _, n := range nodes {
		if len(n.Path) == 0 || len(n.Subs) > 0 || n.Path[0] == "help" {
			continue
		}
		out = append(out, n)
	}
	if len(out) < ccMinLeaves {
		t.Fatalf("only %d leaf commands: the help parser is broken", len(out))
	}
	return out
}

// ccDescription is the first line of a help page: cobra prints the Long
// text (or Short) first, so it is the command's one-line description.
func ccDescription(help string) string {
	first, _, _ := strings.Cut(help, "\n")
	return strings.TrimSpace(first)
}

// ccExampleLines are the lines of the Examples block that run lore (the
// "# title" lines left out).
func ccExampleLines(help string) []string {
	var out []string
	for _, l := range cliBlock(help, "Examples:") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || cliExampleWords(l) == nil {
			continue
		}
		out = append(out, l)
	}
	return out
}

// ccRequired reports whether a leaf needs input a bare call lacks: a
// required <argument> in its usage line or a flag marked (required).
func ccRequired(n cliNode) bool {
	for _, u := range cliBlock(n.Help, "Usage:") {
		if strings.Contains(u, "<") {
			return true
		}
	}
	for _, l := range cliBlock(n.Help, "Flags:") {
		if strings.Contains(l, "(required)") {
			return true
		}
	}
	return false
}

// ccNoStack fails when a process printed a Go stack or a panic.
func ccNoStack(t *testing.T, r cliProc) {
	t.Helper()
	for _, m := range ccStackMarks {
		if strings.Contains(r.Stdout+r.Stderr, m) {
			t.Errorf("%q in the output (a crash, not a refusal): %s", m, r)
			return
		}
	}
}

// ccTypedRefusal holds a failed run to the text refusal shape: a non-zero
// documented exit that is not 1 (E_INTERNAL), an `ERROR: [E_…]` first
// line on stderr, a hint line naming the next step, nothing on stdout.
func ccTypedRefusal(t *testing.T, r cliProc, wantHint bool) {
	t.Helper()
	ccNoStack(t, r)
	switch {
	case r.TimedOut:
		t.Errorf("hung with no input: %s", r)
	case r.Code == 0 || r.Code == th.ExitError || !slices.Contains(cliExits, r.Code):
		t.Errorf("want a typed refusal exit (not 0, not 1, documented): %s", r)
	case !strings.HasPrefix(r.Stderr, errorLinePrefix):
		t.Errorf("stderr does not start with %q: %s", errorLinePrefix, r)
	case wantHint && !strings.Contains(r.Stderr, ccHintPrefix):
		t.Errorf("no hint line naming the next step: %s", r)
	case r.Stdout != "":
		t.Errorf("a refusal prints nothing on stdout: %s", r)
	}
}

// errorLinePrefix starts every text error line.
const errorLinePrefix = "ERROR: [E_"

// ccTreeHash is a hash of every file under dir (path and bytes), for
// "this run wrote nothing" checks; a missing dir hashes as empty.
func ccTreeHash(t *testing.T, dir string) string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	h := sha256.New()
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(dir, p)
		h.Write([]byte(rel + "\x00"))
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
