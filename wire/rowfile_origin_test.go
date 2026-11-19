package wire

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
)

// TestCommittedBadRowFileNamesCommit is devco:R-8: a row file a teammate
// committed broken (pulled in, unchanged here) is named with the commit
// and its author, and the next step undoes that commit; the old step
// ("put it back: git checkout -- <file>") restored the same broken file.
// Once the reader edits the file, the file's own step comes back.
func TestCommittedBadRowFileNamesCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := gitRepo(t)
	gitIn(t, repo, nil, "config", "user.name", "Ada Reader")
	gitIn(t, repo, nil, "config", "user.email", "ada@example.test")
	home := t.TempDir()
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	open := func() *App {
		t.Helper()
		a, nerr := New(Options{RepoDir: repo, Home: home, Actor: actor})
		if nerr != nil {
			t.Fatal(nerr)
		}
		t.Cleanup(func() { _ = a.Close() }) // test teardown
		return a
	}
	a := open()
	withProject(t, a)
	out, err := run(t, a, opsmemory.OpAdd, `{"text":"fine"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	id := idOf(t, out)
	writeRepoFile(t, repo, memPath(repo, id), "{broken")
	kim := []string{"GIT_AUTHOR_NAME=Kim Example", "GIT_AUTHOR_EMAIL=kim@example.test", "GIT_COMMITTER_NAME=Kim Example", "GIT_COMMITTER_EMAIL=kim@example.test"}
	gitIn(t, repo, kim, "add", "-A")
	gitIn(t, repo, kim, "commit", "-q", "--no-verify", "-m", "notes")
	sha := gitIn(t, repo, nil, "log", "-1", "--format=%h")

	r, err := opsdoctor.Run(ctx, open(), opsdoctor.RunIn{})
	if err != nil {
		t.Fatal(err)
	}
	msg := strings.Join(r.Errors, "\n")
	if !strings.Contains(msg, "came in commit "+sha+" by Kim Example <kim@example.test>") || !strings.Contains(msg, "revert "+sha) || strings.Contains(msg, "checkout --") {
		t.Fatalf("doctor must name the commit, its author and the revert: %q", r.Errors)
	}

	writeRepoFile(t, repo, memPath(repo, id), "{still broken")
	r, err = opsdoctor.Run(ctx, open(), opsdoctor.RunIn{})
	if err != nil {
		t.Fatal(err)
	}
	if msg = strings.Join(r.Errors, "\n"); strings.Contains(msg, "came in commit") || !strings.Contains(msg, "checkout --") {
		t.Fatalf("a file edited here keeps its own step: %q", r.Errors)
	}
}
