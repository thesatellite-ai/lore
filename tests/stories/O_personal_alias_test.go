//go:build stories && !windows

package stories

// O_personal_alias_test.go — story O, devco D-817 (bug hunt H-75, owner
// HIGH): a person's nickname, full name and role were not names lore knew:
// only a field value, so `resource show`, ${res:…}, search and the
// not-found hint knew nothing of them, and a typo of a nickname found
// nobody. An owner-shaped laptop keeps a person in a personal store, adds
// other names with `lore resource alias add`, and every lookup finds the
// person by one.

import (
	"strings"
	"testing"
)

// TestStoryOPersonalAliases: alias add / list / remove, and show, a
// ${res:<alias>} note, search, ask (also a typo of the alias) and the
// not-found hint of a name a letter off all find the person (devco D-817).
func TestStoryOPersonalAliases(t *testing.T) {
	s := New(t, "O")
	me := s.Remote("github.com/" + oAdminPersonal.Login + "/lore-me")
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	to := "me/" + psFolder
	s.Step("personal-store/person-with-other-names", func() {
		ada.Lore(ada.Home, "store", "init", "--name", "me", "--url", me.Dir, "--yes").OK(t)
		ada.Lore(ada.Home, "store", "folder", "add", "me", psFolder).OK(t)
		ada.Lore(ada.Home, "resource", "kind", "add", "--kind", "person", "--name-format", "dns", "--to", to, "--yes").OK(t)
		ada.Lore(ada.Home, "resource", "field", "add", "--kind", "person", "--field", "role", "--type", "text", "--to", to, "--yes").OK(t)
		ada.Lore(ada.Home, "resource", "add", "--kind", "person", "--name", "robert-kim", "--set", "role=designer", "--to", to, "--yes").OK(t)
		ada.Lore(ada.Home, "memory", "add", "--body", "Lunch with ${res:Bobby} on friday.", "--to", to, "--yes").OK(t)
		ada.Lore(ada.Home, "resource", "alias", "add", "--store", to, "--kind", "person", "--id", "robert-kim", "--alias", "Bobby", "--alias", "Robert Kim", "--alias", "CFO").OK(t).
			Says(t, "alias Bobby, Robert Kim, CFO for person/robert-kim")
		ada.Lore(ada.Home, "resource", "alias", "list", "--store", to).OK(t).
			Says(t, "person/robert-kim  Bobby, Robert Kim, CFO")
	})
	s.Step("personal-store/every-lookup-finds-the-other-names", func() {
		ada.Lore(ada.Home, "resource", "show", "--store", to, "--id", "bobby").OK(t).
			Says(t, "person/robert-kim", "aliases: Bobby, Robert Kim, CFO", "role = designer")
		ada.Lore(ada.Home, "resource", "show", "--store", to, "--with-refs", "--id", "Bobby").OK(t).
			Says(t, "Lunch with ${res:Bobby} on friday.")
		ada.Lore(ada.Home, "search", "--store", "me", "--query", "CFO").OK(t).
			Says(t, "robert-kim")
		for _, q := range []string{"who is Bobby", "who is Boby", "who is Robert Kim"} {
			ada.Lore(ada.Home, "ask", "--store", "me", "--query", q).OK(t).
				Says(t, "robert-kim (person; aka Bobby, Robert Kim, CFO): ", "role: designer")
		}
		r := ada.Lore(ada.Home, "resource", "show", "--store", to, "--id", "robert-kin")
		if r.Code == 0 || !strings.Contains(r.Out(), "did you mean person/robert-kim?") {
			t.Errorf("[O D-817] a name a letter off does not name the near person:\n%s", r)
		}
		ada.Lore(ada.Home, "resource", "alias", "remove", "--store", to, "--id", "robert-kim", "--alias", "CFO").OK(t)
		if r := ada.Lore(ada.Home, "resource", "show", "--store", to, "--id", "CFO"); r.Code == 0 {
			t.Errorf("[O D-817] a removed alias still finds the person:\n%s", r)
		}
	})
}
