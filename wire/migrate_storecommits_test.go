package wire

// migrate_storecommits_test.go — devco:D-707: a migration run lists every
// store commit it made in a clone (the org_defaults_to_policy commits
// too, not only the tip), oldest first with its subject, and never a
// commit that was there before the run (or merged in from a teammate).

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestOwnStoreCommitsD707(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=Ann Example", "-c", "user.email=ann@example.test", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "a teammate's note")
	before := git("rev-parse", "HEAD")
	git("commit", "-q", "--allow-empty", "-m", "lore: migrate org defaults to policies")
	git("commit", "-q", "--allow-empty", "-m", "lore: policies row")
	tip := git("rev-parse", "HEAD")

	got := ownStoreCommits(ctx, journalPlace{Clone: true, Dir: dir, TipBefore: before, TipAfter: tip})
	if len(got) != 2 || got[0].subject != "lore: migrate org defaults to policies" || got[1].subject != "lore: policies row" {
		t.Fatalf("own store commits = %+v, want the run's two, oldest first", got)
	}
	if !strings.HasPrefix(tip, got[1].short) {
		t.Errorf("short id %q is not the tip %s", got[1].short, tip)
	}
	if none := ownStoreCommits(ctx, journalPlace{Clone: true, Dir: dir, TipBefore: tip, TipAfter: tip}); len(none) != 0 {
		t.Errorf("a clone the run did not commit in lists %+v", none)
	}
}
