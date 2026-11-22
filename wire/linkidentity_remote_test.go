package wire

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// TestRepoHomeStoreOfRemoteLinkedRepo is devco:D-912 (public issue #126):
// in a client repo with no lore data that a store links by its remote,
// RepoHomeStore answered "the repo store, not linked", so `lore secrets
// allow --file` (the line a refused commit prints) wrote into the repo
// store, a client-class store, pointing at the linked folder's project
// (F15 refused it), and the commit check never read the folder's allows.
// It now answers the linked folder; with that folder not attached here it
// is E_STORE_UNREACHABLE naming the link (linked true), never the repo.
func TestRepoHomeStoreOfRemoteLinkedRepo(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	app := newTestAppWith(t, repo, func(o *Options) {
		o.RepoLinkedByRemote = func(context.Context) string { return "git.example.test/client/shop" }
	})
	src, linked, err := app.RepoHomeStore(context.Background())
	if !linked {
		t.Fatalf("RepoHomeStore of a repo linked by its remote = %q, linked false, %v: want the linked folder", src, err)
	}
	if src == store.SourceRepo {
		t.Fatalf("RepoHomeStore named the client repo's own store")
	}
	if errs.CodeOf(err) != errs.StoreUnreachable || !strings.Contains(err.Error(), "git.example.test/client/shop") {
		t.Fatalf("a linked folder not attached here: %v, want E_STORE_UNREACHABLE naming the link", err)
	}
	// the commit check reads no allow from the repo store there, and says
	// nothing about a folder it cannot read
	if got := app.linkedAllows(context.Background()); len(got.entries) != 0 || got.home != "" {
		t.Fatalf("linkedAllows with the folder unreachable = %+v, want none", got)
	}
}

// TestRepoHomeStoreOfUnlinkedRepo: a repo no store links keeps the repo
// store as its home (the remote branch answers only a linked repo).
func TestRepoHomeStoreOfUnlinkedRepo(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	app := newTestAppWith(t, repo, func(o *Options) {
		o.RepoLinkedByRemote = func(context.Context) string { return "" }
	})
	src, linked, err := app.RepoHomeStore(context.Background())
	if err != nil || linked || src != store.SourceRepo {
		t.Fatalf("RepoHomeStore of an unlinked repo = %q, %v, %v: want the repo store", src, linked, err)
	}
}

// TestLeakHintOfLinkedRepo (devco:D-912, issue #126): a refused commit in a
// linked repo told the person to "add the new .lore/data/secret_allows
// file", which a linked repo never gets (the entry is saved in the linked
// folder). The hint now names that folder and says there is no file to
// add; an unlinked repo keeps the file step.
func TestLeakHintOfLinkedRepo(t *testing.T) {
	k := findingKinds{file: true}
	linked := leakHint(checkWhereCommit, false, "acme/client_app", k)
	for _, want := range []string{"acme/client_app", "no file to add", "then commit again", "no --no-verify needed"} {
		if !strings.Contains(linked, want) {
			t.Errorf("linked repo hint %q lacks %q", linked, want)
		}
	}
	if strings.Contains(linked, ".lore/data/secret_allows") {
		t.Errorf("linked repo hint asks for a .lore/data file it never gets: %q", linked)
	}
	if push := leakHint(LeakWherePush, false, "acme/client_app", k); !strings.Contains(push, "then push again") {
		t.Errorf("linked repo push hint %q lacks the push step", push)
	}
	if own := leakHint(checkWhereCommit, false, "", k); !strings.Contains(own, "add the new .lore/data/secret_allows file") {
		t.Errorf("unlinked repo hint lost the file step: %q", own)
	}
}
