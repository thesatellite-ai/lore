//go:build journeys && !windows

package journeys

// fix_commit_test.go — committing after `lore fix` (bugs B1-14, B1-20;
// decisions D2, D3). Dana's old repo has a memory whose body has held a
// key-shaped value since lore 0.1.17. `lore fix` rewrites every row file
// and warns once about that row (id and fingerprint, never the value). A
// code commit of README alone holds README alone: the pending migration is
// not swept in. A refused commit leaves the index exactly as she staged
// it. The migration commit itself goes through: the value was already in
// that row at HEAD, so it is not new. A truly new key is still refused.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const fixWarnMemory = "mem_01a1cb5743e371b7bab946c6cf48aa01"

// fixOldRepoWarned writes a v0.1.17 project whose memory body holds key,
// and commits it.
func fixOldRepoWarned(t testing.TB, c *teamharness.Clone, key string) {
	t.Helper()
	c.WriteFile(".lore/data/_meta.json", `{"_v": 1, "project_id": "`+fixProject+`"}`+"\n")
	c.WriteFile(".lore/data/projects/"+fixProject+".json", `{"_table": "projects", "_v": 1, "archived_at": null, "created_at": "2026-11-03T10:00:00.000000000Z", "id": "`+fixProject+
		`", "name": "widgets", "origin_url": null, "updated_at": "2026-11-03T10:00:00.000000000Z"}`+"\n")
	c.WriteFile(".lore/data/memories/"+fixWarnMemory+".json", `{"_table": "memories", "_v": 1, "body": "the staging key is `+key+`", "created_at": "2026-11-03T10:00:01.000000000Z", "id": "`+fixWarnMemory+
		`", "kind": "retrieved", "project_id": "`+fixProject+`", "source_kind": "manual", "trust_score": 0.5, "updated_at": "2026-11-03T10:00:01.000000000Z"}`+"\n")
	c.WriteFile("README.md", "# widgets\n")
	for _, rel := range []string{"_meta.json", "projects/" + fixProject + ".json", "memories/" + fixWarnMemory + ".json"} {
		fixCheckoutMode(t, c, ".lore/data/"+rel)
	}
	c.Git("add", "-A")
	c.Git("commit", "-q", "--no-verify", "-m", "lore 0.1.17 rows") // the old history, as it is
	c.Git("push", "-q", "origin", "HEAD")                          // … and pushed long ago: history (bug L-1 counts only what was pushed)
}

// fixCheckoutMode gives a file the 0644 a checkout gives committed files
// (the harness writes 0600).
func fixCheckoutMode(t testing.TB, c *teamharness.Clone, rel string) {
	t.Helper()
	if err := os.Chmod(filepath.Join(c.Dir, filepath.FromSlash(rel)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestJourneyCommitAfterFix(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	dana := w.Persona("Dana Reyes", "dana@widgets.example")
	lap := dana.Laptop()
	old := "AKIA" + strings.Repeat("Q", 16) // built at run time: no key-shaped literal in the repo
	fresh := "AKIA" + strings.Repeat("Z", 16)
	var c *teamharness.Clone

	w.Step("day1/old-repo-with-a-committed-key", func() {
		c = lap.Clone(w.Remote("github.com/widgets/keys"), "keys")
		fixOldRepoWarned(t, c, old)
	})

	w.Step("day1/lore-fix-warns-once-by-id-and-fingerprint", func() {
		r := lap.LoreIn(c, "fix").OK(t)
		r.Says(t, "Upgraded", "secret-looking value(s) in the rewritten rows", fixWarnMemory, "sha256:", "lore secrets allow").Never(t, old)
		if n := strings.Count(r.Out(), "secret-looking value(s) in the rewritten rows"); n != 1 {
			t.Fatalf("the warning appears %d times:\n%s", n, r.Out())
		}
	})

	w.Step("day1/a-readme-commit-holds-only-readme", func() {
		c.WriteFile("README.md", "# widgets\n\nnotes\n")
		c.Git("add", "README.md")
		r := c.TryGit("commit", "-q", "-m", "docs: notes")
		r.OK(t).Says(t, "format migration", "not added to this commit")
		if files := c.Git("show", "--name-only", "--format=", "HEAD"); files != "README.md" {
			t.Fatalf("the commit holds %q, want README.md only", files)
		}
	})

	w.Step("day1/a-refused-commit-leaves-the-index-as-staged", func() {
		c.WriteFile("README.md", "# widgets\n\nkey "+fresh+"\n")
		c.Git("add", "README.md")
		before := c.Git("diff", "--cached", "--name-only")
		c.TryGit("commit", "-q", "-m", "docs: key").Refused(t, "E_LEAK_BLOCKED").Never(t, fresh)
		if after := c.Git("diff", "--cached", "--name-only"); after != before {
			t.Fatalf("the refused commit changed the index: before %q after %q", before, after)
		}
		c.Git("reset", "-q", "HEAD", "--", "README.md")
		c.Git("checkout", "--", "README.md")
	})

	w.Step("day1/the-migration-commit-goes-through", func() {
		c.Git("add", "-A", ".lore")
		r := c.TryGit("commit", "-q", "-m", "lore: migrate format")
		r.OK(t).Says(t, "already in the same row(s) at HEAD").Never(t, old)
	})

	w.Step("day1/a-new-key-in-that-row-is-still-refused", func() {
		path := ".lore/data/memories/" + fixWarnMemory + ".json"
		body, _ := c.ReadFile(path)
		c.WriteFile(path, strings.Replace(body, old, old+" and "+fresh, 1))
		fixCheckoutMode(t, c, path)
		c.Git("add", path)
		c.TryGit("commit", "-q", "-m", "memory: second key").Refused(t, "E_LEAK_BLOCKED", fixWarnMemory).Never(t, fresh)
	})
}
