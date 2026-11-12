package orgcfg

import (
	"context"
	"path/filepath"
	"testing"
)

// TestGatherNotGitRepo: Gather records whether the folder is in a git work
// tree at all, so whoami can say "not a git repo" for a plain folder
// instead of the client role the resolution gives it (B1-4); a clone, and
// a subfolder of one, is a repo.
func TestGatherNotGitRepo(t *testing.T) {
	home := t.TempDir()
	plain, err := Gather(context.Background(), In{Home: home, Dir: t.TempDir()})
	if err != nil || !plain.NotGitRepo {
		t.Fatalf("plain folder: NotGitRepo %v err %v, want true", plain != nil && plain.NotGitRepo, err)
	}
	repo := gitRepo(t, "git@github.com:acme/tools.git", "")
	sub := filepath.Join(repo, "src")
	writeFile(t, filepath.Join(sub, "main.go"), "package main\n")
	for _, dir := range []string{repo, sub} {
		s, err := Gather(context.Background(), In{Home: home, Dir: dir})
		if err != nil || s.NotGitRepo {
			t.Fatalf("%s: NotGitRepo %v err %v, want false", dir, s != nil && s.NotGitRepo, err)
		}
	}
}
