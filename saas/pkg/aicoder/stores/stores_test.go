package stores

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/orgresolve"
)

// F12 matrix: marker present/missing × allowed files / code file / workflow
// / README / nested project / root project.
func TestCheckTreeMatrix(t *testing.T) {
	cases := []struct {
		name   string
		paths  []string
		marker bool // requireMarker
		ok     bool
	}{
		{"marker only", []string{MarkerFile}, true, true},
		{"no marker", []string{"acme/.lore/data/_meta.json"}, true, false},
		{"no marker allowed at init", []string{"README.md"}, false, true},
		{"readme license attrs", []string{MarkerFile, "README.md", "README", "LICENSE", "LICENSE.txt", ".gitattributes"}, true, true},
		{"lore workflow", []string{MarkerFile, ".github/workflows/lore-merge.yml"}, true, true},
		{"other workflow", []string{MarkerFile, ".github/workflows/deploy.yml"}, true, false},
		{"workflow yaml ext", []string{MarkerFile, ".github/workflows/lore-merge.yaml"}, true, false},
		{"nested workflow", []string{MarkerFile, ".github/workflows/x/lore-a.yml"}, true, false},
		{"project rows", []string{MarkerFile, "acme/.lore/data/_meta.json", "acme/.lore/data/memories/m.json", "org/.lore/lore-org.yaml"}, true, true},
		{"root project", []string{MarkerFile, ".lore/data/_meta.json"}, true, true},
		{"code file", []string{MarkerFile, "src/app.ts"}, true, false},
		{"file in project folder outside .lore", []string{MarkerFile, "acme/notes.md"}, true, false},
		{"nested project folder", []string{MarkerFile, "a/b/.lore/data/_meta.json"}, true, false},
		{"package.json at root", []string{MarkerFile, "package.json"}, true, false},
		{"empty at init", nil, false, true},
		{"empty in use", nil, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckTree("example.test/o/s", "main", tc.paths, tc.marker)
			if (err == nil) != tc.ok {
				t.Fatalf("CheckTree(%v) = %v, want ok=%v", tc.paths, err, tc.ok)
			}
			if err != nil && !errors.Is(err, ErrNotAStore) {
				t.Fatalf("error must wrap ErrNotAStore: %v", err)
			}
		})
	}
}

// The refusal names at most three offenders, folders once.
func TestCheckTreeNamesFirstOffenders(t *testing.T) {
	err := CheckTree("h/o/r", "main", []string{"src/a.ts", "src/b.ts", "src/c.ts", "web/x.ts", "zz.txt", "zz2.txt"}, true)
	var ne *NotAStoreError
	if !errors.As(err, &ne) {
		t.Fatalf("want NotAStoreError, got %v", err)
	}
	if len(ne.Offenders) != 3 || ne.Total != 6 || !ne.NoMarker || ne.Offenders[0] != "src/" || ne.Offenders[1] != "web/" {
		t.Fatalf("offenders %v total %d nomarker %v", ne.Offenders, ne.Total, ne.NoMarker)
	}
	if !strings.Contains(err.Error(), "src/") || strings.Contains(err.Error(), "src/a.ts") {
		t.Fatalf("a folder is named once: %v", err)
	}
}

func TestMetaRoundTripKeepsUnknownKeys(t *testing.T) {
	in := []byte(`{"_v":1,"project_id":"prj_1","future":{"x":1},"links":[{"project_id":"prj_9","remote":"github.com/b/z"},{"project_id":"prj_8","remote":"github.com/a/y","default":true}]}`)
	m, err := ParseStoreMeta(in)
	mustOK(t, err)
	if m.ProjectID != "prj_1" || len(m.Links) != 2 || m.Extra["future"] == nil {
		t.Fatalf("parsed %+v", m)
	}
	out, err := m.Encode()
	mustOK(t, err)
	m2, err := ParseStoreMeta(out)
	mustOK(t, err)
	if m2.Links[0].Remote != "github.com/a/y" || !m2.Links[0].Default {
		t.Fatalf("links sorted by remote, flags kept: %+v", m2.Links)
	}
	if !strings.Contains(string(out), "future") {
		t.Fatalf("unknown keys survive: %s", out)
	}
	for _, bad := range []string{`{"links":"x"}`, `{"links":[1]}`, `{"links":[{"remote":"a"}]}`, `[]`} {
		if _, err := ParseStoreMeta([]byte(bad)); err == nil {
			t.Fatalf("%s must be refused", bad)
		}
	}
}

func TestAddLinkIsASet(t *testing.T) {
	var m StoreMeta
	l := LinkRecord{ProjectID: "prj_1", Remote: "github.com/acme/app"}
	if !m.AddLink(l) || m.AddLink(l) {
		t.Fatal("adding the same link twice changes once")
	}
	l.Default = true
	if !m.AddLink(l) || len(m.Links) != 1 || !m.Links[0].Default {
		t.Fatalf("re-link with --default updates in place: %+v", m.Links)
	}
	if !m.RemoveLink("prj_1", "github.com/acme/app") || m.RemoveLink("prj_1", "github.com/acme/app") || len(m.Links) != 0 {
		t.Fatal("remove once")
	}
}

// Three-way merge of _meta.json: links added on both sides both survive,
// a link removed on one side stays removed.
func TestMergeStoreFileUnionsLinks(t *testing.T) {
	enc := func(ls ...LinkRecord) []byte {
		b, err := (StoreMeta{ProjectID: "prj_s", Links: ls}).Encode()
		mustOK(t, err)
		return b
	}
	a := LinkRecord{ProjectID: "prj_a", Remote: "github.com/acme/a"}
	b := LinkRecord{ProjectID: "prj_b", Remote: "github.com/acme/b"}
	c := LinkRecord{ProjectID: "prj_c", Remote: "github.com/acme/c"}
	out, err := MergeStoreFile("acme/.lore/data/_meta.json", enc(a), enc(a, b), enc(a, c))
	mustOK(t, err)
	m, err := ParseStoreMeta(out)
	mustOK(t, err)
	if len(m.Links) != 3 {
		t.Fatalf("union expected: %s", out)
	}
	out, err = MergeStoreFile("acme/.lore/data/_meta.json", enc(a, b), enc(b), enc(a, b, c))
	mustOK(t, err)
	if strings.Contains(string(out), "prj_a") || !strings.Contains(string(out), "prj_c") {
		t.Fatalf("removal honoured, addition kept: %s", out)
	}
	if strings.Contains(string(out), "<<<<<<<") {
		t.Fatal("links never conflict")
	}
}

func TestLayoutOneClonePerURL(t *testing.T) {
	l := Layout{Home: "/h/.lore"}
	var dirs []string
	for _, u := range []string{"https://github.com/northwind/lore", "git@github.com:northwind/lore.git", "ssh://git@GitHub.com/northwind/lore/"} {
		d, err := l.CloneDir(u)
		mustOK(t, err)
		dirs = append(dirs, d)
	}
	// clones-v017: the v0.1.17 store code keeps its own clones beside the
	// core's <home>/clones (commit 9c85131), so the two never share a
	// worktree; one clone per URL still holds.
	if dirs[0] != "/h/.lore/clones-v017/github.com/northwind/lore.git" || dirs[1] != dirs[0] || dirs[2] != dirs[0] {
		t.Fatalf("one clone per URL: %v", dirs)
	}
	for _, bad := range []string{"", "/local/path", "file:///x/y"} {
		if _, err := l.CloneDir(bad); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
}

// F12 init: only an empty (or lore-only) repo becomes a store; the marker
// is one parentless commit; a second init is a no-op; --branch creates a
// separate parentless branch.
func TestInit(t *testing.T) {
	e := newEnv(t)
	m := e.manager(t.TempDir(), nil)
	code := e.remote("o/code")
	e.seed("o/code", "main", map[string]string{"main.go": "package main\n"})
	st, err := m.Open(ctx(), code)
	mustOK(t, err)
	if _, err := st.Init(ctx(), "main", NewMarker(sig.When, "org"), sig); !errors.Is(err, ErrNotAStore) {
		t.Fatalf("a code repo must be refused: %v", err)
	}
	// A new lore branch on a code repo is refused too (TEAM-014).
	if _, err := st.Init(ctx(), "lore", NewMarker(sig.When, "org"), sig); !errors.Is(err, ErrNotAStore) {
		t.Fatalf("a code repo's default branch blocks any store branch: %v", err)
	}
	if rev := e.originRev("o/code", "refs/heads/lore"); rev != "" {
		t.Fatal("nothing pushed to a code repo")
	}

	url := e.remote("o/store")
	st, err = m.Open(ctx(), url)
	mustOK(t, err)
	res, err := st.Init(ctx(), "main", NewMarker(sig.When, "org"), sig)
	mustOK(t, err)
	if !res.Created || !res.Write.Pushed {
		t.Fatalf("init result %+v", res)
	}
	body, ok := e.originFile("o/store", "refs/heads/main", MarkerFile)
	if !ok || !strings.Contains(body, "lore_store: 1") {
		t.Fatalf("marker: %q", body)
	}
	parents := gitOut(t, e.remoteDir("o/store"), "rev-list", "--parents", "-n1", "main")
	if len(strings.Fields(parents)) != 1 || gitOut(t, e.remoteDir("o/store"), "rev-list", "--count", "main") != "1" {
		t.Fatalf("one parentless commit expected: %s", parents)
	}
	res, err = st.Init(ctx(), "main", NewMarker(sig.When, "org"), sig)
	mustOK(t, err)
	if !res.Already || res.Write.Changed {
		t.Fatalf("second init is a no-op: %+v", res)
	}
	res, err = st.Init(ctx(), "staging", NewMarker(sig.When, "org"), sig)
	mustOK(t, err)
	if !res.Created || gitOut(t, e.remoteDir("o/store"), "rev-list", "--count", "staging") != "1" {
		t.Fatalf("staging branch is its own parentless lore: %+v", res)
	}
	mustOK(t, st.Check(ctx(), "main"))
	if st.DefaultBranch() != "main" {
		t.Fatalf("default branch %q", st.DefaultBranch())
	}
	// The clone is bare.
	if out := gitOut(t, st.Dir, "rev-parse", "--is-bare-repository"); out != "true" {
		t.Fatal("store clones are bare")
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	r, err := gitref.Open(ctx(), dir, nil)
	mustOK(t, err)
	out, err := r.Run(ctx(), gitref.Invocation{Args: args})
	mustOK(t, err)
	return strings.TrimSpace(string(out))
}

// Every use checks the marker: a file added outside the allowed list later
// makes Check refuse.
func TestCheckRefusesAfterCodeIsAdded(t *testing.T) {
	e := newEnv(t)
	m := e.manager(t.TempDir(), nil)
	url := e.remote("o/store")
	st, err := m.Open(ctx(), url)
	mustOK(t, err)
	_, err = st.Init(ctx(), "main", NewMarker(sig.When, "org"), sig)
	mustOK(t, err)
	e.seed("o/store", "main", map[string]string{"src/app.ts": "x"})
	_, err = st.Fetch(ctx(), true)
	mustOK(t, err)
	if err := st.Check(ctx(), "main"); !errors.Is(err, ErrNotAStore) {
		t.Fatalf("want E_NOT_A_STORE after code was added: %v", err)
	}
	_, err = st.WriteRows(ctx(), "main", "acme", RowChanges{Put: map[string][]byte{"memories/m.json": []byte(`{}`)}}, sig, false)
	if !errors.Is(err, ErrNotAStore) {
		t.Fatalf("a write into a non-store is refused: %v", err)
	}
}

func initStore(t *testing.T, e *env, m *Manager, slug string) *Store {
	t.Helper()
	url := e.remote(slug)
	st, err := m.Open(ctx(), url)
	mustOK(t, err)
	_, err = st.Init(ctx(), "main", NewMarker(sig.When, "org"), sig)
	mustOK(t, err)
	return st
}

// F11 steps 2–5: link creates the folder project once, is idempotent,
// refuses a folder that is not a project, unlink removes it; a missing
// branch is refused with the init hint (Q8).
func TestWriteLink(t *testing.T) {
	e := newEnv(t)
	m := e.manager(t.TempDir(), nil)
	st := initStore(t, e, m, "o/store")
	link := LinkRecord{ProjectID: "prj_app", Remote: "github.com/acme/app"}
	res, err := st.WriteLink(ctx(), LinkChange{Branch: "main", Folder: "acme", Link: link, NewProject: newProjectFiles("prj_acme"), Signature: sig})
	mustOK(t, err)
	if !res.CreatedFolder || !res.Changed || !res.Write.Pushed {
		t.Fatalf("first link: %+v", res)
	}
	meta, ok := e.originFile("o/store", "refs/heads/main", "acme/.lore/data/_meta.json")
	if !ok || !strings.Contains(meta, "prj_app") || !strings.Contains(meta, "prj_acme") {
		t.Fatalf("meta: %s", meta)
	}
	res, err = st.WriteLink(ctx(), LinkChange{Branch: "main", Folder: "acme", Link: link, NewProject: newProjectFiles("prj_other"), Signature: sig})
	mustOK(t, err)
	if res.CreatedFolder || res.Changed || res.Write.Changed {
		t.Fatalf("relink is a no-op: %+v", res)
	}
	e.seed("o/store", "main", map[string]string{"junk/.lore/notes.md": "x"})
	_, err = st.WriteLink(ctx(), LinkChange{Branch: "main", Folder: "junk", Link: link, NewProject: newProjectFiles("prj_j"), Signature: sig})
	if !errors.Is(err, ErrNotAProject) {
		t.Fatalf("a folder that is not a project is refused: %v", err)
	}
	_, err = st.WriteLink(ctx(), LinkChange{Branch: "nope", Folder: "acme", Link: link, NewProject: newProjectFiles("prj_x"), Signature: sig})
	if !errors.Is(err, ErrNoBranch) || !strings.Contains(err.Error(), "lore store init") {
		t.Fatalf("a missing store branch is refused with the init hint: %v", err)
	}
	res, err = st.WriteLink(ctx(), LinkChange{Branch: "main", Folder: "acme", Link: link, Remove: true, Signature: sig})
	mustOK(t, err)
	if !res.Changed {
		t.Fatal("unlink changes")
	}
	meta, _ = e.originFile("o/store", "refs/heads/main", "acme/.lore/data/_meta.json")
	if strings.Contains(meta, "prj_app") || !strings.Contains(meta, "prj_acme") {
		t.Fatalf("unlink keeps the project: %s", meta)
	}
}

// F11 race: two machines link two repos to one folder at the same time;
// both links survive (set union), no markers.
func TestWriteLinkRaceIsAUnion(t *testing.T) {
	e := newEnv(t)
	st0 := initStore(t, e, e.manager(t.TempDir(), nil), "o/store")
	_, err := st0.WriteLink(ctx(), LinkChange{Branch: "main", Folder: "acme", Link: LinkRecord{ProjectID: "prj_0", Remote: "github.com/acme/app"}, NewProject: newProjectFiles("prj_acme"), Signature: sig})
	mustOK(t, err)
	const n = 4
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m := e.manager(t.TempDir(), nil)
			st, err := m.Open(ctx(), st0.URL)
			if err != nil {
				errs[i] = err
				return
			}
			_, errs[i] = st.WriteLink(ctx(), LinkChange{Branch: "main", Folder: "acme",
				Link:       LinkRecord{ProjectID: "prj_" + string(rune('a'+i)), Remote: "github.com/acme/r" + string(rune('a'+i))},
				NewProject: newProjectFiles("prj_dup"), Signature: sig})
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		mustOK(t, err)
	}
	meta, _ := e.originFile("o/store", "refs/heads/main", "acme/.lore/data/_meta.json")
	m, err := ParseStoreMeta([]byte(meta))
	mustOK(t, err)
	if len(m.Links) != n+1 || m.ProjectID != "prj_acme" {
		t.Fatalf("every concurrent link survives: %s", meta)
	}
}

// Offline write: committed locally, not lost, reported Offline; Unpushed
// lists it; a teammate writes meanwhile; PushPending reconciles both rows.
func TestOfflineWriteIsKeptAndPushedLater(t *testing.T) {
	e := newEnv(t)
	home := t.TempDir()
	f := &faults{}
	m := e.manager(home, f)
	st := initStore(t, e, m, "o/store")
	f.offline = true
	st, err := m.Open(ctx(), st.URL) // re-open so the remote is offline
	mustOK(t, err)
	res, err := st.WriteRows(ctx(), "main", "acme", RowChanges{Put: map[string][]byte{"memories/mem_1.json": []byte(`{"id":"mem_1"}`)}}, sig, false)
	mustOK(t, err)
	if !res.Offline || res.Pushed || !res.Changed {
		t.Fatalf("offline result %+v", res)
	}
	un, err := st.Unpushed(ctx())
	mustOK(t, err)
	if len(un) != 1 || un[0] != "refs/heads/main" {
		t.Fatalf("unpushed %v", un)
	}
	// A teammate writes another row meanwhile.
	other := e.manager(t.TempDir(), nil)
	ost, err := other.Open(ctx(), st.URL)
	mustOK(t, err)
	_, err = ost.WriteRows(ctx(), "main", "acme", RowChanges{Put: map[string][]byte{"memories/mem_2.json": []byte(`{"id":"mem_2"}`)}}, sig, false)
	mustOK(t, err)

	f.offline = false
	st, err = m.Open(ctx(), st.URL)
	mustOK(t, err)
	pushed, off, err := st.PushPending(ctx(), sig)
	mustOK(t, err)
	if off || len(pushed) != 1 {
		t.Fatalf("pushed %v offline %v", pushed, off)
	}
	for _, id := range []string{"mem_1", "mem_2"} {
		if _, ok := e.originFile("o/store", "refs/heads/main", "acme/.lore/data/memories/"+id+".json"); !ok {
			t.Fatalf("%s lost", id)
		}
	}
	un, err = st.Unpushed(ctx())
	mustOK(t, err)
	if len(un) != 0 {
		t.Fatalf("nothing unpushed after the push: %v", un)
	}
}

// WriteRows never replaces the store's _meta.json (its links) with a
// mirror's link-less one.
func TestWriteRowsKeepsStoreMeta(t *testing.T) {
	e := newEnv(t)
	st := initStore(t, e, e.manager(t.TempDir(), nil), "o/store")
	_, err := st.WriteLink(ctx(), LinkChange{Branch: "main", Folder: "acme", Link: LinkRecord{ProjectID: "prj_app", Remote: "github.com/acme/app"}, NewProject: newProjectFiles("prj_acme"), Signature: sig})
	mustOK(t, err)
	mirror, err := (StoreMeta{ProjectID: "prj_acme"}).Encode()
	mustOK(t, err)
	_, err = st.WriteRows(ctx(), "main", "acme", RowChanges{Put: map[string][]byte{metaFileName: mirror, "memories/m.json": []byte(`{}`)}, Delete: []string{metaFileName}}, sig, false)
	mustOK(t, err)
	meta, _ := e.originFile("o/store", "refs/heads/main", "acme/.lore/data/_meta.json")
	if !strings.Contains(meta, "prj_app") {
		t.Fatalf("links must survive a row write: %s", meta)
	}
	files, commit, err := st.ReadProject(ctx(), "main", "acme")
	mustOK(t, err)
	if commit == "" || files["memories/m.json"] == nil || files[metaFileName] == nil {
		t.Fatalf("read project: %v", files)
	}
}

// LinkSource matrix: link found (id AND remote), fork with the same id,
// staging entry skipped, offline with no index → unknown (fail closed),
// offline with a fresh index → cached, not-a-store → error.
func TestLinkSource(t *testing.T) {
	e := newEnv(t)
	home := t.TempDir()
	f := &faults{}
	m := e.manager(home, f)
	st := initStore(t, e, m, "o/store")
	_, err := st.WriteLink(ctx(), LinkChange{Branch: "main", Folder: "acme", Link: LinkRecord{ProjectID: "prj_app", Remote: "github.com/acme/app", Default: true}, NewProject: newProjectFiles("prj_acme"), Signature: sig})
	mustOK(t, err)
	cfg := &lorecfg.Config{Orgs: []lorecfg.Org{
		{Name: "northwind", Stores: []lorecfg.Store{{URL: st.URL, OrgFolder: "org"}}, Repos: []string{"github.com/acme/*"}},
		{Name: "northwind-stag", Stores: []lorecfg.Store{{URL: st.URL, Branch: "staging"}}},
	}}
	ls := &LinkSource{Manager: m, Config: cfg}
	res, err := ls.Links(ctx(), "prj_app", []string{"github.com/acme/app"})
	mustOK(t, err)
	if res.Status != orgresolve.LinkStatusKnown || len(res.Links) != 1 || res.Links[0].Entry != "northwind" || res.Links[0].Folder != "acme" || !res.Links[0].Default {
		t.Fatalf("link: %+v", res)
	}
	res, err = ls.Links(ctx(), "prj_app", []string{"github.com/someone/app-fork"})
	mustOK(t, err)
	if len(res.Links) != 0 || !strings.Contains(res.Detail, "shares a project id") || !strings.Contains(res.Detail, "lore project reid") {
		t.Fatalf("fork: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(home, CacheDir, "links-northwind.json")); err != nil {
		t.Fatalf("index cached per entry: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, CacheDir, "links-northwind-stag.json")); !os.IsNotExist(err) {
		t.Fatal("staging entries are never scanned")
	}

	// Offline with a fresh cache: still known (stale but within max age).
	f.offline = true
	m.FetchTTL = 0
	res, err = ls.Links(ctx(), "prj_app", []string{"github.com/acme/app"})
	mustOK(t, err)
	if res.Status != orgresolve.LinkStatusKnown || len(res.Links) != 1 {
		t.Fatalf("offline within max age uses the clone/cache: %+v", res)
	}
	// Offline on a machine that never fetched: unknown.
	fresh := e.manager(t.TempDir(), &faults{offline: true})
	res, err = (&LinkSource{Manager: fresh, Config: cfg}).Links(ctx(), "prj_app", []string{"github.com/acme/app"})
	mustOK(t, err)
	if res.Status != orgresolve.LinkStatusUnknown {
		t.Fatalf("never fetched + offline must fail closed: %+v", res)
	}
	// An unreachable store of an entry whose patterns do not match this
	// repo cannot decide it: reported, not fail-closed.
	other := "https://" + fakeHost + "/o/missing"
	cfg2 := &lorecfg.Config{Orgs: append(append([]lorecfg.Org{}, cfg.Orgs...), lorecfg.Org{Name: "betalabs", Stores: []lorecfg.Store{{URL: other}}, Own: []string{"github.com/betalabs/*"}})}
	res, err = (&LinkSource{Manager: e.manager(t.TempDir(), nil), Config: cfg2}).Links(ctx(), "prj_app", []string{"github.com/acme/app"})
	mustOK(t, err)
	if res.Status != orgresolve.LinkStatusKnown || len(res.Links) != 1 || !strings.Contains(res.Detail, "betalabs") {
		t.Fatalf("an unrelated unreadable store is reported, not fatal: %+v", res)
	}
	res, err = (&LinkSource{Manager: e.manager(t.TempDir(), nil), Config: cfg2}).Links(ctx(), "prj_b", []string{"github.com/betalabs/api"})
	mustOK(t, err)
	if res.Status != orgresolve.LinkStatusUnknown {
		t.Fatalf("an unreadable store whose patterns match must fail closed: %+v", res)
	}
	// Offline long after the last fetch: unknown.
	m.Clock = advanced(sig.When, 2*cfg.LinkIndexMaxAge())
	res, err = ls.Links(ctx(), "prj_app", []string{"github.com/acme/app"})
	mustOK(t, err)
	if res.Status != orgresolve.LinkStatusUnknown {
		t.Fatalf("an index older than link_index.max_age must not be trusted offline: %+v", res)
	}
	f.offline = false

	// A store that holds code: every resolve fails with E_NOT_A_STORE.
	code := e.remote("o/docs")
	e.seed("o/docs", "main", map[string]string{"package.json": "{}"})
	bad := &LinkSource{Manager: e.manager(t.TempDir(), nil), Config: &lorecfg.Config{Orgs: []lorecfg.Org{{Name: "x", Stores: []lorecfg.Store{{URL: code}}}}}}
	if _, err := bad.Links(ctx(), "prj_app", nil); !errors.Is(err, ErrNotAStore) {
		t.Fatalf("a code repo under stores: must be refused: %v", err)
	}
}

// F18 roster check: an agent is registered only when an active actor with
// its stable key is in the org folder; unreadable → not checked.
func TestRosterCheck(t *testing.T) {
	e := newEnv(t)
	m := e.manager(t.TempDir(), nil)
	st := initStore(t, e, m, "o/store")
	active, gone := `{"id":"act_1","stable_key":"agent:frontend-2","active":true}`, `{"id":"act_2","stable_key":"agent:old","active":false}`
	_, err := st.WriteRows(ctx(), "main", "org", RowChanges{Put: map[string][]byte{"actors/act_1.json": []byte(active), "actors/act_2.json": []byte(gone)}}, sig, false)
	mustOK(t, err)
	org := &lorecfg.Org{Name: "northwind", Stores: []lorecfg.Store{{URL: st.URL, OrgFolder: "org"}}}
	rc := &RosterCheck{Manager: m, Org: org}
	if reg, chk := rc.AgentRegistered("agent:frontend-2"); !reg || !chk {
		t.Fatal("registered agent")
	}
	if reg, chk := rc.AgentRegistered("agent:old"); reg || !chk {
		t.Fatal("a deactivated agent is not registered")
	}
	if reg, chk := rc.AgentRegistered("agent:ghost"); reg || !chk {
		t.Fatal("unknown agent")
	}
	off := &RosterCheck{Manager: e.manager(t.TempDir(), &faults{offline: true}), Org: org}
	if _, chk := off.AgentRegistered("agent:frontend-2"); chk {
		t.Fatal("never fetched + offline is not checked")
	}
}
