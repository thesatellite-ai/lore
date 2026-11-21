//go:build stories && !windows

package stories

// b_orgadmin_test.go — story B, step 9 (devco:D-852, bug hunt H-109): who
// may change the org's own config is the admin role on the org's roster,
// not an admin setup on the machine. Cora starts the org with `lore org
// create` and no guided setup, so her laptop holds no admin setup; she is
// the roster's admin and changes lore-org.yaml. Nia joins with `lore org
// import` as a member and is refused, by name and by the roster.

import (
	"testing"
)

func TestStoryBOrgAdminIsTheRosterRole(t *testing.T) {
	b := newBCompany(t)
	s := b.s
	const store = "acme_team"

	s.Step("D-852/creator-without-a-setup-changes-the-org-file", func() {
		b.person(t, bCTO)
		b.lore(bCTO, "store", "init", "--name", store, "--url", b.store.SSHURL(), "--yes").OK(t)
		b.lore(bCTO, "org", "create", "--org", bOrg, "--store", store, "--own", "github.com/acme/*",
			"--key", "human:"+bCTO.Email, "--name", bCTO.Name, "--handle", "cora", "--yes").OK(t).Says(t, "org role admin")
		b.lore(bCTO, "org", "config", "set", "--org", bOrg, "--key", "own", "--add", "github.com/acme-labs/*", "--yes").OK(t).
			Says(t, "  + github.com/acme-labs/*", "✓ own in").Never(t, "admin setup")
		b.lore(bCTO, "org", "config", "get", "--org", bOrg, "--key", "own").OK(t).Says(t, "github.com/acme-labs/*")
		b.lore(bCTO, "member", "add", "--email", bHire.Email, "--name", bHire.Name, "--handle", "nia", "--yes").OK(t)
		b.lore(bCTO, "sync").OK(t)
	})

	s.Step("D-852/a-member-is-refused-by-the-roster", func() {
		s.AdvanceDays(7)
		b.person(t, bHire)
		b.lore(bHire, "org", "import", "--url", b.store.SSHURL(), "--key", "human:"+bHire.Email, "--name", bHire.Name, "--handle", "nia", "--yes").OK(t)
		r := b.lore(bHire, "org", "config", "set", "--org", bOrg, "--key", "own", "--add", "github.com/nia-tools/*", "--yes").
			Refused(t, "only an admin of "+bOrg, "human:"+bHire.Email, "holds no admin role on the roster of "+bOrg).Never(t, "admin setup")
		s.NamedFlags(t, r)
		b.lore(bHire, "org", "config", "get", "--org", bOrg, "--key", "own").OK(t).Never(t, "github.com/nia-tools/*")
	})
}
