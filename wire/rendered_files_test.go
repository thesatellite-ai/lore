package wire

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestIsRenderedFile: the committed rendered files match, by the same
// rules git applies to the .gitattributes patterns (a `*` stays in one
// folder); row files and a person's own files do not.
func TestIsRenderedFile(t *testing.T) {
	t.Parallel()
	for rel, want := range map[string]bool{
		".lore/LORE.md":                         true,
		".lore/LORE.full.md":                    true,
		`.lore\LORE.full.md`:                    true,
		".claude/rules/lore/lore-path-src-2.md": true,
		".cursor/rules/lore-dir-api.mdc":        true,
		".lore/data/rules/rul_x.json":           false,
		".claude/rules/lore/mine.md":            false,
		".claude/rules/lore/sub/lore-path-a.md": false,
		"AGENTS.md":                             false,
		"docs/LORE.full.md":                     false,
	} {
		if got := IsRenderedFile(rel); got != want {
			t.Errorf("IsRenderedFile(%q) = %v, want %v", rel, got, want)
		}
	}
}

// TestDoctorCountsRenderedConflict (devco D-19): a pull that leaves
// .lore/LORE.full.md unmerged (a merge with no keep-ours rule for it) was
// "open conflicts: 0" with no word about the file. Doctor now counts it
// and names the file and the render + add that settle it.
// Without the unmerged-rendered-file check OpenConflicts
// stays 0 and no warning names the file.
func TestDoctorCountsRenderedConflict(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	id := []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com"}
	full := filepath.Join(repo, ".lore", "LORE.full.md")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("# full\n\nbase\n")
	gitIn(t, repo, nil, "add", "-A")
	gitIn(t, repo, id, "commit", "-q", "-m", "init")
	trunk := gitIn(t, repo, nil, "rev-parse", "--abbrev-ref", "HEAD")
	gitIn(t, repo, nil, "checkout", "-q", "-b", "side")
	write("# full\n\nside rule\n")
	gitIn(t, repo, id, "commit", "-q", "-am", "side")
	gitIn(t, repo, nil, "checkout", "-q", trunk)
	write("# full\n\nmain rule\n")
	gitIn(t, repo, id, "commit", "-q", "-am", "main")
	if _, err := (gitRun{dir: repo, env: id, args: []string{"merge", "-q", "--no-edit", "side"}}).output(background()); err == nil {
		t.Fatal("the merge was meant to conflict in LORE.full.md")
	}
	b := doctorBase(t, a)
	if b.Sync == nil || b.Sync.OpenConflicts != 1 {
		t.Fatalf("an unmerged LORE.full.md is an open conflict: %+v", b.Sync)
	}
	if !hasLine(b.Warnings, doctorRenderedConflictLine(".lore/LORE.full.md")) {
		t.Errorf("no warning naming the file and the fix: %v", b.Warnings)
	}
}
