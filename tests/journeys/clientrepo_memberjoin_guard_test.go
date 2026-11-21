//go:build journeys && !windows

package journeys

// clientrepo_memberjoin_guard_test.go — devco D-802 (owner: a member's
// setup or join installs the client leak guard in the clone itself, and
// fails closed). Lee links the client repo cart to a store folder and adds
// an internal rule; Ann is invited, clones cart and joins the org from
// inside that clone. Her clone ends the join with lore's commit-msg and
// pre-push blocks and whoami says the guards are on, so a commit quoting
// the rule is refused. In a second clone whose hooks folder is committed
// with the repo (lore never writes the client's files), her next setup
// step stops with E_REFUSED and the lines to add, instead of leaving the
// clone unguarded in silence.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	mjSlug   = "github.com/client-co/basket"
	mjFolder = "client_basket"
	mjRule   = "Never quote the internal margin table in basket commits."
)

func TestJourneyClientRepoMemberJoinGuard(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	remote := w.Remote(mjSlug)
	var line string

	w.Step("day1/lee-links-the-client-repo-and-invites-ann", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		parent := filepath.Join(lee.Home, "code")
		lee.Lore(parent, "store", "folder", "add", "team", mjFolder).OK(t)
		basket := jPlainRepo(t, lee, remote, filepath.Join(parent, "basket"))
		lee.LoreIn(basket, "org", "link", tlOrg, "--project", mjFolder, "--yes").OK(t)
		lee.LoreIn(basket, "rule", "add", "--body", mjRule, "--severity", "must").OK(t).Says(t, mjFolder)
		lee.LoreIn(basket, "sync").OK(t)
		_, line = tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlAnn.Email)
	})

	ann := tt.person(tlAnn)
	w.Step("day1/ann-joins-from-her-clone-of-the-client-repo", func() {
		c := ann.Clone(remote, "basket")
		ann.MoveClone(t, c, filepath.Join(ann.Home, "code", "basket"))
		tlJoin(t, ann, c.Dir, tlTeamSetup, line, tlAnn, []string{tlFolders})
		for _, hook := range []string{"commit-msg", "pre-push"} {
			b, err := os.ReadFile(filepath.Join(c.Dir, ".git", "hooks", hook))
			if err != nil || !strings.Contains(string(b), "lore") {
				t.Fatalf("Ann's join left no lore %s block in her client clone (err %v):\n%s", hook, err, b)
			}
		}
		ann.LoreIn(c, "whoami").OK(t).Says(t, "guards on").Never(t, "guards OFF")
		ann.LoreIn(c, "doctor").Never(t, "leak guard off in this clone", "leak guard missing")
		jWrite(t, filepath.Join(c.Dir, "notes.txt"), mjRule+"\n")
		c.Git("add", "-A")
		r := c.TryGit("commit", "-q", "-m", "Add notes")
		if r.Code == 0 {
			t.Fatalf("Ann committed an internal rule sentence in her joined client clone: %s", r)
		}
		r.Says(t, "E_LEAK_BLOCKED")
		if st := c.Git("status", "--porcelain", "--ignored", "--", ".lore", ".gitattributes"); strings.Contains(st, ".lore") || strings.Contains(st, ".gitattributes") {
			t.Fatalf("the join wrote lore files into the client repo:\n%s", st)
		}
	})

	w.Step("day1/a-guard-lore-cannot-install-stops-the-setup-step", func() {
		c := ann.Clone(remote, "basket-two")
		ann.MoveClone(t, c, filepath.Join(ann.Home, "code", "basket-two"))
		own := "#!/bin/sh\nexit 0\n"
		jWrite(t, filepath.Join(c.Dir, ".githooks", "commit-msg"), own)
		c.Git("add", ".githooks")
		c.Git("commit", "-q", "--no-verify", "-m", "Team hooks")
		c.Git("config", "--local", "core.hooksPath", ".githooks")
		r := ann.Lore(c.Dir, "setup", "apply", tlTeamSetup, "--step", "this_folder", "--answers", "{}")
		r.Refused(t, "E_REFUSED", "setup stopped", "leak guard", `lore check leaks --staged --commit-msg "$1" || exit $?`, `lore sync hook pre-push "$@" || exit $?`)
		if b, _ := os.ReadFile(filepath.Join(c.Dir, ".githooks", "commit-msg")); string(b) != own {
			t.Fatalf("lore changed the client's committed hook:\n%s", b)
		}
		if st := c.Git("status", "--porcelain"); st != "" {
			t.Fatalf("the refused setup step changed the client repo:\n%s", st)
		}
	})
}
