package orgcfg

// later 131 (v0.1.17 stores.WriteLink / WriteOrgFile on a lore_store: 2
// store, ported to cmd/lore): a link writes only the folder's _meta.json
// (no projects row, nothing else in the folder), adopts a folder that has
// rows but no _meta.json without touching the rows, and a re-link is a
// no-op; lore-org.yaml keeps a same-name file and refuses another org's.

import (
	"context"
	"encoding/json"
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

const (
	lwStoreURL  = "https://github.com/agency/notes"
	lwStoreNorm = "github.com/agency/notes"
	lwRemote    = "github.com/acme/app"
	lwProject   = "prj_01a1a5aca7f57659be7320c5f4b70011"
	lwFolderID  = "prj_01a1a5aca7f57659be7320c5f4b70022"
)

// linkWorld is a bare store at github.com/agency/notes (through
// url.insteadOf, so the store url is a real hosted one) whose main holds
// the marker and a folder "rows/" with a row file but no _meta.json.
type linkWorld struct {
	origin string
	links  *GitLinks
}

func newLinkWorld(t *testing.T) *linkWorld {
	t.Helper()
	root := t.TempDir()
	hosts := filepath.Join(root, "hosts")
	gc := filepath.Join(root, "gitconfig")
	writeFile(t, gc, "[user]\n\tname = t\n\temail = t@example.com\n[init]\n\tdefaultBranch = main\n[url \""+hosts+"/\"]\n\tinsteadOf = https://github.com/\n")
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
	writeFile(t, filepath.Join(seed, storeMarkerFile), "version: 2\n")
	writeFile(t, filepath.Join(seed, "rows", ".lore", "data", "memories", "mem_01a1a5aca7f57659be7320c5f4b70033.json"), `{"_v":2,"id":"mem_01a1a5aca7f57659be7320c5f4b70033"}`+"\n")
	testGit(t, seed, "add", ".")
	testGit(t, seed, "commit", "-q", "-m", "init")
	testGit(t, seed, "push", "-q", "origin", "main")
	cfg, err := orgconf.Parse([]byte("orgs:\n  - name: agency\n    stores:\n      - url: " + lwStoreURL + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	return &linkWorld{origin: origin, links: &GitLinks{Home: filepath.Join(root, "home"), Config: cfg}}
}

func (w *linkWorld) link(t *testing.T, folder string) (LinkWriteResult, error) {
	t.Helper()
	return w.links.WriteLink(context.Background(), LinkWrite{
		Plan:      orgresolve.LinkPlan{Entry: "agency", StoreURL: lwStoreNorm, Branch: "main", Folder: folder, ProjectID: lwProject, Remote: lwRemote},
		Signature: gitref.Signature{Name: "Mira", Email: "mira@agency.example", When: time.Unix(1_800_000_000, 0)},
		FolderProjectID: func(store, branch, f string) (string, error) {
			if store != lwStoreNorm || branch != "main" || f != folder {
				return "", errors.New("unexpected folder " + store + " " + branch + " " + f)
			}
			return lwFolderID, nil
		},
	})
}

// changed is the list of paths the last commit on the remote's main touched.
func (w *linkWorld) changed(t *testing.T) []string {
	t.Helper()
	return strings.Fields(testGit(t, w.origin, "show", "--name-only", "--format=", "main"))
}

func (w *linkWorld) metaOf(t *testing.T, folder string) map[string]json.RawMessage {
	t.Helper()
	doc := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(testGit(t, w.origin, "show", "main:"+metaPathOf(folder))), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// TestWriteLinkOnlyMetaJSON: a new folder gets exactly one file, its
// _meta.json (_v, project_id from FolderProjectID, links), never a
// projects row; a folder with rows and no _meta.json is adopted with the
// rows untouched; linking again changes nothing (no commit).
func TestWriteLinkOnlyMetaJSON(t *testing.T) {
	cases := []struct {
		name, folder string
	}{
		{"new folder", "clients/acme"},
		{"adopt a folder with rows and no _meta.json", "rows"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newLinkWorld(t)
			res, err := w.link(t, c.folder)
			if err != nil {
				t.Fatalf("link: %v", err)
			}
			if !res.CreatedFolder || !res.Changed || !res.Pushed || res.StoreURL != lwStoreNorm {
				t.Fatalf("result %+v", res)
			}
			if got := w.changed(t); len(got) != 1 || got[0] != metaPathOf(c.folder) {
				t.Fatalf("the link commit touched %v, want only %s", got, metaPathOf(c.folder))
			}
			if a := testGit(t, w.origin, "log", "-1", "--format=%ae %s", "main"); a != "mira@agency.example lore: link "+lwProject {
				t.Fatalf("link commit %q", a)
			}
			doc := w.metaOf(t, c.folder)
			if string(doc[metaVersionKey]) != metaFormatVersion || string(doc[metaProjectKey]) != `"`+lwFolderID+`"` {
				t.Fatalf("_meta.json %v", doc)
			}
			var links []linkRecord
			if uerr := json.Unmarshal(doc[metaLinksKey], &links); uerr != nil || len(links) != 1 || links[0].ProjectID != lwProject || links[0].Remote != lwRemote {
				t.Fatalf("links %s %v", doc[metaLinksKey], uerr)
			}
			if len(doc) != 3 {
				t.Fatalf("_meta.json keys %v, want _v, project_id, links", doc)
			}
			files := testGit(t, w.origin, "ls-tree", "-r", "--name-only", "main")
			if strings.Contains(files, "/projects/") {
				t.Fatalf("a link wrote a projects row:\n%s", files)
			}
			if !strings.Contains(files, "rows/.lore/data/memories/mem_01a1a5aca7f57659be7320c5f4b70033.json") {
				t.Fatalf("the adopted folder's row is gone:\n%s", files)
			}
			head := testGit(t, w.origin, "rev-parse", "main")
			again, err := w.link(t, c.folder)
			if err != nil || again.Changed || again.CreatedFolder || testGit(t, w.origin, "rev-parse", "main") != head {
				t.Fatalf("second link: %+v %v (must be a no-op)", again, err)
			}
		})
	}
}

// TestWriteLinkKeepsAnExistingMeta: a folder whose _meta.json exists keeps
// its project_id and every other key; FolderProjectID is not asked.
func TestWriteLinkKeepsAnExistingMeta(t *testing.T) {
	w := newLinkWorld(t)
	seed := filepath.Join(t.TempDir(), "seed")
	testGit(t, filepath.Dir(seed), "clone", "-q", lwStoreURL, seed)
	writeFile(t, filepath.Join(seed, "kept", ".lore", "data", "_meta.json"), `{"_v":2,"project_id":"prj_01a1a5aca7f57659be7320c5f4b70044","extra":"x"}`+"\n")
	testGit(t, seed, "add", ".")
	testGit(t, seed, "commit", "-q", "-m", "kept")
	testGit(t, seed, "push", "-q", "origin", "main")
	res, err := w.links.WriteLink(context.Background(), LinkWrite{
		Plan:      orgresolve.LinkPlan{Entry: "agency", StoreURL: lwStoreNorm, Branch: "main", Folder: "kept", ProjectID: lwProject, Remote: lwRemote},
		Signature: gitref.Signature{Name: "Mira", Email: "mira@agency.example", When: time.Unix(1_800_000_000, 0)},
		FolderProjectID: func(string, string, string) (string, error) {
			return "", errors.New("FolderProjectID asked for a folder that has a _meta.json")
		},
	})
	if err != nil || res.CreatedFolder || !res.Changed {
		t.Fatalf("link: %+v %v", res, err)
	}
	doc := w.metaOf(t, "kept")
	if string(doc[metaProjectKey]) != `"prj_01a1a5aca7f57659be7320c5f4b70044"` || string(doc["extra"]) != `"x"` || string(doc[metaVersionKey]) != "2" {
		t.Fatalf("existing keys not kept: %v", doc)
	}
}

// TestWriteOrgFileSameNameKeptOtherRefused: the first write commits
// lore-org.yaml; the same org again is Existed with no commit; another
// org's name is ErrOrgFileExists with no commit.
func TestWriteOrgFileSameNameKeptOtherRefused(t *testing.T) {
	w := newLinkWorld(t)
	ctx := context.Background()
	sig := gitref.Signature{Name: "Mira", Email: "mira@agency.example", When: time.Unix(1_800_000_000, 0)}
	write := func(name string) (OrgFileResult, error) {
		doc := OrgFileDoc{Name: name, Stores: []OrgFileStore{{URL: lwStoreURL, OrgFolder: "org"}}}
		return w.links.WriteOrgFile(ctx, OrgFileWrite{URL: lwStoreURL, OrgFolder: "org", Doc: doc, Signature: sig})
	}
	res, err := write("agency")
	if err != nil || res.Existed || !res.Pushed {
		t.Fatalf("first write: %+v %v", res, err)
	}
	if got := w.changed(t); len(got) != 1 || got[0] != orgFilePath("org") {
		t.Fatalf("org file commit touched %v", got)
	}
	head := testGit(t, w.origin, "rev-parse", "main")
	res, err = write("agency")
	if err != nil || !res.Existed || testGit(t, w.origin, "rev-parse", "main") != head {
		t.Fatalf("same org again: %+v %v (must keep the file, no commit)", res, err)
	}
	_, err = write("globex")
	if !errors.Is(err, ErrOrgFileExists) || testGit(t, w.origin, "rev-parse", "main") != head {
		t.Fatalf("another org: %v (want ErrOrgFileExists, no commit)", err)
	}
	if body := testGit(t, w.origin, "show", "main:"+orgFilePath("org")); !strings.Contains(body, "name: agency") {
		t.Fatalf("lore-org.yaml:\n%s", body)
	}
}

// TestWriteLinkAfterAStaleEmptyFetch (later 134): a clone made and fetched
// while the store had no commit yet must not answer "no branch main" once
// the store's first commit is pushed: WriteLink fetches every store before
// it writes (the fetch refspecs force-update the tracking refs).
func TestWriteLinkAfterAStaleEmptyFetch(t *testing.T) {
	w := newLinkWorld(t)
	ctx := context.Background()
	empty := filepath.Join(filepath.Dir(w.origin), "fresh.git")
	testGit(t, filepath.Dir(empty), "init", "-q", "--bare", "-b", "main", empty)
	const freshURL = "https://github.com/agency/fresh"
	cfg, err := orgconf.Parse([]byte("orgs:\n  - name: agency\n    stores:\n      - url: " + freshURL + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	g := &GitLinks{Home: w.links.Home, Config: cfg}
	dir := g.cloneDirOf("github.com/agency/fresh")
	if err := ensureBare(ctx, dir, freshURL); err != nil {
		t.Fatal(err)
	}
	if err := fetch(ctx, dir); err != nil {
		t.Fatalf("fetch of the empty store: %v", err)
	}
	if _, ok := branchTip(ctx, dir, "main"); ok {
		t.Fatal("an empty store has a main tip")
	}
	seed := filepath.Join(t.TempDir(), "seed")
	testGit(t, filepath.Dir(seed), "clone", "-q", freshURL, seed)
	writeFile(t, filepath.Join(seed, storeMarkerFile), "version: 2\n")
	testGit(t, seed, "add", ".")
	testGit(t, seed, "commit", "-q", "-m", "first")
	testGit(t, seed, "push", "-q", "origin", "main")
	plan := orgresolve.LinkPlan{Entry: "agency", StoreURL: "github.com/agency/fresh", Branch: "main", Folder: "acme", ProjectID: lwProject, Remote: lwRemote}
	write := func() (LinkWriteResult, error) {
		return g.WriteLink(ctx, LinkWrite{Plan: plan, Signature: gitref.Signature{Name: "Mira", Email: "mira@agency.example", When: time.Unix(1_800_000_000, 0)},
			FolderProjectID: func(string, string, string) (string, error) { return lwFolderID, nil }})
	}
	res, err := write()
	if err != nil || !res.Pushed || !res.CreatedFolder {
		t.Fatalf("link after the store's first commit: %+v %v", res, err)
	}
}
