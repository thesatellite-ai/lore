//go:build teame2e

package teame2e

// P3 — stores, links, targets, references, leak guards, render
// (LORE_TEAM_SPEC.md F11–F16, F20, E9–E11, §6, cookbook 3, 4, 8, 10, 12,
// 13, 14, side effects 4, 5, 20, 32).

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// internalNote is a 14-word internal sentence; the F16 text rule blocks any
// run of 8 consecutive words of it in client-readable places.
const internalNote = "client is slow to pay so always send the estimate before the call"

// storeHas reports whether a store project holds a row containing text, on
// its live ref or its store branch.
func (a *agency) storeHas(t *testing.T, folder, text string) bool {
	t.Helper()
	for _, ref := range []string{storeLiveRef, refMain} {
		for _, f := range a.store.Files(ref) {
			if !strings.HasPrefix(f, folder+"/") {
				continue
			}
			if body, _ := a.store.Read(ref, f); strings.Contains(body, text) {
				return true
			}
		}
	}
	return false
}

// checkoutMeta is the part of .lore/data/_meta.json these tests read.
type checkoutMeta struct {
	RepoID string `json:"repo_id"`
}

// readCheckoutMeta reads the checkout's .lore/data/_meta.json.
func readCheckoutMeta(t *testing.T, c *th.Clone) checkoutMeta {
	t.Helper()
	body, ok := c.ReadFile(dataDir + "/" + metaFile)
	if !ok {
		t.Fatal("no _meta.json")
	}
	var m checkoutMeta
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("_meta.json: %v %s", err, body)
	}
	return m
}

// repoID reads the checkout's repo id (rep_) from .lore/data/_meta.json:
// private render files live under ~/.lore/render/<repo_id>/ (CFG6).
func repoID(t *testing.T, c *th.Clone) string {
	t.Helper()
	m := readCheckoutMeta(t, c)
	if m.RepoID == "" {
		t.Fatal("_meta.json names no repo_id")
	}
	return m.RepoID
}

// ---------------------------------------------------------------- F12 store checks

// E10 / F12: a code repo listed under stores: is refused by every command.
func TestE10_CodeRepoListedAsAStoreIsRefused(t *testing.T) {
	// The store checks arrive with the stores feature; whoami alone exists
	// earlier and would make this test run (and fail) before them.
	w := newTeamWorld(t, th.Cmd("whoami"), th.Cmd("store", "init"))
	docs := w.Remote("github.com/northwind/globex-docs")
	alice := w.Human("alice", "alice@northwind.example")
	seed := alice.Clone(docs, "docs-seed")
	seed.WriteFile("src/index.ts", "export {}\n")
	seed.WriteFile("package.json", "{}\n")
	seed.CommitAll("code")
	seed.AdminPush("origin", "main")
	org := northwindOrg(aliceKey, aliceName, "alice")
	org.Stores = []th.Store{{URL: docs.URL(), OrgFolder: folderOrg}}
	alice.WriteConfig(th.Config{Orgs: []th.Org{org}, Live: th.FreshFetch()})
	tools := alice.Clone(w.Remote(slugOwnTools), "tools")
	tools.MustLore("init", "--non-interactive", "--name=tools")
	tools.Lore("whoami").Fails(t, th.ExitGuard, "E_NOT_A_STORE", "lore store init")
	if docs.Rev(refMain) != seed.Head() {
		t.Fatal("lore must never commit into a code repo listed as a store")
	}
}

// F12 init: only an empty repo becomes a store; the marker is one parentless
// commit on the store branch.
func TestF12_StoreInitOnlyOnAnEmptyRepo(t *testing.T) {
	w := newTeamWorld(t, th.Cmd("store", "init"))
	alice := w.Human("alice", "alice@northwind.example")
	used := w.Remote("github.com/northwind/has-code")
	seed := alice.Clone(used, "seed")
	seed.WriteFile("main.go", "package main\n")
	seed.CommitAll("code")
	seed.AdminPush("origin", "main")
	initStore(w, alice, "used", used.URL(), "", "--yes").Refused(t)
	empty := w.Remote(slugStore)
	initStore(w, alice, "empty", empty.URL(), "", "--yes").OK(t)
	if !hasItem(empty.Files(refMain), storeMarker) || empty.CommitCount(refMain) != 1 || empty.Parents(refMain) != 0 {
		t.Fatalf("store init writes %s as one parentless commit: %v", storeMarker, empty.Files(refMain))
	}
}

// F12 matrix: allowed files keep a store valid; anything else makes every
// use refuse with E_NOT_A_STORE.
func TestF12_StoreAllowedFiles(t *testing.T) {
	cases := []struct {
		path string
		ok   bool
	}{
		{"README.md", true},
		{"LICENSE", true},
		{".gitattributes", true},
		{".github/workflows/lore-merge.yml", true},
		{".github/workflows/deploy.yml", false},
		{"src/app.ts", false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			a := newAgency(t)
			sc := a.alice.Clone(a.store, "store-plain")
			sc.WriteFile(tc.path, "x\n")
			sc.CommitAll("add " + tc.path)
			sc.AdminPush("origin", "main")
			r := a.aliceApp.Lore("whoami")
			if tc.ok {
				r.OK(t)
			} else {
				r.Fails(t, th.ExitGuard, "E_NOT_A_STORE")
			}
		})
	}
}

// §5.3: org entries on one URL share one clone. STORE layout (2026-11-12,
// STORE_SERVER_SPEC 6.1 "Store clones"): a store is ONE plain git clone at
// ~/.lore/stores/<owner>/<repo> (the host never in the path), its files
// checked out, lore's own files (the lock, the audit journal, the fetch
// and push stamps) inside its .git/lore and nowhere else; a pinned branch
// is its own clone <repo>@<branch>, so two branches never share a
// checkout. No bare twin, no work/ folder, no second (v0.1.17) clone.
func TestF12_EntriesOnOneURLShareOneClone(t *testing.T) {
	a := newAgency(t)
	a.aliceApp.With(th.EnvLoreOrg + "=" + orgStaging).Lore("whoami")
	base := a.alice.LorePath("stores", "northwind")
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatalf("store clone dir: %v", err)
	}
	if !slices.ContainsFunc(entries, func(e os.DirEntry) bool { return e.Name() == "lore" }) {
		t.Fatalf("one clone per URL expected at %s/lore, got %v", base, entries)
	}
	for _, e := range entries {
		name := e.Name()
		if name != "lore" && name != "lore@"+stagingBranch {
			t.Fatalf("only the northwind/lore store may be cloned here, found %s in %v", name, entries)
		}
		clone := filepath.Join(base, name)
		if out := a.w.Git(clone, "rev-parse", "--is-bare-repository"); out != "false" {
			t.Fatalf("a store clone is a plain clone, not bare: %s", clone)
		}
		if fi, serr := os.Stat(filepath.Join(clone, ".git")); serr != nil || !fi.IsDir() {
			t.Fatalf("%s has no .git folder (a pointer file means a worktree): %v", clone, serr)
		}
		for _, old := range []string{"work", "lock", "fetch-stamp", "state", "push_stamp", "push_fail_stamp", "compact_stamp"} {
			if _, serr := os.Lstat(filepath.Join(clone, old)); serr == nil {
				t.Fatalf("%s holds lore's own %s among the store's files; it belongs in .git/lore", clone, old)
			}
		}
		if _, serr := os.Stat(filepath.Join(clone, ".git", "lore", "lock")); serr != nil {
			t.Fatalf("the store lock is not in %s/.git/lore: %v", clone, serr)
		}
	}
	for _, gone := range []string{"clones", "clones-v017"} {
		if _, serr := os.Stat(a.alice.LorePath(gone)); serr == nil {
			t.Fatalf("~/.lore/%s exists: a second copy of the store", gone)
		}
	}
}

// ---------------------------------------------------------------- F11 links

// E9 / F11: a link made on one machine is seen by a teammate's next command;
// nothing is written to the client repo; a fork with the same project id is
// not linked at its own remote and lore says so. Since StepMovedLink (a
// client repo that moved keeps its leak guards, journeys agency) lore
// cannot tell a fork from a moved repo, so the fork keeps the guards too
// and whoami names the old remote and both ways out (re-link here, or
// `lore project reid` for a different project).
func TestE09_LinkSeenByTeammateNotByFork(t *testing.T) {
	a := newAgency(t)
	if a.app.Rev(refMain) != a.acmeApp.Head() || len(a.app.Refs("refs/lore/")) != 0 {
		t.Fatal("linking must write nothing to the client repo")
	}
	a.dev1App.MustLore("whoami").Says(t, "link", folderAcme)
	fork := a.w.Remote(slugAppFork)
	a.aliceApp.Git("push", "-q", fork.URL(), "+refs/heads/*:refs/heads/*")
	fc := a.dev1.Clone(fork, "app-fork")
	fc.MustLore("whoami").Says(t, "shares a project id", "lore project reid", "at old remote", "leak guards stay on")
	a.dev1App.MustLore("whoami").Says(t, "link", folderAcme)
}

// F11 / F4: two people linking two repos to one folder at once both win
// (links merge as a set union, never as conflict markers).
func TestF11_Race_ConcurrentLinksMergeAsUnion(t *testing.T) {
	a := newAgency(t)
	var clones []*th.Clone
	for i, slug := range []string{"github.com/acme/web", "github.com/acme/mobile"} {
		r := a.w.Remote(slug)
		boot := a.acmedev.Clone(r, strings.TrimPrefix(slug, "github.com/acme/"))
		boot.MustLore("init", "--non-interactive", "--name="+strings.TrimPrefix(slug, "github.com/acme/"))
		boot.CommitAll("lore")
		boot.AdminPush("origin", "main")
		who := a.alice
		if i == 1 {
			who = a.dev1
		}
		clones = append(clones, who.Clone(r, strings.TrimPrefix(slug, "github.com/acme/")))
	}
	for _, r := range a.w.Simultaneously(
		clones[0].Prepare("org", "link", orgNorthwind, "--project", folderAcme, "--yes"),
		clones[1].Prepare("org", "link", orgNorthwind, "--project", folderAcme, "--yes"),
	) {
		r.OK(t)
	}
	// C5: each person sends their link with lore sync; the two merge as a union
	clones[0].MustLore("sync")
	clones[1].MustLore("sync")
	meta, _ := a.store.Read(refMain, folderAcme+"/"+metaFile)
	for _, slug := range []string{"github.com/acme/web", "github.com/acme/mobile", slugApp} {
		if !strings.Contains(meta, slug) {
			t.Fatalf("link for %s lost: %s", slug, meta)
		}
	}
	if strings.Contains(meta, "<<<<<<<") {
		t.Fatal("links merge as a set union, never with markers")
	}
}

// Side effect 4 / TEAM-032: a repo linked from two folders needs a default
// for --to <org>; without one lore refuses and lists the candidates.
func TestSideEffect04_TwoLinksNeedADefault(t *testing.T) {
	a := newAgency(t, th.Flag("to", "memory", "add"), th.Flag("default", "org", "link"))
	a.aliceApp.MustLore("org", "link", orgNorthwind, "--project", folderGlobex, "--yes")
	a.aliceApp.Lore("memory", "add", "--body", "which folder?", "--to", orgNorthwind).Refused(t, folderAcme, folderGlobex)
	a.aliceApp.MustLore("org", "link", orgNorthwind, "--project", folderAcme, "--default", "--yes")
	a.aliceApp.MustLore("memory", "add", "--body", "goes to the default link", "--to", orgNorthwind)
	a.aliceApp.MustLore("sync") // C5: store writes reach the remote with the writer's sync
	if !a.storeHas(t, folderAcme, "goes to the default link") {
		t.Fatal("--to <org> must use the link marked default")
	}
}

// ---------------------------------------------------------------- F13 several stores

// F13 / cookbook 14: one store per client; the link says which store; folder
// names are unique across an entry's stores.
func TestRecipe14_OneStorePerClient(t *testing.T) {
	w := newTeamWorld(t, append(append([]th.Cap{}, agencyCaps...), th.Flag("store", "org", "link"), th.Cmd("store", "folder", "add"))...)
	orgStore, acmeStore := w.Remote("github.com/northwind/lore-org"), w.Remote("github.com/northwind/lore-acme")
	alice := w.Human("alice", "alice@northwind.example")
	org := northwindOrg(aliceKey, aliceName, "alice")
	org.Stores = []th.Store{{URL: orgStore.URL(), OrgFolder: "."}, {URL: acmeStore.URL()}}
	alice.WriteConfig(th.Config{Personal: &th.Personal{Store: th.PersonalLocal}, Orgs: []th.Org{org}, Live: th.FreshFetch()})
	app := w.Remote(slugApp)
	client := w.Human("acmedev", "dev@acme.example").Clone(app, "app")
	client.MustLore("init", "--non-interactive", "--name=app")
	client.CommitAll("lore")
	client.AdminPush("origin", "main")
	initStore(w, alice, "", orgStore.URL(), "", "--yes").OK(t)
	initStore(w, alice, "", acmeStore.URL(), "", "--yes").OK(t)
	c := alice.Clone(app, "app")
	c.MustLore("org", "link", orgNorthwind, "--project", folderAcme, "--store", acmeStore.URL(), "--yes")
	c.MustLore("memory", "add", "--body", "NDA client note")
	c.MustLore("sync") // C5: the note reaches the store with alice's sync
	found := false
	for _, ref := range []string{storeLiveRef, refMain} {
		for _, f := range acmeStore.Files(ref) {
			if body, _ := acmeStore.Read(ref, f); strings.HasPrefix(f, folderAcme+"/") && strings.Contains(body, "NDA client note") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("the note must live in the client's own store")
	}
	if objs := orgStore.AllObjectsContain("NDA client note"); len(objs) != 0 {
		t.Fatal("the company store must never get the client note")
	}
	c.Lore("org", "link", orgNorthwind, "--project", folderAcme, "--store", orgStore.URL(), "--yes").Refused(t, folderAcme)
	// store folder add refuses the name too, and writes nothing to the
	// company store (team matrix F13)
	before := orgStore.Files(refMain)
	c.Lore("store", "folder", "add", "--store", orgStore.URL(), "--folder", folderAcme).Refused(t, "E_INVALID_INPUT", folderAcme, "unique across an org's stores")
	if after := orgStore.Files(refMain); strings.Join(after, ",") != strings.Join(before, ",") {
		t.Fatalf("a refused folder add changed the company store: %v -> %v", before, after)
	}
}

// Cookbook 4: a contractor listing only the company store and Acme's store
// sees company rules and Acme's notes, never another client's.
func TestRecipe04_ContractorSeesOnlyTheirClient(t *testing.T) {
	w := newTeamWorld(t, append(append([]th.Cap{}, agencyCaps...), th.Flag("store", "org", "link"), th.Flag("to", "memory", "add"))...)
	orgStore, acmeStore, globexStore := w.Remote("github.com/northwind/lore-org"), w.Remote("github.com/northwind/lore-acme"), w.Remote("github.com/northwind/lore-globex")
	alice := w.Human("alice", "alice@northwind.example")
	full := northwindOrg(aliceKey, aliceName, "alice")
	full.Stores = []th.Store{{URL: orgStore.URL(), OrgFolder: "."}, {URL: acmeStore.URL()}, {URL: globexStore.URL()}}
	alice.WriteConfig(th.Config{Orgs: []th.Org{full}, Live: th.FreshFetch()})
	for _, s := range []*th.Remote{orgStore, acmeStore, globexStore} {
		initStore(w, alice, "", s.URL(), "", "--yes").OK(t)
	}
	app, globex := w.Remote(slugApp), w.Remote("github.com/globex/backend")
	for _, r := range []*th.Remote{app, globex} {
		b := w.Human("owner-"+strings.ReplaceAll(r.Slug, "/", "-"), "o@client.example").Clone(r, "repo")
		b.MustLore("init", "--non-interactive", "--name="+filepath.Base(r.Slug))
		b.CommitAll("lore")
		b.AdminPush("origin", "main")
	}
	ac := alice.Clone(app, "app")
	ac.MustLore("org", "link", orgNorthwind, "--project", folderAcme, "--store", acmeStore.URL(), "--yes")
	ac.MustLore("memory", "add", "--body", "acme internal: staging resets on Sunday")
	ac.MustLore("rule", "add", "--body", "company rule: never deploy on Friday", "--to", orgNorthwind+"/.")
	ic := alice.Clone(globex, "globex")
	ic.MustLore("org", "link", orgNorthwind, "--project", folderGlobex, "--store", globexStore.URL(), "--yes")
	ic.MustLore("memory", "add", "--body", "globex internal: proxy strips headers")
	ac.MustLore("sync") // C5: alice sends her store writes before the contractor reads
	ic.MustLore("sync")

	cont := w.Human("cont1", "contractor@northwind.example")
	narrow := northwindOrg("human:contractor@northwind.example", "Contractor Name", "cont1")
	narrow.Stores = []th.Store{{URL: orgStore.URL(), OrgFolder: "."}, {URL: acmeStore.URL()}}
	narrow.Repos = []string{slugApp}
	cont.WriteConfig(th.Config{Orgs: []th.Org{narrow}, Live: th.FreshFetch()})
	cc := cont.Clone(app, "app")
	cc.MustLore("search", "internal").Says(t, "staging resets on Sunday").Never(t, "proxy strips headers")
	cc.MustLore("rule", "list").Says(t, "never deploy on Friday")
	if _, err := os.Stat(cont.LorePath("stores", "northwind", "lore-globex")); !os.IsNotExist(err) {
		t.Fatal("a contractor never clones a store they do not list")
	}
}

// ---------------------------------------------------------------- F14 targets

// F14 / guard 1 / TEAM-035: in a client repo every kind follows the safe
// default; rules without a TTY or by agents need --to; --to repo is the
// deliberate way into the client repo; another org is refused.
func TestF14_ClientRepoWriteTargets(t *testing.T) {
	a := newAgency(t, th.Flag("to", "memory", "add"), th.Flag("to", "rule", "add"), th.Cmd("agent", "start"), th.Flag("create", "memory", "add"))
	a.aliceApp.MustLore("memory", "add", "--body", "memory goes internal by default")
	a.aliceApp.MustLore("sync") // C5: store writes reach the remote with the writer's sync
	if !a.storeHas(t, folderAcme, "memory goes internal by default") {
		t.Fatal("a memory without --to in a client repo goes to the linked project")
	}
	a.aliceApp.Lore("rule", "add", "--body", "rule without a tty").Refused(t, "--to")
	agent := startAgent(t, a.alice, a.aliceApp, agentLabelBackend)
	a.aliceApp.As(agent).Lore("rule", "add", "--body", "require prepayment from this client").Refused(t, "--to")
	r := a.aliceApp.MustLore("rule", "add", "--body", "money is stored in minor units", "--to", "repo")
	id := mustID(t, r, "rul")
	if _, ok := a.aliceApp.ReadFile(mainRowPath("", tblRules, id)); !ok {
		t.Fatal("--to repo writes the client repo's branch data")
	}
	a.aliceApp.Lore("memory", "add", "--body", "other company", "--to", orgBetalabs).Refused(t)
	// Q-stores-2 (coordinator 15:05, typo safety): a folder that exists in no
	// listed store is refused with the existing folders and a hint; only
	// --create makes a new store project.
	a.aliceApp.Lore("memory", "add", "--body", "for the globex group", "--to", orgNorthwind+"/"+folderGlobex).Refused(t, folderAcme, "--create")
	a.aliceApp.MustLore("sync") // C5: store writes reach the remote with the writer's sync
	if a.storeHas(t, folderGlobex, "for the globex group") {
		t.Fatal("a refused --to <org>/<new folder> must write nothing")
	}
	a.aliceApp.MustLore("memory", "add", "--body", "for the globex group", "--to", orgNorthwind+"/"+folderGlobex, "--create")
	a.aliceApp.MustLore("sync") // C5: store writes reach the remote with the writer's sync
	if !a.storeHas(t, folderGlobex, "for the globex group") {
		t.Fatal("--to <org>/<folder> writes that folder of the resolved org's stores")
	}
	// Q11: starting an agent inside the client clone must not register its
	// label or actor id in the client repo.
	if f := a.clientSees(t, "memory goes internal by default", "require prepayment", "for the globex group", agentLabelBackend, agent.Env(th.EnvLoreActor), agentActorID(t, a.store, agentLabelBackend)); len(f) != 0 {
		t.Fatalf("internal and refused rows, and the agent's label, key and id, must never reach the client: %v", f)
	}
}

// F14 edge case: the store's lore-org.yaml default wins over a looser personal
// default.
func TestF14_StoreDefaultBeatsLooserPersonalDefault(t *testing.T) {
	a := newAgency(t)
	sc := a.alice.Clone(a.store, "store-plain")
	sc.WriteFile(folderOrg+"/lore-org.yaml", "defaults:\n  client_repos: {memory: northwind}\n")
	sc.CommitAll("team defaults")
	sc.AdminPush("origin", "main")
	loose := northwindOrg(dev1Key, "Dev One", "dev1")
	loose.Defaults = &th.OrgDefaults{ClientRepos: map[string]string{"memory": "repo"}}
	a.dev1.WriteConfig(th.Config{Orgs: []th.Org{loose}, Live: th.FreshFetch()})
	r := a.dev1App.MustLore("memory", "add", "--body", "stricter default wins")
	if _, ok := a.dev1App.ReadFile(mainRowPath("", tblMemories, mustID(t, r, "mem"))); ok {
		t.Fatal("a personal 'repo' default must not override the store's stricter default")
	}
	a.dev1App.MustLore("sync") // C5: store writes reach the remote with the writer's sync
	if !a.storeHas(t, folderAcme, "stricter default wins") {
		t.Fatal("the memory goes to the linked project")
	}
}

// ---------------------------------------------------------------- F15 references

// F15: pointers flow inward only; an internal comment on a client task is
// seen by Northwind, never by Acme; a client row pointing at an internal
// row is refused.
func TestF15_PointersOnlyFlowInward(t *testing.T) {
	a := newAgency(t, th.Flag("to", "comment", "add"), th.Flag("to", "task", "add"), th.Flag("ref", "task", "add"))
	clientTask := mustID(t, a.acmeApp.MustLore("task", "add", "migrate checkout to the new API", "--tasklist", a.tl), "tsk")
	a.acmeApp.MustLore("sync")
	a.aliceApp.MustLore("comment", "add", "--on-table", tblTasks, "--on-id", clientTask, "--body", "client is slow to test, push them", "--to", orgNorthwind)
	a.aliceApp.MustLore("sync") // C5: the comment reaches dev1 with alice's sync
	a.dev1App.MustLore("task", "show", clientTask).Says(t, "client is slow to test", "["+orgNorthwind+"/"+folderAcme+"]")
	a.acmeApp.MustLore("task", "show", clientTask).Never(t, "client is slow to test")
	internalTask := mustID(t, a.aliceApp.MustLore("task", "add", "internal follow-up", "--to", orgNorthwind), "tsk")
	// FLAG5: the refusal names the row and the rule (E_LEAK_BLOCKED, F15),
	// never a not-found that hides why.
	a.aliceApp.Lore("comment", "add", "--on-table", tblTasks, "--on-id", internalTask, "--body", "visible pointer", "--to", "repo").Refused(t, "E_LEAK_BLOCKED", internalTask)
	a.aliceApp.Lore("task", "add", "follow the internal one", "--to", "repo", "--ref", internalTask).Refused(t, "E_LEAK_BLOCKED", internalTask)
	a.aliceApp.Lore("task", "edit", clientTask, "--ref", internalTask).Refused(t, "E_LEAK_BLOCKED", internalTask)
	if f := a.clientSees(t, internalTask, "internal follow-up"); len(f) != 0 {
		t.Fatalf("a pointer alone reveals the hidden row: %v", f)
	}
}

// ---------------------------------------------------------------- F16 leaks

// E11 / F16 text rule: 8 consecutive words of an internal note anywhere in a
// commit (any staged file, the message) block it; 7 words do not; the
// normaliser ignores case and punctuation.
func TestE11_PastedInternalSentenceIsBlocked(t *testing.T) {
	a := newAgency(t)
	memID := mustID(t, a.aliceApp.MustLore("memory", "add", "--body", internalNote), "mem")
	// 0.1.18: a store write is pushed by `lore sync` (STORE_SERVER_SPEC 6.1
	// Sync, the git outbox), not at once like v0.1.17's live ref.
	a.aliceApp.MustLore("sync")
	c := a.dev1App
	c.MustLore("sync")
	c.MustLore("memory", "list")
	words := strings.Fields(internalNote)

	c.WriteFile("src/checkout.ts", "// "+strings.Join(words[2:9], " ")+"\n")
	c.Git("add", "src/checkout.ts")
	if r := c.TryGit("commit", "-m", "seven words are fine"); r.Code != 0 {
		t.Fatalf("7 matching words must not block: %s", r.Out())
	}

	c.WriteFile("src/checkout.ts", "// "+strings.ToUpper(strings.Join(words[2:10], ", "))+"!\n")
	c.Git("add", "src/checkout.ts")
	r := c.TryGit("commit", "-m", "fix checkout")
	if r.Code == 0 || !strings.Contains(r.Out(), "src/checkout.ts") || !strings.Contains(r.Out(), memID) {
		t.Fatalf("8 words (any case, punctuation) must block and name the file and note: %s", r.Out())
	}
	c.Git("reset", "-q", "--hard", "HEAD")

	c.WriteFile("src/other.ts", "export {}\n")
	c.Git("add", "src/other.ts")
	if r := c.TryGit("commit", "-m", strings.Join(words[4:12], " ")); r.Code == 0 || !strings.Contains(r.Out(), "commit message") {
		t.Fatalf("an internal sentence in the commit message must block: %s", r.Out())
	}
}

// F16 CI mode: the client's CI, with no store access, catches a row whose id
// belongs elsewhere and names no internal id, store or org.
func TestF16_CIModeCatchesForeignIdsAndNamesNothingInternal(t *testing.T) {
	a := newAgency(t, th.Cmd("check", "leaks"), th.Flag("ci", "check", "leaks"))
	memID := mustID(t, a.aliceApp.MustLore("memory", "add", "--body", internalNote), "mem")
	a.aliceApp.MustLore("sync") // 0.1.18 pushes store writes on sync (spec 6.1)
	var body string
	for _, ref := range []string{refMain, storeLiveRef} {
		for _, f := range a.store.FilesMatching(ref, memID) {
			body, _ = a.store.Read(ref, f)
		}
	}
	if body == "" {
		t.Fatal("internal memory not found in the store")
	}
	a.aliceApp.Git("checkout", "-q", "-b", "careless")
	a.aliceApp.WriteFile(mainRowPath("", tblMemories, memID), body)
	a.aliceApp.Git("add", "-A")
	a.aliceApp.Git("commit", "-q", "--no-verify", "-m", "oops")
	a.aliceApp.Git("push", "-q", "--no-verify", "-u", "origin", "careless")
	a.acmeApp.Git("fetch", "-q", "origin", "careless")
	a.acmeApp.Git("checkout", "-q", "FETCH_HEAD")
	r := a.acmeApp.With("CI=1").Lore("check", "leaks", "--ci")
	if r.Code == 0 {
		t.Fatalf("CI mode must fail on a foreign id: %s", r)
	}
	r.Never(t, orgNorthwind, memID, internalNote, slugStore)
}

// F16 fail closed: the commit check cannot read the store and has no cached
// index → the commit is blocked with "cannot check".
func TestF16_UnreadableStoreBlocksTheCommit(t *testing.T) {
	a := newAgency(t)
	a.aliceApp.MustLore("memory", "add", "--body", internalNote)
	a.alice.RemoveStoreClones(t)
	if err := os.RemoveAll(a.alice.LorePath("cache")); err != nil {
		t.Fatal(err)
	}
	a.alice.Offline()
	a.aliceApp.WriteFile("src/a.ts", "export {}\n")
	a.aliceApp.Git("add", "src/a.ts")
	if r := a.aliceApp.TryGit("commit", "-m", "innocent"); r.Code == 0 || !strings.Contains(r.Out(), "cannot check") {
		t.Fatalf("the hook must fail closed when it cannot decide: %s", r.Out())
	}
}

// F16 at the live write: internal text sent to the client's live ref is
// refused before the push (E_LEAK_BLOCKED).
func TestF16_LiveWriteToClientRefIsChecked(t *testing.T) {
	a := newAgency(t, th.Flag("to", "task", "add"))
	a.aliceApp.MustLore("memory", "add", "--body", internalNote)
	a.aliceApp.Lore("task", "add", "follow up: "+internalNote, "--to", "repo", "--tasklist", a.tl).Fails(t, th.ExitGuard, "E_LEAK_BLOCKED")
	if f := a.clientSees(t, "send the estimate before the call"); len(f) != 0 {
		t.Fatalf("a blocked live write reached the client: %v", f)
	}
}

// F16 / §6.6: agents run `lore check pr-body` before opening a PR or posting
// a comment (QUESTION Q7: the body is passed as a file argument).
func TestF16_PRBodyCheck(t *testing.T) {
	a := newAgency(t, th.Cmd("check", "pr-body"))
	a.aliceApp.MustLore("memory", "add", "--body", internalNote)
	a.aliceApp.WriteFile(".git/pr-ok.md", "Fixes checkout rounding.\n")
	a.aliceApp.MustLore("check", "pr-body", ".git/pr-ok.md")
	a.aliceApp.WriteFile(".git/pr-bad.md", "Note: "+internalNote+"\n")
	a.aliceApp.Lore("check", "pr-body", ".git/pr-bad.md").Fails(t, th.ExitGuard, "E_LEAK_BLOCKED")
}

// Store addresses in output: without a TTY or with any CI marker set,
// whoami names the org's store url and the link's folder as a terminal
// does (a store's plain address is no secret; it printed "<redacted>",
// which told the person nothing). Only a credential in a url is masked
// (logic/repourl TestMask).
func TestLeak_StoreAddressShownWithoutATTY(t *testing.T) {
	a := newAgency(t)
	for _, marker := range th.CIMarkers {
		a.aliceApp.With(marker+"=1").MustLore("whoami").Says(t, "northwind/lore").Never(t, "<redacted>")
	}
}

// §6.6 untrusted hosts: doctor warns when stores are configured on a machine
// whose hostname matches untrusted_hosts.
func TestLeak_UntrustedHostWarns(t *testing.T) {
	a := newAgency(t, th.Cmd("doctor"))
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	cfg := aliceConfig()
	cfg.UntrustedHosts = []string{host}
	a.alice.WriteConfig(cfg)
	a.aliceApp.Lore("doctor").Says(t, "untrusted")
}

// §6.5 commit metadata: live, signals and landing commits use fixed messages
// and the org identity, never the person's personal git email.
func TestLeak_LoreCommitsAreAuthoredByTheOrgIdentity(t *testing.T) {
	a := newAgency(t, th.Flag("to", "task", "add"))
	dev9 := a.w.Human("dev9", "dev9@gmail.example")
	dev9.WriteConfig(th.Config{Orgs: []th.Org{northwindOrg("human:dev9@northwind.example", "Dev Nine", "dev9")}, Live: th.FreshFetch()})
	c := dev9.Clone(a.app, "app")
	c.MustLore("actor", "register")
	id := mustID(t, c.MustLore("task", "add", "internal chore", "--to", orgNorthwind), "tsk")
	c.MustLore("task", "done", id)
	const noteText = "the vendor's rate card is under review"
	memID := mustID(t, c.MustLore("memory", "add", "--body", noteText, "--to", orgNorthwind), "mem")
	c.MustLore("sync")
	sawNote := false
	for _, ref := range []string{refMain, storeLiveRef} {
		for _, au := range a.store.Authors(ref) {
			if strings.Contains(au, "gmail.example") {
				t.Fatalf("%s carries a personal email: %s", ref, au)
			}
		}
		if !a.store.Has(ref) {
			continue
		}
		// A commit title names the command and, for a kind with a short
		// title (a task), that title; a note's text never goes into any
		// commit message (docs/team/identity-and-stores.md, store commits).
		for _, s := range strings.Split(a.w.Git(a.store.Dir, "log", "--format=%B", ref), "\n") {
			if strings.Contains(s, noteText) {
				t.Fatalf("a note's text is in a lore commit message on %s: %q", ref, s)
			}
			if strings.Contains(s, "internal chore") && !strings.HasPrefix(s, "lore: task ") {
				t.Fatalf("a task title appears outside its own commit title on %s: %q", ref, s)
			}
			sawNote = sawNote || strings.Contains(s, memID)
		}
	}
	if !sawNote {
		t.Fatalf("the note's commit (%s) is not on the store: the text check above read nothing", memID)
	}
}

// §6.5 secrets: the extended scanner refuses a connection string with a
// password in an internal note too.
func TestLeak_SecretInAnInternalNoteIsRefused(t *testing.T) {
	a := newAgency(t)
	// built at run time (convention 8) so the repo's own leak hook does not
	// read the fixture as a real connection string
	secret := "postgres://admin:" + "hunter2pass" + "@db.internal:5432/app"
	a.aliceApp.Lore("memory", "add", "--body", "prod db is "+secret).Says(t, "secret")
	if f := a.store.AllObjectsContain("hunter2pass"); len(f) != 0 {
		t.Fatal("a secret reached the store")
	}
}

// ---------------------------------------------------------------- F20 render

// F20 / §6.2 / E11 last clause: after `lore render` the checkout holds no
// internal or personal text anywhere (tracked, ignored, .git, objects);
// CLAUDE.local.md is one import line excluded through .git/info/exclude.
func TestF20_RenderKeepsInternalContextOutsideTheCheckout(t *testing.T) {
	a := newAgency(t, th.Flag("to", "memory", "add"), th.Flag("to", "rule", "add"))
	memID := mustID(t, a.aliceApp.MustLore("memory", "add", "--body", internalNote), "mem")
	a.aliceApp.MustLore("memory", "add", "--body", "I forget the staging VPN", "--to", "me")
	a.aliceApp.MustLore("rule", "add", "--body", "money is stored in minor units", "--to", "repo")
	// P2-CTX-1 (FQ13): the private import file is the target profile's
	// private_file, written only for the repo's context.targets; claude_code's
	// is CLAUDE.local.md (a codex-only repo gets none and reads internal
	// context through lore context + hooks). Q-R2-3: the policy row names them.
	a.aliceApp.MustLore("policy", "set", "context.targets", "claude_code", "--to", "repo", "--yes")
	a.aliceApp.MustLore("render")
	pid := repoID(t, a.aliceApp)

	// CONFIG_GUIDE CFG6 / STORE_SERVER_SPEC 7.3 (privrender 307e9f4): one
	// private render file per internal store, ~/.lore/render/<repo>-<repo_id>/<store>-<store_id>.md,
	// and CLAUDE.local.md is exactly one import line per file (v0.1.17
	// wrote one ~/.lore/render/<project-id>.md and one line).
	local, ok := a.aliceApp.ReadFile("CLAUDE.local.md")
	// one import per paragraph: a blank line between them (never a
	// hard-wrapped paragraph)
	var imports []string
	for _, l := range strings.Split(strings.TrimSpace(local), "\n") {
		if strings.TrimSpace(l) != "" {
			imports = append(imports, l)
		}
	}
	if !ok || len(imports) < 2 {
		t.Fatalf("CLAUDE.local.md must hold one import line per internal store (org, me): %q", local)
	}
	var renders []string
	for _, l := range imports {
		// B-17: the folder and file names say which repo and store, ids last
		path, isImport := strings.CutPrefix(l, "@~/.lore/render/")
		dir, rel, _ := strings.Cut(path, "/")
		if !isImport || (dir != pid && !strings.HasSuffix(dir, "-"+pid)) || !strings.Contains(rel, "sto_") || !strings.HasSuffix(rel, ".md") || strings.Contains(rel, "/") {
			t.Fatalf("CLAUDE.local.md must be exactly the import lines: %q", local)
		}
		renders = append(renders, a.alice.LorePath("render", dir, rel))
	}
	if out := a.aliceApp.Git("ls-files", "CLAUDE.local.md"); out != "" {
		t.Fatal("CLAUDE.local.md must never be tracked")
	}
	if ex, _ := a.aliceApp.ReadFile(".git/info/exclude"); !strings.Contains(ex, "CLAUDE.local.md") {
		t.Fatal("excluded through .git/info/exclude")
	}
	if gi, _ := a.aliceApp.ReadFile(".gitignore"); strings.Contains(gi, "CLAUDE.local.md") {
		t.Fatal("never through the client's .gitignore")
	}
	var render string
	for _, p := range renders {
		b, err := os.ReadFile(filepath.Clean(p))
		if err != nil {
			t.Fatalf("internal render: %v", err)
		}
		if first, _, _ := strings.Cut(string(b), "\n"); !strings.Contains(first, "never quote") {
			t.Fatalf("internal render %s must start with the never-quote line: %s", p, b)
		}
		render += string(b)
	}
	if !strings.Contains(render, internalNote) || !strings.Contains(render, "I forget the staging VPN") {
		t.Fatalf("internal render content: %s", render)
	}
	if md, _ := a.aliceApp.ReadFile(".lore/LORE.md"); !strings.Contains(md, "money is stored in minor units") {
		t.Fatal("the committed LORE.md keeps the repo's own rows")
	}
	if f := a.aliceApp.LeakScan(internalNote, "I forget the staging VPN", memID, "northwind/lore"); len(f) != 0 {
		t.Fatalf("internal text inside the client checkout: %v", f)
	}
}

// F20: lore refuses to write CLAUDE.local.md when it is a symlink or would
// be tracked.
func TestF20_RenderRefusesASymlinkOrTrackedClaudeLocal(t *testing.T) {
	a := newAgency(t)
	a.aliceApp.MustLore("memory", "add", "--body", internalNote)
	// P2-CTX-1 (FQ13): CLAUDE.local.md is claude_code's private_file and is
	// written only when the repo's context.targets name claude_code (Q-R2-3:
	// the policy row); v0.1.17 wrote it on every render.
	a.aliceApp.MustLore("policy", "set", "context.targets", "claude_code", "--to", "repo", "--yes")
	target := filepath.Join(a.aliceApp.Dir, "docs", "leak.md")
	a.aliceApp.WriteFile("docs/leak.md", "public\n")
	if err := os.Symlink(target, filepath.Join(a.aliceApp.Dir, "CLAUDE.local.md")); err != nil {
		t.Fatal(err)
	}
	a.aliceApp.Lore("render").Refused(t, "symlink")
	if body, _ := a.aliceApp.ReadFile("docs/leak.md"); body != "public\n" {
		t.Fatal("render must never write through the symlink")
	}
	if err := os.Remove(filepath.Join(a.aliceApp.Dir, "CLAUDE.local.md")); err != nil {
		t.Fatal(err)
	}
	a.aliceApp.WriteFile("CLAUDE.local.md", "tracked\n")
	a.aliceApp.Git("add", "-f", "CLAUDE.local.md")
	a.aliceApp.CommitAll("tracked by mistake")
	a.aliceApp.Lore("render").Refused(t, "CLAUDE.local.md")
}

// F20 meta key: switching the org entry drops the old entry's rows from what
// commands read.
func TestF20_SwitchingOrgDropsTheOldEntrysRows(t *testing.T) {
	a := newAgency(t, th.Cmd("org", "use"))
	a.aliceApp.MustLore("memory", "add", "--body", internalNote)
	a.aliceApp.MustLore("search", "estimate").Says(t, "send the estimate")
	a.aliceApp.MustLore("org", "use", orgStaging)
	a.aliceApp.Lore("search", "estimate").Never(t, "send the estimate")
}

// ---------------------------------------------------------------- cookbook 3, 8, 10, 12, 13

// Cookbook 3: the agent sees everything, the client repo holds only the rule.
func TestRecipe03_ClientProjectWithInternalNotes(t *testing.T) {
	a := newAgency(t, th.Flag("to", "rule", "add"))
	a.aliceApp.MustLore("memory", "add", "--body", "they approve faster on WhatsApp")
	a.aliceApp.MustLore("rule", "add", "--body", "money is stored in minor units", "--to", "repo")
	a.aliceApp.MustLore("render")
	a.aliceApp.CommitAll("rule")
	a.aliceApp.Git("push", "-q", "origin", "main")
	a.acmeApp.Git("pull", "-q")
	a.acmeApp.MustLore("rule", "list").Says(t, "money is stored in minor units")
	if f := a.clientSees(t, "they approve faster on WhatsApp"); len(f) != 0 {
		t.Fatalf("the internal memory reached the client: %v", f)
	}
}

// Cookbook 8: the client's own developers see client rows only; Northwind's
// store, links and claims do not exist for them.
func TestRecipe08_ClientDevelopersSeeOnlyClientRows(t *testing.T) {
	a := newAgency(t, th.Flag("to", "task", "add"))
	a.aliceApp.MustLore("task", "add", "visible to acme", "--to", "repo", "--tasklist", a.tl)
	a.aliceApp.MustLore("task", "add", "internal only", "--to", orgNorthwind)
	titles := listedTitles(t, a.acmeApp)
	if !hasItem(titles, "visible to acme") || hasItem(titles, "internal only") {
		t.Fatalf("client view: %v", titles)
	}
	a.acmeApp.MustLore("whoami").Says(t, "none").Never(t, orgNorthwind)
	if got := a.acmedev.StoreClones(t); len(got) != 0 {
		t.Fatalf("the client's machine never clones a store, found %v", got)
	}
}

// Cookbook 10: a note grows private → team → company. BATCH 2 (owner):
// widening a note's audience is `lore move`, which keeps the id (one home).
// Pinned: an unconfirmed move without a terminal writes nothing; each
// confirmed move lands the row in the next audience's folder under the same
// id and leaves only a tombstone behind.
func TestRecipe10_MoveChainKeepsTheId(t *testing.T) {
	a := newAgency(t, th.Cmd("move"), th.Flag("to", "memory", "add"))
	const text = "prune the pnpm store before the Docker build"
	mine := mustID(t, a.aliceApp.MustLore("memory", "add", "--body", text, "--to", "me"), "mem")
	a.aliceApp.Lore("move", mine, "--to", orgNorthwind).Refused(t)
	a.aliceApp.MustLore("sync") // C5: store writes reach the remote with the writer's sync
	if a.storeHas(t, folderAcme, text) {
		t.Fatal("a refused move writes nothing")
	}
	a.aliceApp.MustLore("move", mine, "--to", orgNorthwind, "--yes")
	a.aliceApp.MustLore("sync") // C5: store writes reach the remote with the writer's sync
	if !a.storeHas(t, folderAcme, text) || !a.storeHas(t, folderAcme, mine) {
		t.Fatal("the team move lands the row, same id, in the linked project")
	}
	a.aliceApp.MustLore("move", mine, "--to", orgNorthwind+"/"+folderOrg, "--yes")
	a.aliceApp.MustLore("sync") // C5: store writes reach the remote with the writer's sync
	if !a.storeHas(t, folderOrg, text) || a.storeHas(t, folderAcme, text) {
		t.Fatal("the company move lands it in the org folder and leaves no live copy in acme/")
	}
}

// Cookbook 12: an internal task assigned from a client repo lives only in the
// store; dev1 sees it labelled, finishes it, it lands on the store branch;
// the client repo gains nothing a client can fetch.
func TestRecipe12_InternalTasksNeverReachClient(t *testing.T) {
	a := newAgency(t, capLand, th.Flag("to", "task", "add"), th.Flag("assign", "task", "add"), th.Flag("ref", "task", "add"))
	clientTask := mustID(t, a.acmeApp.MustLore("task", "add", "checkout redesign", "--tasklist", a.tl), "tsk")
	a.acmeApp.MustLore("sync")
	a.aliceApp.Git("checkout", "-q", "-b", "feat/checkout")
	appRefs := strings.Join(a.app.Refs(""), ",") + a.app.Rev(refMain) + a.app.Rev(refLive)
	r := a.aliceApp.MustLore("task", "add", "send invoice reminder before demo", "--to", orgNorthwind, "--assign", "@dev1", "--ref", clientTask, "--ref", "commit:a1b2c3")
	id := mustID(t, r, "tsk")
	// A row written into a store is committed on the store branch and sent
	// with alice's lore sync (C5; STORE_SERVER_SPEC 6.1: a store clone has
	// no live ref in 0.1.18), so dev1's clone can see it.
	a.aliceApp.MustLore("sync")
	if row, ok := a.storeMainRow(t, folderAcme, tblTasks, id); !ok || row.Status == statusDone {
		t.Fatalf("the open task must be on the store branch after the push: %v", a.store.Files(refMain))
	}
	a.dev1App.Git("checkout", "-q", "-b", "feat/login")
	a.dev1App.MustLore("task", "list").Says(t, "["+orgNorthwind+"/"+folderAcme+"]", "send invoice reminder before demo")
	a.dev1App.MustLore("task", "done", id)
	a.dev1App.MustLore("sync") // C5: dev1's done reaches the store with his sync
	if r, ok := a.storeMainRow(t, folderAcme, tblTasks, id); !ok || r.Status != statusDone {
		t.Fatal("done lands on the store branch in acme")
	}
	if now := strings.Join(a.app.Refs(""), ",") + a.app.Rev(refMain) + a.app.Rev(refLive); now != appRefs {
		t.Fatal("the client repo must gain nothing")
	}
	if f := a.clientSees(t, "send invoice reminder", id); len(f) != 0 {
		t.Fatalf("the client can fetch the internal task: %v", f)
	}
}

// Cookbook 13 / Q-a / TEAM-011: a staging lore on another store branch never
// mixes with the main one, and lore never moves its rows to main.
//
// Q8 (coordinator, changed): `lore org link` never creates a missing store
// branch, because a typo in the branch name would silently start a new lore;
// it refuses with a hint, and `lore store init --name <url> --branch staging`
// creates the branch explicitly as a parentless commit (C2).
func TestRecipe13_StagingLoreIsSeparate(t *testing.T) {
	a := newAgency(t, th.Cmd("org", "use"), th.Flag("to", "task", "add"), th.Cmd("store", "init"), th.Flag("from", "task", "done"))
	stagingHead := "refs/heads/" + stagingBranch
	a.aliceApp.MustLore("org", "use", orgStaging)
	a.aliceApp.Lore("org", "link", orgStaging, "--project", folderAcme, "--yes").Refused(t, stagingBranch, "lore store init")
	if a.store.Has(stagingHead) {
		t.Fatal("a refused link must not create the store branch")
	}
	initStore(a.w, a.alice, "northwind_staging", "https://"+slugStore, stagingBranch, "--yes").OK(t)
	if !a.store.Has(stagingHead) || a.store.Parents(stagingHead) != 0 || a.store.CommitCount(stagingHead) != 1 {
		t.Fatalf("store init --branch makes one parentless commit: parents=%d commits=%d", a.store.Parents(stagingHead), a.store.CommitCount(stagingHead))
	}
	a.aliceApp.MustLore("org", "link", orgStaging, "--project", folderAcme, "--yes")
	id := mustID(t, a.aliceApp.MustLore("task", "add", "seed staging with demo data", "--to", orgStaging), "tsk")
	a.aliceApp.MustLore("sync") // C5: store writes reach the remote with the writer's sync
	// Ported to STORE_SERVER_SPEC 6.1 (LORE_TEAM_SPEC 3.2): a store clone
	// has no live ref in 0.1.18, so the open row is committed on the staging
	// store branch (was refs/lore/live/staging).
	if _, ok := readRow(t, a.store, stagingHead, storeRowPath(folderAcme, tblTasks, id)); !ok {
		t.Fatalf("the row lives on the staging store branch: %v", a.store.Files(stagingHead))
	}
	if a.storeHas(t, folderAcme, "seed staging with demo data") {
		t.Fatal("the main store branch and its live ref never get a staging row")
	}
	if hasItem(listedTitles(t, a.dev1App), "seed staging with demo data") {
		t.Fatal("people on the main entry never see staging rows")
	}
	a.aliceApp.MustLore("org", "use", orgNorthwind)
	a.aliceApp.MustLore("task", "done", id, "--from", orgStaging+"/"+folderAcme)
	a.aliceApp.MustLore("sync") // C5: store writes reach the remote with the writer's sync
	if a.storeHas(t, folderAcme, "seed staging with demo data") {
		t.Fatal("lore never moves rows between store branches")
	}
}

// ---------------------------------------------------------------- side effects 20, 32

// Side effect 20: a bare store clone holding unpushed work cannot be
// forgotten; once pushed it can.
func TestSideEffect20_StoreForgetRefusesUnpushedWork(t *testing.T) {
	a := newAgency(t, th.Cmd("store", "forget"))
	a.alice.Offline()
	a.aliceApp.MustLore("memory", "add", "--body", "written while offline")
	a.alice.Lore("", "store", "forget", "https://"+slugStore).Refused(t, "unpushed")
	a.alice.Online()
	a.aliceApp.MustLore("sync")
	if !a.storeHas(t, folderAcme, "written while offline") {
		t.Fatal("the offline store write must be pushed on reconnect")
	}
	a.alice.Lore("", "store", "forget", "https://"+slugStore).OK(t)
}

// Side effect 32: several lore processes sharing one bare store clone at the
// same time all succeed and lose nothing.
func TestSideEffect32_Race_ProcessesShareOneBareClone(t *testing.T) {
	a := newAgency(t)
	second := a.alice.Clone(a.app, "app-2")
	res := a.w.Simultaneously(
		a.aliceApp.Prepare("memory", "add", "--body", "first writer"),
		second.Prepare("memory", "add", "--body", "second writer"),
		a.dev1App.Prepare("memory", "list"),
	)
	for _, r := range res {
		r.OK(t)
	}
	a.aliceApp.MustLore("sync") // C5: store writes reach the remote with the writer's sync
	for _, s := range []string{"first writer", "second writer"} {
		if !a.storeHas(t, folderAcme, s) {
			t.Fatalf("%q lost with a shared clone", s)
		}
	}
}

// ---------------------------------------------------------------- side effects 6, 17, 36, 40

// Side effect 6: linking flips the client guards for the whole team, so it
// always asks; without a TTY and without --yes nothing is written.
func TestSideEffect06_LinkAsksForConfirmation(t *testing.T) {
	a := newAgency(t)
	web := a.w.Remote("github.com/acme/web")
	boot := a.acmedev.Clone(web, "web")
	boot.MustLore("init", "--non-interactive", "--name=web")
	boot.CommitAll("lore")
	boot.AdminPush("origin", "main")
	before := a.store.Rev(refMain)
	a.alice.Clone(web, "web").Lore("org", "link", orgNorthwind, "--project", folderAcme).Refused(t, "CLIENT")
	if a.store.Rev(refMain) != before {
		t.Fatal("an unconfirmed link must write nothing")
	}
}

// Side effect 17: a link is checked before patterns (F10 step 4 before 5), so
// a second entry whose repos: pattern also matches does not make it ambiguous.
func TestSideEffect17_LinkBeatsPatterns(t *testing.T) {
	a := newAgency(t)
	cfg := aliceConfig()
	cfg.Orgs[2].Repos = []string{"github.com/acme/*"}
	a.alice.WriteConfig(cfg)
	a.aliceApp.MustLore("whoami").Says(t, orgNorthwind, "link").Never(t, "E_ORG_AMBIGUOUS", aliceBetaKey)
}

// Side effect 36: `_live/purged.json` entries expire after 30 days; a clone
// idle longer that pushes the purged row back is detected by doctor.
func TestSideEffect36_DoctorDetectsAReaddedPurgedID(t *testing.T) {
	rt := newRepoTeam(t, th.Cmd("live", "purge"), th.Cmd("doctor"))
	rt.dev1.SetNow(t0)
	rt.dev2.SetNow(t0)
	id := rt.addTask(t, rt.c1, "leaked then purged")
	rt.c2.MustLore("task", "list")
	rt.dev2.Offline()
	rt.c2.MustLore("task", "edit", id, "--priority", "high")
	rt.c1.MustLore("live", "purge", id, "--confirm")
	if purged, _ := rt.app.Read(refLive, livePurgedPath); !strings.Contains(purged, id) {
		t.Fatalf("control: the purge must be recorded: %q", purged)
	}
	rt.dev1.SetNow(t0.Add(31 * 24 * time.Hour))
	rt.dev2.SetNow(t0.Add(31 * 24 * time.Hour))
	rt.c1.MustLore("sync")
	rt.dev2.Online()
	rt.c2.Lore("task", "list")
	if _, ok := rt.liveTask(t, id); ok {
		rt.c1.Lore("doctor").Says(t, id, "purged")
	}
}

// Side effect 40, FLAG3 (owner decision, round 2): `lore cache prune`
// removes the cache files of stores no repo on this machine attaches any
// more (0.1.18 layout ~/.lore/cache/stores/<store id>/<branch>/<folder>.db,
// replacing v0.1.17's 90-day idle rule for ~/.lore/cache/<project-id>.db),
// lists them and asks first: --dry-run only lists, no terminal and no --yes
// is E_NEEDS_CONFIRM, --yes deletes. It never deletes anything outside the
// cache folder (the store's clone and alice's render folder stay).
func TestSideEffect40_CachePruneRemovesOldProjects(t *testing.T) {
	a := newAgency(t, th.Cmd("cache", "prune"))
	a.aliceApp.MustLore("render")
	caches := storeCacheFiles(t, a.alice)
	if len(caches) == 0 {
		t.Fatalf("alice's acme/app attaches the northwind store: its cache must live under %s", a.alice.LorePath("cache", "stores"))
	}
	a.alice.Lore("", "cache", "prune", "--yes").OK(t)
	for _, f := range caches {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("a cache a repo here still attaches must stay: %s: %v", f, err)
		}
	}
	// alice's checkout goes away: no repo on this machine attaches the
	// store any more.
	if err := os.RemoveAll(a.aliceApp.Dir); err != nil {
		t.Fatal(err)
	}
	listed := prunedListing(a.alice.Lore("", "cache", "prune", "--dry-run").OK(t).Stdout, a.alice.LorePath("cache"))
	if len(listed) == 0 {
		t.Fatal("--dry-run must list the caches of the stores only alice's removed checkout attached")
	}
	// Q-R2-5: the old commands exit their code's core class exit, so the
	// unconfirmed prune with no terminal exits 26 (E_NEEDS_CONFIRM).
	a.alice.Lore("", "cache", "prune").Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", "--yes")
	for _, f := range caches {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("--dry-run and an unconfirmed prune delete nothing: %s: %v", f, err)
		}
	}
	a.alice.Lore("", "cache", "prune", "--yes").OK(t)
	gone := map[string]bool{}
	for _, f := range listed {
		gone[f] = true
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Fatalf("a cache no repo here attaches must be pruned: %s", f)
		}
	}
	for _, f := range caches {
		if _, err := os.Stat(f); err != nil && !gone[f] {
			t.Fatalf("a cache prune did not list must stay (another repo here attaches it): %s", f)
		}
	}
	a.alice.Lore("", "cache", "prune", "--dry-run").OK(t).Says(t, "nothing to prune")
	if _, err := os.Stat(a.alice.LorePath("render")); err != nil {
		t.Fatalf("prune deletes only inside ~/.lore/cache; the render folder stays: %v", err)
	}
}

// prunedListing is the cache file paths a `lore cache prune` listing names
// (one indented "<path>  <size>" line each, every path under cacheDir).
func prunedListing(out, cacheDir string) []string {
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && strings.HasPrefix(f[0], cacheDir+string(filepath.Separator)) {
			paths = append(paths, f[0])
		}
	}
	return paths
}

// storeCacheFiles lists actor's 0.1.18 store cache files
// (~/.lore/cache/stores/**/*.db), sorted.
func storeCacheFiles(t *testing.T, actor *th.Actor) []string {
	t.Helper()
	var out []string
	root := actor.LorePath("cache", "stores")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && p == root {
				return fs.SkipDir
			}
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".db") {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// Open bugs pinned by a regression test before lore-stores' fix lands
// (coordinator 18:40). Open is false once the fix is merged, so a regression fails.
var (
	bugD2ActorCopyStableKey = th.Bug{ID: "BUG-D2", Owner: "lore-stores"} // fixed in lore-stores 5013e22; passes with LORE_TEAM_STRICT=1 on lore-core
)

// clientActorFields is all a client repo's actor file may hold (F18 client
// copy: {id, display name, kind}, plus the engine's file bookkeeping).
var clientActorFields = map[string]bool{"_table": true, "_v": true, "id": true, "display_name": true, "kind": true, "created_at": true, "updated_at": true}

// clientActorEnvelopeOK reports whether a 0.1.18 row-envelope key (model/row,
// STORE_SERVER_SPEC 4.1: every v2 row file carries them) may sit in a client
// actor copy: bookkeeping only, naming nothing internal. It mirrors the leak
// guard (leakcheck envelopeKeyAllowed): _hlc text; created_by/updated_by an
// act_ id; _o {store: "repo"} plus only cmd, dir, repo (text) and git
// {state, commit, branch} (PROV8); _w {v} only, never the machine id m
// (Q-R3-3).
func clientActorEnvelopeOK(k string, raw json.RawMessage) bool {
	switch k {
	case "_hlc":
		var s string
		return json.Unmarshal(raw, &s) == nil
	case "created_by", "updated_by":
		var s string
		return json.Unmarshal(raw, &s) == nil && actorIDOnlyRe.MatchString(s)
	case "_o":
		var o map[string]json.RawMessage
		if json.Unmarshal(raw, &o) != nil || string(o["store"]) != `"repo"` {
			return false
		}
		for ok, ov := range o {
			switch ok {
			case "store":
			case "cmd", "dir", "repo":
				var s string
				if json.Unmarshal(ov, &s) != nil {
					return false
				}
			case "git":
				if !textMembersOnly(ov, "state", "commit", "branch") {
					return false
				}
			default:
				return false
			}
		}
		return true
	case "_w":
		return textMembersOnly(raw, "v")
	}
	return false
}

// textMembersOnly reports whether raw is an object whose every member is
// one of keys and holds text.
func textMembersOnly(raw json.RawMessage, keys ...string) bool {
	var o map[string]json.RawMessage
	if json.Unmarshal(raw, &o) != nil {
		return false
	}
	for k, v := range o {
		var s string
		if !slices.Contains(keys, k) || json.Unmarshal(v, &s) != nil {
			return false
		}
	}
	return true
}

// actorIDOnlyRe is a bare act_ id.
var actorIDOnlyRe = regexp.MustCompile(`^act_[0-9a-f]{32}$`)

// clientRepoRow reads a row the client repo acme/app holds in alice's
// checkout (.lore/data) or, failing that, on the app's live ref.
func (a *agency) clientRepoRow(t *testing.T, table, id string) (string, bool) {
	t.Helper()
	if body, ok := a.aliceApp.ReadFile(mainRowPath("", table, id)); ok {
		return body, true
	}
	return a.app.Read(refLive, liveRowPath(rootFolder, table, id))
}

// BUG-D1 / §6.4, after BATCH 2 (owner: a row has one home and `lore move`
// hands it over keeping its id): the row lands in the client repo's
// .lore/data under the same id with its text and no origin field, and the
// store keeps only a tombstone (no second live copy anywhere).
func TestLeak_MoveInternalMemoryToClientRepoKeepsIdNoOrigin(t *testing.T) {
	a := newAgency(t, th.Cmd("move"))
	const text = "checkout must support INR and USD"
	internal := mustID(t, a.aliceApp.MustLore("memory", "add", "--body", text), "mem")
	a.aliceApp.MustLore("sync") // C5: store writes reach the remote with the writer's sync
	if !a.storeHas(t, folderAcme, internal) {
		t.Fatal("the memory must start in the store's acme/ project")
	}
	a.aliceApp.MustLore("move", internal, "--to", "repo", "--yes")
	body, ok := a.clientRepoRow(t, tblMemories, internal)
	if !ok || !strings.Contains(body, text) {
		t.Fatalf("the moved row keeps its id and text in the client repo: %v %s", ok, body)
	}
	for _, field := range []string{"origin_id", "source_ref", "supersedes", "superseded_by", "shared_as"} {
		if strings.Contains(body, `"`+field+`"`) {
			t.Fatalf("the moved row carries no origin field %q: %s", field, body)
		}
	}
	a.aliceApp.MustLore("sync") // C5: store writes reach the remote with the writer's sync
	if a.storeHas(t, folderAcme, text) {
		t.Fatal("the store keeps no live copy of the moved row (one home)")
	}
	if !a.storeHas(t, folderAcme, `"_deleted": true`) {
		t.Fatal("the store keeps the moved row's tombstone")
	}
}

// BUG-D2 / F18: when an org member writes a row --to repo in a client repo,
// the actor file exported beside it holds only {id, display name, kind} — no
// stable_key, no email (coordinator 18:40: fixed by not copying, never by
// loosening the check). The client's own CI leak check then passes in that
// repo, and the commit check BLOCKS (not warns about) an actor copy that
// holds more.
func TestF18_ClientRepoActorCopyIsTheProjection(t *testing.T) {
	a := newAgency(t, th.Flag("to", "rule", "add"), th.Cmd("check", "leaks"), th.Flag("ci", "check", "leaks"))
	a.aliceApp.MustLore("rule", "add", "--body", "money amounts are integers in minor units", "--to", "repo")
	actors, _ := filepath.Glob(filepath.Join(a.aliceApp.Dir, dataDir, tblActors, "*.json"))
	if len(actors) == 0 {
		t.Fatal("a client row written by alice must come with alice's actor projection")
	}
	for _, f := range actors {
		raw, err := os.ReadFile(filepath.Clean(f))
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		var doc map[string]json.RawMessage
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("decode %s: %v\n%s", f, err, body)
		}
		bugD2ActorCopyStableKey.PendingIf(t, doc["stable_key"] != nil, filepath.Base(f)+" still has stable_key")
		for k := range doc {
			if !clientActorFields[k] && !clientActorEnvelopeOK(k, doc[k]) {
				t.Fatalf("an actor copy in a client repo may hold only {id, display name, kind}; %s also has %q", f, k)
			}
		}
		if strings.Contains(body, "@northwind.example") || strings.Contains(body, "@acme.example") {
			t.Fatalf("an actor copy names an email: %s", f)
		}
	}
	a.aliceApp.CommitAll("rule for the client")
	a.aliceApp.Git("push", "-q", "origin", "HEAD")
	a.acmeApp.Git("pull", "-q", "origin", "main")
	a.acmeApp.With("CI=1").Lore("check", "leaks", "--ci").OK(t)

	file := filepath.Base(actors[0])
	forged := dataDir + "/" + tblActors + "/" + file
	a.aliceApp.WriteFile(forged, `{"id":"`+strings.TrimSuffix(file, ".json")+`","display_name":"`+aliceName+`","kind":"human","stable_key":"human:alice@northwind.example"}`+"\n")
	a.aliceApp.Git("add", forged)
	if r := a.aliceApp.TryGit("commit", "-m", "touch actor"); r.Code == 0 {
		t.Fatalf("the commit check must block an actor copy holding more than {id, display name, kind}: %s", r.Out())
	}
}
