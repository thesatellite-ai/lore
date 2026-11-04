//go:build teame2e

package main

// One project row per store folder (real-repo trial, step 02): two people
// who create the same store folder from stale store clones must end with ONE
// project row for it, and a folder that older versions already gave two
// rows must keep every @handle command working.

import (
	"encoding/json"
	"path"
	"strings"
	"testing"

	th "saas/cmd/cli/internal/teamharness"
)

// dev2Key is the second developer of the org-folder race.
const dev2Key = "human:dev2@northwind.example"

// orgFolderProjectRows lists the project row files of the store's org folder.
func orgFolderProjectRows(store *th.Remote) []string {
	return store.FilesMatching(refMain, folderOrg+"/"+dataDir+"/projects/")
}

// orgFolderMetaProject is the project_id the org folder's _meta.json names.
func orgFolderMetaProject(t *testing.T, store *th.Remote) string {
	t.Helper()
	body, ok := store.Read(refMain, folderOrg+"/"+dataDir+"/"+metaFile)
	if !ok {
		t.Fatal("the org folder has no _meta.json")
	}
	var m struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("org _meta.json: %v", err)
	}
	return m.ProjectID
}

// orgRace is a store with acme/ linked and two developers whose clones of
// acme/app are set up: dev2's store fetch counts for an hour.
type orgRace struct {
	w          *th.World
	store      *th.Remote
	dev1, dev2 *th.Clone
}

func newOrgRace(t *testing.T, caps ...th.Cap) *orgRace {
	t.Helper()
	w := newTeamWorld(t, append(append([]th.Cap{}, agencyCaps...), append([]th.Cap{th.Cmd("sync"), th.Cmd("team", "list")}, caps...)...)...)
	store := w.Remote(slugStore)
	app := w.Remote(slugApp)
	dev1, dev2, acmedev := w.Human("dev1", "dev1@northwind.example"), w.Human("dev2", "dev2@northwind.example"), w.Human("acmedev", "dev@acme.example")
	dev1.WriteConfig(th.Config{Personal: &th.Personal{Store: th.PersonalLocal}, Orgs: []th.Org{northwindOrg(dev1Key, "Dev One", "dev1")}, Live: th.FreshFetch()})
	// dev2 keeps the default-like fetch cache, so the org folder dev1 is
	// about to create is not in dev2's store clone (the trial's 2 s gap).
	dev2.WriteConfig(th.Config{Personal: &th.Personal{Store: th.PersonalLocal}, Orgs: []th.Org{northwindOrg(dev2Key, "Dev Two", "dev2")}, Live: &th.Live{FetchTTL: "1h"}})
	acmedev.WriteConfig(th.Config{Live: th.FreshFetch()})
	acmeApp := acmedev.Clone(app, "app")
	acmeApp.MustLore("init", "--non-interactive", "--name=app")
	acmeApp.CommitAll("lore")
	acmeApp.AdminPush("origin", "main")
	initStore(w, dev1, "northwind", "https://"+slugStore, "", "--yes").OK(t)
	r := &orgRace{w: w, store: store, dev1: dev1.Clone(app, "app"), dev2: dev2.Clone(app, "app")}
	r.dev1.MustLore("org", "link", orgNorthwind, "--project", folderAcme, "--yes")
	r.dev2.MustLore("sync") // dev2's store clone now: acme/ linked, no org/
	if rows := orgFolderProjectRows(store); len(rows) != 0 {
		t.Fatalf("test premise: no org folder yet: %v", rows)
	}
	return r
}

// Trial step 02: dev1 and dev2 run `lore actor register` seconds apart; dev2's
// store clone does not have the org folder dev1 just pushed. The store ends
// with one project row for org/, and @handle commands work from both clones.
func TestRosterFolderCreatedFromStaleClonesHasOneProjectRow(t *testing.T) {
	r := newOrgRace(t, th.Flag("to", "task", "add"), th.Flag("assign", "task", "add"))
	r.dev1.MustLore("actor", "register")
	r.dev2.MustLore("actor", "register")

	rows := orgFolderProjectRows(r.store)
	if len(rows) != 1 {
		t.Fatalf("the org folder must hold exactly one project row, got %v", rows)
	}
	if id := orgFolderMetaProject(t, r.store); strings.TrimSuffix(path.Base(rows[0]), ".json") != id {
		t.Fatalf("_meta.json names %s, the row is %s", id, rows[0])
	}
	r.dev1.MustLore("team", "list").Never(t, "E_AMBIGUOUS_PROJECT")
	r.dev2.MustLore("team", "list").Never(t, "E_AMBIGUOUS_PROJECT")
	r.dev1.MustLore("task", "add", "review the invoice", "--to", orgNorthwind, "--assign", "@dev2").Never(t, "E_AMBIGUOUS_PROJECT")
	r.dev2.MustLore("task", "add", "send the invoice", "--to", orgNorthwind, "--assign", "@dev1").Never(t, "E_AMBIGUOUS_PROJECT")
}

// A store an older lore left with two project rows in org/ (one per racing
// creator): every roster command still works, resolving to the project
// _meta.json names, and says once that the extra row is ignored.
func TestRosterFolderWithOldDuplicateProjectRowsStillResolves(t *testing.T) {
	r := newOrgRace(t, th.Flag("to", "task", "add"), th.Flag("assign", "task", "add"))
	r.dev1.MustLore("actor", "register")
	rows := orgFolderProjectRows(r.store)
	if len(rows) != 1 {
		t.Fatalf("test premise: one row after the first register: %v", rows)
	}
	// Write the duplicate exactly as the old random-id path did: the same
	// folder's project row under a second id.
	const strayID = "prj_0190aaaa0000700080000000000000ab"
	body, _ := r.store.Read(refMain, rows[0])
	keep := strings.TrimSuffix(path.Base(rows[0]), ".json")
	admin := r.w.Human("admin", "admin@northwind.example").Clone(r.store, "store")
	admin.WriteFile(folderOrg+"/"+dataDir+"/projects/"+strayID+".json", strings.ReplaceAll(body, keep, strayID))
	admin.CommitAll("older lore: second org project row")
	admin.AdminPush("origin", "HEAD:main")
	if got := len(orgFolderProjectRows(r.store)); got != 2 {
		t.Fatalf("test premise: two project rows, got %d", got)
	}

	r.dev2.MustLore("sync")
	r.dev2.MustLore("actor", "register").Never(t, "E_AMBIGUOUS_PROJECT")
	r.dev2.MustLore("team", "list").Says(t, strayID).Never(t, "E_AMBIGUOUS_PROJECT")
	r.dev1.MustLore("task", "add", "review the invoice", "--to", orgNorthwind, "--assign", "@dev2").Never(t, "E_AMBIGUOUS_PROJECT")
	r.dev2.MustLore("task", "add", "send the invoice", "--to", orgNorthwind, "--assign", "@dev1").Never(t, "E_AMBIGUOUS_PROJECT")
	if id := orgFolderMetaProject(t, r.store); id != keep {
		t.Fatalf("_meta.json must keep naming %s, now %s", keep, id)
	}
}

// A refused @handle writes nothing anywhere: every command that resolves a
// handle refuses before a row (or the roster folder's skeleton, on a store
// nobody registered in yet) can reach the store.
func TestUnknownHandleRefusalLeavesTheStoreUntouched(t *testing.T) {
	r := newOrgRace(t, th.Flag("to", "task", "add"), th.Flag("assign", "task", "add"), th.Cmd("team", "add"),
		th.Cmd("agent", "start"), th.Flag("for", "handoff", "add"), th.Flag("assign", "task", "reassign"))
	for _, c := range []struct {
		name string
		args []string
	}{
		// Before anyone registered: no org/ folder exists yet.
		{"task add, no roster yet", []string{"task", "add", "review the invoice", "--to", orgNorthwind, "--assign", "@nobody"}},
		{"team add, no roster yet", []string{"team", "add", "@backend", "--members", "@nobody"}},
		{"agent start, no roster yet", []string{"agent", "start", "--label", "agent-x", "--owner", "@nobody"}},
		{"handoff add, no roster yet", []string{"handoff", "add", "pick this up", "--for", "@nobody"}},
	} {
		before := r.store.Rev(refMain) + r.store.Rev(storeLive(storeBranch))
		if res := r.dev1.Lore(c.args...).Says(t, "@nobody"); res.Code == 0 {
			t.Errorf("%s: an unknown handle must be refused: %s", c.name, res)
		}
		r.dev1.MustLore("whoami") // pushes anything a refusal left committed
		if r.store.Rev(refMain)+r.store.Rev(storeLive(storeBranch)) != before {
			t.Errorf("%s: a refused handle changed the store: %v", c.name, r.store.Log(refMain)[:1])
		}
	}
	r.dev1.MustLore("actor", "register")
	id := mustID(t, r.dev1.MustLore("task", "add", "send the invoice", "--to", orgNorthwind), "tsk")
	for _, c := range []struct {
		name string
		args []string
	}{
		{"task add", []string{"task", "add", "review the invoice", "--to", orgNorthwind, "--assign", "@nobody"}},
		{"team add", []string{"team", "add", "@backend", "--members", "@dev1,@nobody"}},
		{"agent start", []string{"agent", "start", "--label", "agent-x", "--owner", "@nobody"}},
		{"task reassign", []string{"task", "reassign", id, "--assign", "@nobody"}},
	} {
		before := r.store.Rev(refMain) + r.store.Rev(storeLive(storeBranch))
		if res := r.dev1.Lore(c.args...).Says(t, "@nobody"); res.Code == 0 {
			t.Errorf("%s: an unknown handle must be refused: %s", c.name, res)
		}
		r.dev1.MustLore("whoami")
		if r.store.Rev(refMain)+r.store.Rev(storeLive(storeBranch)) != before {
			t.Errorf("%s: a refused handle changed the store: %v", c.name, r.store.Log(refMain)[:1])
		}
	}
}

// A teammate registered after this clone's last store fetch (inside
// fetch_ttl) is found without `lore sync`: a roster miss served from the
// fetch cache fetches once fresh and retries. A real miss says to sync or
// check the handle, never the live "landed on main" peek hint.
func TestRosterMissFetchesFreshBeforeRefusing(t *testing.T) {
	r := newOrgRace(t, th.Flag("to", "task", "add"), th.Flag("assign", "task", "add"), th.Cmd("team", "add"))
	r.dev1.MustLore("actor", "register") // after dev2's last fetch (1h cache)
	r.dev2.MustLore("task", "add", "review the invoice", "--to", orgNorthwind, "--assign", "@dev1").Never(t, "E_NOT_FOUND")
	r.dev2.MustLore("actor", "register")
	r.dev1.MustLore("team", "add", "@backend", "--members", "@dev1,@dev2")

	res := r.dev2.Lore("task", "add", "nobody's task", "--to", orgNorthwind, "--assign", "@nobody")
	if res.Code == 0 {
		t.Fatalf("an unknown handle must be refused: %s", res)
	}
	res.Says(t, "lore sync", "lore team list").Never(t, "landed on main", "sync peek")
}

// In a repo with no org (the roster is the repo's own actors table) there
// is no store copy to be stale: a missing handle says to register, never
// to sync a store.
func TestRepoRosterMissSaysRegisterNotSync(t *testing.T) {
	w := newTeamWorld(t, th.Cmd("actor", "register"), th.Flag("for", "handoff", "add"), th.Flag("assign", "task", "add"))
	app := w.Remote(slugApp)
	alice := w.Human("alice", "alice@acme.example")
	alice.WriteConfig(th.Config{Live: th.FreshFetch()})
	c := alice.Clone(app, "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	c.CommitAll("lore")
	c.AdminPush("origin", "main")
	c.MustLore("actor", "register")

	for _, args := range [][]string{
		{"handoff", "add", "pick this up", "--for", "@dave"},
		{"task", "add", "make the buttons blue", "--assign", "@dave"},
	} {
		res := c.Lore(args...)
		if res.Code == 0 {
			t.Fatalf("an unknown handle must be refused: %s", res)
		}
		res.Says(t, "lore actor register").Never(t, "store copy", "landed on main")
	}
}
