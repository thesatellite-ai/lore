package orgcfg

// A store whose first lore write predates lore writing the marker with it
// (owner report 2026-11-11: `store folder add` on a brand-new repo pushed
// only <folder>/_meta.json): its links are read like any
// store's, and a link written into it adds the marker in the same commit;
// a tree with other files stays refused.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
)

// nmMarker is the marker the test's NewMarker writes.
const nmMarker = "lore_store: 2\nid: sto_01a1a5aca7f57659be7320c5f4b70099\n"

// newNoMarkerWorld is a bare store at github.com/agency/notes whose main
// holds files and no marker.
func newNoMarkerWorld(t *testing.T, files map[string]string) *linkWorld {
	t.Helper()
	root := t.TempDir()
	hosts := filepath.Join(root, "hosts")
	gc := filepath.Join(root, "gitconfig")
	writeFile(t, gc, "[user]\n\tname = t\n\temail = t@example.com\n[init]\n\tdefaultBranch = main\n[url \""+filepath.ToSlash(hosts)+"/\"]\n\tinsteadOf = https://github.com/\n")
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv(envAutopush, "")
	origin := filepath.Join(hosts, "agency", "notes.git")
	if err := os.MkdirAll(origin, 0o700); err != nil {
		t.Fatal(err)
	}
	testGit(t, origin, "init", "-q", "--bare", "-b", "main")
	seed := filepath.Join(root, "seed")
	testGit(t, root, "clone", "-q", lwStoreURL, seed)
	for p, body := range files {
		writeFile(t, filepath.Join(seed, filepath.FromSlash(p)), body)
	}
	testGit(t, seed, "add", ".")
	testGit(t, seed, "commit", "-q", "-m", "init")
	testGit(t, seed, "push", "-q", "origin", "main")
	cfg, err := orgconf.Parse([]byte("orgs:\n  - name: agency\n    stores:\n      - url: " + lwStoreURL + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	return &linkWorld{origin: origin, links: &GitLinks{Home: filepath.Join(root, "home"), Config: cfg}}
}

// linkWith writes a link to folder, adding the marker when newMarker.
func (w *linkWorld) linkWith(t *testing.T, folder string, newMarker bool) (LinkWriteResult, error) {
	t.Helper()
	lw := LinkWrite{
		Plan:            orgresolve.LinkPlan{Entry: "agency", StoreURL: lwStoreNorm, Branch: "main", Folder: folder, ProjectID: lwProject, Remote: lwRemote},
		Signature:       gitref.Signature{Name: "Mira", Email: "mira@agency.example", When: time.Unix(1_800_000_000, 0)},
		FolderProjectID: func(string, string, string) (string, error) { return lwFolderID, nil },
	}
	if newMarker {
		lw.NewMarker = func() ([]byte, error) { return []byte(nmMarker), nil }
	}
	return w.links.WriteLink(context.Background(), lw)
}

func TestLinkAdoptsAStoreWithoutMarker(t *testing.T) {
	meta := map[string]string{"client_z/_meta.json": `{"_v":1,"project_id":"` + lwFolderID + `"}` + "\n"}

	t.Run("links are read", func(t *testing.T) {
		w := newNoMarkerWorld(t, meta)
		if _, err := w.links.Links(context.Background(), lwProject, []string{lwRemote}); err != nil {
			t.Fatalf("reading the links of a lore-only store without a marker: %v", err)
		}
	})
	t.Run("a link adds the marker in its commit", func(t *testing.T) {
		w := newNoMarkerWorld(t, meta)
		if _, err := w.linkWith(t, "client_z", true); err != nil {
			t.Fatalf("link: %v", err)
		}
		got := w.changed(t)
		if len(got) != 2 || !strings.Contains(strings.Join(got, " "), storeMarkerFile) || !strings.Contains(strings.Join(got, " "), metaPathOf("client_z")) {
			t.Fatalf("the link commit touched %v, want the marker and the folder's _meta.json", got)
		}
		if body := testGit(t, w.origin, "show", "main:"+storeMarkerFile); body+"\n" != nmMarker && body != nmMarker {
			t.Fatalf("marker %q", body)
		}
	})
	t.Run("without NewMarker it is refused", func(t *testing.T) {
		w := newNoMarkerWorld(t, meta)
		if _, err := w.linkWith(t, "client_z", false); !errors.Is(err, ErrNotAStore) {
			t.Fatalf("link without NewMarker: %v, want ErrNotAStore", err)
		}
	})
	t.Run("a tree with code stays refused", func(t *testing.T) {
		w := newNoMarkerWorld(t, map[string]string{"main.go": "package main\n", "client_z/_meta.json": "{}\n"})
		if _, err := w.linkWith(t, "client_z", true); !errors.Is(err, ErrNotAStore) {
			t.Fatalf("link into a code repo: %v, want ErrNotAStore", err)
		}
		if strings.Contains(testGit(t, w.origin, "ls-tree", "-r", "--name-only", "main"), storeMarkerFile) {
			t.Fatal("a marker was written into a code repo")
		}
	})
}
