//go:build stories && !windows

package stories

// O_client_gitwiring_test.go — devco:D-754 (hunt H-13) on the owner-shaped
// machine (story O): a repo lore set up as an own project (row files and
// lore's .gitattributes block committed) that the org later names a client
// repo. An older lore wrote its .gitattributes block without the lines a
// newer lore adds. `lore doctor --fix` there rewrote the tracked
// .gitattributes while the same run said "agent hooks: skipped: a client
// repo (lore never changes its tracked files)". Now the run changes no
// tracked file: the merge rules go to the clone's own .git/info/attributes,
// and git status stays clean.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOClientRepoGitWiringLocal(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	billing := s.Seed(s.Remote("github.com/acme-client/billing"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	bc := ada.CloneTo(billing, ada.OrgDir("billing"))
	oSetupFlow(t, ada, bc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	attrsPath := filepath.Join(bc.Dir, ".gitattributes")

	s.Step("D-754/set-up-as-own-by-an-older-lore-then-named-a-client-repo", func() {
		ada.Lore(bc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/acme-client/billing", "--yes").OK(t)
		ada.Lore(bc.Dir, "init", "--yes").OK(t)
		ada.Lore(bc.Dir, "memory", "add", "--body", "Invoices close on the 3rd working day.", "--to", "repo").OK(t)
		// as an older lore wrote it: the block without the lines a newer lore adds
		b, err := os.ReadFile(attrsPath)
		if err != nil {
			t.Fatal(err)
		}
		var kept []string
		for _, l := range strings.Split(string(b), "\n") {
			if !strings.Contains(l, "LORE.full.md") && !strings.Contains(l, "rules/lore") {
				kept = append(kept, l)
			}
		}
		writeFile(t, attrsPath, strings.Join(kept, "\n"))
		bc.Git("add", "-A")
		bc.TryGit("commit", "-q", "--no-verify", "-m", "lore (older)").OK(t)
		ada.Lore(bc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--remove", "github.com/acme-client/billing", "--yes").OK(t)
		ada.Lore(bc.Dir, "org", "config", "set", "--org", oOrg, "--key", "repos", "--add", "github.com/acme-client/*", "--yes").OK(t)
		ada.Lore(bc.Dir, "whoami").Says(t, "client repo")
	})

	s.Step("D-754/doctor-fix-changes-no-tracked-file-of-the-client-repo", func() {
		before, _ := os.ReadFile(attrsPath)
		out := ada.Lore(bc.Dir, "doctor", "--fix", "--yes").Out()
		if !strings.Contains(out, "lore never changes its tracked files") {
			t.Fatalf("doctor --fix did not treat the repo as a client repo:\n%s", out)
		}
		if after, _ := os.ReadFile(attrsPath); string(after) != string(before) {
			t.Fatalf("doctor --fix rewrote the client repo's tracked .gitattributes while saying lore never changes its tracked files:\n%s\n--- output:\n%s", after, out)
		}
		if st := strings.TrimSpace(bc.Git("status", "--porcelain", "--", ".gitattributes", ".gitignore")); st != "" {
			t.Fatalf("doctor --fix left tracked files changed in a client repo: %q\n%s", st, out)
		}
		if strings.Contains(out, "wait for your commit") {
			t.Fatalf("doctor --fix asks to commit lore's lines in a client repo:\n%s", out)
		}
		local, err := os.ReadFile(filepath.Join(bc.Dir, ".git", "info", "attributes"))
		if err != nil || !strings.Contains(string(local), "LORE.full.md text eol=lf merge=lore-keep-ours") {
			t.Fatalf("lore's merge rules are not in the clone's own .git/info/attributes: %q %v", local, err)
		}
	})
}
