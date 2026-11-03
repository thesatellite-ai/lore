package stores

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/lorecfg"
)

// ValidateBranch is fixed rule 3: one path segment, never "/" (so
// refs/lore/live/<branch> and refs/lore/signals/<branch> of two branches
// never clash), plus git's own ref rules for a single segment.
func TestValidateBranchMatrix(t *testing.T) {
	ok := []string{"main", "staging", "release-2026", "a.b", "x.lock.y", "日本", "x@y", "a+b", "a=b", "v1_2", strings.Repeat("b", 200)}
	bad := []string{
		"", ".", "..", "-x", "--force", ".hidden", "x.lock", "a..b", "a@{b", "a/b", "/main", "main/",
		"a b", "a\tb", "a~b", "a^b", "a:b", "a?b", "a*b", "a[b", "a\\b",
	}
	for _, b := range ok {
		if err := ValidateBranch(b); err != nil {
			t.Errorf("ValidateBranch(%q) = %v, want ok", b, err)
		}
	}
	for _, b := range bad {
		err := ValidateBranch(b)
		if !errors.Is(err, ErrBadBranch) {
			t.Errorf("ValidateBranch(%q) = %v, want ErrBadBranch", b, err)
		}
	}
}

// Every name ValidateBranch accepts must also be a name git accepts under
// the refs lore builds from it; otherwise a store write fails late inside
// git instead of at the config check.
func TestValidateBranchAcceptsOnlyGitValidRefs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	cands := []string{"main", "staging", "a.b", "x.lock.y", "日本", "x@y", "a+b", "a#b", "a,b", "a%b", "v1_2"}
	for _, b := range cands {
		if ValidateBranch(b) != nil {
			continue
		}
		for _, ref := range []string{"refs/heads/" + b, "refs/lore/live/" + b, "refs/lore/signals/" + b} {
			if out, err := exec.Command("git", "check-ref-format", ref).CombinedOutput(); err != nil {
				t.Errorf("ValidateBranch accepts %q but git refuses %s: %s", b, ref, out)
			}
		}
	}
}

func TestLayoutWorktreeAndMirrorDirs(t *testing.T) {
	l := Layout{Home: "/h/.lore"}
	forms := []string{"https://github.com/northwind/lore", "git@github.com:northwind/lore.git", "ssh://git@GitHub.com/northwind/lore/"}
	for _, u := range forms {
		w, err := l.WorktreeDir(u, "main")
		mustOK(t, err)
		if w != "/h/.lore/worktrees/github.com/northwind/lore/main" {
			t.Errorf("WorktreeDir(%q) = %q", u, w)
		}
		m, err := l.MirrorDir(u, "staging", "acme")
		mustOK(t, err)
		if m != "/h/.lore/cache/stores/github.com/northwind/lore/staging/acme" {
			t.Errorf("MirrorDir(%q) = %q", u, m)
		}
		root, err := l.MirrorDir(u, "main", RootFolder)
		mustOK(t, err)
		if root != "/h/.lore/cache/stores/github.com/northwind/lore/main/_root" {
			t.Errorf("root project mirror must not collapse into the branch folder: %q", root)
		}
	}
	// A branch and a root folder mirror never share a folder.
	a, _ := l.MirrorDir(forms[0], "main", RootFolder)
	b, _ := l.MirrorDir(forms[0], "main", "acme")
	if a == b || filepath.Dir(a) != filepath.Dir(b) {
		t.Fatalf("root and named folder mirrors: %q %q", a, b)
	}
	for _, bad := range []string{"", "/local/path", "file:///x/y", "not a url"} {
		if _, err := l.WorktreeDir(bad, "main"); !errors.Is(err, ErrBadStoreURL) {
			t.Errorf("WorktreeDir(%q) must refuse: %v", bad, err)
		}
		if _, err := l.MirrorDir(bad, "main", "acme"); !errors.Is(err, ErrBadStoreURL) {
			t.Errorf("MirrorDir(%q) must refuse: %v", bad, err)
		}
	}
	// Every derived folder stays inside the lore home.
	for _, d := range []string{a, b} {
		if !strings.HasPrefix(d, l.Home+string(filepath.Separator)) {
			t.Fatalf("%q escapes the home", d)
		}
	}
}

func TestOrgFilePathAndParse(t *testing.T) {
	if p := OrgFilePath("org"); p != "org/.lore/"+OrgDefaultsFile {
		t.Errorf("OrgFilePath(org) = %q", p)
	}
	if p := OrgFilePath(RootFolder); p != ".lore/"+OrgDefaultsFile {
		t.Errorf("OrgFilePath(root) = %q", p)
	}
	cases := []struct {
		name    string
		in      string
		wantErr bool
		check   func(t *testing.T, f OrgFileDefaults)
	}{
		{"empty file is no defaults", "", false, func(t *testing.T, f OrgFileDefaults) {
			if f.Name != "" || len(f.Stores) != 0 || f.Defaults.ClientRepos != nil {
				t.Errorf("%+v", f)
			}
		}},
		{"full", "name: agency\nstores:\n  - url: https://github.com/agency/notes\n    branch: main\n    org_folder: org\nown: [github.com/agency/*]\nrepos: [github.com/client/*]\ndefaults:\n  client_repos:\n    memories: agency\n    tasks: repo\n", false, func(t *testing.T, f OrgFileDefaults) {
			if f.Name != "agency" || len(f.Stores) != 1 || f.Stores[0].OrgFolder != "org" || f.Stores[0].Branch != "main" ||
				len(f.Own) != 1 || len(f.Repos) != 1 || f.Defaults.ClientRepos["memories"] != "agency" || f.Defaults.ClientRepos["tasks"] != "repo" {
				t.Errorf("%+v", f)
			}
		}},
		{"unknown keys tolerated", "name: x\nfuture_key: 1\n", false, func(t *testing.T, f OrgFileDefaults) {
			if f.Name != "x" {
				t.Errorf("%+v", f)
			}
		}},
		{"unicode name", "name: \"日本チーム\"\n", false, func(t *testing.T, f OrgFileDefaults) {
			if f.Name != "日本チーム" {
				t.Errorf("%+v", f)
			}
		}},
		{"wrong type", "stores: hello\n", true, nil},
		{"not yaml", "name: [unclosed\n", true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := ParseOrgFile([]byte(tc.in))
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), OrgDefaultsFile) {
					t.Fatalf("want an error naming the file, got %v", err)
				}
				return
			}
			mustOK(t, err)
			tc.check(t, f)
		})
	}
}

// ReadOrgFile and FolderExists read the branch tip of a real store clone.
func TestReadOrgFileAndFolderExists(t *testing.T) {
	e := newEnv(t)
	m := e.manager(t.TempDir(), nil)
	st := initStore(t, e, m, "o/store")

	// Before anything is written: no defaults, no folder.
	if _, ok, err := st.ReadOrgFile(ctx(), "main", "org"); err != nil || ok {
		t.Fatalf("no lore-org.yaml yet: ok=%v err=%v", ok, err)
	}
	if ok, err := st.FolderExists(ctx(), "main", "acme"); err != nil || ok {
		t.Fatalf("no folder yet: %v %v", ok, err)
	}
	// A branch that does not exist reads as absent, not as an error.
	if _, ok, err := st.ReadOrgFile(ctx(), "nope", "org"); err != nil || ok {
		t.Fatalf("missing branch: ok=%v err=%v", ok, err)
	}
	if ok, err := st.FolderExists(ctx(), "nope", "acme"); err != nil || ok {
		t.Fatalf("missing branch folder: %v %v", ok, err)
	}

	_, err := st.WriteLink(ctx(), LinkChange{Branch: "main", Folder: "acme", Link: LinkRecord{ProjectID: "prj_app", Remote: "github.com/acme/app"}, NewProject: newProjectFiles("prj_acme"), Signature: sig})
	mustOK(t, err)
	e.seed("o/store", "main", map[string]string{"org/.lore/lore-org.yaml": "name: agency\ndefaults:\n  client_repos:\n    memories: agency\n"})
	_, err = st.Fetch(ctx(), true)
	mustOK(t, err)

	if ok, err := st.FolderExists(ctx(), "main", "acme"); err != nil || !ok {
		t.Fatalf("linked folder exists: %v %v", ok, err)
	}
	if ok, err := st.FolderExists(ctx(), "main", "other"); err != nil || ok {
		t.Fatalf("other folder: %v %v", ok, err)
	}
	f, ok, err := st.ReadOrgFile(ctx(), "main", "org")
	if err != nil || !ok || f.Name != "agency" || f.Defaults.ClientRepos["memories"] != "agency" {
		t.Fatalf("org file: %+v %v %v", f, ok, err)
	}
	// The file belongs to one org folder only.
	if _, ok, _ := st.ReadOrgFile(ctx(), "main", "acme"); ok {
		t.Fatal("another folder has no lore-org.yaml")
	}

	// A malformed file is an error, never silently "no defaults".
	e.seed("o/store", "main", map[string]string{"org/.lore/lore-org.yaml": "stores: [unclosed\n"})
	_, err = st.Fetch(ctx(), true)
	mustOK(t, err)
	if _, ok, err := st.ReadOrgFile(ctx(), "main", "org"); err == nil || ok {
		t.Fatalf("malformed org file must error: ok=%v err=%v", ok, err)
	}
}

// Lookup never creates a clone; Open does; LastFetch is zero until a fetch.
func TestLookupAndLastFetch(t *testing.T) {
	e := newEnv(t)
	m := e.manager(t.TempDir(), nil)
	url := e.remote("o/store")
	if _, err := m.Lookup(ctx(), url); !errors.Is(err, ErrNoClone) {
		t.Fatalf("never cloned: %v", err)
	}
	dir, err := m.Layout.CloneDir(url)
	mustOK(t, err)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("Lookup must not create the clone folder: %v", err)
	}
	if _, err := m.Lookup(ctx(), "/local/path"); !errors.Is(err, ErrBadStoreURL) {
		t.Fatalf("bad url: %v", err)
	}
	st, err := m.Open(ctx(), url)
	mustOK(t, err)
	if !st.LastFetch().IsZero() {
		t.Fatalf("a new clone was never fetched: %v", st.LastFetch())
	}
	_, err = st.Init(ctx(), "main", NewMarker(sig.When, "org"), sig)
	mustOK(t, err)
	_, err = st.Fetch(ctx(), true)
	mustOK(t, err)
	got := st.LastFetch()
	if !got.Equal(sig.When) {
		t.Fatalf("LastFetch = %v, want the manager clock %v", got, sig.When)
	}
	again, err := m.Lookup(ctx(), "git@"+fakeHost+":o/store.git")
	mustOK(t, err)
	if again.Dir != st.Dir || !again.LastFetch().Equal(got) {
		t.Fatalf("Lookup by another URL form opens the same clone: %q vs %q", again.Dir, st.Dir)
	}
	// A corrupt stamp reads as never fetched (zero), not a panic.
	mustOK(t, os.WriteFile(st.statePath(fetchStampFile), []byte("garbage"), 0o644))
	if !st.LastFetch().IsZero() {
		t.Fatal("a corrupt fetch stamp reads as never fetched")
	}
}

func TestIsConfigUnsetMiss(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain", errors.New("x"), false},
		{"exit 5", &gitref.Error{ExitCode: gitConfigUnsetMissing}, true},
		{"wrapped exit 5", errors.Join(errors.New("ctx"), &gitref.Error{ExitCode: gitConfigUnsetMissing}), true},
		{"exit 1", &gitref.Error{ExitCode: 1}, false},
		{"never ran", &gitref.Error{ExitCode: -1}, false},
	}
	for _, tc := range cases {
		if got := isConfigUnsetMiss(tc.err); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
}

func TestStoreFor(t *testing.T) {
	o := &lorecfg.Org{Stores: []lorecfg.Store{
		{URL: "git@github.com:agency/notes.git", Branch: "main"},
		{URL: "not a url"},
		{URL: "https://github.com/agency/other"},
	}}
	if s := StoreFor(o, "github.com/agency/notes"); s == nil || s.Branch != "main" {
		t.Fatalf("ssh config URL matches the normalised index URL: %+v", s)
	}
	if s := StoreFor(o, "github.com/agency/other"); s == nil || s != &o.Stores[2] {
		t.Fatal("returns a pointer into the entry, not a copy")
	}
	for _, norm := range []string{"", "github.com/agency", "github.com/agency/notes.git", "not a url"} {
		if s := StoreFor(o, norm); s != nil {
			t.Errorf("StoreFor(%q) = %+v, want nil", norm, s)
		}
	}
	if StoreFor(&lorecfg.Org{}, "github.com/agency/notes") != nil {
		t.Fatal("no stores, no match")
	}
}

// EntryLinks returns only the chosen entry's links for this project and
// these remotes; CachedLinks reads the same index offline.
func TestEntryLinksAndCachedLinks(t *testing.T) {
	e := newEnv(t)
	home := t.TempDir()
	m := e.manager(home, nil)
	st := initStore(t, e, m, "o/store")
	_, err := st.WriteLink(ctx(), LinkChange{Branch: "main", Folder: "acme", Link: LinkRecord{ProjectID: "prj_app", Remote: "github.com/acme/app", Default: true}, NewProject: newProjectFiles("prj_acme"), Signature: sig})
	mustOK(t, err)
	org := lorecfg.Org{Name: "agency", Stores: []lorecfg.Store{{URL: st.URL}}}
	ls := &LinkSource{Manager: m, Config: &lorecfg.Config{Orgs: []lorecfg.Org{org}}}

	if _, _, ok := CachedLinks(m.Layout, "agency"); ok {
		t.Fatal("no index before the first read")
	}
	links, ok, err := ls.EntryLinks(ctx(), &org, "prj_app", []string{"github.com/acme/app"})
	mustOK(t, err)
	if !ok || len(links) != 1 || links[0].Folder != "acme" || !links[0].Default || links[0].Entry != "agency" {
		t.Fatalf("entry links: %+v %v", links, ok)
	}
	for _, tc := range []struct {
		name    string
		project string
		remotes []string
	}{
		{"other project id", "prj_other", []string{"github.com/acme/app"}},
		{"empty project id", "", []string{"github.com/acme/app"}},
		{"fork remote", "prj_app", []string{"github.com/someone/app"}},
		{"no remotes", "prj_app", nil},
	} {
		got, ok, err := ls.EntryLinks(ctx(), &org, tc.project, tc.remotes)
		if err != nil || !ok || len(got) != 0 {
			t.Errorf("%s: %+v %v %v", tc.name, got, ok, err)
		}
	}
	cached, built, ok := CachedLinks(m.Layout, "agency")
	if !ok || len(cached) != 1 || cached[0].ProjectID != "prj_app" || built.IsZero() {
		t.Fatalf("cached index: %+v %v %v", cached, built, ok)
	}
	if _, _, ok := CachedLinks(m.Layout, "someone-else"); ok {
		t.Fatal("indexes are per entry")
	}

	// A store that cannot be read and was never cached: ok=false (fail
	// closed upstream), never an empty "known" answer.
	missing := lorecfg.Org{Name: "ghost", Stores: []lorecfg.Store{{URL: "https://" + fakeHost + "/o/missing"}}}
	ls2 := &LinkSource{Manager: e.manager(t.TempDir(), &faults{offline: true}), Config: &lorecfg.Config{Orgs: []lorecfg.Org{missing}}, MaxAge: time.Hour}
	got, ok, err := ls2.EntryLinks(ctx(), &missing, "prj_app", []string{"github.com/acme/app"})
	if err != nil || ok || got != nil {
		t.Fatalf("unreadable entry: %+v ok=%v err=%v", got, ok, err)
	}
}
