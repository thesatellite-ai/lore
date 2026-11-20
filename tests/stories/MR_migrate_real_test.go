//go:build stories && !windows

package stories

// MR_migrate_real_test.go — story MR, `lore migrate run` on real repos
// (owner 2026-11-19, "bugs found migrating real repos"). An admin's org
// holds a lore 0.1.17 lore-org.yaml with routing defaults; two of their
// repos are still on the lore 0.1.17 format with lore's commit hooks in
// place: one is a client repo (its remote matches no own pattern, so it is
// a client repo, fail closed), the other an own repo of the org.
//
//   - devco:D-705: the migration result, committed through lore's own
//     pre-commit and commit-msg hooks, goes through in both: _meta.json's
//     migrated_by names the person by name, never their roster email (the
//     client repo's leak check refused that very commit) nor actor id.
//   - devco:D-708: the client repo carries the lore-sync-merge workflow an
//     older lore pinned; `lore migrate run --dry-run` says the workflow
//     update would be written and leaves the repo tree, .git/config, its
//     hooks and the lore home exactly as they were (the dry run rewrote the
//     workflow while printing "Nothing written").
//   - devco:D-705 follow-up: a _meta.json lore 0.1.18 already wrote with
//     migrated_by "Name <email>": the commit lore's hook refuses names
//     `lore migrate run`, which keeps the name only, and the commit after
//     it goes through the same hooks.
//   - devco:D-759: the own repo committed 0.1.17's LORE.md and never had
//     a LORE.full.md; the migration commit lore prints carries the full
//     file the pre-commit refresh writes, so git status is clean after it.
//   - devco:D-707: the run also moves the org's lore-org.yaml defaults into
//     a policy row of the org store; it lists the store commits it made
//     (store, commit, what) and says `lore sync` sends them, and the org
//     store's remote does not move.
//
// Every name, email and url is made up.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const (
	mrOrg       = "acmeco"
	mrSetup     = "acmeco-admin"
	mrStoreSlug = "github.com/acme-co/lore-team"
	mrAppSlug   = "github.com/acme-co/platform"
	mrToolsSlug = "github.com/acme-co/tools"
	mrShopSlug  = "github.com/globex-shop/legacy-shop"
	// mrOrgDefaults is the routing block lore 0.1.17 kept in lore-org.yaml.
	mrOrgDefaults = "defaults:\n  client_repos:\n    memory: repo\n    decision: " + mrOrg + "\n"
	// mrClientDir is the org store folder the shop is linked to as a client.
	mrClientDir = "client_shop"
	// mrPinVersion is the release the test build pins the workflow to
	// (LORE_TEST_PIN_VERSION: a test binary is a development build).
	mrPinVersion = "0.1.19"
	// mrWorkflow is the lore-sync-merge workflow file of a repo.
	mrWorkflow = ".github/workflows/lore-sync-merge.yml"
)

var mrAdmin = journeys.Person{Name: "Cora Admin", Email: "cora@example.test", Login: "cora-gh"}

// mrStoreCommitLine is the run's line for one store commit it made: the
// store, the short commit and what it holds.
var mrStoreCommitLine = regexp.MustCompile(`store commit [0-9a-f]{7} in ` + mrOrg + `[^\n]*: lore: migrate org defaults to policies`)

// mrMigratedAt is _meta.json's migrated_at line.
var mrMigratedAt = regexp.MustCompile(`"migrated_at": "[^"]*"`)

// mrHooks installs lore's pre-commit and commit-msg entry points in c, as
// lore 0.1.17 left them in every clone it worked in: the same hook
// commands lore's blocks run, so a migration commit goes through lore's
// own checks (the leak check of the commit-msg block included).
func mrHooks(t testing.TB, c *teamharness.Clone) {
	t.Helper()
	hooks := strings.TrimSpace(c.Git("rev-parse", "--path-format=absolute", "--git-path", "hooks"))
	writeExec(t, filepath.Join(hooks, "pre-commit"), "#!/bin/sh\nlore sync hook pre-commit || exit $?\n")
	writeExec(t, filepath.Join(hooks, "commit-msg"), "#!/bin/sh\nlore task hook commit-msg --check-leaks \"$1\" || exit $?\nlore check leaks --staged --commit-msg \"$1\" || exit $?\n")
}

// mrOrgFileWithDefaults puts lore 0.1.17's routing defaults into the org
// store's lore-org.yaml on its remote (what an org set up with 0.1.17
// carries), from a scratch clone.
func mrOrgFileWithDefaults(t testing.TB, s *Story, store *teamharness.Remote) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "orgfile")
	s.Git(filepath.Dir(dir), "clone", "-q", store.Dir, dir)
	p := ""
	for _, c := range []string{filepath.Join(dir, "org", "lore-org.yaml"), filepath.Join(dir, "lore-org.yaml")} {
		if _, err := os.Stat(c); err == nil {
			p = c
			break
		}
	}
	if p == "" {
		t.Fatalf("the org store has no lore-org.yaml: %s", s.Git(dir, "ls-files"))
	}
	appendFile(t, p, mrOrgDefaults)
	s.Git(dir, "-c", "user.name=Org Setup", "-c", "user.email=setup@example.test", "commit", "-qam", "org defaults as lore 0.1.17 kept them")
	s.Git(dir, "push", "-q", "origin", "HEAD")
}

// TestStoryMRMigrateRealRepos walks the migration of a client and an own
// repo on the lore 0.1.17 format (D-705, D-707).
func TestStoryMRMigrateRealRepos(t *testing.T) {
	s := New(t, "MR")
	store := s.Remote(mrStoreSlug)
	app := s.Seed(s.Remote(mrAppSlug), func(dir string) { PlainRepo(t, dir) })
	shop := s.Seed(s.Remote(mrShopSlug), func(dir string) { V017Repo(t, dir, "legacy-shop", SmallShape) })
	tools := s.Seed(s.Remote(mrToolsSlug), func(dir string) { V017Repo(t, dir, "tools", SmallShape) })
	m := s.Persona(mrAdmin.Name, mrAdmin.Email).Laptop()
	journeys.GitHubLogin(s.T, s.World, m, mrAdmin.Login)
	var platform, sc, tc *teamharness.Clone
	clone := func(r *teamharness.Remote, name string) *teamharness.Clone {
		c := m.Clone(r, name)
		m.MoveClone(t, c, filepath.Join(m.Home, "code", name))
		return c
	}

	s.Step("day1/org-with-0117-defaults-and-two-0117-repos", func() {
		platform = clone(app, "platform")
		journeys.StartTeam(t, m, platform.Dir, mrSetup, mrOrg, store.SSHURL(), mrAdmin, []string{"~/code/**"})
		mrOrgFileWithDefaults(t, s, store)
		m.Lore(platform.Dir, "sync").OK(t)
		sc, tc = clone(shop, "legacy-shop"), clone(tools, "tools")
		// D-708: the workflow as an older lore installed it (auto pin: each
		// newer lore moves it to its own release)
		m.LoreIn(sc, "sync", "install-action").OK(t)
		sc.Git("add", mrWorkflow)
		sc.Git("commit", "-q", "-m", "lore-sync-merge workflow")
		mrHooks(t, sc)
		mrHooks(t, tc)
		// the shop is the client's code: linked as a client repo of the org
		m.LoreIn(sc, "org", "link", "--org", mrOrg, "--project", mrClientDir, "--yes").OK(t)
	})

	var storeBefore string
	s.Step("day1/dry-run-says-the-org-store-step", func() {
		// the shop's remote matches no own pattern of the org: a client repo
		// (fail closed); the tools repo is the org's own
		m.LoreIn(sc, "whoami").OK(t).Says(t, "client repo")
		m.LoreIn(tc, "whoami").OK(t).Says(t, "own repo")
		// D-708: a dry run writes nothing, ever, and nothing of the repo or
		// home moved; D-742: no command but install-action and doctor --fix
		// moves the workflow pin, so the run names that command instead
		m.SetEnv("LORE_TEST_PIN_VERSION", mrPinVersion)
		roots := []string{sc.Dir, m.LorePath()}
		before := drySnap(t, roots...)
		dry := m.LoreIn(sc, "migrate", "run", "--dry-run").OK(t)
		dry.Says(t, "org_defaults_to_policy", "Nothing written (dry run)",
			mrWorkflow+" pins lore ", "; run `lore sync install-action` to update it to v"+mrPinVersion)
		if d := dryDiff(before, drySnap(t, roots...)); len(d) > 0 {
			t.Errorf("lore migrate run --dry-run changed files:\n%s\n%s", strings.Join(d, "\n"), dry)
		}
		storeBefore = s.RefsOf(store)
	})

	s.Step("day1/client-repo-migrates-and-commits-through-the-hooks", func() {
		mr := m.LoreIn(sc, "migrate", "run").OK(t)
		// D-705: no person's address or actor id in the committed file, and
		// lore's own hooks take the migration commit
		meta, err := os.ReadFile(filepath.Join(sc.Dir, ".lore", "data", "_meta.json"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(meta), "@") || fActorID.Match(meta) {
			t.Errorf("_meta.json names a person's email or actor id:\n%s", meta)
		}
		before := sc.Head()
		sc.Git("add", ".lore/data")
		r := sc.TryGit("commit", "-q", "-m", "lore: migrate rows to format 2")
		if r.Code != 0 || strings.Contains(r.Stdout+r.Stderr, "E_LEAK_BLOCKED") || sc.Head() == before {
			t.Errorf("lore's own hooks refused the migration commit in a client repo: %s", r)
		}
		// D-707: the store commits the run made, each with what it holds,
		// and that lore sync sends them; the store's remote did not move
		if !mrStoreCommitLine.MatchString(mr.Stdout + mr.Stderr) {
			t.Errorf("migrate run does not list the org store commit it made (store, commit, what):\n%s", mr)
		}
		s.NoPushSince(t, store, storeBefore, "lore migrate run (lore sync sends the store commits)")
		s.NamedFlags(t, mr)
		mr.Says(t, "org_defaults_to_policy", "by "+mrAdmin.Name+", ", "the store commits above are local: lore sync sends them")
		if got := strings.TrimSpace(sc.TryGit("config", "--get", "lore.clientrepo").Stdout); got != "true" {
			t.Errorf("lore.clientrepo = %q: the commit's leak check did not class the shop as a client repo (the case under test)", got)
		}
	})

	s.Step("day1/own-repo-migrates-and-commits-through-the-hooks", func() {
		m.LoreIn(tc, "migrate", "run").OK(t).Says(t, "by "+mrAdmin.Name+", ")
		before := tc.Head()
		tc.Git("add", ".lore/data")
		r := tc.TryGit("commit", "-q", "-m", "lore: migrate rows to format 2")
		if r.Code != 0 || tc.Head() == before {
			t.Fatalf("lore's own hooks refused the migration commit in an own repo: %s", r)
		}
		// D-759: the repo committed lore 0.1.17's LORE.md and never had a
		// LORE.full.md; the hook's refresh writes it, and it goes into the
		// same commit (named in the hook's line), so git status is clean
		// right after the commit lore printed
		if st := tc.Status(); st != "" {
			t.Errorf("git status after the migration commit lore printed is not clean:\n%s\n%s", st, r)
		}
		if !strings.Contains(tc.Git("show", "--name-only", "--format=", "HEAD"), ".lore/LORE.full.md") || !strings.Contains(r.Stderr, "(and added .lore/LORE.full.md beside it)") {
			t.Errorf("the migration commit does not carry the new .lore/LORE.full.md, or the hook does not name it:\n%s", r)
		}
	})

	s.Step("day1/0118-migrated-by-email-left-behind", func() {
		// D-705 follow-up: lore 0.1.18's migrate wrote "Name <email>" and the
		// client repo's hook refused the commit, leaving the file uncommitted
		p := filepath.Join(sc.Dir, ".lore", "data", "_meta.json")
		meta, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		left := strings.Replace(string(meta), `"migrated_by": "`+mrAdmin.Name+`"`, `"migrated_by": "`+mrAdmin.Name+` <`+mrAdmin.Email+`>"`, 1)
		if left == string(meta) {
			t.Fatalf("no migrated_by to age into lore 0.1.18's shape:\n%s", meta)
		}
		// 0.1.18's run had its own time: the file differs from the commit
		// beyond the email, as the uncommitted 0.1.18 migration did
		left = mrMigratedAt.ReplaceAllString(left, `"migrated_at": "2026-11-19T09:00:00Z"`)
		writeFile(t, p, left)
		before := sc.Head()
		sc.Git("add", ".lore/data")
		r := sc.TryGit("commit", "-q", "-m", "lore: migrate rows to format 2")
		if r.Code == 0 || sc.Head() != before || !strings.Contains(r.Stdout+r.Stderr, "run `lore migrate run`, which keeps the name only") {
			t.Fatalf("the hook's refusal of 0.1.18's _meta.json does not name lore migrate run: %s", r)
		}
		mr := m.LoreIn(sc, "migrate", "run").OK(t)
		mr.Says(t, "migrated_by names an email", "now keeps the name only ("+mrAdmin.Name+")")
		s.NamedFlags(t, mr)
		if b, rerr := os.ReadFile(p); rerr != nil || strings.Contains(string(b), "@") {
			t.Fatalf("_meta.json still names the email (%v):\n%s", rerr, b)
		}
		sc.Git("add", ".lore/data")
		r = sc.TryGit("commit", "-q", "-m", "lore: record who migrated by name")
		if r.Code != 0 || sc.Head() == before {
			t.Fatalf("lore's own hooks refused the commit after lore migrate run: %s", r)
		}
	})

	s.Step("day1/lore-sync-sends-the-store-commits", func() {
		m.LoreIn(sc, "sync").OK(t)
		if s.RefsOf(store) == storeBefore {
			t.Error("lore sync did not send the org store commits of the migration")
		}
	})
}
