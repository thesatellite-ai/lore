package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/ids"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestOrgStoreWriteCarriesOrgIdentity (plainfiles:4, C19): from a repo no
// org matches, the open's identity is the person's fallback (here
// --actor, standing in for the OS user the chain lands on when git's
// user.email is an org key). A write into a store the org lists is stamped
// with the person's identity in that org (created_by, updated_by, the _hlc
// actor) and that actor's row is written there with its key; a store no
// org lists keeps the open's identity.
func TestOrgStoreWriteCarriesOrgIdentity(t *testing.T) {
	t.Parallel()
	const orgKey = "human:mira@example.test"
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	otherURL := folderStoreAt(t, "sto_01a1a5aca7f57659be7320c5f4b70004", rowfs.VisibilityInternal)
	home := t.TempDir()
	// the org entry (its store urls come from the OrgStoreURLs hook: the
	// org config refuses a local url)
	config := "orgs:\n  - name: acme\n    identity: {key: " + orgKey + ", name: Mira, handle: mira}\n"
	if err := os.WriteFile(orgcfg.ConfigPath(home), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := gitRepo(t)
	// PersonOnly: no agent detection, so the run is the same under an
	// agent's shell (detected, the org store gets the org-owned agent)
	a, err := New(Options{RepoDir: repo, Home: home, FlagActor: "human:solo@example.test", PersonOnly: true,
		OrgStores: func(context.Context) ([]attach.Store, error) {
			return []attach.Store{{Name: "acme", URL: orgURL, Folder: "clients"}, {Name: "side", URL: otherURL, Folder: "notes"}}, nil
		},
		OrgStoreURLs: func(context.Context) ([]OrgStoreURL, error) { return []OrgStoreURL{{Org: "acme", URL: orgURL}}, nil },
		RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	withProject(t, a)
	mine, err := a.Actor(background())
	if err != nil {
		t.Fatal(err)
	}
	org, err := identity.ActorID(orgKey)
	if err != nil {
		t.Fatal(err)
	}
	if mine == org {
		t.Fatal("the test needs an open identity other than the org's")
	}

	out, err := run(t, a, opsmemory.OpAdd, `{"body":"invoices go out on the 1st","to":"acme"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(fileuri.LocalPath(orgURL), "clients")
	f := rowFile(t, filepath.Join(dir, schema.TableMemories, idOf(t, out)+".json"))
	for _, k := range []string{"created_by", "updated_by"} {
		if got := fieldString(t, f, k); got != org.String() {
			t.Errorf("%s in the org store = %s, want the org identity %s (open identity %s)", k, got, org, mine)
		}
	}
	if hlc := fieldString(t, f, "_hlc"); !strings.HasSuffix(hlc, org.String()) {
		t.Errorf("_hlc in the org store = %s, want the org identity's actor", hlc)
	}
	row := rowFile(t, filepath.Join(dir, schema.TableActors, org.String()+".json"))
	if fieldString(t, row, "stable_key") != orgKey || fieldString(t, row, "display_name") != "Mira" {
		t.Errorf("the org store's actor row is not the org identity's: %v", row)
	}
	if _, err := os.Stat(filepath.Join(dir, schema.TableActors, mine.String()+".json")); !os.IsNotExist(err) {
		t.Errorf("the open's own actor row was written into the org store (%v)", err)
	}

	// the role check places each store by the org listing it (D-694)
	if got, known := a.StoreOrg("acme"); got != "acme" || !known {
		t.Errorf("StoreOrg(acme) = %q, %v; want acme, true", got, known)
	}
	if got, known := a.StoreOrg("side"); got != "" || !known {
		t.Errorf("StoreOrg(side) = %q, %v; want \"\", true (no org lists it)", got, known)
	}

	sideOut, err := run(t, a, opsmemory.OpAdd, `{"body":"a note elsewhere","to":"side"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sf := rowFile(t, filepath.Join(fileuri.LocalPath(otherURL), "notes", schema.TableMemories, idOf(t, sideOut)+".json"))
	if got := fieldString(t, sf, "created_by"); got != mine.String() {
		t.Errorf("a store no org lists: created_by = %s, want the open identity %s", got, mine)
	}
}

// TestRosterCallerIsTheOrgWriter (devco D-694): the role check judges the
// caller as the identity a write into the org's store is stamped with
// (storeWho), not the open's own identity: from a repo no org matches,
// the open is the OS user while the admin's write into their org store
// carries their org identity, and the admin was refused their own add.
// A roster of an org with no writer of its own keeps the open's actor.
func TestRosterCallerIsTheOrgWriter(t *testing.T) {
	t.Parallel()
	open, err := identity.ActorID("human:solo@example.test")
	if err != nil {
		t.Fatal(err)
	}
	org, err := identity.ActorID("human:mira@example.test")
	if err != nil {
		t.Fatal(err)
	}
	st := &state{actor: open, who: identity.Resolved{StableKey: "human:solo@example.test", Kind: schema.ActorKindHuman},
		storeWho: map[store.Source]storeWriter{"acme/clients": {id: org}}}
	a := &App{}
	for _, c := range []struct {
		src  store.Source
		want ids.ID[ids.Actor]
	}{{"acme/org", org}, {"acme/clients", org}, {"other/org", open}, {store.SourceRepo, open}} {
		if got := a.rosterPerson(context.Background(), st, rosterAt{src: c.src}); got != c.want {
			t.Errorf("roster caller on %s = %s, want %s", c.src, got, c.want)
		}
	}
}
