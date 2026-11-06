package wire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// TestStoreRemoveLooksUpBeforeAsking (G-CORE-4): `lore store remove`
// answers E_NOT_FOUND for a name this repo does not attach, with no
// question and nothing written; an attached name asks first, naming the
// store and its url; --yes detaches it.
func TestStoreRemoveLooksUpBeforeAsking(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	kit := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	writeStoresYAML(t, repo, "stores:\n  - name: kit\n    url: "+kit+"\n")
	yamlPath := filepath.Join(repo, filepath.FromSlash(attach.FileName))
	before := string(mustRead(t, yamlPath))
	a := openWith(t, repo, Options{})

	for _, yes := range []bool{false, true} {
		_, err := run(t, a, opsstores.OpRemove, `{"name":"nope"}`, yes)
		var e *errs.Error
		if !errs.Has(err, errs.NotFound) || !errors.As(err, &e) || e.Question != "" {
			t.Fatalf("unknown name (yes=%v): %v, want E_NOT_FOUND with no question", yes, err)
		}
	}
	_, err := run(t, a, opsstores.OpRemove, `{"name":"kit"}`, false)
	var e *errs.Error
	if !errs.Has(err, errs.NeedsConfirm) || !errors.As(err, &e) || !strings.Contains(e.Question, "kit") || !strings.Contains(e.Question, kit) {
		t.Fatalf("attached name without --yes: %v, want a question naming kit and its url", err)
	}
	if string(mustRead(t, yamlPath)) != before {
		t.Fatal("a refused or unconfirmed remove changed stores.yaml")
	}
	if _, err = run(t, a, opsstores.OpRemove, `{"name":"kit"}`, true); err != nil {
		t.Fatalf("remove --yes: %v", err)
	}
	if strings.Contains(string(mustRead(t, yamlPath)), kit) {
		t.Fatal("remove --yes left the entry")
	}
}

// TestCleanFileURLCredentials (G-CORE-6, SECURITY): the url a store
// command writes into the committed stores.yaml never holds an https/http
// user part; a password or a token-shaped user is refused without echoing
// it; an ssh login user and the scp form are kept; file:// is untouched.
func TestCleanFileURLCredentials(t *testing.T) {
	token := "ghp_" + strings.Repeat("A", 36) // built at run time (CLAUDE.md conv. 8)
	keep := []struct{ in, want string }{
		{"https://tok@example.test/acme/kb.git", "https://example.test/acme/kb.git"},
		{"http://someone@example.test/acme/kb", "http://example.test/acme/kb"},
		{"https://example.test/acme/kb.git", "https://example.test/acme/kb.git"},
		{"ssh://git@example.test/acme/kb.git", "ssh://git@example.test/acme/kb.git"},
		{"git@example.test:acme/kb.git", "git@example.test:acme/kb.git"},
	}
	for _, c := range keep {
		got, err := cleanFileURL(c.in)
		if err != nil || got != c.want {
			t.Errorf("%s: %q %v, want %q", c.in, got, err, c.want)
		}
	}
	for _, in := range []string{
		"https://user:pw@example.test/acme/kb.git",
		"https://" + token + "@example.test/acme/kb.git",
		"ssh://" + token + "@example.test/acme/kb.git",
		"http://u:p@example.test/x",
	} {
		_, err := cleanFileURL(in)
		if !errs.Has(err, errs.URLUserinfo) {
			t.Errorf("%s: %v, want E_URL_CREDENTIALS", strings.Replace(in, token, "<token>", 1), err)
			continue
		}
		if strings.Contains(err.Error(), "pw") || strings.Contains(err.Error(), token) {
			t.Errorf("the refusal echoes the credential: %v", err)
		}
	}
	dir := t.TempDir()
	if got, err := cleanFileURL("file://" + filepath.ToSlash(dir)); err != nil || got != "file://"+filepath.ToSlash(dir) {
		t.Errorf("file url: %q %v", got, err)
	}

	// end to end: the bytes of stores.yaml after `store add` hold no user part
	hosts := histGitWorld(t)
	kbURL, _ := bareStore(t, hosts, "acme/kb")
	seed := openWith(t, gitRepo(t), Options{})
	if _, err := run(t, seed, opsstores.OpInit, `{"name":"kb","url":"`+kbURL+`"}`, true); err != nil {
		t.Fatalf("init: %v", err)
	}
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withUser := strings.Replace(kbURL, "https://", "https://tok@", 1)
	if _, err := run(t, a, opsstores.OpAdd, `{"name":"kb","url":"`+withUser+`"}`, true); err != nil {
		t.Fatalf("add: %v", err)
	}
	body := string(mustRead(t, filepath.Join(repo, filepath.FromSlash(attach.FileName))))
	if strings.Contains(body, "tok@") || !strings.Contains(body, kbURL) {
		t.Fatalf("stores.yaml holds the https user:\n%s", body)
	}
}

// TestStoreInitMeOutsideRepo (G-CORE-7): `lore store init me` runs
// outside any git work tree (a fresh folder store, then "already"; a git
// url; a bad scheme refused), writing personal.store in config.yaml; a
// named store outside a repo still needs one (E_NOT_A_STORE).
func TestStoreInitMeOutsideRepo(t *testing.T) {
	plain := t.TempDir()
	home := t.TempDir()
	a := openWith(t, plain, Options{Home: home})
	out, err := run(t, a, opsstores.OpInit, `{"name":"me"}`, false)
	if err != nil || !strings.Contains(out, `"personal":true`) || strings.Contains(out, `"already"`) {
		t.Fatalf("init me outside a repo: %s %v", out, err)
	}
	mc, err := readMachineConfig(home)
	if err != nil || !strings.HasSuffix(mc.personalURL(), "/stores/me") {
		t.Fatalf("personal.store: %q %v", mc.personalURL(), err)
	}
	if out, err = run(t, a, opsstores.OpInit, `{"name":"me"}`, false); err != nil || !strings.Contains(out, `"already":true`) {
		t.Fatalf("init me again: %s %v", out, err)
	}
	if _, err = run(t, a, opsstores.OpInit, `{"name":"me","url":"s3://b/me"}`, false); !errs.Has(err, errs.StoreScheme) {
		t.Fatalf("init me with s3: %v, want E_STORE_SCHEME", err)
	}
	other := filepath.Join(t.TempDir(), "kit")
	if _, err = run(t, a, opsstores.OpInit, `{"name":"kit","url":"file://`+filepath.ToSlash(other)+`"}`, true); !errs.Has(err, errs.NotAStore) {
		t.Fatalf("init kit outside a repo: %v, want E_NOT_A_STORE", err)
	}
	if _, serr := os.Stat(other); !os.IsNotExist(serr) {
		t.Fatal("a refused named init made the store folder")
	}
	if _, err = os.Stat(filepath.Join(plain, ".lore")); !os.IsNotExist(err) {
		t.Fatal("init me outside a repo wrote into the folder it ran in")
	}

	hosts := histGitWorld(t)
	meURL, meDir := bareStore(t, hosts, "ana/lore-me")
	b := openWith(t, t.TempDir(), Options{Home: t.TempDir()})
	if _, err = run(t, b, opsstores.OpInit, `{"name":"me","url":"`+meURL+`"}`, false); err != nil {
		t.Fatalf("init me with a git url outside a repo: %v", err)
	}
	if tree := gitIn(t, meDir, nil, "ls-tree", "-r", "--name-only", "main"); !strings.Contains(tree, rowfs.MarkerFile) {
		t.Fatalf("the git personal store got no marker: %q", tree)
	}
}

// TestOrgStoresStayOutOfListStores (G-CORE-8 regression guard): the org's
// stores reach store.list only through OrgStoreEntries; ListStores (read
// by migrate's marker step and the private render) holds stores.yaml and
// me only, never an org row or a redacted url. store init|add refuse a
// name an org store answers at another place; the org's own store at the
// same place is not a clash (B-FLOW4-1).
func TestOrgStoresStayOutOfListStores(t *testing.T) {
	t.Parallel()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: "agency", URL: orgURL}, {Name: rosterStore, URL: orgURL, Folder: "org"}}, nil
	}
	repo := gitRepo(t)
	kit := folderStoreAt(t, folderStore, rowfs.VisibilityInternal)
	writeStoresYAML(t, repo, "stores:\n  - name: kit\n    url: "+kit+"\n")
	a := openWith(t, repo, Options{OrgStores: hook})

	list, err := a.ListStores(background())
	if err != nil || len(list) != 1 || list[0].Name != "kit" || list[0].Source != opsstores.SourceStoresFile {
		t.Fatalf("ListStores: %+v %v, want only kit from stores.yaml", list, err)
	}
	org, err := a.OrgStoreEntries(background())
	if err != nil || len(org) != 2 {
		t.Fatalf("OrgStoreEntries: %+v %v", org, err)
	}
	for _, e := range org {
		if e.Source != opsstores.SourceOrg || e.URL != orgURL {
			t.Fatalf("org entry %+v: want source org and the real url", e)
		}
	}
	other := folderStoreAt(t, "sto_01a1a5aca7f57659be7320c5f4b70077", rowfs.VisibilityInternal)
	if _, err = run(t, a, opsstores.OpAdd, `{"name":"agency","url":"`+other+`"}`, true); !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "org") {
		t.Fatalf("add a name the org uses elsewhere: %v, want E_INVALID_INPUT", err)
	}
	if _, err = run(t, a, opsstores.OpAdd, `{"name":"agency","url":"`+orgURL+`"}`, true); err != nil {
		t.Fatalf("add the org's own store under its org name: %v", err)
	}
}

// TestStoreAddMissingBranch (G-CORE-5): attaching a pinned branch the
// store lacks is E_NOT_FOUND naming the branch (not "not a store"), and
// once the branch is pushed the same add works: the clone the first
// attempt left (no commit) moves onto the remote branch.
func TestStoreAddMissingBranch(t *testing.T) {
	hosts := histGitWorld(t)
	kbURL, kbDir := bareStore(t, hosts, "acme/kb")
	seed := openWith(t, gitRepo(t), Options{})
	if _, err := run(t, seed, opsstores.OpInit, `{"name":"kb","url":"`+kbURL+`"}`, true); err != nil {
		t.Fatalf("init: %v", err)
	}
	repo := gitRepo(t)
	home := t.TempDir()
	a := openWith(t, repo, Options{Home: home})
	_, err := run(t, a, opsstores.OpAdd, `{"name":"stage","url":"`+kbURL+`","branch":"staging"}`, true)
	if !errs.Has(err, errs.NotFound) || !strings.Contains(err.Error(), "no branch staging") {
		t.Fatalf("missing branch: %v, want E_NOT_FOUND naming it", err)
	}
	gitIn(t, kbDir, nil, "branch", "staging", "main")
	b := openWith(t, repo, Options{Home: home}) // same machine: the clone of the first try is reused
	if _, err = run(t, b, opsstores.OpAdd, `{"name":"stage","url":"`+kbURL+`","branch":"staging"}`, true); err != nil {
		t.Fatalf("add after the branch was pushed: %v", err)
	}
}

// TestOrgResolutionClockReachesLinks: NewOrgResolution hands its GitLinks
// the resolution's clock, read at each call, so cmd/lore's Now (set after
// construction, LORE_NOW in a test build) reaches the fetch stamps.
func TestOrgResolutionClockReachesLinks(t *testing.T) {
	t.Parallel()
	g := &orgcfg.GitLinks{Home: t.TempDir()}
	r := NewOrgResolution(t.TempDir(), t.TempDir(), g)
	fixed := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	r.Now = func() time.Time { return fixed }
	if g.Now == nil || !g.Now().Equal(fixed) {
		t.Fatalf("GitLinks clock does not follow the resolution's Now")
	}
	own := func() time.Time { return fixed.Add(time.Hour) }
	g2 := &orgcfg.GitLinks{Home: t.TempDir(), Now: own}
	NewOrgResolution(t.TempDir(), t.TempDir(), g2)
	if !g2.Now().Equal(fixed.Add(time.Hour)) {
		t.Fatal("a GitLinks clock set by the caller was replaced")
	}
}
