//go:build journeys && !windows

package journeys

// storealias_test.go — devco D-493 (and the D-480 shape): a member whose
// store clone's origin names the store through an ssh host alias of theirs
// (git@<alias>:owner/repo.git, where ~/.ssh/config says Host <alias> /
// HostName github.com, the way a person with several GitHub accounts
// joins) still has that clone matched to the org's entry
// (git@github.com:owner/repo.git): reads at the clone top label rows with
// the org, never with the repository's name, and a read in the clone's
// folder lists that folder plus the org rows.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// saOrgRule is the org-wide rule every folder read must also show.
const saOrgRule = "org wide rule for aliases"

// saSyncDir is the sync folder of m's clone of the team store (the one
// folder named sync under m's lore home stores/).
func saSyncDir(t testing.TB, m *Machine) string {
	t.Helper()
	var dirs []string
	_ = filepath.Walk(m.LorePath("stores"), func(p string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() && filepath.Base(p) == jssSync {
			dirs = append(dirs, p)
		}
		return nil
	})
	if len(dirs) != 1 {
		t.Fatalf("want one %s folder in %s's store clones, got %v", jssSync, m.Home, dirs)
	}
	return dirs[0]
}

func TestJourneyStoreCloneThroughSSHAliasIsTheOrgStore(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	ann := tt.person(tlAnn)
	parent := filepath.Join(lee.Home, "code")

	w.Step("day1/lee-fills-a-folder-and-the-org", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", jssSync).OK(t)
		lee.Lore(parent, "rule", "add", "--to", tlOrg+"/"+jssSync, "--body", jssSyncRule, "--yes").OK(t)
		lee.Lore(parent, "rule", "add", "--to", tlOrg+"/org", "--body", saOrgRule, "--yes").OK(t)
		lee.Lore(parent, "sync").OK(t)
	})

	w.Step("day2/ann-joins-then-reaches-the-store-through-her-ssh-alias", func() {
		_, line := tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlAnn.Email)
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{"~/**"})
		sync := saSyncDir(t, ann)
		top := filepath.Dir(sync)
		const alias = "work-alias"
		// The alias is known only from ann's ssh config: lore must resolve
		// it there to tell the clone is the org's store.
		sshDir := filepath.Join(ann.Home, ".ssh")
		if err := os.MkdirAll(sshDir, 0o700); err != nil {
			t.Fatal(err)
		}
		cfg := "Host " + alias + "\n  HostName github.com\n  User git\n"
		if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
		origin := strings.TrimSpace(w.Git(top, "config", "--get", "remote.origin.url"))
		if !strings.HasPrefix(origin, "git@github.com:") {
			t.Fatalf("ann's clone origin %q is not the github ssh url", origin)
		}
		w.Git(top, "remote", "set-url", "origin", strings.Replace(origin, "git@github.com:", "git@"+alias+":", 1))
		// This world has no ssh server: git's transport for the alias goes
		// to the same fake host github.com uses. lore never reads this.
		gc := filepath.Join(ann.Home, ".gitconfig")
		w.Git(ann.Home, "config", "--file", gc, "--add", "url."+filepath.Join(w.Root, "hosts", "github.com")+"/.insteadOf", "git@"+alias+":")

		repo := filepath.Base(top)
		ann.Lore(top, "rule", "list").OK(t).Says(t, jssSyncRule, saOrgRule, "["+tlOrg+"/").Never(t, "["+repo+"/")
		ann.Lore(sync, "rule", "list").OK(t).Says(t, jssSyncRule, saOrgRule).Never(t, "(no rules)", "["+repo+"/")
		ann.Lore(sync, "rule", "list", "--store", tlOrg+"/"+jssSync).OK(t).Says(t, jssSyncRule)
	})
}
