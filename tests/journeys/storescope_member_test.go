//go:build journeys && !windows

package journeys

// storescope_member_test.go — devco D-480 guard: a read inside a folder of
// a person's own store clone lists that folder plus the org folder, in
// every home shape a joiner can have: a member's fresh home, the same clone
// reached through an ssh host alias origin (git@<alias>:owner/repo.git,
// routed to github.com the way ~/.ssh/config does), and an admin's second
// laptop whose member join was saved as an admin setup. The store test saw
// `(no rules)` there on one machine; none of these shapes shows it, so the
// journey keeps them from starting to.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// smOrgRule is the org-wide rule every folder read must also show.
const smOrgRule = "org wide rule here"

// smSyncDir is the sync folder of m's clone of the team store (the one
// folder named sync under m's lore home stores/).
func smSyncDir(t testing.TB, m *Machine) string {
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

// smReadsFolder checks every read in m's clone folder dir sees the folder
// rule and the org rule, and that the clone top does too.
func smReadsFolder(t testing.TB, m *Machine, dir string) {
	t.Helper()
	m.Lore(dir, "rule", "list").OK(t).Says(t, jssSyncRule, smOrgRule).Never(t, "(no rules)")
	m.Lore(dir, "rule", "list", "--store", tlOrg+"/"+jssSync).OK(t).Says(t, jssSyncRule)
	m.Lore(filepath.Dir(dir), "rule", "list").OK(t).Says(t, jssSyncRule, smOrgRule)
}

func TestJourneyStoreFolderReadsInEveryJoinerHome(t *testing.T) {
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
		lee.Lore(parent, "rule", "add", "--to", tlOrg+"/org", "--body", smOrgRule, "--yes").OK(t)
		lee.Lore(parent, "sync").OK(t)
		smReadsFolder(t, lee, smSyncDir(t, lee))
	})

	w.Step("day2/ann-joins-and-reads-in-her-clone-folder", func() {
		_, line := tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlAnn.Email)
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{"~/**"})
		smReadsFolder(t, ann, smSyncDir(t, ann))
	})

	w.Step("day2/ann-clone-origin-through-an-ssh-alias", func() {
		sync := smSyncDir(t, ann)
		top := filepath.Dir(sync)
		origin := strings.TrimSpace(w.Git(top, "config", "--get", "remote.origin.url"))
		const alias = "work-alias"
		w.Git(top, "remote", "set-url", "origin", strings.Replace(origin, "git@github.com:", "git@"+alias+":", 1))
		gc := filepath.Join(ann.Home, ".gitconfig")
		w.Git(ann.Home, "config", "--file", gc, "--add", "url."+filepath.Join(w.Root, "hosts", "github.com")+"/.insteadOf", "git@"+alias+":")
		smReadsFolder(t, ann, sync)
	})

	w.Step("day3/lee-second-laptop-saved-as-admin", func() {
		w.AdvanceDays(1)
		desk := w.personas[0].NewLaptop("desk")
		tlGitHub(t, w, desk, tlLee.Login)
		tlFirstSteps(t, desk, desk.Home, tlTeamSetup, "member", tlLee)
		tlApply(t, desk, desk.Home, tlTeamSetup, "join", map[string]any{"invite": tt.store.SSHURL()}).Says(t, "saved as an admin setup")
		tlLastSteps(t, desk, desk.Home, tlTeamSetup, []string{"~/**"})
		smReadsFolder(t, desk, smSyncDir(t, desk))
	})
}
