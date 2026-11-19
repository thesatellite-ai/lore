package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/branchlayer"
	"github.com/thesatellite-ai/lore/store"
)

// TestLayerRepoKeyResolvesAliasD532 (devco D-532): a checkout whose origin
// is an ssh host alias (git@<alias>:acme/app.git, the alias a Host of the
// ssh config with HostName github.com) keys its branch layers by
// github.com/acme/app, as a clone from github.com does, so the two share
// their layers; `branch land --repo` takes the alias url the same way. A
// repo whose open layers were all made under the alias spelling's key
// before keeps that key (read, never moved) until a layer exists under the
// resolved one.
func TestLayerRepoKeyResolvesAliasD532(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "Test")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "test@example.com")
	}
	ctx := context.Background()
	// the person's ssh config names the alias; wire's resolver (storeaccess.go
	// init) reads it and caches per host, so the alias is this test's own
	user := t.TempDir()
	t.Setenv("HOME", user)
	if err := os.MkdirAll(filepath.Join(user, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(user, ".ssh", "config"), []byte("Host work-gh-d532\n  HostName github.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	codeAt := func(remote string) codeRepo {
		t.Helper()
		dir := t.TempDir()
		run(dir, "init", "-q", "--initial-branch=feat-x")
		run(dir, "remote", "add", "origin", remote)
		c, ok := readCodeRepo(ctx, dir)
		if !ok {
			t.Fatalf("readCodeRepo(%s): no repo", remote)
		}
		return c
	}
	const want = "github.com/acme/app"
	alias := codeAt("git@work-gh-d532:acme/app.git")
	plain := codeAt("https://github.com/acme/app.git")
	if alias.Remote != want || alias.PlainRemote != "work-gh-d532/acme/app" {
		t.Fatalf("alias clone: Remote %q PlainRemote %q, want %q and the alias spelling", alias.Remote, alias.PlainRemote, want)
	}
	if plain.Remote != want || plain.PlainRemote != "" {
		t.Fatalf("github.com clone: Remote %q PlainRemote %q", plain.Remote, plain.PlainRemote)
	}
	arg, err := normalizeRepoArg(ctx, "git@work-gh-d532:acme/app.git")
	if err != nil || arg.Remote != want || arg.PlainRemote != "work-gh-d532/acme/app" {
		t.Fatalf("--repo alias url: %+v %v", arg, err)
	}

	bare := filepath.Join(t.TempDir(), "notes.git")
	seedStore(t, bare)
	gb, err := git.OpenClone(ctx, bare, filepath.Join(t.TempDir(), "clone"), rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gb.Close() })
	resolved, legacy := branchlayer.RepoKey(want), branchlayer.RepoKey(alias.PlainRemote)
	if got, other := layerRepoKey(ctx, gb, alias), layerRepoKey(ctx, gb, plain); got != resolved || other != resolved {
		t.Fatalf("no layers yet: alias clone key %s, github.com clone key %s, want both %s", got, other, resolved)
	}
	mkLayer := func(repoKey string) {
		t.Helper()
		k := branchlayer.Key{StoreBranch: gb.Branch(), Folder: rowfs.FolderRoot, RepoKey: repoKey, Branch: "feat-x"}
		run(gb.Dir(), "update-ref", k.Ref(), "HEAD")
	}
	mkLayer(legacy) // a layer written before D-532 under the alias spelling
	if got := layerRepoKey(ctx, gb, alias); got != legacy {
		t.Fatalf("layers only under the alias key: got %s, want the old key %s", got, legacy)
	}
	mkLayer(resolved)
	if got := layerRepoKey(ctx, gb, alias); got != resolved {
		t.Fatalf("layers under both keys: got %s, want the resolved key %s", got, resolved)
	}
}
