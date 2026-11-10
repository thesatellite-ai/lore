//go:build teame2e

package teame2e

// F11 link index freshness (TEAM-025: `lore sync` always fetches) and
// links per store branch (TEAM-011): what `lore whoami` reports as
// "links known N link(s)".

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// linksLine is the "links" line of `lore whoami`.
func linksLine(t *testing.T, c *th.Clone) string {
	t.Helper()
	return whoamiField(t, c.MustLore("whoami"), "links")
}

// F11 / TEAM-025: a teammate's link is known right after `lore sync`, even
// when the last store fetch is younger than live.fetch_ttl.
func TestF11_SyncRefreshesTheLinkIndex(t *testing.T) {
	w := newTeamWorld(t, append(append([]th.Cap{}, agencyCaps...), th.Cmd("whoami"), th.Cmd("sync"))...)
	w.Remote(slugStore)
	app := w.Remote(slugApp)
	alice, dev1, acmedev := w.Human("alice", "alice@northwind.example"), w.Human("dev1", "dev1@northwind.example"), w.Human("acmedev", "dev@acme.example")
	alice.WriteConfig(th.Config{Personal: &th.Personal{Store: th.PersonalLocal}, Orgs: []th.Org{northwindOrg(aliceKey, aliceName, "alice")}, Live: th.FreshFetch()})
	// dev1 keeps the default-like cache: a fetch counts for an hour.
	dev1.WriteConfig(th.Config{Personal: &th.Personal{Store: th.PersonalLocal}, Orgs: []th.Org{northwindOrg(dev1Key, "Dev One", "dev1")}, Live: &th.Live{FetchTTL: "1h"}})
	acmedev.WriteConfig(th.Config{Live: th.FreshFetch()})

	acmeApp := acmedev.Clone(app, "app")
	acmeApp.MustLore("init", "--non-interactive", "--name=app")
	acmeApp.CommitAll("lore")
	acmeApp.AdminPush("origin", "main")
	initStore(w, alice, "northwind", "https://"+slugStore, "", "--yes").OK(t)

	dev1App := dev1.Clone(app, "app")
	if l := linksLine(t, dev1App); !strings.Contains(l, "0 link(s)") {
		t.Fatalf("before the link: %q", l)
	}
	alice.Clone(app, "app").MustLore("org", "link", orgNorthwind, "--project", folderAcme, "--yes")
	if l := linksLine(t, dev1App); !strings.Contains(l, "0 link(s)") {
		t.Fatalf("test premise: inside fetch_ttl whoami reads its last fetch: %q", l)
	}
	dev1App.MustLore("sync")
	if l := linksLine(t, dev1App); !strings.Contains(l, "1 link(s)") {
		t.Fatalf("after lore sync the teammate's link is known: %q", l)
	}
}

// TEAM-011 / side effect 19: links exist per store branch, so in a repo
// that chose the staging entry `whoami` counts the staging branch's links,
// not the main branch's.
func TestF10_StagingWhoamiCountsTheStagingBranchLinks(t *testing.T) {
	a := newAgency(t, th.Cmd("org", "use"), th.Cmd("whoami"), th.Cmd("store", "init"))
	if l := linksLine(t, a.aliceApp); !strings.Contains(l, "1 link(s)") {
		t.Fatalf("on the main entry the main branch's link counts: %q", l)
	}
	initStore(a.w, a.alice, "northwind_staging", "https://"+slugStore, stagingBranch, "--yes").OK(t)
	a.aliceApp.MustLore("org", "use", orgStaging)
	if l := linksLine(t, a.aliceApp); !strings.Contains(l, "0 link(s)") || !strings.Contains(l, stagingBranch) {
		t.Fatalf("nothing is linked on the staging branch yet: %q", l)
	}
	a.aliceApp.MustLore("org", "link", orgStaging, "--project", folderAcme, "--yes")
	if l := linksLine(t, a.aliceApp); !strings.Contains(l, "1 link(s)") {
		t.Fatalf("after linking on staging: %q", l)
	}
}

// F11 confirmation + trial F1: a refused non-tty link leaves no local state
// (nothing committed in the store clone for a later command to push), and
// the confirmed re-run reports what it did even when its first push lost a
// race against a teammate's store write.
func TestF11_RefusedLinkLeavesNothingAndTheRerunSaysWhatItDid(t *testing.T) {
	w := newTeamWorld(t, append(append([]th.Cap{}, agencyCaps...), th.Cmd("whoami"))...)
	store := w.Remote(slugStore)
	app := w.Remote(slugApp)
	alice, dev1, acmedev := w.Human("alice", "alice@northwind.example"), w.Human("dev1", "dev1@northwind.example"), w.Human("acmedev", "dev@acme.example")
	// alice's store fetch counts for an hour, so her link builds on a
	// store branch that dev1 moves meanwhile.
	alice.WriteConfig(th.Config{Personal: &th.Personal{Store: th.PersonalLocal}, Orgs: []th.Org{northwindOrg(aliceKey, aliceName, "alice")}, Live: &th.Live{FetchTTL: "1h"}})
	dev1.WriteConfig(th.Config{Personal: &th.Personal{Store: th.PersonalLocal}, Orgs: []th.Org{northwindOrg(dev1Key, "Dev One", "dev1")}, Live: th.FreshFetch()})
	acmedev.WriteConfig(th.Config{Live: th.FreshFetch()})
	acmeApp := acmedev.Clone(app, "app")
	acmeApp.MustLore("init", "--non-interactive", "--name=app")
	acmeApp.CommitAll("lore")
	acmeApp.AdminPush("origin", "main")
	initStore(w, alice, "northwind", "https://"+slugStore, "", "--yes").OK(t)

	aliceApp := alice.Clone(app, "app")
	before := store.Rev(refMain)
	aliceApp.Lore("org", "link", orgNorthwind, "--project", folderAcme).Refused(t, "--yes")
	aliceApp.MustLore("whoami") // pushes any waiting store write
	if store.Rev(refMain) != before {
		t.Fatal("a refused link must leave nothing for a later command to push")
	}
	if st := aliceApp.Status(); st != "" {
		t.Fatalf("a refused link writes nothing in the repo: %q", st)
	}

	dev1.Clone(app, "app").MustLore("actor", "register") // moves the store branch
	r := aliceApp.MustLore("org", "link", orgNorthwind, "--project", folderAcme, "--yes")
	r.Says(t, "created; pushed").Never(t, "already so")
	if _, ok := store.Read(refMain, folderAcme+"/.lore/data/_meta.json"); !ok {
		t.Fatal("the link reached the store")
	}
	aliceApp.MustLore("org", "link", orgNorthwind, "--project", folderAcme, "--yes").Says(t, "already so")
}
