//go:build stories && !windows

package stories

// O_alias_join_test.go — devco D-626 on the owner-shaped machine
// (ownershape.go, story O): `setup join` with the invite's store spelled
// through the org account's ssh alias (git@<alias>:<owner>/<repo>.git, the
// alias from ~/.ssh/config Host/HostName github.com and url.insteadOf) was
// refused E_SETUP_ORG_MISMATCH against the org entry for the github.com
// spelling of the same store, after it had already written the org entry:
// the setup stayed "not saved yet, next: join", half applied. Now the alias
// spelling is the same store, and a join that is refused writes nothing.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// oAliasInvite re-spells an invite line's store url through alias, as a
// person whose org account sits behind an ssh alias pastes it.
func oAliasInvite(t testing.TB, line, store string) string {
	t.Helper()
	inv, err := onboard.DecodeInvite(line)
	if err != nil {
		t.Fatal(err)
	}
	inv.Store = store
	out, err := onboard.EncodeInvite(inv)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// oHomeFiles is every file under LORE_HOME with its hash (store clones by
// their HEAD and status): what "a refused join writes nothing" compares.
func oHomeFiles(t testing.TB, m *OwnerMachine) string {
	t.Helper()
	root := m.LorePath()
	var lines []string
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if fi.IsDir() {
			if fi.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(rel, ".lock") || strings.Contains(rel, "cache") || strings.Contains(rel, "logs") {
			return nil
		}
		lines = append(lines, rel+" "+oHash(p))
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// oJoinSteps saves a member setup's steps up to (not including) join.
func oJoinSteps(t testing.TB, m *OwnerMachine, dir, setup string, p journeys.Person) {
	t.Helper()
	oApply(t, m, dir, setup, "kind", map[string]any{"kind": "member", "name": setup})
	oApply(t, m, dir, setup, "identity", map[string]any{"name": p.Name, "email": p.Email})
	oApply(t, m, dir, setup, "github", map[string]any{"github_account": p.Login, "host_alias": oAlias})
	oApply(t, m, dir, setup, "personal_store", map[string]any{"personal_store": "local"})
}

// oJoin applies a join step with the invite line; the result as is.
func oJoin(t testing.TB, m *OwnerMachine, dir, setup, line string) teamharness.Result {
	t.Helper()
	b, err := json.Marshal(map[string]any{"invite": line})
	if err != nil {
		t.Fatal(err)
	}
	return m.Lore(dir, "setup", "apply", "--setup", setup, "--step", "join", "--answers", string(b), "--json")
}

func TestStoryOAliasJoin(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	other := s.Remote("github.com/" + oOwner + "/lore-other")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote("github.com/"+oOwner+"/web"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ada.LoreIn(pc, "sync").OK(t)
	aliasURL := "git@" + oAlias + ":" + oOwner + "/lore-team.git"

	mo := s.OwnerShaped(OwnerShape{Personal: oMemPersonal, Org: oMemOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	mwc := mo.CloneTo(web, mo.OrgDir("web"))
	_, line := journeys.Invite(t, ada.Machine, pc.Dir, oMemOrg.Email)
	// the member joins with the line as sent (the github.com spelling):
	// the org entry acme is for git@github.com:<owner>/lore-team.git
	oSetupFlow(t, mo, mwc.Dir, oMSetup, "member", oMemOrg, oAlias, map[string]any{"invite": line}, []string{"~/code/" + oOrg + "/**"})
	mo.LoreIn(mwc, "sync").OK(t)

	// devco:D-626 (1): a join whose invite spells the entry's store through
	// the ssh alias (as the owner pasted it) is the same store: it was
	// E_SETUP_ORG_MISMATCH "an org entry named acme exists for store
	// git@github.com:…, not git@<alias>:…"; the entry keeps its spelling
	s.Step("D-626/alias-join-is-the-same-store", func() {
		oJoinSteps(t, mo, mwc.Dir, "acme-alias", oMemOrg)
		r := oJoin(t, mo, mwc.Dir, "acme-alias", oAliasInvite(t, line, aliasURL))
		r.Never(t, "E_SETUP_ORG_MISMATCH", "exists for store")
		r.OK(t)
		// the entry keeps the spelling it was made with (one store, one
		// url: the alias join adds its setup, never a second url)
		mo.Lore(mo.Home, "org", "list", "--json").OK(t).Says(t, `"url": "`+team.SSHURL()+`"`, `"name": "acme-alias"`).Never(t, aliasURL)
		mo.Lore(mo.Home, "setup", "list").OK(t).Says(t, "acme-alias").Never(t, "next: join")
	})

	// devco:D-626 (2): a join refused for an org entry of another store
	// writes nothing first: it rewrote the entry's identity (the identity
	// step's new name), then refused, leaving the setup half applied
	s.Step("D-626/refused-join-writes-nothing", func() {
		renamed := oMemOrg
		renamed.Name = "Mo Renamed"
		oJoinSteps(t, mo, mwc.Dir, "acme-bad", renamed)
		before := oHomeFiles(t, mo)
		r := oJoin(t, mo, mwc.Dir, "acme-bad", oAliasInvite(t, line, other.SSHURL()))
		if r.Code == 0 {
			t.Fatalf("a join of acme on another store was not refused: %s", r)
		}
		r.Says(t, "E_SETUP_ORG_MISMATCH", "nothing was written")
		if after := oHomeFiles(t, mo); after != before {
			t.Errorf("a refused join changed the home:\nbefore\n%s\nafter\n%s", before, after)
		}
		mo.Lore(mo.Home, "org", "list").OK(t).Never(t, "Mo Renamed")
	})

	// devco:D-626 (class): the other setup step that makes a store, the
	// personal store, refuses a store it cannot read or write before it
	// saves anything (its step gate), for every way of naming one
	s.Step("D-626/refused-personal-store-writes-nothing", func() {
		for _, ans := range []map[string]any{
			{"personal_store": "existing", "store_url": "git@github.com:" + oOwner + "/no-such-repo.git"},
			{"personal_store": "existing", "store_url": "git@" + oAlias + ":" + oOwner + "/no-such-repo.git"},
			{"personal_store": "create", "store_url": "git@github.com:nobody-here/lore-me.git"},
			{"personal_store": "existing", "store_url": "file:///nonexistent/dir"},
		} {
			before := oHomeFiles(t, mo)
			b, err := json.Marshal(ans)
			if err != nil {
				t.Fatal(err)
			}
			r := mo.Lore(mwc.Dir, "setup", "apply", "--setup", oMSetup, "--step", "personal_store", "--answers", string(b), "--json")
			if r.Code == 0 {
				t.Errorf("personal_store %v was saved: %s", ans, r)
			}
			if after := oHomeFiles(t, mo); after != before {
				t.Errorf("a refused personal_store %v changed the home:\nbefore\n%s\nafter\n%s", ans, before, after)
			}
		}
	})

	// devco:D-626 (class): `org link --store` in the alias spelling names the
	// org's store of the github.com spelling (it was refused "not one of
	// acme's stores"); a store that is not the org's is refused with
	// nothing written
	s.Step("D-626/link-store-by-alias", func() {
		before := oHomeFiles(t, ada)
		r := ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "platform", "--store", other.SSHURL(), "--yes")
		r.Refused(t, "is not one of "+oOrg+"'s stores")
		if after := oHomeFiles(t, ada); after != before {
			t.Errorf("a refused org link changed the home:\nbefore\n%s\nafter\n%s", before, after)
		}
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "platform", "--store", aliasURL, "--yes").OK(t).
			Says(t, "linked to github.com/"+oOwner+"/lore-team ").Never(t, "is not one of")
	})
}
