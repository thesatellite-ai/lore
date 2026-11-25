//go:build stories && !windows

package stories

// O_history_leaks_test.go — devco:D-870 (owner org-setup item 11, bug hunt
// H-118) on the owner-shaped machine (story O): the admin answers "was a
// key ever committed to this repo?" before opening it to a new team. The
// hooks, --ci and --as-client read only what a commit or push changes or
// the current files, so a key committed past the hooks and removed later
// was never reported. `lore check leaks --history` now reads every file
// version and commit message once and names each value with the commit
// that added it; each printed `not a secret?` line runs where printed and
// clears its finding. In a repo lore is not set up in, the commit check's
// `not a secret?` line is the .gitleaksignore entry (`lore secrets allow`
// refuses there); following it lets the commit through, and the history
// check accepts the committed line too.

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// oHistoryIgnore is the .gitleaksignore line the history check prints.
var oHistoryIgnore = regexp.MustCompile(`and commit it: ([0-9a-f]{40}:[^\s]+)`)

func TestStoryOHistoryLeakScan(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	scratch := s.Seed(s.Remote("github.com/"+oAdminPersonal.Login+"/scratch"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	removed := "AKIA" + strings.Repeat("Q", 16) // built at run time, never a literal key
	kept := "AKIA" + strings.Repeat("W", 16)
	var before string

	s.Step("D-870/a-key-committed-past-the-hooks-and-removed", func() {
		// a repo set up for lore: `lore secrets allow` saves its entries in .lore/data
		ada.Lore(pc.Dir, "init", "--non-interactive", "--name=platform").OK(t)
		pc.CommitAll("lore init")
		before = pc.Head()
		pc.WriteFile("deploy.env", "AWS_ACCESS_KEY_ID="+removed+"\n")
		pc.WriteFile("testdata/fixture.txt", "id="+kept+"\n")
		pc.Git("add", "-A")
		pc.Git("-c", "core.hooksPath=/dev/null", "commit", "-q", "--no-verify", "-m", "deploy settings")
		for i := range 3 { // later versions of the fixture keep the same value
			pc.WriteFile("testdata/fixture.txt", "id="+kept+"\n"+strings.Repeat("# more\n", i+1))
			pc.Git("add", "-A")
			pc.Git("-c", "core.hooksPath=/dev/null", "commit", "-q", "--no-verify", "-m", "fixture")
		}
		pc.Git("rm", "-q", "deploy.env")
		pc.Git("-c", "core.hooksPath=/dev/null", "commit", "-q", "--no-verify", "-m", "drop deploy settings")
		// the current-files check does not see the removed key
		ada.Lore(pc.Dir, "check", "leaks", "--ci", "--since", pc.Head()).Never(t, "deploy.env")
	})

	s.Step("D-870/history-names-each-value-once-with-its-commit", func() {
		r := ada.Lore(pc.Dir, "check", "leaks", "--history").
			Fails(t, 19, "E_LEAK_BLOCKED", "--history: 2 secret(s) in the history of HEAD",
				"deploy.env:1", "removed since, still in history",
				"testdata/fixture.txt:1", "still in the current files, in 4 versions of the file",
				"rotate each credential first").
			Never(t, removed, kept)
		out := r.Stdout + r.Stderr
		if strings.Count(out, "testdata/fixture.txt:1 ") != 1 {
			t.Errorf("one value in four versions is listed more than once:\n%s", out)
		}
		// --since: only the commits after it; the key added before it is not new
		// (platform is a client repo by the fail-closed default, so its notes are read too: D-994)
		ada.Lore(pc.Dir, "check", "leaks", "--history", "--since", pc.Head()).OK(t).Says(t, "✓", "no secret or internal note in 0 commit(s)")
		ada.Lore(pc.Dir, "check", "leaks", "--history", "--since", before).Fails(t, 19, "deploy.env:1")
	})

	s.Step("D-870/each-printed-not-a-secret-line-clears-its-finding", func() {
		r := ada.Lore(pc.Dir, "check", "leaks", "--history")
		out := r.Stdout + r.Stderr
		m := oHistoryIgnore.FindStringSubmatch(out)
		if m == nil || !strings.Contains(m[1], ":deploy.env:") {
			t.Fatalf("no .gitleaksignore line for the removed key:\n%s", out)
		}
		allow := ""
		for _, l := range strings.Split(out, "\n") {
			if _, cmd, ok := strings.Cut(l, "not a secret? lore secrets allow --file testdata/fixture.txt"); ok {
				allow = "lore secrets allow --file testdata/fixture.txt" + cmd
			}
		}
		if allow == "" {
			t.Fatalf("the value still in the files has no lore secrets allow line:\n%s", out)
		}
		args := strings.Fields(strings.TrimPrefix(strings.Replace(allow, `"<why>"`, "test-fixture", 1), "lore "))
		ada.Lore(pc.Dir, args...).OK(t)
		pc.WriteFile(".gitleaksignore", m[1]+"\n")
		pc.Git("add", "-A")
		pc.Git("commit", "-q", "-m", "accept the history findings")
		ada.Lore(pc.Dir, "check", "leaks", "--history").OK(t).Says(t, "✓ lore check leaks --history: no secret or internal note in")
	})

	s.Step("D-870/a-repo-lore-is-not-set-up-in-gets-the-gitleaksignore-line", func() {
		sc := ada.CloneTo(scratch, filepath.Join(ada.Home, "scratch"))
		sc.WriteFile("fixture.txt", "id="+kept+"\n")
		sc.Git("add", "-A")
		ada.Lore(sc.Dir, "check", "leaks", "--staged").
			Fails(t, 19, "not a secret? add this line to the repo's .gitleaksignore and stage it: fixture.txt:aws_access_key:1").
			Never(t, "not a secret? lore secrets allow", kept)
		sc.WriteFile(".gitleaksignore", "fixture.txt:aws_access_key:1\n")
		sc.Git("add", ".gitleaksignore")
		ada.Lore(sc.Dir, "check", "leaks", "--staged").OK(t)
		sc.Git("commit", "-q", "-m", "fixture")
		// the committed line is the history's accepted finding too
		ada.Lore(sc.Dir, "check", "leaks", "--history").OK(t).Says(t, "✓ lore check leaks --history: no secret in 2 commit(s)")
	})
}
