package wire

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// scannedStores offers the remote stores of repos lore has not
// initialised under the includeIf / setup folders: a gitdir "**" pattern
// is walked as its folder, a known repo and a local store are left out.
func TestScannedStores(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	put := func(root, body string) {
		t.Helper()
		p := filepath.Join(home, root, ".lore", "stores.yaml")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put("work/acme/app", "stores:\n  - name: team\n    url: git@github.com:acme/lore.git\n  - name: mine\n    url: file:///tmp/x\n  - name: abs\n    url: /tmp/y\n")
	put("work/acme/known", "stores:\n  - name: hub\n    url: git@github.com:acme/hub.git\n")
	put("code/side", "stores:\n  - name: side\n    url: https://github.com/alice/notes.git\n")
	got := scannedStores(context.Background(), home, []string{"~/work/acme/**", "~/code/", "~/work/acme/"}, []string{filepath.Join(home, "work", "acme", "known")})
	want := []opssetup.KnownStore{
		{URL: "https://github.com/alice/notes.git", Kind: opssetup.SrcRepoScan, Detail: "~/code/side (side)"},
		{URL: "git@github.com:acme/lore.git", Kind: opssetup.SrcRepoScan, Detail: "~/work/acme/app (team)"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("scanned\n got %+v\nwant %+v", got, want)
	}
	if none := scannedStores(context.Background(), home, nil, nil); none != nil {
		t.Errorf("no folders = %+v, want nil", none)
	}
}
