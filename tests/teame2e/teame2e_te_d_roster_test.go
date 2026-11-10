//go:build teame2e

package teame2e

// later.md 570 (BUG-T1, BUG-T3), 569 (BUG-T6) and 288 (org rename) on
// cmd/lore, across clones and machines: the roster a person joins is the
// one every other clone resolves handles on, at once.

import (
	"os"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// teDSoloConfig is a no-org machine whose person has a handle.
func teDSoloConfig(key, name, handle string) th.Config {
	return th.Config{
		Identity: &th.Identity{Key: key, Name: name, Handle: handle},
		Personal: &th.Personal{Store: th.PersonalLocal},
		Live:     th.FreshFetch(),
	}
}

// BUG-T1 (Q12): in a repo no org applies to, `lore actor register` puts the
// actors row on the live ref and pushes it, so a teammate's clone that never
// pulled main resolves @handle on its next command; nothing is left in the
// registering clone's work tree or branch.
func TestTeD_NoOrgRegisterReachesSecondClone(t *testing.T) {
	w := newTeamWorld(t, th.Cmd("actor", "register"), th.Flag("assign", "task", "add"))
	r := w.Remote(slugLoose)
	bob := w.Human("bob", "bob@solo.example")
	bob.WriteConfig(teDSoloConfig("human:bob@solo.example", "Bob Roe", "bob"))
	carol := w.Human("carol", "carol@solo.example")
	carol.WriteConfig(teDSoloConfig("human:carol@solo.example", "Carol Poe", "carol"))

	bc := bob.Clone(r, "tool")
	bc.MustLore("init", "--non-interactive", "--name=tool")
	tl := createTasklist(t, bc)
	bc.CommitAll("lore")
	bc.AdminPush("origin", "main")
	cc := carol.Clone(r, "tool")

	// Before bob registers, carol cannot assign him.
	cc.Lore("task", "add", "before register", "--tasklist", tl, "--assign", "@bob").Refused(t, "@bob")

	headBefore := bc.Head()
	bc.MustLore("actor", "register")
	if st := bc.Status(); strings.Contains(st, tblActors) {
		t.Fatalf("the actors row goes on the live ref, not the work tree: %q", st)
	}
	onLive := false
	for _, f := range r.Files(refLive) {
		if strings.Contains(f, "/"+tblActors+"/") {
			if body, _ := r.Read(refLive, f); strings.Contains(body, "Bob Roe") {
				onLive = true
			}
		}
	}
	if !onLive {
		t.Fatalf("bob's actors row must be pushed on the live ref: %v", r.Files(refLive))
	}
	// LORE_TEAM_SPEC 1240: the live row "lands like a finished row", so a
	// landing commit on origin main is by design; landing never touches the
	// person's local branch (F5), so bob's checkout gains no commit.
	if h := bc.Head(); h != headBefore {
		t.Fatalf("register commits nothing on bob's branch: %s -> %s", headBefore, h)
	}

	// carol never pulls main: her next command resolves @bob.
	id := mustID(t, cc.MustLore("task", "add", "review the parser", "--tasklist", tl, "--assign", "@bob"), "tsk")
	cc.MustLore("task", "show", id).Says(t, "Bob Roe")
	// And bob sees the assignment in his clone.
	bc.MustLore("task", "show", id).Says(t, "review the parser", "Bob Roe")
}

// teDAgencyNotes is the store slug of the org mira creates.
const teDAgencyNotes = "github.com/agency/notes"

// BUG-T3 + org rename: an `org import` whose roster register fails (the
// handle is taken) exits non-zero with the cause's code and says the entry
// was kept; a second import under another entry name joins the roster.
// `org rename` is a machine-local label change: the old name is gone, the
// rows stay readable, the roster is unchanged; a rename onto a taken name
// or an invalid one is refused and changes nothing.
func TestTeD_OrgImportRegisterFailureAndRename(t *testing.T) {
	w := newTeamWorld(t, th.Cmd("org", "create"), th.Cmd("org", "import"), th.Cmd("org", "rename"), th.Flag("to", "memory", "add"))
	notes := w.Remote(teDAgencyNotes)
	appR := w.Remote("github.com/agency/app")
	mira := w.Human("mira", "mira@agency.example")
	mira.WriteConfig(th.Config{Personal: &th.Personal{Store: th.PersonalLocal}, Live: th.FreshFetch()})
	initStore(w, mira, "", "https://"+teDAgencyNotes, "", "--yes").OK(t)
	app := mira.Clone(appR, "app")
	app.MustLore("init", "--non-interactive", "--name=app")
	app.CommitAll("lore")
	app.AdminPush("origin", "main")
	app.MustLore("org", "create", "agency", "--store", "https://"+teDAgencyNotes, "--own", "github.com/agency/*",
		"--key", "human:mira@agency.example", "--name", "Mira Moe", "--handle", "mira", "--yes")
	app.MustLore("whoami").Says(t, "agency", "human:mira@agency.example")

	rosterFiles := func() int {
		n := 0
		for _, f := range notes.Files(refMain) {
			if strings.HasPrefix(f, folderOrg+"/"+dataDir+"/"+tblActors+"/") {
				n++
			}
		}
		return n
	}
	if n := rosterFiles(); n != 1 {
		t.Fatalf("create registers its maker: %d roster rows", n)
	}

	// bob imports with mira's handle: the entry is added, the register is
	// refused, and the command says so with a non-zero exit (BUG-T3).
	bob := w.Human("bob", "bob@agency.example")
	bob.WriteConfig(th.Config{Personal: &th.Personal{Store: th.PersonalLocal}, Live: th.FreshFetch()})
	outside := bob.Home + "/outside"
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	imp := func(extra ...string) th.Result {
		return bob.Lore(outside, append([]string{"org", "import", "https://" + teDAgencyNotes, "--key", "human:bob@agency.example", "--name", "Bob Roe", "--yes"}, extra...)...)
	}
	res := imp("--handle", "mira")
	if res.Code == 0 {
		t.Fatalf("an import whose register failed must not exit 0: %s", res)
	}
	res.Says(t, "added", "actor register")
	if cfg, _ := os.ReadFile(bob.LorePath("config.yaml")); !strings.Contains(string(cfg), "name: agency") {
		t.Fatalf("the entry stays after the failed register: %s", cfg)
	}
	if n := rosterFiles(); n != 1 {
		t.Fatalf("a refused register writes no roster row: %d", n)
	}
	// The same name again is taken; --as another name with a free handle
	// joins the roster.
	imp("--handle", "bob").Refused(t, "agency")
	imp("--handle", "bob", "--as", "agency-2").OK(t)
	if n := rosterFiles(); n != 2 {
		t.Fatalf("the second import registers bob: %d roster rows", n)
	}

	// Rename on bob's machine onto a taken name: refused, nothing changes.
	bob.Lore(outside, "org", "rename", "agency-2", "agency").Refused(t)
	bob.Lore(outside, "org", "rename", "agency-2", "Bad Name").Refused(t)
	bob.Lore(outside, "org", "rename", "nope", "x").Fails(t, th.ExitNotFound)
	if cfg, _ := os.ReadFile(bob.LorePath("config.yaml")); !strings.Contains(string(cfg), "name: agency-2") {
		t.Fatalf("a refused rename keeps the entry: %s", cfg)
	}

	// Rename on mira's machine, inside her own repo: rows stay readable,
	// whoami names the new entry, the old name is gone, the roster is the
	// same.
	app.MustLore("memory", "add", "--body", "the agency bills monthly in arrears", "--to", "agency")
	app.MustLore("org", "rename", "agency", "northwind")
	app.MustLore("whoami").Says(t, "northwind", "human:mira@agency.example").Never(t, "agency  ")
	app.MustLore("memory", "list").Says(t, "the agency bills monthly in arrears")
	app.Lore("memory", "add", "--body", "old name", "--to", "agency").Refused(t)
	app.MustLore("memory", "add", "--body", "the new name writes", "--to", "northwind")
	if n := rosterFiles(); n != 2 {
		t.Fatalf("a rename is a label change; the roster keeps its rows: %d", n)
	}
	if st := app.Status(); st != "" {
		t.Fatalf("a rename writes nothing in the repo: %q", st)
	}
}

// BUG-T6 (later 569) with a real remote, for @handles: a handle miss on a
// roster copy fetched within fetch_ttl syncs the store once and finds a
// teammate who registered since; a hit costs no fetch; a miss the sync
// does not fix is refused after exactly one extra fetch.
func TestTeD_RosterMissOnCachedFetchSyncsOnce(t *testing.T) {
	a := newAgency(t, th.Flag("assign", "task", "add"), th.Flag("to", "task", "add"))
	cfg := aliceConfig()
	cfg.Live = &th.Live{FetchTTL: "1h"}
	a.alice.WriteConfig(cfg)
	assign := func(title, handle string) th.Result {
		return a.aliceApp.Lore("task", "add", title, "--to", "repo", "--tasklist", a.tl, "--assign", "@"+handle)
	}
	// The first roster read fetches the store copy (the setup's fetches ran
	// under fetch_ttl 0s); from then on the copy is cached for an hour.
	assign("prime", "dev1").OK(t)
	storeFetches := func() int { return len(teDStoreFetches(a.w, "alice")) }

	n0 := storeFetches()
	assign("hit", "dev1").OK(t)
	if d := storeFetches() - n0; d != 0 {
		t.Fatalf("a roster hit on a cached copy costs no fetch: %d %v", d, teDStoreFetches(a.w, "alice")[n0:])
	}

	dev2 := a.w.Human("dev2", "dev2@northwind.example")
	dev2.WriteConfig(th.Config{Personal: &th.Personal{Store: th.PersonalLocal}, Orgs: []th.Org{northwindOrg("human:dev2@northwind.example", "Dev Two", "dev2")}, Live: th.FreshFetch()})
	dev2.Clone(a.app, "app").MustLore("actor", "register")

	n1 := storeFetches()
	id := mustID(t, assign("new teammate", "dev2").OK(t), "tsk")
	if d := storeFetches() - n1; d != 1 {
		t.Fatalf("a miss on a cached copy fetches the store once: %d fetches", d)
	}
	a.aliceApp.MustLore("task", "show", id).Says(t, "Dev Two")

	n2 := storeFetches()
	assign("nobody", "ghost").Refused(t, "@ghost")
	if d := storeFetches() - n2; d != 1 {
		t.Fatalf("a miss the sync does not fix costs exactly one fetch: %d (%v)", d, teDStoreFetches(a.w, "alice")[n2:])
	}
}

// teDStoreFetches is actor's logged `git fetch` calls inside a store clone
// (~/.lore/clones), the code repo's own fetches left out.
func teDStoreFetches(w *th.World, actor string) []string {
	var out []string
	for _, c := range w.GitSubcommandCalls(actor, "fetch") {
		if strings.Contains(c, "/.lore/clones/") {
			out = append(out, c)
		}
	}
	return out
}
