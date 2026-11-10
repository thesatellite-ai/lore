//go:build userscen

package userscen

// sync_store_test.go — attaching, using and detaching lore stores from a
// code repo, the way a person does it, through the real binary. Gaps left
// by tests/teame2e (which covers v0.1.17 org stores, links, leak guards and
// `store init` on non-empty repos); scenarios here:
//
//   - backend matrix (git remote vs file:// folder): store init → list →
//     write --to → the row lands in the store, never in the code repo's git
//     objects → a teammate's clone of the code repo reads it (labelled with
//     the store) → sync reports one entry per store, twice identically →
//     remove asks first, then detaches (rows stay in the store) → removing
//     again is E_NOT_FOUND → store add brings the same rows back
//   - store names: reserved words, non-snake_case, unicode, spaces, path
//     tricks, very long, empty, duplicate → E_INVALID_INPUT with the reason
//   - bad urls: no scheme, a code repo (E_NOT_A_STORE), a missing remote
//     (E_STORE_UNREACHABLE)
//   - --read-only refuses writes with E_STORE_READ_ONLY and its hint
//   - visibility: confirm needed for shareable, unchanged is a no-op,
//     a bad value and an unknown store are refused
//   - set-url: keeps the store id and its rows; same url is changed=false
//   - offline store write: committed in the local copy, sync reports the
//     store offline, then sends it once online; the teammate sees it

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// syncSlugStore is the team's store repo on the fake GitHub.
const syncSlugStore = "github.com/acme/notes"

// syncStoreRef is the store branch rows are written to.
const syncStoreRef = "refs/heads/main"

// syncStoreList is `lore store list --json`.
type syncStoreList struct {
	Stores []struct {
		Name       string `json:"name"`
		URL        string `json:"url"`
		Source     string `json:"source"`
		Visibility string `json:"visibility"`
		Reach      string `json:"reach"`
		ReadOnly   bool   `json:"read_only"`
	} `json:"stores"`
	Count int `json:"count"`
}

// syncStoreOut is `store init|add|remove|set-url|visibility --json`.
type syncStoreOut struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	ID         string `json:"id"`
	ReadOnly   bool   `json:"read_only"`
	Visibility string `json:"visibility"`
	Previous   string `json:"previous"`
	Changed    bool   `json:"changed"`
}

// syncMemorySources is `memory list --json` with each row's store label.
type syncMemorySources struct {
	Data []struct {
		ID     string `json:"id"`
		Body   string `json:"body"`
		Source *struct {
			Store   string `json:"store"`
			StoreID string `json:"store_id"`
		} `json:"_source"`
	} `json:"data"`
}

// syncStoreNames lists the names `lore store list --json` shows.
func syncStoreNames(t *testing.T, c *th.Clone) []string {
	t.Helper()
	l := th.DecodeJSON[syncStoreList](t, c.MustLore("store", "list", "--json"))
	if l.Count != len(l.Stores) {
		t.Fatalf("store list count %d != %d entries", l.Count, len(l.Stores))
	}
	var out []string
	for _, s := range l.Stores {
		out = append(out, s.Name)
	}
	return out
}

// syncMemoryStore returns the store label of memory id in c ("" absent).
func syncMemoryStore(t *testing.T, c *th.Clone, id string) (store string, found bool) {
	t.Helper()
	for _, d := range th.DecodeJSON[syncMemorySources](t, c.MustLore("memory", "list", "--json")).Data {
		if d.ID == id {
			if d.Source == nil {
				return "", true
			}
			return d.Source.Store, true
		}
	}
	return "", false
}

// syncFolderHolds reports whether any file under dir contains needle.
func syncFolderHolds(t *testing.T, dir, needle string) bool {
	t.Helper()
	hit := false
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || hit {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		hit = strings.Contains(string(b), needle)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return hit
}

// syncStoreBackend is one store backend of the matrix: its url and how to
// tell the row reached it.
type syncStoreBackend struct {
	name  string
	url   func(w *th.World) string
	holds func(t *testing.T, w *th.World, id string) bool
}

var syncStoreBackends = []syncStoreBackend{
	{
		name: "git",
		url:  func(w *th.World) string { return w.Remote(syncSlugStore).URL() },
		holds: func(t *testing.T, w *th.World, id string) bool {
			r := w.Remote(syncSlugStore)
			for _, f := range r.Files(syncStoreRef) {
				if strings.Contains(f, id) {
					return true
				}
			}
			return false
		},
	},
	{
		name: "folder",
		url:  func(w *th.World) string { return "file://" + filepath.Join(w.Root, "shared", "notes") },
		holds: func(t *testing.T, w *th.World, id string) bool {
			return syncFolderHolds(t, filepath.Join(w.Root, "shared", "notes"), id)
		},
	},
}

// Backend matrix: the whole life of a store attached to a code repo.
func TestSyncStore_LifecycleGitAndFolder(t *testing.T) {
	scenParallel(t) // its sub-tests each build their own World and run in parallel
	for _, be := range syncStoreBackends {
		t.Run(be.name, func(t *testing.T) {
			st := newSyncTeam(t, 2)
			a, b := st.clones[0], st.clones[1]
			url := be.url(st.w)

			ini := th.DecodeJSON[syncStoreOut](t, a.MustLore("store", "init", "notes", url, "--yes", "--json"))
			if ini.Name != "notes" || ini.URL != url || !strings.HasPrefix(ini.ID, "sto_") {
				t.Fatalf("store init --json: %+v", ini)
			}
			l := th.DecodeJSON[syncStoreList](t, a.MustLore("store", "list", "--json"))
			if l.Count != 1 || l.Stores[0].Name != "notes" || l.Stores[0].URL != url || l.Stores[0].Source != "stores.yaml" || l.Stores[0].Visibility != "internal" {
				t.Fatalf("store list after init: %+v", l)
			}

			const body = "the staging database resets every sunday night"
			mem := th.DecodeJSON[syncAddOut](t, a.MustLore("memory", "add", body, "--to", "notes", "--json"))
			if mem.Store != "notes" {
				t.Fatalf("memory add --to notes went to %q", mem.Store)
			}
			if !be.holds(t, st.w, mem.ID) {
				t.Fatalf("the row must be in the %s store", be.name)
			}
			if strings.Contains(a.Status(), "memories") {
				t.Fatalf("a store row is never a file of the code repo: %q", a.Status())
			}
			a.CommitAll("attach notes")
			a.AdminPush("origin", "main")
			if found := st.app.AllObjectsContain(body, mem.ID); len(found) > 0 {
				t.Fatalf("a store row must not reach the code repo's git objects: %v", found)
			}

			b.Git("pull", "-q", "--no-rebase", "origin", "main")
			if src, ok := syncMemoryStore(t, b, mem.ID); !ok || src != "notes" {
				t.Fatalf("the teammate reads the store row labelled notes: found=%v store=%q", ok, src)
			}

			for i := 0; i < 2; i++ {
				rep := th.DecodeJSON[syncReport](t, a.MustLore("sync", "--json"))
				if len(rep.Data.Stores) != 1 || rep.Data.Stores[0].Store != "notes" || rep.Data.Stores[0].Refused != 0 ||
					len(rep.Data.Stores[0].Problems) != 0 || rep.Data.Stores[0].Offline {
					t.Fatalf("sync run %d store report: %+v", i+1, rep.Data.Stores)
				}
				if s := rep.Data.Stores[0]; i == 1 && s.Sent+s.Received+s.Merged != 0 {
					t.Fatalf("the second sync must exchange nothing: %+v", s)
				}
			}

			a.Lore("store", "remove", "notes").Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", "its rows stay in the store", "--yes")
			if !syncHas(syncStoreNames(t, a), "notes") {
				t.Fatal("an unconfirmed remove must change nothing")
			}
			rm := th.DecodeJSON[syncStoreOut](t, a.MustLore("store", "remove", "notes", "--yes", "--json"))
			if rm.Name != "notes" || rm.URL != url {
				t.Fatalf("store remove --json: %+v", rm)
			}
			if names := syncStoreNames(t, a); len(names) != 0 {
				t.Fatalf("removed store still listed: %v", names)
			}
			if _, ok := syncMemoryStore(t, a, mem.ID); ok {
				t.Fatal("a detached store's rows are no longer read here")
			}
			if !be.holds(t, st.w, mem.ID) {
				t.Fatal("remove detaches; the rows stay in the store")
			}
			a.Lore("store", "remove", "notes", "--yes").Fails(t, th.ExitNotFound, "E_NOT_FOUND", "no store named notes", "lore store list")

			a.MustLore("store", "add", "notes", url, "--yes").Says(t, "attached store notes")
			if src, ok := syncMemoryStore(t, a, mem.ID); !ok || src != "notes" {
				t.Fatal("attaching the store again brings its rows back")
			}
		})
	}
}

// Store names are snake_case and not one of lore's own words; each refusal
// says why, and nothing is attached.
func TestSyncStore_NameMatrix(t *testing.T) {
	st := newSyncTeam(t, 1)
	a := st.clones[0]
	url := st.w.Remote(syncSlugStore).URL()
	a.MustLore("store", "init", "notes", url, "--yes")
	cases := []struct {
		name string
		say  []string
	}{
		{"repo", []string{"`repo` is reserved", "pick another name"}},
		{"me", []string{"`me` is reserved", "lore store init me"}},
		{"team", []string{"`team` is reserved"}},
		{"company", []string{"`company` is reserved"}},
		{"UPPER", []string{"`UPPER` must be snake_case"}},
		{"café", []string{"`café` must be snake_case"}},
		{"a-b", []string{"must be snake_case"}},
		{"with space", []string{"`with space` must be snake_case"}},
		{"a.b", []string{"must be snake_case"}},
		{"../escape", []string{"must be snake_case"}},
		{"1abc", []string{"must be snake_case"}},
		{"_lead", []string{"must be snake_case"}},
		{strings.Repeat("a", 200), []string{"must be snake_case"}},
		{"", []string{"missing required field name"}},
	}
	for _, tc := range cases {
		a.Lore("store", "add", tc.name, url, "--yes").Fails(t, 20, append([]string{"E_INVALID_INPUT"}, tc.say...)...)
	}
	// notes again: the very same entry is a no-op (round 4: attaching what
	// is attached already never fails); notes for another place is refused
	// with that entry's url and the command that drops it.
	a.MustLore("store", "add", "notes", url, "--yes")
	a.Lore("store", "add", "notes", st.w.Remote("github.com/acme/other-notes").URL(), "--yes").Fails(t, 20, "E_INVALID_INPUT", "already used in .lore/stores.yaml (for "+url+")", "lore store remove notes")
	if names := syncStoreNames(t, a); len(names) != 1 || names[0] != "notes" {
		t.Fatalf("refused names must attach nothing: %v", names)
	}
	// Each valid name attaches a store of its own: a second entry for the
	// place notes already attaches is refused (store add duplicate place).
	a.MustLore("store", "init", "golang_kit", st.w.Remote("github.com/acme/golang-kit").URL(), "--yes").Says(t, "golang_kit")
	a.MustLore("store", "init", "x", st.w.Remote("github.com/acme/x-store").URL(), "--yes")
	if names := syncStoreNames(t, a); len(names) != 3 {
		t.Fatalf("valid snake_case names attach: %v", names)
	}
}

// Urls that cannot be a store are refused with the code that says why.
func TestSyncStore_BadURLs(t *testing.T) {
	st := newSyncTeam(t, 1)
	a := st.clones[0]
	a.Lore("store", "add", "nourl", "not a url", "--yes").Fails(t, 20, "E_STORE_SCHEME", "no scheme")
	a.Lore("store", "add", "coderepo", st.app.URL(), "--yes").Fails(t, th.ExitGuard, "E_NOT_A_STORE", "a lore store needs a repository of its own", "new, empty repository")
	a.Lore("store", "add", "missing", "https://github.com/acme/missing", "--yes").Fails(t, th.ExitOffline, "E_STORE_UNREACHABLE", "go online once")
	if names := syncStoreNames(t, a); len(names) != 0 {
		t.Fatalf("no bad url may be attached: %v", names)
	}
}

// A store attached --read-only is read but never written.
func TestSyncStore_ReadOnlyRefusesWrites(t *testing.T) {
	st := newSyncTeam(t, 1)
	a := st.clones[0]
	url := st.w.Remote(syncSlugStore).URL()
	a.MustLore("store", "init", "notes", url, "--yes")
	mem := th.DecodeJSON[syncAddOut](t, a.MustLore("memory", "add", "written before", "--to", "notes", "--json")).ID
	a.MustLore("store", "remove", "notes", "--yes")
	ro := th.DecodeJSON[syncStoreOut](t, a.MustLore("store", "add", "kit", url, "--read-only", "--yes", "--json"))
	if !ro.ReadOnly {
		t.Fatalf("store add --read-only --json: %+v", ro)
	}
	if src, ok := syncMemoryStore(t, a, mem); !ok || src != "kit" {
		t.Fatal("a read-only store is still read")
	}
	before := st.w.Remote(syncSlugStore).Rev(syncStoreRef)
	a.Lore("memory", "add", "must not land", "--to", "kit").Fails(t, th.ExitRefused, "E_STORE_READ_ONLY", "attached read-only", "write to another store with --to")
	a.Lore("memory", "edit", mem, "--body", "changed").Refused(t, "E_STORE_READ_ONLY")
	if st.w.Remote(syncSlugStore).Rev(syncStoreRef) != before {
		t.Fatal("a refused write must not reach the store")
	}
}

// Visibility and address changes: confirm where a change widens who sees
// rows, unchanged values are no-ops, bad input names the problem.
func TestSyncStore_VisibilityAndSetURL(t *testing.T) {
	st := newSyncTeam(t, 1)
	a := st.clones[0]
	r := st.w.Remote(syncSlugStore)
	ini := th.DecodeJSON[syncStoreOut](t, a.MustLore("store", "init", "notes", r.URL(), "--yes", "--json"))
	mem := th.DecodeJSON[syncAddOut](t, a.MustLore("memory", "add", "kept across a rename", "--to", "notes", "--json")).ID

	a.Lore("store", "visibility", "notes", "shareable").Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", "make it shareable")
	vis := []struct {
		to, prev string
		changed  bool
	}{
		{"internal", "internal", false},
		{"shareable", "internal", true},
		{"shareable", "shareable", false},
		{"internal", "shareable", true},
	}
	for _, v := range vis {
		got := th.DecodeJSON[syncStoreOut](t, a.MustLore("store", "visibility", "notes", v.to, "--yes", "--json"))
		if got.Visibility != v.to || got.Previous != v.prev || got.Changed != v.changed {
			t.Fatalf("visibility → %s: %+v", v.to, got)
		}
	}
	a.Lore("store", "visibility", "notes", "public", "--yes").Fails(t, 20, "E_INVALID_INPUT", "internal or shareable, not `public`")
	a.Lore("store", "visibility", "nope", "internal", "--yes").Fails(t, th.ExitNotFound, "E_NOT_FOUND", "lore store list")

	same := th.DecodeJSON[syncStoreOut](t, a.MustLore("store", "set-url", "notes", r.URL(), "--yes", "--json"))
	if same.Changed || same.ID != ini.ID {
		t.Fatalf("set-url to the same url: %+v", same)
	}
	moved := th.DecodeJSON[syncStoreOut](t, a.MustLore("store", "set-url", "notes", r.SSHURL(), "--yes", "--json"))
	if !moved.Changed || moved.ID != ini.ID || moved.Previous != r.URL() || moved.URL != r.SSHURL() {
		t.Fatalf("set-url keeps the id and records the new url: %+v", moved)
	}
	if src, ok := syncMemoryStore(t, a, mem); !ok || src != "notes" {
		t.Fatal("rows stay readable after set-url")
	}
	a.MustLore("memory", "add", "written after the rename", "--to", "notes")
	a.Lore("store", "set-url", "nope", r.URL(), "--yes").Fails(t, th.ExitNotFound, "E_NOT_FOUND", "lore store list")
	a.Lore("store", "set-url", "notes", "::bad::", "--yes").Fails(t, 20, "E_STORE_SCHEME")
}

// A store write made offline is committed in the local copy; sync reports
// the store offline (exit 0), and once online sends it, and the teammate
// reads it.
func TestSyncStore_OfflineWriteIsSentByTheNextSync(t *testing.T) {
	st := newSyncTeam(t, 2)
	a, b := st.clones[0], st.clones[1]
	r := st.w.Remote(syncSlugStore)
	a.MustLore("store", "init", "notes", r.URL(), "--yes")
	a.CommitAll("attach notes")
	a.AdminPush("origin", "main")
	b.Git("pull", "-q", "--no-rebase", "origin", "main")
	b.MustLore("memory", "list")

	st.people[0].Offline()
	w := a.MustLore("memory", "add", "written on the train", "--to", "notes", "--json")
	w.Says(t, "committed in the local copy of store notes", "the next lore command that reaches the store pushes it, or run `lore sync`")
	mem := th.DecodeJSON[syncAddOut](t, w).ID
	for _, f := range r.Files(syncStoreRef) {
		if strings.Contains(f, mem) {
			t.Fatal("an offline write cannot be on the store remote yet")
		}
	}
	rep := th.DecodeJSON[syncReport](t, a.MustLore("sync", "--json"))
	if len(rep.Data.Stores) != 1 || !rep.Data.Stores[0].Offline || rep.Data.Stores[0].Sent != 0 {
		t.Fatalf("an offline sync reports the store offline: %+v", rep.Data.Stores)
	}
	a.MustLore("sync").Says(t, "offline: local changes kept, sent on the next sync")

	st.people[0].Online()
	rep = th.DecodeJSON[syncReport](t, a.MustLore("sync", "--json"))
	if rep.Data.Stores[0].Offline || rep.Data.Stores[0].Sent == 0 || rep.Data.Stores[0].Refused != 0 {
		t.Fatalf("online, sync sends the waiting store commit: %+v", rep.Data.Stores)
	}
	if src, ok := syncMemoryStore(t, b, mem); !ok || src != "notes" {
		t.Fatal("the teammate reads the store row after the author's sync")
	}
	again := th.DecodeJSON[syncReport](t, a.MustLore("sync", "--json"))
	if again.Data.Stores[0].Sent != 0 || again.Data.Stores[0].Received != 0 {
		t.Fatalf("the next sync exchanges nothing: %+v", again.Data.Stores)
	}
}

// A store write whose push failed offline is pushed by the next plain
// command that reads the store and reaches its remote, without `lore
// sync`, even when that store was fetched moments before (the failed push
// clears the fetch stamp); the offline note says so.
func TestSyncStore_OfflineWriteIsPushedByTheNextReadCommand(t *testing.T) {
	// fetch_ttl 1h: without the cleared stamp no read would sync again
	st := newSyncTeam(t, 1, th.Config{Live: &th.Live{FetchTTL: "1h"}})
	a := st.clones[0]
	r := st.w.Remote(syncSlugStore)
	a.MustLore("store", "init", "notes", r.URL(), "--yes")
	a.MustLore("memory", "list", "--store", "notes") // online: a fresh fetch stamp

	st.people[0].Offline()
	w := a.MustLore("memory", "add", "placeholder offline note", "--to", "notes", "--json")
	w.Says(t, "the next lore command that reaches the store pushes it, or run `lore sync`")
	mem := th.DecodeJSON[syncAddOut](t, w).ID
	onRemote := func() bool {
		for _, f := range r.Files(syncStoreRef) {
			if strings.Contains(f, mem) {
				return true
			}
		}
		return false
	}
	if onRemote() {
		t.Fatal("an offline write cannot be on the store remote yet")
	}
	a.MustLore("memory", "list", "--store", "notes") // still offline: nothing pushed, nothing fails
	if onRemote() {
		t.Fatal("an offline read cannot push")
	}

	st.people[0].Online()
	a.MustLore("memory", "list", "--store", "notes")
	if !onRemote() {
		t.Fatal("the first read command that reaches the store must push the waiting write")
	}
}
