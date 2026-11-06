package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	opstag "github.com/thesatellite-ai/lore/ops/tag"
	opswhoami "github.com/thesatellite-ai/lore/ops/whoami"
	"github.com/thesatellite-ai/lore/store"
)

// treeFiles maps every file under dir (outside .git) to its content.
func treeFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(filepath.Clean(p))
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(dir, p) // p is under dir
		files[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// ownRepo is a second checkout, github.com/agency/site, matching the
// org's own: pattern, with a project id.
func (w *orgWorld) ownRepo(t *testing.T, home string) string {
	t.Helper()
	bare := filepath.Join(w.hosts, "agency/site.git")
	if err := os.MkdirAll(bare, 0o700); err != nil {
		t.Fatal(err)
	}
	orgGit(t, bare, "init", "-q", "--bare", "-b", "main")
	dir := filepath.Join(w.root, "site")
	orgGit(t, w.root, "clone", "-q", "https://github.com/agency/site", dir)
	orgGit(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
	orgGit(t, dir, "push", "-q", "origin", "main")
	if err := os.MkdirAll(filepath.Join(dir, ".lore", "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	app, err := New(StandaloneOptions(dir, home, dir, "test"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()
	pid, err := ids.New[ids.Project]()
	if err != nil {
		t.Fatal(err)
	}
	if err := app.SeedRepoMeta(context.Background(), pid); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestOrgLinkClientLeakSmoke (later 130, e2e smoke (a) on cmd/lore):
// store init → org create → org link of a client repo. The link writes
// nothing into the client repo; the repo is client-class and whoami says
// so; a tag made in the org store (--to agency, the linked folder) on a
// repo row is E_LEAK_BLOCKED with nothing written, the same tag on a row
// --to agency is fine; no file the link or the writes made in the client
// repo names the store url (only stores.yaml, written by store init, does).
// In one of the org's OWN repos the same tag on a repo row is allowed.
func TestOrgLinkClientLeakSmoke(t *testing.T) {
	w := newOrgWorld(t)
	ctx := context.Background()
	mira := w.home(t, "mira")
	h := orgHostAt(t, w.repo, mira)
	if _, err := h.Create(ctx, opsorgcmd.CreateIn{Org: "agency", Store: "agency", OrgFolder: "org", Own: []string{"github.com/agency/*"},
		Key: "human:mira@agency.example", Name: "Mira", Handle: "mira"}, true); err != nil {
		t.Fatalf("create: %v", err)
	}
	before := treeFiles(t, w.repo)
	rep, err := h.Link(ctx, opsorgcmd.LinkIn{Org: "agency", Project: "acme"}, true)
	if err != nil || !strings.Contains(strings.Join(rep.Lines, "\n"), "nothing was written to this repo") {
		t.Fatalf("link: %v %v", rep.Lines, err)
	}
	if after := treeFiles(t, w.repo); len(after) != len(before) {
		t.Fatalf("org link wrote into the client repo: %d files before, %d after", len(before), len(after))
	}
	if !strings.Contains(w.storeFiles(t), "acme/.lore/data/_meta.json") {
		t.Fatalf("the link is not in the store:\n%s", w.storeFiles(t))
	}

	app, err := New(StandaloneOptions(w.repo, mira, w.repo, "test"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()
	withProject(t, app)
	if c, cerr := app.StoreClass(ctx, store.SourceRepo); cerr != nil || c != direction.Client {
		t.Fatalf("repo class = %v %v, want client", c, cerr)
	}
	who, err := run(t, app, opswhoami.OpShow, `{}`, false)
	if err != nil || !strings.Contains(who, `"repo_role":"client"`) {
		t.Fatalf("whoami in the client repo: %s %v", who, err)
	}
	if _, err = run(t, app, opstag.OpAdd, `{"name":"staging_creds","to":"agency"}`, true); err != nil {
		t.Fatalf("tag add --to agency: %v", err)
	}
	beforeWrite := treeFiles(t, w.repo)
	_, err = run(t, app, opsmemory.OpAdd, `{"body":"rotate the staging password","tags":["staging_creds"],"to":"repo"}`, true)
	if !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("client repo row tagged with an internal tag: %v, want E_LEAK_BLOCKED", err)
	}
	// The refused write adds no row; the only data file it may leave is
	// the caller's client projection in actors/ (display name and kind,
	// no handle, no stable key: what every client repo row's *_by needs).
	data := filepath.Join(".lore", "data") + string(filepath.Separator)
	for rel, body := range treeFiles(t, w.repo) {
		if _, ok := beforeWrite[rel]; ok || !strings.HasPrefix(rel, data) {
			continue
		}
		if !strings.HasPrefix(rel, data+"actors"+string(filepath.Separator)) || strings.Contains(body, "stable_key") || strings.Contains(body, `"handle"`) {
			t.Fatalf("a refused write left %s:\n%s", rel, body)
		}
	}
	out, err := run(t, app, opsmemory.OpAdd, `{"body":"rotate the staging password","tags":["staging_creds"],"to":"agency"}`, true)
	if err != nil || !strings.Contains(out, `"store":"agency/acme"`) {
		t.Fatalf("the same tag --to agency: %s %v", out, err)
	}
	for rel, body := range treeFiles(t, w.repo) {
		if rel != filepath.Join(".lore", "stores.yaml") && (strings.Contains(body, "agency/notes") || strings.Contains(body, "rotate the staging")) {
			t.Fatalf("client file %s names the store or the internal note:\n%s", rel, body)
		}
	}

	site := w.ownRepo(t, mira)
	own, err := New(StandaloneOptions(site, mira, site, "test"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = own.Close() }()
	withProject(t, own)
	if c, cerr := own.StoreClass(ctx, store.SourceRepo); cerr != nil || c != direction.Internal {
		t.Fatalf("own repo class = %v %v, want internal", c, cerr)
	}
	if _, err = run(t, own, opstag.OpAdd, `{"name":"staging_creds","to":"agency"}`, true); err != nil {
		t.Fatalf("tag add in the own repo: %v", err)
	}
	if _, err = run(t, own, opsmemory.OpAdd, `{"body":"rotate the staging password","tags":["staging_creds"],"to":"repo"}`, true); err != nil {
		t.Fatalf("own repo row with the org's tag: %v, want allowed", err)
	}
}
