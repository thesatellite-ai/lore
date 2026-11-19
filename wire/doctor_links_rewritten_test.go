package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRewriteMapAndGoneLinks (devco Q-9): after git filter-repo rewrote
// the history and dropped the old objects, links still named the dropped
// shas and doctor said nothing (it only reported links whose old commit
// was still present). The rewrite's commit-map now tells such a link from
// one to a commit not fetched yet, and doctor names it with the ready
// remap pair; with no map, nothing is said.
func TestRewriteMapAndGoneLinks(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "-C", dir, "init", "-q")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	ctx := context.Background()
	if m := rewriteMap(ctx, dir); m != nil {
		t.Fatalf("no rewrite ran: map = %v, want nil", m)
	}
	const oldA, newA = "5a4ea01a00000000000000000000000000000001", "63d60a8b00000000000000000000000000000001"
	const oldB = "5a4ea01a00000000000000000000000000000002"
	mapFile := filepath.Join(dir, ".git", filepath.FromSlash(filterRepoMap))
	if err := os.MkdirAll(filepath.Dir(mapFile), 0o750); err != nil {
		t.Fatal(err)
	}
	body := "old                                      new\n" + oldA + " " + newA + "\n" + oldB + " " + strings.Repeat("0", fullSHALen) + "\n"
	if err := os.WriteFile(mapFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m := rewriteMap(ctx, dir)
	if m[oldA] != newA || m[oldB] != "" || len(m) != 2 {
		t.Fatalf("map = %v", m)
	}
	line := goneLinksLine([]link{{id: "cml_b", entity: "tsk_2", sha: oldB}, {id: "cml_a", entity: "tsk_1", sha: oldA}}, m)
	for _, want := range []string{"2 link(s) name a commit that no longer exists in this repository", "cml_a (tsk_1 → ", "lore link remap --pair " + oldA + "=" + newA, "lore link remove --id"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q lacks %q", line, want)
		}
	}
}
