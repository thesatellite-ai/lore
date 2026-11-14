package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestOpenDuringInitIgnoresTheRemoteLink: `lore init` makes an empty .lore/
// before it opens the repo; that open sets the project up, so a store link
// naming the repo's remote must not turn it into a read-only linked repo
// (which failed "that folder is not attached" on init in a second clone of
// a linked repo, once an empty .lore/ stopped counting as a project, D-20).
// A read of a repo with no .lore/ still reads it as linked.
func TestOpenDuringInitIgnoresTheRemoteLink(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	linked := func(o *Options) {
		o.RepoLinkedByRemote = func(context.Context) string { return "github.com/acme/svc" }
	}

	// a read in a repo with no .lore/: linked by its remote
	reader := newTestAppWith(t, repo, linked)
	if st, err := reader.state(WithNoCreate(context.Background())); err != nil {
		t.Fatal(err)
	} else if st.linkedByRemote == "" {
		t.Fatal("a read in a repo with no .lore/ that a link names is not read as linked")
	}

	// init's open, after its RootAt made an empty .lore/
	if err := os.Mkdir(filepath.Join(repo, ".lore"), 0o755); err != nil {
		t.Fatal(err)
	}
	initApp := newTestAppWith(t, repo, linked)
	st, err := initApp.state(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.linkedByRemote != "" {
		t.Fatalf("init's open read the repo as linked by %s; it must set the project up", st.linkedByRemote)
	}
}
