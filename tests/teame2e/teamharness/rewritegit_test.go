package teamharness

import "testing"

// TestHarness_RewriteGitAlwaysRewrites (devco:D-459): a forced rebase made
// in the same second as the commits it replays writes the same commit ids
// (git dates to the second), so a test that expects lore's post-rewrite
// hook to move a commit link flaked. The commits here are pinned to one
// second, as if every step ran inside it; RewriteGit still gets new ids.
func TestHarness_RewriteGitAlwaysRewrites(t *testing.T) {
	w := newFakeWorld(t, "")
	app := w.Remote("github.com/acme/rewrite")
	dev := w.Human("dev1", "dev1@acme.example")
	c := dev.Clone(app, "app").With("GIT_COMMITTER_DATE=@1700000000 +0000", "GIT_AUTHOR_DATE=@1700000000 +0000")
	c.WriteFile("a.txt", "a")
	c.CommitAll("one")
	c.WriteFile("b.txt", "b")
	c.CommitAll("two")
	before := c.Git("rev-parse", "HEAD~1", "HEAD")
	if r := c.TryGit("rebase", "--force-rebase", "HEAD~1"); r.Code != 0 || c.Git("rev-parse", "HEAD~1", "HEAD") != before {
		t.Fatalf("precondition: a same-second forced rebase keeps the ids (exit %d %s)", r.Code, r.Stderr)
	}
	if r := c.RewriteGit("rebase", "--force-rebase", "HEAD~1"); r.Code != 0 {
		t.Fatalf("rebase: exit %d\n%s", r.Code, r.Stderr)
	}
	if after := c.Git("rev-parse", "HEAD~1", "HEAD"); after[:40] != before[:40] || after == before {
		t.Errorf("RewriteGit: want HEAD~1 kept and HEAD rewritten, got\n%s\nbefore\n%s", after, before)
	}
}
