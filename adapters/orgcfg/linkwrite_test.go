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
	"slices"
	"strings"
	"testing"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
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
	writeFile(t, filepath.Join(seed, storeMarkerFile), "lore_store: 1\nid: sto_01936f0000007a1b8c2d3e4f5a6b7c8d\n")
	writeFile(t, filepath.Join(seed, "rows", "memories", "mem_01a1a5aca7f57659be7320c5f4b70033.json"), `{"_v":2,"id":"mem_01a1a5aca7f57659be7320c5f4b70033"}`+"\n")
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
			if !strings.Contains(files, "rows/memories/mem_01a1a5aca7f57659be7320c5f4b70033.json") {
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
// its project_id and every other key; FolderProjectID is not asked. A
// folder still in the old layout (<folder>/.lore/data/_meta.json) gets the
// link in that file, never a second _meta.json beside it.
func TestWriteLinkKeepsAnExistingMeta(t *testing.T) {
	for _, old := range []bool{false, true} {
		t.Run(map[bool]string{false: "store layout", true: "old layout"}[old], func(t *testing.T) { testWriteLinkKeepsAnExistingMeta(t, old) })
	}
}

func testWriteLinkKeepsAnExistingMeta(t *testing.T, old bool) {
	w := newLinkWorld(t)
	seed := filepath.Join(t.TempDir(), "seed")
	testGit(t, filepath.Dir(seed), "clone", "-q", lwStoreURL, seed)
	metaRel, other := metaPathOf("kept"), oldMetaPathOf("kept")
	if old {
		metaRel, other = other, metaRel
	}
	writeFile(t, filepath.Join(seed, filepath.FromSlash(metaRel)), `{"_v":2,"project_id":"prj_01a1a5aca7f57659be7320c5f4b70044","extra":"x"}`+"\n")
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
	var doc map[string]json.RawMessage
	if jerr := json.Unmarshal([]byte(testGit(t, w.origin, "show", "main:"+metaRel)), &doc); jerr != nil {
		t.Fatal(jerr)
	}
	if string(doc[metaProjectKey]) != `"prj_01a1a5aca7f57659be7320c5f4b70044"` || string(doc["extra"]) != `"x"` || string(doc[metaVersionKey]) != "2" || len(doc[metaLinksKey]) == 0 {
		t.Fatalf("existing keys not kept or no link: %v", doc)
	}
	if files := testGit(t, w.origin, "ls-tree", "-r", "--name-only", "main"); strings.Contains(files, other) {
		t.Fatalf("a second _meta.json %s appeared:\n%s", other, files)
	}
}

// TestWriteOrgFileSameNameKeptOtherRefused: the first write commits
// lore-org.yaml in the org folder (org/lore-org.yaml, D-e) and, in the same
// commit, org_folder: org in the store's marker, which named no org folder
// before; the same org again is Existed with no commit; another org's name
// is ErrOrgFileExists with no commit.
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
	if before := testGit(t, w.origin, "show", "main~1:"+storeMarkerFile); strings.Contains(before, "org_folder") {
		t.Fatalf("the store named an org folder before it had one:\n%s", before)
	}
	if got := w.changed(t); len(got) != 2 || !slices.Contains(got, orgFilePath("org")) || !slices.Contains(got, storeMarkerFile) {
		t.Fatalf("org file commit touched %v, want org/lore-org.yaml and the marker", got)
	}
	if mk := testGit(t, w.origin, "show", "main:"+storeMarkerFile); !strings.Contains(mk, "org_folder: org") {
		t.Fatalf("marker after the org file:\n%s", mk)
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
	if err = ensureClone(ctx, dir, freshURL, false); err != nil {
		t.Fatal(err)
	}
	if err = fetch(ctx, nil, dir); err != nil {
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

// TestWriteLinkInTheStoreClone (1.4): the link is written in the store's
// one clone (~/.lore/stores/<owner>/<repo>, no clones-v017), which ends
// up checked out on the store branch with the link's _meta.json among its
// files and nothing left uncommitted; lore's state stays in .git/lore.
func TestWriteLinkInTheStoreClone(t *testing.T) {
	w := newLinkWorld(t)
	if _, err := w.link(t, "acme"); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(w.links.Home, gitbackend.StoresDir, "agency", "notes")
	if got := testGit(t, clone, "symbolic-ref", "--short", "HEAD"); got != "main" {
		t.Fatalf("the clone has %q checked out, want main", got)
	}
	if _, err := os.Stat(filepath.Join(clone, metaPathOf("acme"))); err != nil {
		t.Fatalf("the link's _meta.json is not among the clone's files: %v", err)
	}
	if st := testGit(t, clone, "status", "--porcelain"); st != "" {
		t.Fatalf("the clone is not clean after a link: %q", st)
	}
	if testGit(t, clone, "rev-parse", "HEAD") != testGit(t, w.origin, "rev-parse", "main") {
		t.Fatal("the clone's HEAD is not the pushed link commit")
	}
	for _, gone := range []string{filepath.Join(w.links.Home, "clones-v017"), filepath.Join(w.links.Home, "clones")} {
		if _, err := os.Stat(gone); err == nil {
			t.Fatalf("%s exists: a second copy of the store", gone)
		}
	}
	if _, err := os.Stat(filepath.Join(gitbackend.StateDirOf(clone), fetchStampFile)); err != nil {
		t.Fatalf("no fetch stamp in .git/lore: %v", err)
	}
	// a second link on the checked-out branch moves the files with it
	if _, err := w.link(t, "globex"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(clone, metaPathOf("globex"))); err != nil {
		t.Fatalf("the second link did not reach the checkout: %v", err)
	}
}
