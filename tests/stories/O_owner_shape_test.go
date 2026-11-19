//go:build stories && !windows

package stories

// O_owner_shape_test.go — story O (tests/stories/O-owner-shape.md): the admin
// and the member story flows on owner-shaped machines (ownershape.go): two
// git identities by folder, the org account behind an ssh alias, two orgs
// and two setups on one laptop, an admins-only store, a store moved to
// another GitHub org, a repo that gitignores .lore/, a CLAUDE.md linked out
// of its repo and client repos matched by prefix patterns. Each step checks
// the owner's standing rules: the org identity for every org store (the
// clone's git user and every commit), nothing in the tracked files of a
// private or client repo, nothing written through a link out, nothing
// pushed unless asked, and every printed count true.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The orgs, people and repos of story O (placeholders).
const (
	oOrg      = "acme"
	oSetup    = "acme-admin"
	oMSetup   = "acme-member"
	oKite     = "kite"
	oKSetup   = "kite-home"
	oAlias    = "github-acme"
	oOwner    = "acme-co"
	oNewOwner = "acme-labs"
	oFolder   = "web"
)

var (
	oAdminPersonal = journeys.Person{Name: "Ada Home", Email: "ada@home.example", Login: "ada-home"}
	oAdminOrg      = journeys.Person{Name: "Ada Admin", Email: "ada@acme.example", Login: "ada-acme"}
	oMemPersonal   = journeys.Person{Name: "Mo Home", Email: "mo@home.example", Login: "mo-home"}
	oMemOrg        = journeys.Person{Name: "Mo Member", Email: "mo@acme.example", Login: "mo-acme"}
)

// oApply saves one setup step; a refusal fails with what setup check says.
func oApply(t testing.TB, m *OwnerMachine, dir, setup, step string, answers map[string]any) {
	t.Helper()
	b, err := json.Marshal(answers)
	if err != nil {
		t.Fatal(err)
	}
	r := m.Lore(dir, "setup", "apply", "--setup", setup, "--step", step, "--answers", string(b), "--json")
	if r.Code != 0 {
		c := m.Lore(dir, "setup", "check", "--setup", setup, "--step", step, "--answers", string(b), "--json")
		t.Fatalf("setup apply %s --step %s: %s\ncheck says: %s", setup, step, r, c.Stdout)
	}
}

// oSetup saves every step of a setup: kind admin (team) or member (join).
func oSetupFlow(t testing.TB, m *OwnerMachine, dir, setup, kind string, p journeys.Person, alias string, last map[string]any, folders []string) {
	t.Helper()
	oApply(t, m, dir, setup, "kind", map[string]any{"kind": kind, "name": setup})
	oApply(t, m, dir, setup, "identity", map[string]any{"name": p.Name, "email": p.Email})
	oApply(t, m, dir, setup, "github", map[string]any{"github_account": p.Login, "host_alias": alias})
	oApply(t, m, dir, setup, "personal_store", map[string]any{"personal_store": "local"})
	step := "team"
	if kind == "member" {
		step = "join"
	}
	oApply(t, m, dir, setup, step, last)
	oApply(t, m, dir, setup, "tools", map[string]any{"tools": []string{"claude_code"}})
	oApply(t, m, dir, setup, "secrets", map[string]any{"backend": "none"})
	oApply(t, m, dir, setup, "folders", map[string]any{"folders": folders, "default": false})
	oApply(t, m, dir, setup, "this_folder", map[string]any{})
	oApply(t, m, dir, setup, "summary", map[string]any{})
}

// oFinding is one broken standing rule, by finding id when one is open.
type oFinding struct{ ID, Text string }

// oWaiting are findings another branch's fix covers (by key), logged as
// waiting instead of failing until that fix lands and the entry goes.
var oWaiting = map[string]string{}

// oCheck reports a broken rule: a failure, or a log line while it waits.
func oCheck(t testing.TB, key string, ok bool, format string, args ...any) {
	t.Helper()
	if ok {
		return
	}
	if why, w := oWaiting[key]; w {
		t.Logf("WAITING for %s: [%s] "+format, append([]any{why, key}, args...)...)
		return
	}
	t.Errorf("[O %s] "+format, append([]any{key}, args...)...)
}

// oTracked is the tracked files of repo (a link by its target) and its git
// status: what "nothing in tracked files" compares.
func oTracked(t testing.TB, s *Story, repo string) string {
	t.Helper()
	return wTracked(t, s, repo) + "\n--status--\n" + s.Git(repo, "status", "--porcelain")
}

// oHash is a file's sha256 ("" when missing).
func oHash(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// oSent is the commit count `store push` printed ("sent N"), -1 if none.
var oSent = regexp.MustCompile(`(?i)\bsent (\d+)`)

// oStoreClone is the clone folder of a store slug's owner/repo under ~/.lore/stores.
func oStoreClone(m *OwnerMachine, owner, repo string) string {
	return m.LorePath("stores", owner, repo)
}

// TestStoryOOwnerShape: the admin flow then the member flow.
func TestStoryOOwnerShape(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	ops := s.Remote("github.com/" + oOwner + "/lore-ops")
	vault := s.Remote("github.com/" + oOwner + "/lore-vault")
	kiteStore := s.Remote("github.com/kite-me/lore-kite")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote("github.com/"+oOwner+"/web"), func(dir string) { PlainRepo(t, dir) })
	shop := s.Seed(s.Remote("github.com/"+oOwner+"/client-shop"), func(dir string) { Monorepo(t, dir) })
	ign := s.Seed(s.Remote("github.com/"+oOwner+"/private-notes"), func(dir string) { GitignoredLore(t, dir) })
	lnk := s.Seed(s.Remote("github.com/"+oOwner+"/linked-claude"), func(dir string) {
		ClaudeLinkOut(t, dir, filepath.Join("..", "..", "..", "notes", "CLAUDE.md"))
	})
	kiteApp := s.Seed(s.Remote("github.com/kite-me/kite-app"), func(dir string) { PlainRepo(t, dir) })
	all := []*teamharness.Remote{team, ops, vault, kiteStore, platform, web, shop, ign, lnk, kiteApp}

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner, oNewOwner}})
	notes := filepath.Join(ada.Home, "notes", "CLAUDE.md")
	var pc, wc, sc, ic, lc, kc *teamharness.Clone
	refs := func() map[string]string {
		out := map[string]string{}
		for _, r := range all {
			out[r.Slug] = s.RefsOf(r)
		}
		return out
	}
	noPush := func(before map[string]string, what string) {
		for _, r := range all {
			if b, ok := before[r.Slug]; ok {
				s.NoPushSince(t, r, b, what)
			}
		}
	}

	s.Step("admin/machine-shape", func() {
		pc = ada.CloneTo(platform, ada.OrgDir("platform"))
		if got := ada.GitUser(t, pc.Dir); got != oAdminOrg.Email {
			t.Fatalf("fixture: an org repo's git user is %q, want the org identity %q (includeIf)", got, oAdminOrg.Email)
		}
		kc = ada.CloneTo(kiteApp, ada.Code("kite", "kite-app"))
		if got := ada.GitUser(t, kc.Dir); got != oAdminPersonal.Email {
			t.Fatalf("fixture: a personal repo's git user is %q, want %q", got, oAdminPersonal.Email)
		}
		// the alias carries git: an org url goes over ssh to the fake GitHub
		if out := s.Git(pc.Dir, "config", "--get-regexp", "^remote.origin.url"); !strings.Contains(out, oOwner) {
			t.Fatalf("fixture: origin %q", out)
		}
	})

	s.Step("admin/setup-two-orgs", func() {
		oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
			map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
		ada.LoreIn(pc, "sync").OK(t)
		oSetupFlow(t, ada, kc.Dir, oKSetup, "admin", oAdminPersonal, "github.com",
			map[string]any{"org": oKite, "store_url": kiteStore.SSHURL()}, []string{"~/code/kite/**"})
		ada.LoreIn(kc, "sync").OK(t)
		ada.LoreIn(pc, "org", "store", "add", "--org", oOrg, "--url", ops.SSHURL(), "--audience", "admins", "--push", "--yes").OK(t)
		ada.LoreIn(pc, "org", "config", "set", "--org", oOrg, "--key", "repos", "--value", "github.com/"+oOwner+"/client-*", "--push", "--yes").OK(t)
	})

	// identity: every org store's clone has the org identity as its git user
	// and every commit on it is authored so (the kite store: the personal one)
	identity := func(step string) {
		for _, st := range []struct {
			r    *teamharness.Remote
			want string
		}{{team, oAdminOrg.Email}, {ops, oAdminOrg.Email}, {kiteStore, oAdminPersonal.Email}} {
			parts := strings.Split(st.r.Slug, "/")
			repo := parts[2]
			clone := oStoreClone(ada, parts[1], repo)
			if _, err := os.Stat(clone); err != nil {
				continue
			}
			got := ada.GitUser(t, clone)
			oCheck(t, step+"/clone-user/"+repo, got == st.want, "%s: the clone %s has git user %q, want the org identity %q", step, clone, got, st.want)
			for _, a := range st.r.Authors("main") {
				oCheck(t, step+"/author/"+repo, strings.Contains(a, st.want) || strings.Contains(a, "seed@"), "%s: a commit on %s is authored %q, want %q", step, st.r.Slug, a, st.want)
			}
		}
	}
	s.Step("admin/identity-after-setup", func() { identity("setup") })

	// devco:D-530: the global email is the personal one and an includeIf
	// gives the org folder the org email; setup list and doctor see that
	// as right and never advise changing the global identity.
	s.Step("admin/identity-advice", func() {
		for _, args := range [][]string{{"setup", "list"}, {"doctor", "--all"}} {
			r := ada.Lore(ada.Home, args...)
			out := r.Stdout + r.Stderr
			oCheck(t, "identity-advice/global", !strings.Contains(out, "--global"), "%v advises a --global git change:\n%s", args, out)
			oCheck(t, "identity-advice/git_email", !strings.Contains(out, "identity.git_email"), "%v warns identity.git_email for an includeIf identity:\n%s", args, out)
		}
	})

	var shopTree, ignTree, notesHash string
	ids := map[string]string{}
	s.Step("admin/folders-rows-links-render", func() {
		ada.LoreIn(pc, "store", "folder", "add", "team", oFolder).OK(t)
		for noun, args := range map[string][]string{
			"memory": {"--body", "owner-shape memory"},
			"task":   {"--title", "owner-shape task"},
		} {
			r := ada.LoreIn(pc, append([]string{noun, "add", "--json", "--to", oOrg + "/" + oFolder, "--yes"}, args...)...).OK(t)
			var got struct {
				ID   string `json:"id"`
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			_ = json.Unmarshal([]byte(r.Stdout), &got)
			ids[noun] = got.ID + got.Data.ID
		}
		wc = ada.CloneTo(web, ada.OrgDir("web"))
		ada.LoreIn(wc, "org", "link", "--org", oOrg, "--project", oFolder, "--yes").OK(t)

		sc = ada.CloneTo(shop, ada.OrgDir("client-shop"))
		shopTree = oTracked(t, s, sc.Dir)
		ada.LoreIn(sc, "org", "link", "--org", oOrg, "--project", "client_shop", "--yes")

		if err := os.MkdirAll(filepath.Dir(notes), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, notes, "# my own notes\n")
		notesHash = oHash(notes)
		ic = ada.CloneTo(ign, ada.OrgDir("private-notes"))
		ignTree = oTracked(t, s, ic.Dir)
		lc = ada.CloneTo(lnk, ada.OrgDir("linked-claude"))
		for _, c := range []*teamharness.Clone{ic, lc} {
			ada.LoreIn(c, "init")
			ada.LoreIn(c, "memory", "add", "--body", "private note", "--yes")
			ada.LoreIn(c, "render")
		}
		ada.LoreIn(sc, "render")
		ada.LoreIn(wc, "render")
	})

	s.Step("admin/nothing-in-private-or-client-repos", func() {
		shopNow, ignNow := oTracked(t, s, sc.Dir), oTracked(t, s, ic.Dir)
		oCheck(t, "client-tracked", shopNow == shopTree, "a client repo (matched by github.com/%s/client-*) changed its tracked files or status: %v", oOwner, wLineDiff(shopTree, shopNow))
		oCheck(t, "ignored-tracked", ignNow == ignTree, "a repo that gitignores .lore/ changed its tracked files or status: %v", wLineDiff(ignTree, ignNow))
		// init in that repo is the person's own step (it may write .gitignore);
		// the link itself must stay a link to the same place
		tgt, lerr := os.Readlink(filepath.Join(lc.Dir, "CLAUDE.md"))
		oCheck(t, "linkout-link", lerr == nil && tgt == filepath.Join("..", "..", "..", "notes", "CLAUDE.md"), "CLAUDE.md is no longer the link out of the repo (%q, %v)", tgt, lerr)
		oCheck(t, "linkout-target", oHash(notes) == notesHash, "lore wrote through the CLAUDE.md link to %s", notes)
	})

	s.Step("admin/reads-push-nothing", func() {
		before := refs()
		for _, dir := range []string{ada.Home, pc.Dir, wc.Dir, sc.Dir, ic.Dir, lc.Dir, oStoreClone(ada, oOwner, "lore-team")} {
			ada.Lore(dir, "task", "list", "--store", oOrg)
			ada.Lore(dir, "memory", "list", "--store", oOrg)
			ada.Lore(dir, "store", "list")
		}
		noPush(before, "reads from every place")
	})

	s.Step("admin/sync", func() {
		ada.LoreIn(pc, "sync").OK(t)
		// the admins-only store gets its own folder and a row (D-542: the
		// commit note names the store written, not the org)
		r := ada.LoreIn(pc, "store", "folder", "add", "lore-ops", "servers").OK(t)
		oCheck(t, "folder-note", strings.Contains(r.Stderr, "store lore-ops;") && !strings.Contains(r.Stderr, "store acme;"), "store folder add lore-ops names another store as the one committed to:\n%s", r)
		ada.LoreIn(pc, "memory", "add", "--body", "ops note", "--to", "lore-ops/servers", "--yes").OK(t)
		before := refs()
		ada.LoreIn(pc, "sync").OK(t)
		oCheck(t, "ops-pushed", s.RefsOf(ops) != before[ops.Slug], "lore sync did not send the admins-only store's commits")
		// a second admins-only store nobody wrote to yet (its remote empty)
		ada.LoreIn(pc, "org", "store", "add", "--org", oOrg, "--url", vault.SSHURL(), "--audience", "admins", "--push", "--yes").OK(t)
		identity("sync")
	})

	// The store moves to another GitHub org (a transfer), then set-url.
	var team2, ops2 *teamharness.Remote
	// devco:D-683: task next over the whole setup, the whole org store and
	// at the store clone's top attached the company folder twice when a
	// folder sorted before it ("[E_CONFIG_INVALID] store acme/org attached
	// twice", no hint); the org's admins-only store's folders rank too.
	// With two org setups on the machine, plain task next and member add
	// from home refuse naming --store and --setup (devco:D-692: nothing is
	// guessed between two setups), and the printed ways work there.
	s.Step("admin/task-next-every-placement", func() {
		ada.LoreIn(pc, "store", "folder", "add", "team", "api").OK(t)
		ada.LoreIn(pc, "task", "add", "--title", "api task before org", "--to", oOrg+"/api", "--yes").OK(t)
		ada.LoreIn(pc, "task", "add", "--title", "company task", "--to", oOrg+"/org", "--yes").OK(t)
		before := refs()
		top := oStoreClone(ada, oOwner, "lore-team")
		for _, c := range []struct {
			key, dir string
			args     []string
		}{
			{"home-setup", ada.Home, []string{"--setup", oSetup}},
			{"home-store-org", ada.Home, []string{"--store", oOrg}},
			{"home-store-folder", ada.Home, []string{"--store", oOrg + "/api"}},
			{"store-top", top, nil},
			{"store-top-setup", top, []string{"--setup", oSetup}},
			{"ops-store", oStoreClone(ada, oOwner, "lore-ops"), nil},
		} {
			r := ada.Lore(c.dir, append([]string{"task", "next"}, c.args...)...)
			oCheck(t, "D-683/"+c.key, r.Code == 0 && !strings.Contains(r.Out(), "attached twice"), "task next %v in %s: want the ranked store tasks, got:\n%s", c.args, c.dir, r)
		}
		r := ada.Lore(ada.Home, "task", "next", "--setup", oSetup)
		oCheck(t, "D-683/ranks-every-folder", strings.Contains(r.Out(), "api task before org") || strings.Contains(r.Out(), "company task") || strings.Contains(r.Out(), "owner-shape task"), "task next --setup %s names no task of the org's folders:\n%s", oSetup, r)
		r = ada.Lore(ada.Home, "task", "next")
		oCheck(t, "D-683/home-plain", r.Code != 0 && strings.Contains(r.Out(), "lore task next --store <org>") && strings.Contains(r.Out(), "--setup <setup>") && !strings.Contains(r.Out(), "does not apply"), "plain task next from home with two org setups: want the refusal naming --store and --setup, got:\n%s", r)
		r = ada.Lore(ada.Home, "member", "add", "--email", "pat@acme.example", "--name", "Pat", "--yes")
		oCheck(t, "D-692/home-two-setups", r.Code != 0 && strings.Contains(r.Out(), "--setup <setup>") && strings.Contains(r.Out(), oSetup) && strings.Contains(r.Out(), oKSetup), "member add from home with two org setups: want the refusal naming both setups, got:\n%s", r)
		noPush(before, "task next reads from every placement")
	})

	// devco:D-695, D-696, D-704 (owner use pass 5 weak 1, 2, 10): commands
	// from the home folder on a machine with two org setups (acme and the
	// second org kite, both admin).
	//   D-695: an agent's write from home `--to <org>/<folder>` credited the
	//   row to the setup's agent while the commit's Lore-Agent trailer named
	//   the home folder's fallback agent (another key); the trailer is the
	//   same one a write from inside the setup's repo carries.
	//   D-696: `task next --setup <second org's setup>` printed "(or lore
	//   task claim --id … --push)" and that line, run as printed from home,
	//   failed "no store named kite is set up for this folder … the stores
	//   --to takes here" (and task release --id the same).
	//   D-704: member add from home refused with two org setups; with a
	//   default org setup it runs on that setup and says so.
	s.Step("admin/home-two-setups", func() {
		agentEnv := []string{"LORE_HUMAN=", "LORE_NO_DETECT=0", "CLAUDE_CODE_CHILD_SESSION=1", "CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=o-d695"}
		home := (&teamharness.Clone{A: ada.Actor, Dir: ada.Home}).With(agentEnv...)
		top := oStoreClone(ada, oOwner, "lore-team")
		trailer := func() string {
			for _, l := range strings.Split(s.Git(top, "log", "-1", "--format=%B"), "\n") {
				if strings.HasPrefix(l, "Lore-Agent:") {
					return l
				}
			}
			return ""
		}
		pc.With(agentEnv...).Lore("memory", "add", "--body", "agent note from the org repo", "--to", oOrg+"/"+oFolder, "--yes").OK(t)
		inRepo := trailer()
		r := home.Lore("memory", "add", "--body", "agent note from home", "--to", oOrg+"/"+oFolder, "--yes")
		fromHome := trailer()
		oCheck(t, "D-695/trailer", r.Code == 0 && inRepo != "" && fromHome == inRepo, "an agent's write from home names another agent in its commit trailer than the same write in the org's repo (repo %q, home %q):\n%s", inRepo, fromHome, r)

		before := refs()
		r = ada.Lore(ada.Home, "member", "add", "--email", "pat.home@acme.example", "--name", "Pat Home", "--yes")
		oCheck(t, "D-704/two-setups-refusal", r.Code != 0 && strings.Contains(r.Out(), "--setup "+oSetup) && strings.Contains(r.Out(), "--default"), "member add from home with two admin org setups and no default: want the refusal naming the full command with --setup and the default, got:\n%s", r)
		ada.Lore(ada.Home, "setup", "edit", "--name", oSetup, "--default", "--yes").OK(t)
		r = ada.Lore(ada.Home, "member", "add", "--email", "pat.home@acme.example", "--name", "Pat Home", "--yes")
		oCheck(t, "D-704/default-setup", r.Code == 0 && strings.Contains(r.Out(), "setup "+oSetup), "member add from home with default setup %s: want it added there and the setup named, got:\n%s", oSetup, r)
		noPush(before, "member add from home")
		// with the company setup the default (the home folder resolves it),
		// the second org's task by id from home
		ada.Lore(kc.Dir, "task", "add", "--json", "--title", "kite queue task", "--to", oKite+"/org", "--yes").OK(t)
		ada.LoreIn(kc, "sync").OK(t)
		r = ada.Lore(ada.Home, "task", "next", "--setup", oKSetup)
		m := oClaimHint.FindStringSubmatch(r.Out())
		oCheck(t, "D-696/hint", r.Code == 0 && m != nil, "task next --setup %s from home prints no claim line:\n%s", oKSetup, r)
		if m != nil {
			args := strings.Fields(m[1])
			c := ada.Lore(ada.Home, args...)
			oCheck(t, "D-696/claim-as-printed", c.Code == 0, "the printed %q run from home failed:\n%s", "lore "+m[1], c)
			oCheck(t, "D-696/no-unasked-flag", !strings.Contains(c.Out(), "--to"), "the claim from home talks about --to, which was never typed:\n%s", c)
			id := args[slices.Index(args, "--id")+1]
			c = ada.Lore(ada.Home, "task", "release", "--id", id)
			oCheck(t, "D-696/release", c.Code == 0, "task release --id %s from home failed:\n%s", id, c)
		}

		ada.Lore(ada.Home, "setup", "edit", "--name", oSetup, "--no-default", "--yes").OK(t)
	})

	s.Step("admin/store-moves-org", func() {
		team2 = s.MoveRemote(t, team, oNewOwner, false)
		ops2 = s.MoveRemote(t, ops, oNewOwner, false)
		// GitHub's transfer: the old addresses are gone
		for i, r := range all {
			switch r {
			case team:
				all[i] = team2
			case ops:
				all[i] = ops2
			}
		}
		team, ops = team2, ops2
		for _, mv := range []struct {
			name string
			r    *teamharness.Remote
			old  string
		}{{oOrg, team2, "lore-team"}, {"lore-ops", ops2, "lore-ops"}} {
			r := ada.Lore(ada.Home, "store", "set-url", "--name", mv.name, "--url", mv.r.SSHURL(), "--yes")
			if mv.name != oOrg {
				// an org's second store, by its name from $HOME (D-540)
				oCheck(t, "seturl-store-name", r.Code == 0, "store set-url --name %s (the org's admins-only store) from $HOME refused:\n%s", mv.name, r)
				if r.Code != 0 {
					// the way that works meanwhile: from inside an org repo, as sync's hint says
					r = ada.LoreIn(pc, "store", "set-url", "--name", mv.name, "--url", mv.r.SSHURL(), "--yes")
				}
			}
			if r.Code != 0 {
				t.Errorf("[O set-url] store set-url --name %s from $HOME: %s", mv.name, r)
				continue
			}
			newClone := oStoreClone(ada, oNewOwner, mv.old)
			_, oldErr := os.Stat(oStoreClone(ada, oOwner, mv.old))
			_, newErr := os.Stat(newClone)
			oCheck(t, "seturl-move/"+mv.old, oldErr != nil && newErr == nil, "set-url --name %s left the clone at the old owner's folder (old there: %v, new there: %v)\n%s", mv.name, oldErr == nil, newErr == nil, r)
			if newErr == nil {
				got := ada.GitUser(t, newClone)
				oCheck(t, "seturl-user/"+mv.old, got == oAdminOrg.Email, "set-url --name %s: the moved clone's git user is %q, want the org identity %q", mv.name, got, oAdminOrg.Email)
				oCheck(t, "seturl-says/"+mv.old, !strings.Contains(r.Out(), "git user set to") || strings.Contains(r.Out(), got), "set-url --name %s says a git user that is not the one set (%q):\n%s", mv.name, got, r)
			}
		}
		identity("set-url")
	})

	s.Step("admin/push-counts", func() {
		ada.LoreIn(wc, "memory", "add", "--body", "after the move", "--to", oOrg+"/"+oFolder, "--yes")
		before := team2.CommitCount("main")
		r := ada.Lore(ada.Home, "store", "push", "--store", oOrg)
		oCheck(t, "push-by-name", r.Code == 0, "store push --store %s from $HOME refused:\n%s", oOrg, r)
		if r.Code != 0 {
			r = ada.Lore(ada.Home, "store", "push", "--store", team2.SSHURL())
		}
		sent := team2.CommitCount("main") - before
		if m := oSent.FindStringSubmatch(r.Out()); m != nil {
			n, _ := strconv.Atoi(m[1])
			oCheck(t, "push-count", n == sent, "store push printed sent %d after %d commits reached %s:\n%s", n, sent, team2.Slug, r)
		}
		identity("push")
	})

	s.Step("admin/org-list-store-show", func() {
		// devco D-570: org list was an unknown command; it lists each org
		// entry with its stores and the setups (and role) that use it.
		r := ada.Lore(ada.Home, "org", "list")
		oCheck(t, "org-list", r.Code == 0 && strings.Contains(r.Out(), oOrg) && strings.Contains(r.Out(), "(admin)") && strings.Contains(r.Out(), "lore-team"),
			"org list from $HOME does not list the org entry %s with its store and admin setup:\n%s", oOrg, r)
		// devco D-571: store show --store <org> printed only push mode,
		// compaction and epoch; it now prints the clone and folders too.
		r = ada.Lore(ada.Home, "store", "show", "--store", oOrg)
		oCheck(t, "store-show-named", r.Code == 0 && strings.Contains(r.Out(), "  clone ") && strings.Contains(r.Out(), "  folders ") && strings.Contains(r.Out(), oFolder),
			"store show --store %s prints no clone or folders:\n%s", oOrg, r)
	})

	// The member: an owner-shaped machine of their own joins acme.
	mo := s.OwnerShaped(OwnerShape{Personal: oMemPersonal, Org: oMemOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner, oNewOwner}})
	var mwc *teamharness.Clone
	s.Step("member/join", func() {
		_, line := journeys.Invite(t, ada.Machine, pc.Dir, oMemOrg.Email)
		mwc = mo.CloneTo(web, mo.OrgDir("web"))
		oSetupFlow(t, mo, mwc.Dir, oMSetup, "member", oMemOrg, oAlias, map[string]any{"invite": line}, []string{"~/code/" + oOrg + "/**"})
		mo.LoreIn(mwc, "sync").OK(t)
		_, err := os.Stat(oStoreClone(mo, oNewOwner, "lore-ops"))
		_, err2 := os.Stat(oStoreClone(mo, oOwner, "lore-ops"))
		oCheck(t, "member-no-admin-store", err != nil && err2 != nil, "a member's machine cloned the admins-only store")
	})

	s.Step("member/reads-writes-sync", func() {
		before := refs()
		mo.LoreIn(mwc, "task", "list").OK(t)
		mo.Lore(mo.Home, "memory", "list", "--store", oOrg).OK(t)
		noPush(before, "member reads")
		mo.LoreIn(mwc, "memory", "add", "--body", "member note", "--to", oOrg+"/"+oFolder, "--yes").OK(t)
		mo.LoreIn(mwc, "sync").OK(t)
		clone := oStoreClone(mo, oNewOwner, "lore-team")
		if _, err := os.Stat(clone); err == nil {
			got := mo.GitUser(t, clone)
			oCheck(t, "member-clone-user", got == oMemOrg.Email, "the member's store clone has git user %q, want the org identity %q", got, oMemOrg.Email)
		}
		for _, a := range team2.Authors("main") {
			oCheck(t, "member-author", !strings.Contains(a, oMemPersonal.Email), "a commit on the org store is authored with the member's personal identity: %q", a)
		}
	})

	// devco:D-683, D-692: the member's machine has one org setup, so a
	// plain task next and a roster read from the home folder run on it and
	// say so; before, task next said "--store does not apply" while
	// `task next --store <org>` worked
	s.Step("member/home-routes-to-the-one-setup", func() {
		before := refs()
		r := mo.Lore(mo.Home, "task", "next", "--setup", oMSetup)
		oCheck(t, "D-683/member-home-setup", r.Code == 0, "the member's task next --setup %s from home: want the queue, got:\n%s", oMSetup, r)
		r = mo.Lore(mo.Home, "task", "next", "--store", oOrg)
		oCheck(t, "D-683/member-home-store", r.Code == 0, "the member's task next --store %s from home: want the queue, got:\n%s", oOrg, r)
		r = mo.Lore(mo.Home, "task", "next")
		oCheck(t, "D-683/member-home-plain", r.Code == 0 && strings.Contains(r.Out(), "setup "+oMSetup+" (the only org setup on this machine)") && !strings.Contains(r.Out(), "does not apply"), "the member's plain task next from home: want the queue of setup %s, got:\n%s", oMSetup, r)
		r = mo.Lore(mo.Home, "team", "list")
		oCheck(t, "D-692/member-home-roster", r.Code == 0 && strings.Contains(r.Out(), "setup "+oMSetup), "the member's team list from home: want the roster of setup %s, got:\n%s", oMSetup, r)
		noPush(before, "member reads from home")
	})

	// devco:D-684 (owner use pass 4 weak 2): the admin's unpushed store
	// commits of mixed kinds while the member pushed 2, then `lore sync` in
	// a repo outside every setup's folders that attaches no store (setup
	// none): the waiting clone's push said "store acme: pushed (sent 4,
	// received 0, …)" (rows, nothing received) with no push line
	s.Step("admin/no-setup-sync-after-member-push", func() {
		scratch := s.Seed(s.Remote("github.com/solo-dev/scratch"), func(dir string) { PlainRepo(t, dir) })
		xc := ada.CloneTo(scratch, filepath.Join(ada.Home, "elsewhere", "scratch"))
		clone := oStoreClone(ada, oNewOwner, "lore-team")
		ada.LoreIn(pc, "sync").OK(t)
		ada.Lore(ada.Home, "member", "add", "--email", "pat.one@example.test", "--name", "Pat One", "--setup", oSetup, "--yes").OK(t)
		ada.Lore(ada.Home, "member", "add", "--email", "pat.two@example.test", "--name", "Pat Two", "--setup", oSetup, "--yes").OK(t)
		ada.LoreIn(pc, "org", "config", "set", "--org", oOrg, "--key", "repos", "--add", "github.com/client-two/*", "--yes").OK(t)
		moved := s.MoveRemote(t, team2, "acme-moved", true)
		ada.Lore(ada.Home, "store", "set-url", "--name", oOrg, "--url", moved.SSHURL(), "--yes").OK(t)
		ada.Lore(ada.Home, "store", "set-url", "--name", oOrg, "--url", team2.SSHURL(), "--yes").OK(t)
		ada.LoreIn(wc, "memory", "add", "--body", "the web app's cdn purges on deploy", "--to", oOrg+"/"+oFolder, "--yes").OK(t)
		ada.LoreIn(wc, "policy", "set", "--key", "context.budget.internal", "--value", "22000", "--to", oOrg+"/"+oFolder, "--yes").OK(t)
		for _, body := range []string{"the member notes the release train", "the member keeps a runbook"} {
			mo.LoreIn(mwc, "memory", "add", "--body", body, "--to", oOrg+"/"+oFolder, "--yes").OK(t)
		}
		mo.LoreIn(mwc, "sync").OK(t)

		head := wqGit(t, clone, "rev-parse", "HEAD")
		remoteBefore := wqGit(t, team2.Dir, "rev-parse", "main")
		r := ada.Lore(xc.Dir, "sync").OK(t)
		remoteAfter := wqGit(t, team2.Dir, "rev-parse", "main")
		sent, received := wqCounts(t, r)
		went := wctCount(t, team2.Dir, remoteBefore+".."+remoteAfter)
		arrived := wctCount(t, clone, head+".."+remoteBefore)
		oCheck(t, "D-684/no-setup-sent", sent == went && went > 0, "lore sync in a repo with no setup says sent %d, git rev-list on the store remote counts %d:\n%s", sent, went, r)
		oCheck(t, "D-684/no-setup-received", received == arrived && arrived > 0, "lore sync in a repo with no setup says received %d, git counts %d commits the member pushed:\n%s", received, arrived, r)
		oCheck(t, "D-684/no-setup-push-line", strings.Contains(r.Stderr, "lore: pushed store "+oOrg), "lore sync in a repo with no setup pushed the store with no push line:\n%s", r)
	})

	s.Step("admin/empty-store-moves", func() {
		// the empty admins-only store moves too (D-541): the refusal names
		// the store set-url was given, never the org's team store
		vault2 := s.MoveRemote(t, vault, oNewOwner, false)
		for i, r := range all {
			if r == vault {
				all[i] = vault2
			}
		}
		r := ada.LoreIn(pc, "store", "set-url", "--name", "lore-vault", "--url", vault2.SSHURL(), "--yes")
		oCheck(t, "seturl-empty-store", r.Code == 0 || !strings.Contains(r.Out(), "--name "+oOrg+" "), "set-url --name lore-vault refuses and tells the person to set-url the org's team store (--name %s) to the vault's url:\n%s", oOrg, r)
	})
}

// oClaimHint is task next's "(or lore task claim --id … --push …)" line.
var oClaimHint = regexp.MustCompile(`\(or lore (task claim --id \S+[^)]*)\)`)
