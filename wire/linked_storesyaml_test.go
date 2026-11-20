package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestStoresYAMLOnlyRepoLinkedByRemote (devco D-750): a repo whose .lore/
// holds only the stores.yaml `lore store init` writes, which a store link
// names by its remote (`lore org link --own` there), is a repo linked by
// its remote: open reads it so, and so does linkedByRemote. Before the fix
// open asked "is there a .lore/ with anything in it" while the link side
// asked "is there a project id", so the repo was neither, and every write
// failed "this repository has no lore project" while whoami named the
// link. A repo with a project id of its own stays a lore project.
func TestStoresYAMLOnlyRepoLinkedByRemote(t *testing.T) {
	const remote = "git.example.test/acme/app"
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	lore := filepath.Join(repo, ".lore")
	if err := os.MkdirAll(lore, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lore, "stores.yaml"), []byte("stores: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// (an empty list: the test opens no store; the file's presence is the shape)
	withLink := func(o *Options) { o.RepoLinkedByRemote = func(context.Context) string { return remote } }
	ctx := WithNoSetUp(context.Background()) // a plain write (rule add): it may not set the repo up

	app := newTestAppWith(t, repo, withLink)
	st, err := app.state(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.linkedByRemote != remote {
		t.Fatalf("open: linkedByRemote %q, want %q (a .lore/ with only stores.yaml holds no project)", st.linkedByRemote, remote)
	}
	if got := linkedByRemote(ctx, app.opts); got != remote {
		t.Fatalf("linkedByRemote: %q, want %q", got, remote)
	}

	// the same repo with a project id of its own is a lore project
	own := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", own).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(own, ".lore", "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(own, ".lore", "data", "_meta.json"), []byte(`{"project_id":"prj_0123456789abcdef0123456789abcdef"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !ownProjectAt(own) || ownProjectAt(repo) {
		t.Fatalf("ownProjectAt: own=%v storesOnly=%v, want true/false", ownProjectAt(own), ownProjectAt(repo))
	}
	app2 := newTestAppWith(t, own, withLink)
	if got := linkedByRemote(ctx, app2.opts); got != "" {
		t.Fatalf("linkedByRemote of a repo with its own project id: %q, want \"\"", got)
	}
}
