//go:build journeys && !windows

package journeys

// orgconfig_test.go — an admin changes the org's repo patterns with a
// command (devco D-453). Before, nothing edited lore-org.yaml after the
// org was created: the admin hand-edited it in the store clone, committed
// with git and ran `lore org refresh`; and a client pattern could only
// use * as a whole segment, so client repos were listed one by one.
//
//	org config get                     ─▶ own, repos, stores of the file
//	org config set --key repos --add   ─▶ committed in the org store, the
//	                                       machine's entry copies it at once
//	a repo under client-*              ─▶ whoami: client repo

import (
	"path/filepath"
	"testing"
)

// Names of the org-config journey (placeholders).
const (
	ocPattern = "github.com/partner/client-*"
	ocClient  = "github.com/partner/client-shop"
	ocOther   = "github.com/partner/tools"
	ocOwn     = "github.com/partner/*"
)

func TestJourneyOrgConfig(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")

	w.Step("day1/the-admin-adds-a-client-pattern", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "org", "config", "get", "--org", tlOrg).OK(t).Says(t, "own:", "repos:", "stores:").Never(t, ocPattern)
		lee.Lore(parent, "org", "config", "set", "--org", tlOrg, "--key", "repos", "--add", "git*.com/partner/x", "--yes").
			Refused(t, "wildcard for the host", "client-*")
		lee.Lore(parent, "org", "config", "set", "--org", tlOrg, "--key", "colour", "--add", ocPattern, "--yes").
			Refused(t, "is not one of own, repos")
		lee.Lore(parent, "org", "config", "set", "--org", tlOrg, "--key", "repos", "--add", ocPattern).
			Refused(t, "  + "+ocPattern, "--yes")
		// devco D-649: the change as +/- lines and the size, not the
		// whole list twice (was/now)
		lee.Lore(parent, "org", "config", "set", "--org", tlOrg, "--key", "repos", "--add", ocPattern, "--yes").OK(t).
			Says(t, "  + "+ocPattern, "✓ repos in", "changed", "org entry "+tlOrg+" copies it").Never(t, "was: ", "now: ")
		lee.Lore(parent, "org", "config", "get", "--org", tlOrg, "--key", "repos").OK(t).Says(t, ocPattern)
		lee.Lore(parent, "org", "config", "set", "--org", tlOrg, "--key", "repos", "--add", ocPattern, "--yes").OK(t).
			Says(t, "nothing committed")
	})

	w.Step("day1/a-repo-under-the-pattern-is-a-client-repo", func() {
		// own: the whole owner; repos: its client-* repos. client wins.
		lee.Lore(parent, "org", "config", "set", "--org", tlOrg, "--key", "own", "--add", ocOwn, "--yes").OK(t).Says(t, "✓ own in")
		shop := jPlainRepo(t, lee, w.Remote(ocClient), filepath.Join(parent, "client-shop"))
		lee.LoreIn(shop, "whoami").OK(t).Says(t, "client repo")
		tools := jPlainRepo(t, lee, w.Remote(ocOther), filepath.Join(parent, "tools"))
		lee.LoreIn(tools, "whoami").OK(t).Says(t, "own repo").Never(t, "client repo")
	})
}
