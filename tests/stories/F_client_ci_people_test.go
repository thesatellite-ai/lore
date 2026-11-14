//go:build stories && !windows

package stories

// F_client_ci_people_test.go — story F (tests/stories/F-client-ci-people.md):
// an org works in a client's repo through a client store, CI runs every
// variant a hosted runner has, and people join, leave and lose access. Three
// parts so each stays short and they run in parallel: the client repo, CI,
// and people. The contractor-only flow over months (a client store as the
// only store) is the agency persona's journey (TestJourneyAgency*); this
// story replays what round 2 found broken.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// TestStoryFClientRepo: the CTO links a client's monorepo to a client store
// on an empty remote and works there; nothing internal reaches the client's
// tree or history (D-63, D-64, D-65, D-66, D-67, D-68, D-76, D-77, D-78,
// D-92, D-93, D-95).
func TestStoryFClientRepo(t *testing.T) {
	s := New(t, "F")
	store := s.Remote(fStoreSlug)
	clientStore := s.Remote("github.com/fabrikam-co/lore-client-shopco")
	app := s.Seed(s.Remote(fAppSlug), func(dir string) { PlainRepo(t, dir) })
	shop := s.Seed(s.Remote(fShopSlug), func(dir string) { Monorepo(t, dir) })
	php := s.Seed(s.Remote(fPHPSlug), func(dir string) { PlainRepo(t, dir) })
	cto, platform := fPerson(s, fCTO, app)
	var sc, pc *teamharness.Clone
	var phpTree, shopRefs, phpRefs string
	key := FakeSecret("F-client")

	s.Step("day1/admin-starts-the-org", func() {
		journeys.StartTeam(t, cto, platform.Dir, fSetup, fOrg, store.SSHURL(), fCTO, []string{fFolders})
		cto.LoreIn(platform, "init").OK(t)
		sc, pc = fCodeClone(s, cto, shop, "webshop"), fCodeClone(s, cto, php, "billing-php")
		phpTree, shopRefs, phpRefs = TreeOf(t, pc.Dir), s.RefsOf(shop), s.RefsOf(php)
	})

	s.Step("day1/client-store-on-an-empty-remote-and-the-link", func() {
		// D-67: the client store's remote is empty (no branch main yet);
		// adding it and linking the client repo to it must just work.
		r1 := cto.LoreIn(platform, "org", "store", "add", "--org", fOrg, "--url", clientStore.SSHURL(), "--yes").OK(t).Never(t, "no branch main")
		r2 := cto.LoreIn(sc, "org", "link", "--org", fOrg, "--project", fClientDir, "--store", clientStore.SSHURL(), "--yes").OK(t).Says(t, "linked").Never(t, "no branch main")
		s.NamedFlags(t, r1, r2)
		fNoLoreIn(t, sc, "org link")
	})

	var task string
	s.Step("day1/work-in-the-client-repo-goes-to-the-client-store", func() {
		// D-68: the first write after the link works the first time.
		task = fTaskOf(t, cto.LoreIn(sc, "task", "add", "--title", "checkout page badge").OK(t).Says(t, fClientDir))
		// D-77: a plan follows the link, never into the client's .lore/data.
		cto.LoreIn(sc, "plan", "add", "--title", "Q3 search revamp", "--body", "phase one indexes").OK(t).Says(t, fClientDir)
		// D-93: decision and reminder too, with no hooks wired and no init hint.
		cto.LoreIn(sc, "decision", "add", "--title", "cache product pages", "--body", "edge cache for an hour").OK(t).Never(t, "lore init", "E_NOT_FOUND")
		cto.LoreIn(sc, "reminder", "add", "--message", "renew the shop's TLS cert", "--due", "tomorrow").OK(t).Never(t, "lore init", "E_NOT_FOUND")
		// D-76: a comment follows its task's store.
		cto.LoreIn(sc, "comment", "add", "--on-table", "tasks", "--on-id", task, "--body", "shipped to staging").OK(t).Never(t, "E_LEAK_BLOCKED")
		// D-78: a package folder inside the linked repo is linked too.
		cto.LoreIn(sc.In(filepath.Join("libs", "ui")), "task", "add", "--title", "ui: dark mode tokens").OK(t).Never(t, "no internal project is linked")
		cto.LoreIn(sc, "task", "list").OK(t).Says(t, "checkout page badge", "ui: dark mode tokens")
		// D-145: lore where lists the live client clone as using the client
		// store's cache, never "gone" (it has no .lore folder, only .git).
		cto.LoreIn(sc, "where").OK(t).Says(t, sc.Dir).Never(t, "gone: moved or deleted")
		fNoLoreIn(t, sc, "writes from the client repo")
	})

	s.Step("day1/render-and-the-client-ci-guard", func() {
		// D-65, D-66: render and check leaks --ci work in a linked client
		// repo that has no .lore folder of its own.
		cto.LoreIn(sc, "render").OK(t).Says(t, "client repo").Never(t, "E_NOT_A_STORE", "lore init")
		cto.LoreIn(sc, "check", "leaks", "--ci").OK(t).Never(t, "project root")
		fNoLoreIn(t, sc, "render and check leaks --ci")
	})

	s.Step("day1/secrets-in-the-client-repo", func() {
		// D-92: a write refused for a secret leaves no audit file behind.
		r := cto.LoreIn(sc, "decision", "add", "--title", "env", "--body", "the key is "+key).Refused(t, "secret")
		// D-95: --secret-unstored writes to the task's store and installs
		// nothing in the client clone.
		r2 := cto.LoreIn(sc, "comment", "add", "--on-table", "tasks", "--on-id", task, "--body", "staging key "+key, "--secret-unstored", "--to", fOrg).OK(t)
		s.NotPrinted(t, key, r, r2)
		fNoLoreIn(t, sc, "refused and unstored secrets")
	})

	s.Step("day2/internal-details-never-reach-a-client-commit", func() {
		s.AdvanceDays(1)
		// D-64: staged code that carries an internal actor id or a
		// teammate's org email is refused by the staged check (the client's
		// commit is checked even without lore's hooks in the clone).
		who := cto.LoreIn(platform, "whoami", "--json").OK(t)
		act := fActorID.FindString(who.Stdout)
		if act == "" {
			t.Fatalf("whoami printed no actor id: %s", who)
		}
		sc.WriteFile("services/billing/OWNERS", "owner: "+act+"\n")
		sc.Git("add", "services/billing/OWNERS")
		cto.LoreIn(sc, "check", "leaks").Refused(t, "OWNERS")
		sc.Git("rm", "-q", "--cached", "services/billing/OWNERS")
		if err := os.Remove(filepath.Join(sc.Dir, "services/billing/OWNERS")); err != nil {
			t.Fatal(err)
		}
		// a clean code change goes through, and the client's history holds
		// nothing of the org's (the CTO's own email is the commit's author,
		// which the client sees anyway; a teammate's is internal).
		sc.WriteFile("libs/ui/src/theme.ts", "export const theme = \"dark\";\n")
		sc.CommitAll("feat(ui): dark theme")
		cto.LoreIn(sc, "check", "leaks", "--as-client").OK(t)
		sc.Git("push", "-q", "origin", "HEAD:main")
		fNothingInternal(t, shop, act, fLead.Email, fOrg, clientStore.Slug, store.Slug)
	})

	s.Step("day2/setup-team-step-in-the-client-repo-writes-nothing-there", func() {
		// D-63: the team step run from inside the client repo writes no
		// internal store url into it.
		b, err := json.Marshal(map[string]string{"org": fOrg, "store_url": store.SSHURL()})
		if err != nil {
			t.Fatal(err)
		}
		cto.LoreIn(sc, "setup", "apply", fSetup, "--step", "team", "--answers", string(b), "--json")
		fNoLoreIn(t, sc, "setup apply --step team")
	})

	s.Step("day2/rules", func() {
		s.Untouched(t, pc.Dir, phpTree, "the client-repo day")
		s.NoPushSince(t, php, phpRefs, "the client-repo day")
		if got := s.RefsOf(shop); got == shopRefs {
			t.Errorf("the client repo's own git push did not reach it")
		}
		for _, f := range []string{"OWNERS"} {
			if strings.Contains(sc.Git("log", "--all", "--name-only", "--format="), f) {
				t.Errorf("a refused file %s reached the client history", f)
			}
		}
	})
}

// TestStoryFCI: the internal repo (still lore 0.1.17 format 1) as every CI
// variant sees it: a depth-1 clone, a read-only HOME, no git identity, a
// detached HEAD on a pull request's merge ref, CI=true and
// GITHUB_ACTIONS=true. Every read works, exits 0, writes nothing into the
// clone, pushes nothing and leaves the format alone (D-73, D-79, D-80, D-81).
func TestStoryFCI(t *testing.T) {
	s := New(t, "F")
	app := s.Seed(s.Remote(fAppSlug), func(dir string) { V017Repo(t, dir, "platform", SmallShape) })
	dev := s.Person(fLead.Name, fLead.Email)

	s.Step("day1/a-pull-request-and-its-merge-ref", func() {
		c := dev.Clone(app, "platform")
		c.Git("checkout", "-q", "-b", "feature/retry")
		c.WriteFile("retry.go", "package main\n\nconst retries = 3\n")
		c.CommitAll("feat: retry")
		c.Git("push", "-q", "origin", "feature/retry")
		// the host's merge ref of the pull request: the branch merged on main
		c.Git("checkout", "-q", "main")
		c.Git("merge", "-q", "--no-ff", "--no-edit", "feature/retry")
		c.Git("push", "-q", "origin", "HEAD:refs/pull/1/merge")
		c.Git("reset", "-q", "--keep", "HEAD~1")
	})

	s.Step("day1/every-ci-variant-reads-and-changes-nothing", func() {
		ci := s.Persona("CI Runner", "ci@example.test").Laptop()
		fNoIdentity(s, ci)
		ci.SetEnv("CI", "true")
		ci.SetEnv("GITHUB_ACTIONS", "true")
		shallow := s.ShallowClone(t, ci, app, "platform-shallow", 1)
		merge := ci.Clone(app, "platform-pr")
		merge.Git("fetch", "-q", "--depth", "1", "origin", "refs/pull/1/merge")
		merge.Git("checkout", "-q", "--detach", "FETCH_HEAD")
		refs := s.RefsOf(app)
		// the job cannot write its HOME; its workspace (the clones) it can
		fReadOnly(t, ci.Home, true)
		defer fReadOnly(t, ci.Home, false)
		for _, c := range []*teamharness.Clone{shallow, merge} {
			fReadOnly(t, c.Dir, false)
		}
		for _, c := range []*teamharness.Clone{shallow, merge} {
			before := TreeOf(t, c.Dir)
			status := c.Git("status", "--porcelain", "--ignored")
			for _, args := range [][]string{
				// render --check of a fresh clone (D-73) is the journey
				// TestJourneyCIShallowReadOnlyHome's; this 0.1.17 project's
				// rules are over the codex budget, which --check rightly
				// reports.
				{"check", "leaks"},
				{"task", "list"},
				{"search", "--query", "retry"},
			} {
				r := ci.LoreIn(c, args...).OK(t).Never(t, "E_READ_ONLY_FS", "E_REFUSED")
				s.NamedFlags(t, r)
			}
			ci.LoreIn(c, "doctor").Never(t, "DEGRADED", "E_READ_ONLY_FS")
			s.Untouched(t, c.Dir, before, "CI reads")
			if got := c.Git("status", "--porcelain", "--ignored"); got != status {
				t.Errorf("CI reads changed git status (e.g. .lore/lore.db-wal):\nbefore:\n%s\nafter:\n%s", status, got)
			}
			if bad := NewFormatFiles(t, c.Dir); len(bad) != 0 {
				t.Errorf("CI reads raised the 0.1.17 project's format: %v", bad)
			}
		}
		s.NoPushSince(t, app, refs, "CI reads")
	})
}

// TestStoryFPeople: a newhire joins, a leaver leaves with open tasks and
// loses access, an agent session starts, a --to write from a repo without
// lore data, a second org on the CTO's laptop (D-85, D-86, D-87, D-96,
// D-98, D-99).
func TestStoryFPeople(t *testing.T) {
	s := New(t, "F")
	store := s.Remote(fStoreSlug)
	app := s.Seed(s.Remote(fAppSlug), func(dir string) { PlainRepo(t, dir) })
	php := s.Seed(s.Remote(fPHPSlug), func(dir string) { PlainRepo(t, dir) })
	cto, platform := fPerson(s, fCTO, app)
	orgStore := fOrg + "/org"
	const rule = "every estimate is reviewed by two people"

	s.Step("day1/admin-starts-the-org", func() {
		journeys.StartTeam(t, cto, platform.Dir, fSetup, fOrg, store.SSHURL(), fCTO, []string{fFolders})
		cto.LoreIn(platform, "init").OK(t)
		cto.LoreIn(platform, "rule", "add", "--to", orgStore, "--body", rule).OK(t)
		platform.CommitAll("lore: adopt")
		platform.Git("push", "-q", "origin", "HEAD:main")
		cto.LoreIn(platform, "sync").OK(t)
	})

	people := map[string]*journeys.Machine{}
	clones := map[string]*teamharness.Clone{}
	s.Step("week2/newhire-and-leaver-join-with-the-printed-line", func() {
		s.AdvanceDays(7)
		for _, p := range []journeys.Person{fNewhire, fLeaver} {
			_, line := journeys.Invite(t, cto, platform.Dir, p.Email)
			m, c := fPerson(s, p, app)
			journeys.Join(t, m, c.Dir, fJoinSetup, line, p, []string{fFolders})
			people[p.Email], clones[p.Email] = m, c
		}
		people[fNewhire.Email].LoreIn(clones[fNewhire.Email], "rule", "list").OK(t).Says(t, rule)
	})

	s.Step("week2/an-agent-session-leaves-doctor-healthy", func() {
		// D-96: an agent started by a member is in the roster; nobody's
		// doctor goes DEGRADED over it.
		nh := people[fNewhire.Email]
		nh.LoreIn(clones[fNewhire.Email], "agent", "start", "--label", "agent-frontend").OK(t)
		nh.LoreIn(clones[fNewhire.Email], "sync").OK(t)
		cto.LoreIn(platform, "sync").OK(t)
		cto.LoreIn(platform, "doctor").Never(t, "roster does not hold")
	})

	s.Step("week3/the-leaver-is-removed-and-the-work-handed-over", func() {
		s.AdvanceDays(7)
		// the leaver is up to date the morning they are removed, so their
		// next write does not refresh the roster first (the unsynced write
		// of D-85 is one made before they learn of the removal)
		people[fLeaver.Email].LoreIn(clones[fLeaver.Email], "sync").OK(t)
		cto.LoreIn(platform, "org", "link", fOrg, "--project", "platform", "--yes").OK(t)
		cto.LoreIn(platform, "task", "add", "--title", "billing retry", "--assign", fLeaver.Email).OK(t)
		cto.LoreIn(platform, "task", "add", "--title", "retry runbook", "--assign", fLeaver.Email).OK(t)
		r := cto.LoreIn(platform, "member", "remove", "--who", fLeaver.Email, "--reason", "left the company", "--yes").OK(t).
			Says(t, "open tasks (2)", "billing retry", "retry runbook", "lore task reassign --from")
		s.NamedFlags(t, r)
		// D-86: --assign naming the leaver says they left, never "register".
		cto.LoreIn(platform, "task", "reassign", "--from", fLeaver.Email, "--assign", fLeaver.Email, "--yes").
			Refused(t, "left "+fOrg).Never(t, "actor register", "no one is")
		cto.LoreIn(platform, "task", "reassign", "--from", fLeaver.Email, "--assign", "@nia", "--yes").OK(t).Says(t, "2 tasks reassigned")
		cto.LoreIn(platform, "sync").OK(t)
	})

	s.Step("week3/the-leavers-unsynced-write-is-not-pushed", func() {
		// D-85: what the leaver writes after the removal stays on their
		// machine; their next sync says why and sends nothing.
		lv, lc := people[fLeaver.Email], clones[fLeaver.Email]
		before := s.RefsOf(store)
		lv.LoreIn(lc, "memory", "add", "a note after leaving", "--to", orgStore, "--yes").OK(t)
		lv.LoreIn(lc, "sync").Says(t, "marked as left", "kept on this machine")
		s.NoPushSince(t, store, before, "a removed member's sync")
	})

	s.Step("week3/lost-access-is-named", func() {
		// D-87: the store now refuses the newhire (the host answers "no
		// repository", as it does once access is removed): a write is kept
		// locally (writes wait for lore sync) and never sends the person to
		// "check the store's address"; doctor says access was most likely
		// removed, never "lore store init" over the org's store. (sync calling a vanished store "offline" is D-130, story H.)
		nh, nc := people[fNewhire.Email], clones[fNewhire.Email]
		gone := store.Dir + ".revoked"
		if err := os.Rename(store.Dir, gone); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.Rename(gone, store.Dir); err != nil {
				t.Fatal(err)
			}
		}()
		nh.LoreIn(nc, "memory", "add", "a note after access went", "--to", orgStore, "--yes").OK(t).Says(t, "committed locally").Never(t, "check the store's address")
		nh.LoreIn(nc, "doctor").Says(t, "access was most likely removed", "Do not run lore store init").Never(t, "not a lore store yet")
	})

	s.Step("week3/a-to-write-from-a-repo-without-lore-data", func() {
		// D-98: --to <org>/<folder> from inside a repo nobody ran lore init
		// in lands in the org store; the repo stays byte-identical.
		pc := fCodeClone(s, cto, php, "billing-php")
		tree := TreeOf(t, pc.Dir)
		cto.LoreIn(pc, "memory", "add", "php: the invoice pdf font is vendored", "--to", orgStore, "--yes").OK(t).Never(t, "lore init", "E_NOT_A_STORE")
		s.Untouched(t, pc.Dir, tree, "a --to write")
	})

	s.Step("week4/a-second-org-on-the-ctos-laptop", func() {
		// D-99: the second org's first store commit carries that org's
		// identity, not the first org's email.
		s.AdvanceDays(7)
		other := s.Remote("github.com/contoso-co/lore-team")
		tools := s.Seed(s.Remote("github.com/contoso-co/tools"), func(dir string) { PlainRepo(t, dir) })
		tc := cto.Clone(tools, "tools")
		cto.MoveClone(t, tc, filepath.Join(cto.Home, "contoso", "tools"))
		me := journeys.Person{Name: fCTO.Name, Email: "cleo@contoso.example.test", Login: fCTO.Login}
		journeys.StartTeam(t, cto, tc.Dir, "contoso-admin", "contoso", other.SSHURL(), me, []string{"~/contoso/**"})
		for _, a := range other.Authors("refs/heads/main") {
			if strings.Contains(a, fCTO.Email) {
				t.Errorf("the second org's store has a commit authored with the first org's email: %q", a)
			}
		}
	})
}
