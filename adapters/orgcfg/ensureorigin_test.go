package orgcfg

// ensureBare on an existing clone follows the configured url: a clone made
// from an earlier config (a url git could not fetch, or a moved store) must
// fetch the new address, not read as offline forever (docs reference
// example `lore org link agency` after the config's store url changed).

import (
	"context"
	"path/filepath"
	"testing"
)

func TestEnsureBareFollowsTheConfiguredURL(t *testing.T) {
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
			dir := filepath.Join(t.TempDir(), "clone.git")
			if err := ensureBare(ctx, dir, c.first); err != nil {
				t.Fatal(err)
			}
			if err := ensureBare(ctx, dir, remote); err != nil {
				t.Fatal(err)
			}
			if got := testGit(t, dir, "config", "--get", "remote."+remoteName+".url"); got != remote {
				t.Fatalf("origin = %q, want %q", got, remote)
			}
			if err := fetch(ctx, dir); err != nil {
				t.Fatalf("fetch after the url change: %v", err)
			}
			// refspecs stay lore's two (set-url never touches them)
			if got := testGit(t, dir, "config", "--get-all", "remote."+remoteName+".fetch"); got != fetchRefspecs[0]+"\n"+fetchRefspecs[1] {
				t.Fatalf("fetch refspecs = %q", got)
			}
		})
	}
}
