package orgcfg

// ensureClone on an existing clone follows the configured url: a clone made
// from an earlier config (a url git could not fetch, or a moved store) must
// fetch the new address, not read as offline forever (docs reference
// example `lore org link agency` after the config's store url changed).

import (
	"context"
	"path/filepath"
	"testing"
)

func TestEnsureCloneFollowsTheConfiguredURL(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	remote := filepath.Join(root, "notes.git")
	if _, err := gitRun(ctx, "init", "--quiet", "--bare", remote); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		first string // the url the clone was made with
	}{
		{"host/path form git cannot fetch", "example.com/agency/notes"},
		{"moved store", filepath.Join(root, "old-notes.git")},
		{"same url", remote},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "clone")
			if err := ensureClone(ctx, dir, c.first, false); err != nil {
				t.Fatal(err)
			}
			if err := ensureClone(ctx, dir, remote, false); err != nil {
				t.Fatal(err)
			}
			if got := testGit(t, dir, "config", "--get", "remote."+remoteName+".url"); got != remote {
				t.Fatalf("origin = %q, want %q", got, remote)
			}
			if err := fetch(ctx, nil, dir); err != nil {
				t.Fatalf("fetch after the url change: %v", err)
			}
			// the clone's config keeps git's own refspec (a person's plain
			// `git fetch`); lore's live refspec is passed per fetch only
			if got := testGit(t, dir, "config", "--get-all", "remote."+remoteName+".fetch"); got != fetchRefspecs[0] {
				t.Fatalf("fetch refspecs = %q, want git's default %q", got, fetchRefspecs[0])
			}
		})
	}
}
