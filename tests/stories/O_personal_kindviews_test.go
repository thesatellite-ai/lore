//go:build stories && !windows

package stories

// O_personal_kindviews_test.go — story O, devco D-794 (owner P-11, bug
// hunt H-51): on a personal store with people, projects and companies kept
// as resources in one folder, every per-kind view (res_person,
// res_project, res_company, res_<kind id>) listed every resource of the
// store. A kind table is refilled from all the resources of the cache when
// its shape changes (a field added, or a migrated or adopted folder whose
// resources reach the cache before their kinds), and the refill copied
// each resource into the table being refilled whatever its kind. Here an
// owner-shaped laptop writes the resources first, adds fields after, and
// each view lists only its own kind.

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestStoryOPersonalKindViews: res_<kind> views of a personal store folder
// hold only their kind's resources after the kind tables are refilled.
func TestStoryOPersonalKindViews(t *testing.T) {
	s := New(t, "O")
	me := s.Remote("github.com/" + oAdminPersonal.Login + "/lore-me")
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})

	to := "me/" + psFolder
	s.Step("personal-kinds/resources", func() {
		ada.Lore(ada.Home, "store", "init", "--name", "me", "--url", me.Dir, "--yes").OK(t)
		ada.Lore(ada.Home, "store", "folder", "add", "me", psFolder).OK(t)
		for _, k := range []string{"person", "project", "company"} {
			ada.Lore(ada.Home, "resource", "kind", "add", "--kind", k, "--to", to, "--yes").OK(t)
		}
		for _, p := range []string{"alice_ng", "bob_li", "cara_ok"} {
			ada.Lore(ada.Home, "resource", "add", "--kind", "person", "--name", p, "--to", to, "--yes").OK(t)
		}
		ada.Lore(ada.Home, "resource", "add", "--kind", "project", "--name", "orbit", "--to", to, "--yes").OK(t)
		ada.Lore(ada.Home, "resource", "add", "--kind", "company", "--name", "acme_co", "--to", to, "--yes").OK(t)
		// fields after the resources: each kind table is refilled
		ada.Lore(ada.Home, "resource", "field", "add", "--kind", "person", "--field", "role", "--type", "text", "--to", to, "--yes").OK(t)
		ada.Lore(ada.Home, "resource", "field", "add", "--kind", "project", "--field", "status", "--type", "text", "--to", to, "--yes").OK(t)
	})

	s.Step("personal-kinds/views-keep-their-kind", func() {
		clone := filepath.Join(oStoreCloneLocal(t, ada), psFolder)
		for view, want := range map[string]string{"res_person": "3", "res_project": "1", "res_company": "1"} {
			r := ada.Lore(clone, "sql", "--format", "csv", "--query", "SELECT COUNT(*) AS n FROM "+view)
			lines := strings.Split(strings.TrimSpace(r.Out()), "\n")
			if r.Code != 0 || lines[len(lines)-1] != want {
				t.Errorf("[O D-794] `lore sql` on %s in the store folder: want %s rows of its kind\n%s", view, want, r)
			}
		}
		ada.Lore(clone, "sql", "--query", "SELECT name FROM res_person ORDER BY name").OK(t).
			Says(t, "alice_ng", "bob_li", "cara_ok").Never(t, "orbit", "acme_co")
	})
}
