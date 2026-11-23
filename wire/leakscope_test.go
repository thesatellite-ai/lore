package wire

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// Made-up stores of the leak scope tests: the org's main store (everyone)
// and a store shared with the client's team.
const (
	scopeInternalURL = "git@git.example.test:acme-co/internal.git"
	scopeSharedURL   = "git@git.example.test:acme-co/shared.git"
	scopeAdminsURL   = "git@git.example.test:acme-co/admins.git"
	scopeClientTeam  = orgconf.Audience("team:@client")
)

func scopeOrg() *orgconf.Org {
	return &orgconf.Org{Name: "acme", Stores: []orgconf.Store{
		{URL: scopeInternalURL, OrgFolder: "org"},
		{URL: scopeSharedURL, Audience: scopeClientTeam},
		{URL: scopeAdminsURL, Audience: orgconf.AudienceAdmins},
	}}
}

func scopeLink(t *testing.T, raw, folder string, def bool) orgresolve.Link {
	t.Helper()
	n, err := repourl.NormalizeStore(raw)
	if err != nil {
		t.Fatal(err)
	}
	return orgresolve.Link{Entry: "acme", StoreURL: n, Folder: folder, Default: def}
}

// TestLeakClientAudience (#133/#135): the client's team is the team
// audience of the store the repo's primary link (default, else first)
// points at; a link into a store with no team audience gives none.
func TestLeakClientAudience(t *testing.T) {
	org := scopeOrg()
	shared := scopeLink(t, scopeSharedURL, "client_app", false)
	internal := scopeLink(t, scopeInternalURL, "client_ops", false)
	cases := []struct {
		name  string
		links []orgresolve.Link
		want  orgconf.Audience
	}{
		{"linked to the shared store", []orgresolve.Link{shared}, scopeClientTeam},
		{"linked to the internal store", []orgresolve.Link{internal}, ""},
		{"shared first, internal second", []orgresolve.Link{shared, internal}, scopeClientTeam},
		{"internal first, shared default", []orgresolve.Link{internal, func() orgresolve.Link { l := shared; l.Default = true; return l }()}, scopeClientTeam},
		{"no link", nil, ""},
		{"another org's link", []orgresolve.Link{{Entry: "other", StoreURL: shared.StoreURL}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := leakClientAudience(org, tc.links); got != tc.want {
				t.Fatalf("client team %q, want %q", got, tc.want)
			}
		})
	}
	if leakSharedStore(&org.Stores[1], "") || leakSharedStore(&org.Stores[0], scopeClientTeam) || !leakSharedStore(&org.Stores[1], scopeClientTeam) || leakSharedStore(&org.Stores[2], scopeClientTeam) {
		t.Fatal("only the store whose audience is the client's team is shared with it")
	}
}

// fakeClone makes lore's clone folder of raw under home with the store
// folders given (each with its _meta.json), as `lore sync` leaves it.
func fakeClone(t *testing.T, home, raw string, folders ...string) {
	t.Helper()
	dir, _, err := gitbackend.CloneDirOf(home, raw, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range folders {
		p := filepath.Join(dir, filepath.FromSlash(rowfs.LayoutStore.StorePath(f, rowfs.MetaFileName)))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestLeakScopeReadsEveryInternalFolder (#135, #133): in a client repo
// linked to the shared store, the scope adds every folder of each
// internal org store cloned here that no attachment reads (client_ops
// was never attached, so its decision passed the guard), names the
// attached shared store as never indexed, and adds nothing of a store
// not cloned on this machine. An own repo gets no scope.
func TestLeakScopeReadsEveryInternalFolder(t *testing.T) {
	home := t.TempDir()
	fakeClone(t, home, scopeInternalURL, "org", "client_ops", "billing")
	fakeClone(t, home, scopeSharedURL, "client_app", "client_docs")
	// scopeAdminsURL is not cloned here (the person is outside its audience)
	a := &App{opts: Options{Home: home}}
	s := &orgcfg.Situation{Result: orgresolve.Result{Org: scopeOrg(), Links: []orgresolve.Link{
		scopeLink(t, scopeSharedURL, "client_app", false),
		scopeLink(t, scopeInternalURL, "client_ops", false),
	}}}
	attached := []attach.Store{
		{Name: "acme", URL: scopeSharedURL, Folder: "client_app"},
		{Name: "acme/org", URL: scopeInternalURL, Folder: "org"},
	}
	sc := a.leakScopeOf(s, true, attached)
	var got []string
	for _, x := range sc.Extra {
		if !x.ReadOnly {
			t.Fatalf("an extra folder is attached writable: %+v", x)
		}
		got = append(got, string(x.Name)+"="+x.URL+":"+x.Folder)
	}
	slices.Sort(got)
	want := []string{"acme/billing=" + scopeInternalURL + ":billing", "acme/client_ops=" + scopeInternalURL + ":client_ops"}
	if !slices.Equal(got, want) {
		t.Fatalf("extra folders %v, want %v", got, want)
	}
	if !sc.Shared["acme"] || sc.Shared["acme/org"] || len(sc.Shared) != 1 {
		t.Fatalf("shared %v, want only the attached shared store acme", sc.Shared)
	}
	if own := a.leakScopeOf(s, false, attached); len(own.Extra) != 0 || len(own.Shared) != 0 {
		t.Fatalf("an own repo got a leak scope: %+v", own)
	}
	// linked to the internal store only: no client team, nothing shared,
	// and every folder of every cloned org store is internal
	s.Result.Links = []orgresolve.Link{scopeLink(t, scopeInternalURL, "client_ops", true)}
	sc = a.leakScopeOf(s, true, []attach.Store{{Name: "acme", URL: scopeInternalURL, Folder: "client_ops"}, {Name: "acme/org", URL: scopeInternalURL, Folder: "org"}})
	got = got[:0]
	for _, x := range sc.Extra {
		got = append(got, string(x.Name))
	}
	slices.Sort(got)
	if want := []string{"acme/billing", "acme/client_app", "acme/client_docs"}; !slices.Equal(got, want) || len(sc.Shared) != 0 {
		t.Fatalf("with no client team: extra %v shared %v, want %v and none shared", got, sc.Shared, want)
	}
}

// TestLeakScopeNameTaken: a folder name already in use (the same folder
// name in a second store) gets the store's short name in its source name.
func TestLeakScopeNameTaken(t *testing.T) {
	st := &orgconf.Store{URL: scopeInternalURL}
	taken := map[store.Source]bool{"acme/client_app": true}
	if got := leakScopeName("acme", st, "client_app", taken); got != "acme/internal/client_app" {
		t.Fatalf("got %q", got)
	}
	taken["acme/internal/client_app"] = true
	if got := leakScopeName("acme", st, "client_app", taken); got != "acme/internal/client_app-1" {
		t.Fatalf("got %q", got)
	}
	if got := leakScopeName("acme", st, "billing", taken); got != "acme/billing" {
		t.Fatalf("got %q", got)
	}
}

// TestLeakRowsSkipsSharedStores (#133): an attached store the scope names
// shared with the client's team feeds no row to the index, whatever its
// class; the same store unnamed is indexed as before.
func TestLeakRowsSkipsSharedStores(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	a := leakWorld(t, true)
	sources := func(shared map[store.Source]bool) map[string]bool {
		rows, err := a.leakRows(background(), true, nil, shared, nil)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, r := range rows {
			out[r.Source] = true
		}
		return out
	}
	if got := sources(nil); !got["agency"] {
		t.Fatalf("the internal store is not indexed: %v", got)
	}
	if got := sources(map[store.Source]bool{"agency": true}); got["agency"] || !got["me"] {
		t.Fatalf("a store shared with the client's team was indexed (or the personal store dropped): %v", got)
	}
}
