//go:build journeys && !windows

package journeys

// linkclean_test.go — devco:D-736 (owner use pass 5): a linked repo whose
// notes all moved to its store folder still tracked actor and policy rows
// in .lore/data (an owner saw 4, 1 and 2 rows in three repos). Lee seeds a
// repo, deletes and purges one memory there (a deletion record the
// folder never gets), links the repo and moves its notes out; the deletion record keeps
// the move from tidying up, so the actor row stays, as in a repo moved
// before D-731. `lore doctor` warns with the count and names the deletion
// record it keeps; `lore doctor --fix` moves the policy row to the folder
// (it has none) and removes the actor row the folder holds, as a diff
// lore's own pre-commit takes; what is left is the repo's identity
// (_meta.json, its project and repos rows) and the named deletion record.
// Reads, writes with no --to, policy show and branch mode still work, and
// `lore secrets allow --file` for a finding in a repo file writes its
// entry to the folder, which the commit check then honours.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const jLcSlug = "github.com/acme-co/billing-clean"

func TestJourneyLinkedRepoDoctorCleansRows(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	place := tlOrg + "/" + jMlFolder
	repo := jPlainRepo(t, lee, w.Remote(jLcSlug), filepath.Join(parent, "billing-clean"))
	var s jMlIDs
	var gone string

	w.Step("day1/a-linked-repo-with-notes-and-a-deletion-record", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", jMlFolder).OK(t)
		s = jMlSeed(t, lee, repo)
		gone = jMlAdd(t, lee, repo, "memory", "add", "--body", "an old note nobody needs", "--to", "repo")
		lee.LoreIn(repo, "memory", "delete", gone, "--yes").OK(t)
		lee.LoreIn(repo, "trash", "purge", "--id", gone, "--confirm", "--yes").OK(t)
		repo.CommitAll("lore: drop an old note")
		lee.LoreIn(repo, "org", "link", tlOrg, "--project", jMlFolder, "--yes").OK(t)
		if st := repo.Status(); st != "" {
			repo.CommitAll("lore: link")
		}
		lee.LoreIn(repo, "move", "--from", "repo", "--to", place, "--yes").OK(t).Says(t, "✓ moved ")
		repo.Git("add", "-A", ".lore/data")
		if c := repo.TryGit("commit", "-m", "lore: move the notes"); c.Code != 0 {
			t.Fatalf("the commit after the move failed: exit %d\n%s", c.Code, c.Out())
		}
		files := repo.Git("ls-files", ".lore/data")
		for _, want := range []string{"/actors/", "/policies/", "/memories/" + gone} {
			if !strings.Contains(files, want) {
				t.Fatalf("the setup wants %s left after the move:\n%s", want, files)
			}
		}
	})

	w.Step("day2/doctor-warns-fix-cleans-commit-passes", func() {
		w.AdvanceDays(1)
		// owner item 29: the identity lives in the link, so the project and
		// repos rows go too (4 = policy, actor, project, repos)
		lee.LoreIn(repo, "doctor").Says(t,
			"still holds 4 rows the folder already holds", "lore doctor --fix",
			"memories/"+gone+" stays: a deletion record "+place+" has no copy of")
		fix := lee.LoreIn(repo, "doctor", "--fix", "--yes").Says(t, "moved 1 policy row to "+place, "removed 3 rows "+place+" holds")
		fix.Never(t, "could not clean")
		repo.Git("add", "-A", ".lore/data")
		c := repo.TryGit("commit", "-m", "lore: clean the linked repo")
		if c.Code != 0 || strings.Contains(c.Out(), "by removing the file") {
			t.Fatalf("the commit after doctor --fix failed: exit %d\n%s", c.Code, c.Out())
		}
		// the hook names the command that removed the rows (D-736)
		if !strings.Contains(c.Out(), "`lore doctor --fix` moved to "+place) || strings.Contains(c.Out(), "adopt --remove-source") {
			t.Errorf("the pre-commit does not name lore doctor --fix:\n%s", c.Out())
		}
		files := repo.Git("ls-files", ".lore/data")
		for _, f := range strings.Split(strings.TrimSpace(files), "\n") {
			ok := f == ".lore/data/_meta.json" || f == ".lore/data/_purged.json" || strings.HasSuffix(f, "/memories/"+gone+".json")
			if !ok {
				t.Errorf("doctor --fix left %s in the linked repo:\n%s", f, files)
			}
		}
		// a second doctor has nothing to fix; the kept record is still named
		lee.LoreIn(repo, "doctor").Never(t, "lore doctor --fix").Says(t, "memories/"+gone+" stays")
	})

	w.Step("day2/secrets-allow-file-lands-in-the-folder", func() {
		// D-736 (secrets allow class): a finding in a repo file of the
		// cleaned linked repo is allowed with the printed line; the entry
		// lands in the folder (it refused "this repo has no lore project"
		// and wrote into the repo), and the commit check reads it there.
		pw := "Zq7" + strings.Repeat("x9K", 6) // built at run time: no password literal in the repo
		repo.WriteFile("config.yaml", "db:\n  password: \""+pw+"\"\n")
		repo.Git("add", "config.yaml")
		r := repo.TryGit("commit", "-q", "-m", "config").Refused(t, "E_LEAK_BLOCKED", "config.yaml").Never(t, pw)
		lines := allowLine.FindAllString(r.Out(), -1)
		if len(lines) == 0 {
			t.Fatalf("no ready allow line:\n%s", r.Out())
		}
		for _, l := range lines {
			args := append(strings.Fields(strings.TrimPrefix(l, "lore ")), "--reason", "sample password in a fixture", "--yes")
			lee.LoreIn(repo, args...).OK(t).Says(t, "marked not a secret", "saved in "+place).Never(t, "commit the new .lore/data")
		}
		if st := repo.Status(); strings.Contains(st, ".lore/data") {
			t.Errorf("secrets allow --file wrote into the cleaned linked repo:\n%s", st)
		}
		repo.TryGit("commit", "-q", "-m", "config").OK(t)
		lee.LoreIn(repo, "secrets", "allow", "list").OK(t).Says(t, "config.yaml")
	})

	w.Step("day2/reads-writes-policy-and-branch-mode-still-work", func() {
		lee.LoreIn(repo, "policy", "show").OK(t).Says(t, "context.targets")
		lee.LoreIn(repo, "branch", "mode").OK(t)
		for _, id := range s.all() {
			lee.LoreIn(repo, "show", "--id", id).OK(t).Never(t, "E_NOT_FOUND", "two live rows")
		}
		note := jMlAdd(t, lee, repo, "memory", "add", "--body", "written after the clean")
		if st := repo.Status(); strings.Contains(st, ".lore/data") {
			t.Errorf("a write with no --to touched the repo after the clean:\n%s", st)
		}
		lee.LoreIn(repo, "memory", "list", "--store", place, "--json").OK(t).Says(t, note)
		lee.LoreIn(repo, "memory", "list").OK(t).Says(t, note)
		// a render target added here is saved in the folder (D-736)
		lee.LoreIn(repo, "render", "--add-target", "cursor", "--yes").OK(t)
		lee.LoreIn(repo, "policy", "show").OK(t).Says(t, "cursor")
		repo.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(parent, "sync").OK(t)
		lee.LoreIn(repo, "sync").OK(t)
		if !jMlStoreHas(tt.store, "refs/heads/main", note) {
			t.Errorf("the folder has no row %s after sync", note)
		}
		// sync writes no repos row back: the identity is _meta.json + the link
		st := repo.Status()
		for _, l := range strings.Split(st, "\n") {
			if strings.Contains(l, ".lore/data/") && !strings.HasSuffix(l, "/_meta.json") {
				t.Errorf("render --add-target or sync wrote rows into the cleaned linked repo:\n%s", st)
				break
			}
		}
		if out, err := os.ReadDir(filepath.Join(repo.Dir, ".lore", "data", "repos")); err == nil && len(out) > 0 {
			t.Errorf("sync wrote %d repos rows back into the linked repo", len(out))
		}
		if out, err := os.ReadDir(filepath.Join(repo.Dir, ".lore", "data", "projects")); err == nil && len(out) > 0 {
			t.Errorf("a command wrote %d project rows back into the linked repo", len(out))
		}
		lee.LoreIn(repo, "whoami").OK(t).Says(t, place)
	})
}
