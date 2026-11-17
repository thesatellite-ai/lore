//go:build stories && !windows

package stories

// WW_writes_test.go — story WW (tests/stories/WW-writes.md): the write
// sibling of the where-you-stand matrix (W). Every operation of the registry
// that is not a pure read (add/edit/delete/move of every kind, store and org
// admin, setup steps, member and team changes) runs from every place a person
// stands. Each place gets a fresh world (writes mutate), built the same way,
// and the places run in parallel on one binary.
//
// Besides W's rules (answers or refuses with a reason naming --store and
// --setup or the repo-only class; exit code matches output), each run must
// hold:
//
//	(a) every commit it made in a store clone is authored by the org identity
//	    of the store's org, and the clone's git user.name/email equal it;
//	(b) a count of commits it prints ("sent N", "N commits") matches the
//	    commits that moved;
//	(c) two stores of the same shape (the org acme's team store and the org
//	    acmeops's team store) get the same treatment for the same op: same
//	    exit, same messages modulo names, ids and numbers;
//	(d) nothing outside the target store or repo changes (other stores,
//	    other repos, the file a link points at);
//	(e) no remote's refs move unless the op is a push or a sync.
//
// The op list comes from the real registry (wire.Registry), so a write added
// tomorrow joins the matrix the day it lands; inputs are generated from each
// op's required fields, with a small per-op table (wwOverride) where a
// generated value cannot work.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
	"github.com/thesatellite-ai/lore/wire"
)

// The second org of story WW: an ops org whose team store has the same
// shape as acme's (placeholders).
const (
	wwOpsOrg       = "acmeops"
	wwOpsSetup     = "acmeops-admin"
	wwOpsFolders   = "~/ops/**"
	wwOpsStoreSlug = "github.com/acmeops-co/lore-team"
	wwOpsAppSlug   = "github.com/acmeops-co/tools"
	wwNewStoreSlug = "github.com/acme-new/lore-team"
	wwNewOpsSlug   = "github.com/acmeops-new/lore-team"
)

// The people of story WW: one person, a personal git identity on the
// machine, and one org identity per org (the owner's shape: the clone's
// git user must be the org identity, never the personal one).
var (
	wwHome  = journeys.Person{Name: "Ada Home", Email: "ada@home.test", Login: "ada-gh"}
	wwAcme  = journeys.Person{Name: "Ada Admin", Email: "ada@acme.test", Login: "ada-gh"}
	wwOpsID = journeys.Person{Name: "Ada Ops", Email: "ada@acmeops.test", Login: "ada-gh"}
)

// wwPlace is one place of the write matrix.
type wwPlace struct {
	wPlace
	// Org is the org whose rows the place targets ("" = none: home).
	Org string
	// To is appended to an op that takes --to (home naming its store).
	To string
	// Twin is the place of the same shape in the other org ("" = none).
	Twin string
}

// wwWorld is one fresh world of story WW.
type wwWorld struct {
	s       *Story
	m       *journeys.Machine
	places  map[string]wwPlace
	remotes []*teamharness.Remote
	// clones are every git work tree of the machine, by path: the store
	// clones and the code repos (rule d watches all of them).
	clones []string
	// ident is the org identity each store clone must commit as (rule a).
	ident map[string]journeys.Person
	// owner is the org owning each clone ("" = a repo of no org).
	owner map[string]string
	// ids are rows seeded per org then noun (edit/delete targets).
	ids map[string]map[string]string
	// urls are the store urls per org (set-url, forget, push targets).
	urls, newURLs map[string]string
	outside       string
}

// wwPlaceNames lists the places, in report order.
var wwPlaceNames = []string{
	"home", "home-to-acme", "home-to-acmeops",
	"store-root", "ops-root", "store-folder", "ops-folder",
	"own-linked", "client", "lore-ignored", "claude-link-out", "v0117",
}

// wwBuild stands up the world of story WW: W's eight places plus the ops
// org with its own setup, team store and folder.
func wwBuild(t *testing.T) *wwWorld {
	t.Helper()
	s := &Story{World: journeys.NewWorld(t), ID: "WW"}
	w := &wwWorld{s: s, ident: map[string]journeys.Person{}, owner: map[string]string{},
		ids: map[string]map[string]string{wOrg: {}, wwOpsOrg: {}}, urls: map[string]string{}, newURLs: map[string]string{}}
	store := s.Remote(wStoreSlug)
	cstore := s.Remote(wCStoreSlug)
	ostore := s.Remote(wwOpsStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	tools := s.Seed(s.Remote(wwOpsAppSlug), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote(wWebSlug), func(dir string) { PlainRepo(t, dir) })
	shop := s.Seed(s.Remote(wShopSlug), func(dir string) { Monorepo(t, dir) })
	ign := s.Seed(s.Remote(wIgnSlug), func(dir string) {
		PlainRepo(t, dir)
		writeFile(t, filepath.Join(dir, ".gitignore"), ".lore/\n")
	})
	lnk := s.Seed(s.Remote(wLinkSlug), func(dir string) {
		PlainRepo(t, dir)
		if err := os.Symlink(filepath.Join("..", "..", "notes", "CLAUDE.md"), filepath.Join(dir, "CLAUDE.md")); err != nil {
			t.Fatal(err)
		}
	})
	old := s.Seed(s.Remote(wOldSlug), func(dir string) { V017Repo(t, dir, "old-app", SmallShape) })
	// where the stores move to (store set-url): empty bare remotes
	nstore := s.Remote(wwNewStoreSlug)
	nostore := s.Remote(wwNewOpsSlug)
	w.remotes = []*teamharness.Remote{store, cstore, ostore, app, tools, web, shop, ign, lnk, old, nstore, nostore}
	w.urls[wOrg], w.urls[wwOpsOrg] = store.SSHURL(), ostore.SSHURL()
	w.newURLs[wOrg], w.newURLs[wwOpsOrg] = nstore.SSHURL(), nostore.SSHURL()

	m := s.Persona(wwHome.Name, wwHome.Email).Laptop()
	w.m = m
	journeys.GitHubLogin(t, s.World, m, wwHome.Login)
	clone := func(r *teamharness.Remote, under, name string) *teamharness.Clone {
		c := m.Clone(r, name)
		m.MoveClone(t, c, filepath.Join(m.Home, under, name))
		return c
	}
	w.outside = filepath.Join(m.Home, "notes", "CLAUDE.md")
	seed := func(dir, org string) {
		for noun, args := range wwSeedRows {
			r := m.Lore(dir, append([]string{noun, "add", "--json", "--to", org + "/" + wFolder, "--yes"}, args...)...)
			if r.Code != 0 {
				t.Logf("seed %s in %s refused: %s", noun, org, strings.TrimSpace(r.Out()))
				continue
			}
			if id := wwJSONID(r.Stdout); id != "" {
				w.ids[org][noun] = id
			}
		}
	}

	pc := clone(app, "code", "platform")
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wwAcme, []string{wFolders})
	m.LoreIn(pc, "init").OK(t)
	m.LoreIn(pc, "store", "folder", "add", "team", wFolder).OK(t)
	seed(pc.Dir, wOrg)
	pc.Git("add", "-A")
	pc.Git("commit", "-q", "--allow-empty", "-m", "lore setup")

	tc := clone(tools, "ops", "tools")
	journeys.StartTeam(t, m, tc.Dir, wwOpsSetup, wwOpsOrg, ostore.SSHURL(), wwOpsID, []string{wwOpsFolders})
	m.LoreIn(tc, "init").OK(t)
	m.LoreIn(tc, "store", "folder", "add", "team", wFolder).OK(t)
	seed(tc.Dir, wwOpsOrg)
	tc.Git("add", "-A")
	tc.Git("commit", "-q", "--allow-empty", "-m", "lore setup")

	wc := clone(web, "code", "web")
	m.LoreIn(wc, "org", "link", "--org", wOrg, "--project", wFolder, "--yes").OK(t)
	sc := clone(shop, "code", "shop")
	m.LoreIn(pc, "org", "store", "add", "--org", wOrg, "--url", cstore.SSHURL(), "--yes").OK(t)
	m.LoreIn(sc, "org", "link", "--org", wOrg, "--project", wClientProj, "--store", cstore.SSHURL(), "--yes").OK(t)
	ic := clone(ign, "code", "private-notes")
	m.LoreIn(ic, "init").OK(t)
	if err := os.MkdirAll(filepath.Dir(w.outside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.outside, []byte("# my notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lc := clone(lnk, "code", "linked-claude")
	m.LoreIn(lc, "init").OK(t)
	oc := clone(old, "code", "old-app")
	for _, c := range []*teamharness.Clone{wc, sc, ic, lc} {
		c.Git("add", "-A")
		c.Git("commit", "-q", "--allow-empty", "-m", "setup")
	}
	m.LoreIn(pc, "sync").OK(t)
	m.LoreIn(tc, "sync").OK(t)
	// the owner moved both stores to another GitHub org (a transfer: the
	// new remote holds the store); store set-url then points the clone at it
	s.Git(store.Dir, "push", "-q", "--mirror", nstore.Dir)
	s.Git(ostore.Dir, "push", "-q", "--mirror", nostore.Dir)

	root := m.LorePath("stores", "acme-co", "lore-team")
	oroot := m.LorePath("stores", "acmeops-co", "lore-team")
	croot := m.LorePath("stores", "acme-co", "lore-client-shop")
	for _, d := range []string{root, oroot, croot} {
		if _, err := os.Stat(filepath.Join(d, ".git")); err != nil {
			t.Fatalf("no store clone at %s: %v", d, err)
		}
	}
	w.ident[root], w.ident[croot], w.ident[oroot] = wwAcme, wwAcme, wwOpsID
	w.owner[root], w.owner[croot], w.owner[oroot] = wOrg, wOrg, wwOpsOrg
	for _, c := range []*teamharness.Clone{pc, wc, sc, ic, lc, oc} {
		w.owner[c.Dir] = wOrg
	}
	w.owner[tc.Dir] = wwOpsOrg
	w.owner[ic.Dir], w.owner[lc.Dir], w.owner[oc.Dir] = "", "", ""
	w.clones = []string{root, croot, oroot, pc.Dir, tc.Dir, wc.Dir, sc.Dir, ic.Dir, lc.Dir, oc.Dir}
	// where store set-url moves each team store's clone (D-515)
	nroot := m.LorePath("stores", "acme-new", "lore-team")
	noroot := m.LorePath("stores", "acmeops-new", "lore-team")
	w.ident[nroot], w.ident[noroot] = wwAcme, wwOpsID
	w.owner[nroot], w.owner[noroot] = wOrg, wwOpsOrg
	w.clones = append(w.clones, nroot, noroot)

	folder := wFindDir(t, root, wFolder)
	ofolder := wFindDir(t, oroot, wFolder)
	pl := func(name, dir, repo, org, to, twin string) wwPlace {
		return wwPlace{wPlace: wPlace{Name: name, Dir: dir, Repo: repo}, Org: org, To: to, Twin: twin}
	}
	w.places = map[string]wwPlace{
		"home":            pl("home", m.Home, "", "", "", ""),
		"home-to-acme":    pl("home-to-acme", m.Home, "", wOrg, wOrg+"/"+wFolder, "home-to-acmeops"),
		"home-to-acmeops": pl("home-to-acmeops", m.Home, "", wwOpsOrg, wwOpsOrg+"/"+wFolder, "home-to-acme"),
		"store-root":      pl("store-root", root, root, wOrg, "", "ops-root"),
		"ops-root":        pl("ops-root", oroot, oroot, wwOpsOrg, "", "store-root"),
		"store-folder":    pl("store-folder", folder, root, wOrg, "", "ops-folder"),
		"ops-folder":      pl("ops-folder", ofolder, oroot, wwOpsOrg, "", "store-folder"),
		"own-linked":      pl("own-linked", wc.Dir, wc.Dir, wOrg, "", ""),
		"client":          pl("client", sc.Dir, sc.Dir, wOrg, "", ""),
		// repos under acme's folders (~/code/**): the org writes (roster,
		// teams, folders, rows named by id) of a place there go to acme
		"lore-ignored":    pl("lore-ignored", ic.Dir, ic.Dir, wOrg, "", ""),
		"claude-link-out": pl("claude-link-out", lc.Dir, lc.Dir, wOrg, "", ""),
		"v0117":           pl("v0117", oc.Dir, oc.Dir, wOrg, "", ""),
	}
	cl := w.places["claude-link-out"]
	cl.Outside = w.outside
	w.places["claude-link-out"] = cl
	return w
}

// wwSeedRows are the rows seeded in each org's folder so an edit or delete
// has a target (noun -> add arguments).
var wwSeedRows = map[string][]string{
	"memory":   {"--body", "matrix memory"},
	"task":     {"--title", "matrix task"},
	"decision": {"--title", "matrix decision", "--body", "matrix why"},
	"rule":     {"--body", "matrix rule"},
	"pattern":  {"--title", "matrix pattern", "--body", "matrix body"},
	"playbook": {"--title", "matrix playbook", "--body", "matrix body"},
	"prompt":   {"--title", "matrix prompt", "--body", "matrix body"},
	"hotfix":   {"--title", "matrix hotfix", "--body", "matrix body"},
	"incident": {"--title", "matrix incident", "--body", "matrix body"},
	"snapshot": {"--title", "matrix snapshot", "--body", "matrix state"},
	"mission":  {"--title", "matrix mission"},
	"plan":     {"--title", "matrix plan", "--body", "matrix body"},
	"tag":      {"--name", "matrix_tag"},
}

// wwJSONID is the id of an add's --json answer.
func wwJSONID(stdout string) string {
	var got struct {
		ID   string `json:"id"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		return ""
	}
	return got.ID + got.Data.ID
}

// wwOp is one write op, its CLI path and inputs, and the flags its command
// takes (probed once from --help).
type wwOp struct {
	wRead
	Yes, To, Body bool
	Rank          int
}

// wwSkip are writes the matrix does not run, each with its reason.
var wwSkip = map[string]string{
	"agent.start": "starts an agent session (a long-running child process), not a write a person types in a place",
	"upgrade.run": "replaces the lore binary from the network",
	"setup.run":   "the guided setup (a terminal flow); its steps run as setup.apply",
}

// wwPushOps may move a remote's refs: they push or sync by design (rule e).
var wwPushOps = map[string]bool{
	"store.push": true, "store.set_url": true, "store.init": true, "store.add": true,
	"land.run": true, "branch.land": true, "branch.share": true, "branch.promote": true,
	"sync.ci_merge": true, "sync.promote": true, "sync.purge": true, "sync.resolve": true,
	"sync.merge_rows": true, "sync.fix_projects": true, "sync.trash.restore": true,
	"org.create": true, "store.compact": true, "store.folder.add": true, "store.folder.remove": true,
	"team.invite": true, "team.invite.revoke": true,
	// claims, releases and holds reach the remote at once (docs/reference/
	// config.md live.autopush): a claim nobody else sees claims nothing
	"hold.claim": true, "hold.release": true, "task.claim": true, "task.release": true,
	"task.next": true, "task.heartbeat": true, "task.start": true, "task.done": true,
	"task.revoke": true,
}

// wwExits are writes whose documented non-zero exit still prints their
// report, not an error (doctor: 1 degraded, 2 broken).
var wwExits = map[string][]int{"doctor.run": {1, 2}}

// wwLast are ops that take a store, org or setup away: they run last, in
// this order, so they leave the most of the matrix standing.
var wwLast = []string{
	"setup.disable", "setup.rename", "setup.remove", "setup.trash.purge",
	"store.visibility", "store.set", "store.compact", "store.set_url", "store.push",
	"org.unlink", "org.store.remove", "org.rename",
	"store.folder.remove", "store.remove", "store.forget",
	"project.delete", "repo.delete", "migrate.rollback",
}

// wwVerbRank orders the ops of one noun: adds first (their ids feed the
// rest), deletes last.
func wwVerbRank(name string) int {
	for i, n := range wwLast {
		if n == name {
			return 100 + i
		}
	}
	v := name[strings.LastIndex(name, ".")+1:]
	switch {
	case v == "add" || v == "init" || v == "create" || v == "register":
		return 0
	case v == "delete" || v == "remove" || v == "purge" || v == "forget" || v == "rm":
		return 3
	case v == "archive" || v == "deactivate" || v == "offboard":
		return 2
	}
	return 1
}

// wwOps lists every write of the real registry, ordered for one place.
func wwOps(t *testing.T, w *wwWorld) []wwOp {
	t.Helper()
	reg, err := wire.Registry(&wire.App{})
	if err != nil {
		t.Fatal(err)
	}
	var out []wwOp
	for _, e := range reg.Entries() {
		sp := e.Spec()
		if sp.OnlyReads() || sp.Hidden {
			continue
		}
		if _, skip := wwSkip[sp.Name]; skip {
			continue
		}
		o := wwOp{wRead: wRead{Name: sp.Name, Path: op.CLIPath(sp.Name), In: e.InputFields()}, Rank: wwVerbRank(sp.Name)}
		help := w.m.Lore(w.m.Home, append(append([]string{}, o.Path...), "--help")...).Out()
		o.Yes = strings.Contains(help, "--yes ")
		o.To = strings.Contains(help, "--to string")
		o.Body = strings.Contains(help, "--body string")
		out = append(out, o)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rank/100 != out[j].Rank/100 {
			return out[i].Rank < out[j].Rank
		}
		if out[i].Rank >= 100 {
			return out[i].Rank < out[j].Rank
		}
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		return out[i].Name < out[j].Name
	})
	if len(out) < 250 {
		t.Fatalf("only %d writes in the registry; the walk is broken", len(out))
	}
	return out
}

// wwCtx is what a per-op override can name.
type wwCtx struct {
	w   *wwWorld
	p   wwPlace
	org string
	ids map[string]string
}

// wwOverride is the input of a write whose generated one cannot work.
var wwOverride = map[string]func(c wwCtx) []string{
	"config.set":         func(wwCtx) []string { return []string{"--key", "live.autopush", "--value", "false"} },
	"config.unset":       func(wwCtx) []string { return []string{"--key", "live.autopush"} },
	"repo.set_url":       func(wwCtx) []string { return []string{"--url", "git@github.com:acme-co/renamed.git"} },
	"store.push":         func(c wwCtx) []string { return []string{"--store", c.org} },
	"store.set_url":      func(c wwCtx) []string { return []string{"--name", c.org, "--url", c.w.newURLs[c.org]} },
	"store.forget":       func(c wwCtx) []string { return []string{"--url", c.w.urls[c.org]} },
	"org.link":           func(c wwCtx) []string { return []string{"--org", c.org, "--project", wFolder} },
	"org.refresh":        func(c wwCtx) []string { return []string{"--org", c.org} },
	"org.use":            func(c wwCtx) []string { return []string{"--org", c.org} },
	"org.config.set":     func(c wwCtx) []string { return []string{"--org", c.org, "--key", "matrix", "--value", "on"} },
	"member.add":         func(wwCtx) []string { return []string{"--who", "bob@example.test"} },
	"member.edit":        func(wwCtx) []string { return []string{"--who", wwAcme.Email} },
	"member.remove":      func(wwCtx) []string { return []string{"--who", "bob@example.test"} },
	"team.add":           func(wwCtx) []string { return []string{"--handle", "matrix_team"} },
	"team.member.add":    func(wwCtx) []string { return []string{"--handle", "matrix_team", "--members", "ada-gh"} },
	"team.member.remove": func(wwCtx) []string { return []string{"--handle", "matrix_team", "--members", "ada-gh"} },
	"team.rename":        func(wwCtx) []string { return []string{"--handle", "matrix_team", "--to", "matrix_crew"} },
	"team.remove":        func(wwCtx) []string { return []string{"--handle", "matrix_crew"} },
	"team.invite":        func(wwCtx) []string { return []string{"--email", "carol@example.test"} },
	"reminder.add":       func(wwCtx) []string { return []string{"--message", "matrix", "--due", "2030-01-01"} },
	"comment.add": func(c wwCtx) []string {
		return []string{"--on-id", c.ids["memory"], "--body", "matrix comment"}
	},
	"memory.add":   func(wwCtx) []string { return []string{"--body", "matrix memory"} },
	"rule.add":     func(wwCtx) []string { return []string{"--body", "matrix rule"} },
	"task.add":     func(wwCtx) []string { return []string{"--title", "matrix task"} },
	"memory.move":  func(c wwCtx) []string { return []string{"--id", c.ids["memory"], "--to", c.org + "/matrix-folder"} },
	"handoff.add":  func(wwCtx) []string { return []string{"--title", "matrix handoff"} },
	"setup.apply":  func(c wwCtx) []string { return []string{"--setup", wwSetupOf(c.org), "--step", "tools"} },
	"setup.edit":   func(c wwCtx) []string { return []string{"--name", wwSetupOf(c.org)} },
	"setup.enable": func(c wwCtx) []string { return []string{"--name", wwSetupOf(c.org)} },
	"setup.use":    func(c wwCtx) []string { return []string{"--name", wwSetupOf(c.org)} },
}

// wwSetupOf is the setup of org (acme's for a place of no org).
func wwSetupOf(org string) string {
	if org == wwOpsOrg {
		return wwOpsSetup
	}
	return wSetup
}

// wwInput is the CLI arguments of o at place c.p.
func wwInput(o wwOp, c wwCtx, prBody string) []string {
	args := append([]string{}, o.Path...)
	if f, ok := wwOverride[o.Name]; ok {
		args = append(args, f(c)...)
	} else {
		noun := o.Path[0]
		for _, f := range o.In {
			if !f.Required || f.Flag == "" {
				continue
			}
			v := "matrix"
			switch f.Name {
			case "id", "on_id":
				if id := c.ids[noun]; id != "" {
					v = id
				}
			case "file":
				v = prBody
			case "org":
				if c.org != "" {
					v = c.org
				}
			case "setup":
				v = wwSetupOf(c.org)
			case "url":
				if u := c.w.urls[c.org]; u != "" {
					v = u
				}
			case "store":
				if c.org != "" {
					v = c.org
				}
			}
			args = append(args, "--"+f.Flag, v)
		}
	}
	if o.To && c.p.To != "" && !wwHas(args, "--to") {
		args = append(args, "--to", c.p.To)
	}
	if o.Yes {
		args = append(args, "--yes")
	}
	if wwVerb(o.Name) == "add" {
		if o.Body && !wwHas(args, "--body") {
			args = append(args, "--body", "matrix body")
		}
		args = append(args, "--json")
	}
	return args
}

func wwVerb(name string) string { return name[strings.LastIndex(name, ".")+1:] }

func wwHas(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// wwSnap is the state rules a, d and e compare: per clone its branch tips,
// tracked files and status; the outside file; every remote's refs.
type wwSnap struct {
	tips, status  map[string]string
	outside, refs string
	homeGit       bool
	remoteRefs    map[string]string
}

func wwSnapshot(t testing.TB, w *wwWorld) wwSnap {
	t.Helper()
	sn := wwSnap{tips: map[string]string{}, status: map[string]string{}, remoteRefs: map[string]string{}}
	for _, c := range w.clones {
		if _, err := os.Stat(filepath.Join(c, ".git")); err != nil {
			sn.tips[c] = "gone"
			continue
		}
		sn.tips[c] = w.s.Git(c, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads")
		// tracked content: committed changes move a tip, uncommitted ones
		// show in status (hashing every file per op is what made it slow)
		sn.status[c] = w.s.Git(c, "status", "--porcelain", "--untracked-files=no")
	}
	if b, err := os.ReadFile(w.outside); err == nil {
		sn.outside = string(b)
	}
	_, err := os.Stat(filepath.Join(w.m.Home, ".git"))
	sn.homeGit = err == nil
	var refs []string
	for _, r := range w.remotes {
		rr := w.s.RefsOf(r)
		sn.remoteRefs[r.Slug] = rr
		refs = append(refs, r.Slug+"\n"+rr)
	}
	sn.refs = strings.Join(refs, "\n")
	return sn
}

// wwCell is one run's outcome, kept for the twin comparison (rule c).
type wwCell struct {
	Code int
	Norm string
}

// The rules of WW beyond W's (a-hint, a-init, d-exit, b-files).
const (
	wwRuleIdent   = "a-identity" // commits and the clone's git user are the org identity
	wwRuleCount   = "b-count"    // a printed commit count matches what moved
	wwRulePrinted = "g-printed"  // a printed next-step command works where printed (the owner's GUARD)
	wwRuleTwin    = "c-twin"     // same-shape stores, same treatment
	wwRuleScope   = "d-scope"    // nothing outside the target changes
	wwRuleRemote  = "e-remote"   // no remote moves unless push/sync
)

// wwWaiting are findings another branch's fix covers, keyed like W's
// (rule, then "<op>@<place>", "*@<place>", "<op>@*" or "*@*"): logged as
// waiting, never asserted the buggy way, until the fix lands and the entry
// is removed.
var wwWaiting = map[string]map[string]string{}

// wwFindings collects every finding of the run for the summary log.
type wwFindings struct {
	mu  sync.Mutex
	all []string
}

func (f *wwFindings) add(s string) { f.mu.Lock(); f.all = append(f.all, s); f.mu.Unlock() }

// TestStoryWWWrites: every write, from every place.
func TestStoryWWWrites(t *testing.T) {
	t.Parallel()
	var (
		mu    sync.Mutex
		cells = map[string]map[string]wwCell{} // place -> op -> cell
		found wwFindings
	)
	t.Run("matrix", func(t *testing.T) {
		for _, name := range wwPlaceNames {
			name := name
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				w := wwBuild(t)
				w.s.AdvanceDays(1)
				got := wwRunPlace(t, w, w.places[name], &found)
				mu.Lock()
				cells[name] = got
				mu.Unlock()
			})
		}
	})
	// (c) the twins: same exit, same messages modulo names
	for _, a := range wwPlaceNames {
		ca := cells[a]
		if ca == nil {
			continue
		}
		b := wwTwinOf(a)
		if b == "" || a > b || cells[b] == nil {
			continue
		}
		var ops []string
		for o := range ca {
			ops = append(ops, o)
		}
		sort.Strings(ops)
		for _, o := range ops {
			x, y := ca[o], cells[b][o]
			if x.Code == y.Code && x.Norm == y.Norm {
				continue
			}
			cell := o + "@" + a + "|" + b
			if why := wwWaitingFor(wwRuleTwin, o, a); why != "" {
				t.Logf("WAITING for %s: %s", why, cell)
				continue
			}
			msg := fmt.Sprintf("[WW] %s: (c) the twins differ: %s exit %d vs %s exit %d (- %s, + %s)\n  %s", cell, a, x.Code, b, y.Code, a, b, wwShort(strings.Join(wLineDiff(x.Norm, y.Norm), "\n  ")))
			found.add("c-twin " + cell)
			t.Error(msg)
		}
	}
	sort.Strings(found.all)
	t.Logf("FINDINGS %d:\n%s", len(found.all), strings.Join(found.all, "\n"))
}

// wwTwinOf is the place of the same shape in the other org.
func wwTwinOf(name string) string {
	switch name {
	case "home-to-acme":
		return "home-to-acmeops"
	case "home-to-acmeops":
		return "home-to-acme"
	case "store-root":
		return "ops-root"
	case "ops-root":
		return "store-root"
	case "store-folder":
		return "ops-folder"
	case "ops-folder":
		return "store-folder"
	}
	return ""
}

func wwShort(s string) string {
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}

// wwRunPlace runs every write at p in w and checks the rules after each.
func wwRunPlace(t *testing.T, w *wwWorld, p wwPlace, found *wwFindings) map[string]wwCell {
	ops := wwOps(t, w)
	prBody := filepath.Join(t.TempDir(), "pr-body.md")
	if err := os.WriteFile(prBody, []byte("## Summary\nmatrix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	org := p.Org
	if org == "" {
		org = wOrg
	}
	ids := map[string]string{}
	for k, v := range w.ids[org] {
		ids[k] = v
	}
	cells := map[string]wwCell{}
	// (a) before anything: every store clone's git user is its org identity
	for _, b := range wwIdentityOfClones(w) {
		t.Errorf("[WW] setup@%s: %s", p.Name, b)
		found.add("a-identity setup@" + p.Name + " " + b)
	}
	before := wwSnapshot(t, w)
	var pass, refuse int
	for _, o := range ops {
		args := wwInput(o, wwCtx{w: w, p: p, org: org, ids: ids}, prBody)
		res := w.m.Lore(p.Dir, args...)
		if wwVerb(o.Name) == "add" && res.Code == 0 {
			if id := wwJSONID(res.Stdout); id != "" {
				ids[o.Path[0]] = id
			}
		}
		now := wwSnapshot(t, w)
		cell := o.Name + "@" + p.Name
		var bad []string
		for _, b := range wwCheck(t, w, o, p, args, res, before, now) {
			if why := wwWaitingFor(b.Rule, o.Name, p.Name); why != "" {
				t.Logf("WAITING for %s: %s %s", why, cell, b.Text)
				continue
			}
			found.add(b.Rule + " " + cell + " " + strings.SplitN(b.Text, "\n", 2)[0])
			bad = append(bad, b.Text)
		}
		before = now
		if len(bad) > 0 {
			t.Errorf("[WW] %s: lore %s\n  %s\n%s", cell, strings.Join(args, " "), strings.Join(bad, "\n  "), res)
		}
		// the owner's GUARD (devco:D-543..D-548): every next-step command a
		// write prints (restore:, push it with:, hint: lore …) works exactly
		// as printed, here; run after the write's own checks, so a printed
		// push is not the write moving a remote, and the next write's
		// before is taken after it
		printed := false
		for _, mk := range wuMarkers {
			for _, c := range wuPrinted(res.Out(), mk) {
				printed = true
				pr := w.m.Lore(p.Dir, c...)
				if bad := wuBadRefusal(pr.Code, pr.Out()); bad != "" {
					if why := wwWaitingFor(wwRulePrinted, o.Name, p.Name); why != "" {
						t.Logf("WAITING for %s: %s printed `lore %s` (%s)", why, o.Name+"@"+p.Name, strings.Join(c, " "), bad)
						continue
					}
					found.add(wwRulePrinted + " " + o.Name + "@" + p.Name + " lore " + strings.Join(c, " "))
					t.Errorf("[WW] %s@%s: printed `lore %s`, which fails where printed (%s):\n%s", o.Name, p.Name, strings.Join(c, " "), bad, pr)
				}
			}
		}
		if printed {
			before = wwSnapshot(t, w)
		}
		if os.Getenv(wTrace) != "" {
			first := strings.SplitN(strings.TrimSpace(res.Out()), "\n", 2)[0]
			if c := wwJSONCode.FindStringSubmatch(res.Out()); c != nil {
				first = c[1] + " " + first
			}
			t.Logf("CELL %s exit=%d %s", cell, res.Code, first)
		}
		cells[o.Name] = wwCell{Code: res.Code, Norm: wwNormalize(res.Out())}
		if res.Code == 0 {
			pass++
		} else {
			refuse++
		}
	}
	t.Logf("MATRIX %s: %d writes, %d done, %d refused", p.Name, len(ops), pass, refuse)
	return cells
}

// wwWaitingFor is the finding a cell waits on, or "".
func wwWaitingFor(rule, opName, place string) string {
	for _, k := range []string{opName + "@" + place, "*@" + place, opName + "@*", "*@*"} {
		if why, ok := wwWaiting[rule][k]; ok {
			return why
		}
	}
	return ""
}

// wwIdentityOfClones checks every store clone's git user is its org's.
func wwIdentityOfClones(w *wwWorld) []string {
	var bad []string
	for c, want := range w.ident {
		if _, err := os.Stat(filepath.Join(c, ".git")); err != nil {
			continue
		}
		name := strings.TrimSpace(w.s.TryGit(c, "config", "user.name").Stdout)
		email := strings.TrimSpace(w.s.TryGit(c, "config", "user.email").Stdout)
		if name != want.Name || email != want.Email {
			bad = append(bad, fmt.Sprintf("(a) the %s store clone's git user is %q <%s>, not the org identity %q <%s>", w.owner[c], name, email, want.Name, want.Email))
		}
	}
	sort.Strings(bad)
	return bad
}

// wwJSONCode is the code of a --json error object.
var wwJSONCode = regexp.MustCompile(`"code":\s*"(E_[A-Z_]+)"`)

// wwNum finds printed commit counts: "sent N", "N commit(s)".
var wwNum = regexp.MustCompile(`(?i)\bsent (\d+)\b|\b(\d+) commits?\b`)

// wwIDs and the rest normalize output for the twin comparison.
var (
	wwIDs    = regexp.MustCompile(`\b[a-z]{2,5}_(…)?[0-9A-Za-z]{6,}\b|\b[a-z]{2,5}_…[0-9A-Za-z]*`)
	wwInvite = regexp.MustCompile(`lore-invite:\S+`)
	wwHex    = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	wwDigits = regexp.MustCompile(`\d+`)
	// each place's world lives under its own temp folder
	wwRoot = regexp.MustCompile(`/[^\s"'\x60]*?/homes/`)
)

// wwNormalize strips what differs between twin places by name only.
func wwNormalize(out string) string {
	for _, r := range [][2]string{
		{wwOpsStoreSlug, "STORE"}, {wStoreSlug, "STORE"},
		{"acmeops-new", "NEWOWNER"}, {"acme-new", "NEWOWNER"},
		{"acmeops-co", "OWNER"}, {"acme-co", "OWNER"},
		{wwOpsSetup, "SETUP"}, {wSetup, "SETUP"},
		{wwOpsID.Email, "ORGMAIL"}, {wwAcme.Email, "ORGMAIL"},
		{wwOpsID.Name, "ORGNAME"}, {wwAcme.Name, "ORGNAME"},
		{wwOpsOrg, "ORG"}, {wOrg, "ORG"},
		{"/ops/", "/CODE/"}, {"/code/", "/CODE/"},
	} {
		out = strings.ReplaceAll(out, r[0], r[1])
	}
	out = wwRoot.ReplaceAllString(out, "ROOT/homes/")
	out = wwInvite.ReplaceAllString(out, "INVITE")
	// acme has a client store, acmeops none: lists of the org's folders
	out = strings.ReplaceAll(out, "client_shop (lore-client-shop), ", "")
	out = wwIDs.ReplaceAllString(out, "ID")
	out = wwHex.ReplaceAllString(out, "SHA")
	out = wwDigits.ReplaceAllString(out, "N")
	return strings.TrimSpace(out)
}

// wwCheck is every rule for one run; each broken one is a line.
func wwCheck(t testing.TB, w *wwWorld, o wwOp, p wwPlace, args []string, res teamharness.Result, before, now wwSnap) []wBad {
	var bad []wBad
	add := func(rule, text string) { bad = append(bad, wBad{rule, text}) }
	out := res.Out()
	errLine := wErrLine.MatchString(res.Stderr) || wErrLine.MatchString(res.Stdout)
	code := ""
	if c := wCode.FindStringSubmatch(out); c != nil {
		code = c[1]
	}
	// a --json refusal is a JSON error object, not a line
	if c := wwJSONCode.FindStringSubmatch(out); c != nil {
		errLine, code = true, c[1]
	}
	// W's (d): the exit code matches the output
	switch {
	case res.Code == 0 && errLine:
		add(wRuleExit, "(d) exit 0 with an error line")
	case res.Code != 0 && !errLine && !wwDocumentedExit(o.Name, res.Code):
		add(wRuleExit, "(d) exit non-zero with no error line")
	}
	// W's (a): a refusal says why and names the way out
	if res.Code != 0 {
		if strings.Contains(out, "git init") {
			add(wRuleInit, `(a) the refusal says "git init"`)
		}
		if wPlaceCodes[code] {
			if !wRepoOnly(out) && !wNamesStoreAndSetup(out) {
				add(wRuleHint, "(a) a not-here refusal ("+code+") whose hint does not name --store and --setup")
			}
		}
	}
	// allowed: the place's repo, the target org's store clones (at home
	// with no --to, the org an op names in its own input: store push,
	// set-url, forget, org link name acme)
	allowed := map[string]bool{}
	if p.Repo != "" {
		allowed[p.Repo] = true
	}
	target := p.Org
	if target == "" && (wwNamesOrg(args, wOrg) || wwHoldsRow(w, args, wOrg)) {
		target = wOrg
	}
	for c, org := range w.owner {
		if _, isStore := w.ident[c]; isStore && org != "" && org == target {
			allowed[c] = true
		}
	}
	// (a) new commits in a store clone are the org identity's
	moved := 0
	for _, c := range w.clones {
		if now.tips[c] == before.tips[c] {
			continue
		}
		want, isStore := w.ident[c]
		if now.tips[c] == "gone" || before.tips[c] == "gone" {
			if !allowed[c] {
				add(wwRuleScope, "(d) removed or made a clone outside the target: "+c)
			}
			// a clone made or moved here: its git user is the org's
			if isStore && now.tips[c] != "gone" {
				for _, b := range wwIdentityOfClones(&wwWorld{s: w.s, ident: map[string]journeys.Person{c: want}, owner: w.owner}) {
					add(wwRuleIdent, b)
				}
			}
			continue
		}
		log := wwNewCommits(w, c, before.tips[c])
		if isStore {
			for _, l := range log {
				if l != want.Name+"|"+want.Email+"|"+want.Name+"|"+want.Email {
					add(wwRuleIdent, fmt.Sprintf("(a) a commit in the %s store clone is by %s, not the org identity %s <%s>", w.owner[c], l, want.Name, want.Email))
					break
				}
			}
			if len(log) > 0 {
				for _, b := range wwIdentityOfClones(&wwWorld{s: w.s, ident: map[string]journeys.Person{c: want}, owner: w.owner}) {
					add(wwRuleIdent, b)
				}
			}
		}
		moved += len(log)
		if !allowed[c] {
			add(wwRuleScope, fmt.Sprintf("(d) committed %d to %s, outside the target", len(log), c))
		}
	}
	// (d) tracked files of repos outside the target, the link's file
	for _, c := range w.clones {
		if allowed[c] {
			continue
		}
		if now.status[c] != before.status[c] {
			add(wwRuleScope, "(d) changed git status outside the target in "+c+":\n"+before.status[c]+"\n->\n"+now.status[c])
		}
	}
	if now.outside != before.outside {
		add(wwRuleScope, "(d) wrote through the link to "+w.outside)
	}
	if !before.homeGit && now.homeGit {
		add(wwRuleScope, "(d) made the home folder a git repo")
	}
	// (e) no remote moves unless the op pushes or syncs; (b) counts
	pushed := 0
	for _, r := range w.remotes {
		if now.remoteRefs[r.Slug] == before.remoteRefs[r.Slug] {
			continue
		}
		pushed += wwRemoteMoved(w, r, before.remoteRefs[r.Slug], now.remoteRefs[r.Slug])
		if !wwPushOps[o.Name] {
			add(wwRuleRemote, "(e) moved the refs of "+r.Slug+": "+strings.Join(wLineDiff(before.remoteRefs[r.Slug], now.remoteRefs[r.Slug]), "; "))
		}
	}
	if res.Code == 0 {
		for _, mm := range wwNum.FindAllStringSubmatch(out, -1) {
			n, _ := strconv.Atoi(mm[1] + mm[2])
			if strings.HasPrefix(strings.ToLower(mm[0]), "sent") && n != pushed {
				add(wwRuleCount, fmt.Sprintf("(b) printed %q but %d commits reached the remotes", mm[0], pushed))
			}
			if mm[2] != "" && n != pushed && n != moved {
				add(wwRuleCount, fmt.Sprintf("(b) printed %q but %d commits were made and %d sent", mm[0], moved, pushed))
			}
		}
	}
	return bad
}

// wwHoldsRow reports whether an argument is the id of a row a store clone
// of org holds: an edit, delete or restore by id from the home folder
// writes the org whose store holds the row (devco:D-543), so that org's
// stores are its target.
func wwHoldsRow(w *wwWorld, args []string, org string) bool {
	for _, a := range args {
		if !wwRowID.MatchString(a) {
			continue
		}
		for c, o := range w.owner {
			if o != org {
				continue
			}
			if hits, _ := filepath.Glob(filepath.Join(c, "*", "*", a+".json")); len(hits) > 0 {
				return true
			}
		}
	}
	return false
}

// wwRowID is the shape of a row id (<prefix>_<hex>).
var wwRowID = regexp.MustCompile(`^[a-z]+_[0-9a-f]{16,}$`)

// wwNamesOrg reports whether an op's arguments name org (its name, a
// folder of it, its setup or its store's url): such an op at the home
// folder targets that org's store.
func wwNamesOrg(args []string, org string) bool {
	for _, a := range args {
		if a == org || strings.HasPrefix(a, org+"/") || a == wwSetupOf(org) || strings.Contains(a, "/"+org+"-") || strings.Contains(a, ":"+org+"-") {
			return true
		}
	}
	return false
}

// wwDocumentedExit reports a documented report-with-exit of opName.
func wwDocumentedExit(opName string, code int) bool {
	for _, c := range wwExits[opName] {
		if c == code {
			return true
		}
	}
	return false
}

// wwNewCommits lists "an|ae|cn|ce" of the commits on c's branches that
// were not there before (old is the for-each-ref listing before).
func wwNewCommits(w *wwWorld, c, old string) []string {
	args := []string{"log", "--format=%an|%ae|%cn|%ce", "--branches"}
	for _, l := range strings.Split(strings.TrimSpace(old), "\n") {
		if f := strings.Fields(l); len(f) == 2 {
			args = append(args, "^"+f[1])
		}
	}
	r := w.s.TryGit(c, args...)
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(r.Stdout), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// wwRemoteMoved counts the commits that reached r between two ref lists.
func wwRemoteMoved(w *wwWorld, r *teamharness.Remote, old, now string) int {
	args := []string{"rev-list", "--count"}
	for _, l := range strings.Split(strings.TrimSpace(now), "\n") {
		if f := strings.Fields(l); len(f) >= 1 {
			args = append(args, f[0])
		}
	}
	for _, l := range strings.Split(strings.TrimSpace(old), "\n") {
		if f := strings.Fields(l); len(f) >= 1 {
			args = append(args, "^"+f[0])
		}
	}
	res := w.s.TryGit(r.Dir, args...)
	n, _ := strconv.Atoi(strings.TrimSpace(res.Stdout))
	return n
}
