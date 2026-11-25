//go:build stories && !windows

package stories

// b_memberemail_test.go — story B (devco:D-995): a member's email
// changes. member edit had no --email, so the only way was a second
// actor plus an actor merge. Now `lore member edit --email` moves the
// person's key in place: the same actor id, stint and handle; the old
// address stays an identity contact (past commits and the laptop that
// joined with it still resolve to them), and a new laptop that joins
// with the new address is the same person. An address someone else has
// is refused naming them. Every printed command runs as printed.

import (
	"path/filepath"
	"regexp"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

var bActorID = regexp.MustCompile(`act_[0-9a-f]{32}`)

func TestStoryBMemberEmailChanges(t *testing.T) {
	b := newBCompany(t)
	s := b.s
	const newEmail = "nia@acme-new.example.test"
	nia2 := journeys.Person{Name: bHire.Name, Email: newEmail, Login: bHire.Login}
	var id string

	s.Step("D-995/member-joins-with-the-old-address", func() {
		b.start(t)
		b.lore(bCTO, "member", "add", "--email", bHire.Email, "--name", bHire.Name, "--handle", "nia", "--role", "member", "--yes").OK(t)
		b.lore(bCTO, "member", "add", "--email", bLead.Email, "--name", bLead.Name, "--handle", "lio", "--yes").OK(t)
		// a team for the app folder, so a member writes there
		b.lore(bCTO, "team", "add", "--handle", "@"+bProject, "--members", "@nia", "--yes").OK(t)
		b.lore(bCTO, "sync").OK(t)
		id = bActorID.FindString(b.lore(bCTO, "member", "show", "--who", bHire.Email).OK(t).Out())
		if id == "" {
			t.Fatal("member show printed no act_ id")
		}
		nia := b.person(t, bHire)
		b.repo[bHire.Email].Git("pull", "-q", "origin", "main")
		journeys.Join(t, nia, b.repo[bHire.Email].Dir, bSetup, b.store.SSHURL(), bHire, []string{bFolders})
		b.lore(bHire, "whoami").OK(t).Says(t, bOrg, bHire.Email, id)
	})

	s.Step("D-995/member-edit-email-keeps-the-person", func() {
		r := b.lore(bCTO, "member", "edit", "--who", bHire.Email, "--email", newEmail, "--yes").OK(t).
			Says(t, id, "earlier email "+bHire.Email+" stays theirs", "actor id, stints and teams are unchanged")
		s.NamedFlags(t, r)
		b.lore(bCTO, "member", "show", "--who", newEmail).OK(t).Says(t, id, "email:    "+newEmail, "handle:   @nia", "status:   active")
		b.lore(bCTO, "member", "show", "--who", bHire.Email).OK(t).Says(t, id, "email:    "+newEmail)
		b.lore(bCTO, "member", "list").OK(t).Says(t, newEmail, "3 members").Never(t, bHire.Email)
		b.lore(bCTO, "actor", "contact", "list", "--actor", id).OK(t).Says(t, newEmail+" ", bHire.Email+" ").Never(t, "E_")
		b.lore(bCTO, "sync").OK(t)
	})

	s.Step("D-995/the-old-laptop-still-writes-as-them", func() {
		b.lore(bHire, "sync").OK(t)
		b.lore(bHire, "memory", "add", "--body", "the old laptop still writes", "--yes").OK(t)
		b.lore(bCTO, "member", "list").OK(t).Says(t, "3 members")
	})

	s.Step("D-995/a-new-laptop-with-the-new-address-is-the-same-person", func() {
		// the second laptop of the same person (b.person names the machine
		// after the person, and the first laptop has that name)
		m := b.s.Person("Nia Second Laptop", newEmail)
		journeys.GitHubLogin(t, b.s.World, m, nia2.Login)
		c := m.Clone(b.app, "app")
		m.MoveClone(t, c, filepath.Join(m.Home, "code", "app"))
		b.m[newEmail], b.repo[newEmail] = m, c
		b.repo[newEmail].Git("pull", "-q", "origin", "main")
		journeys.Join(t, m, b.repo[newEmail].Dir, bSetup, b.store.SSHURL(), nia2, []string{bFolders})
		b.lore(nia2, "whoami").OK(t).Says(t, bOrg, newEmail, id)
		b.lore(nia2, "memory", "add", "--body", "the new laptop writes as the same person", "--yes").OK(t)
		b.lore(nia2, "sync").OK(t)
		b.lore(bCTO, "sync").OK(t)
		b.lore(bCTO, "member", "list").OK(t).Says(t, "3 members")
		b.lore(bCTO, "actor", "list").OK(t).Says(t, id).Never(t, "Nia Hale (2)")
	})

	s.Step("D-995/an-address-someone-else-has-is-refused", func() {
		r := b.lore(bCTO, "member", "edit", "--who", bLead.Email, "--email", bHire.Email, "--yes").Fails(t, 23, "E_CONTACT_TAKEN").
			Says(t, bHire.Name, "nothing was written")
		s.NamedFlags(t, r)
		b.lore(bCTO, "member", "show", "--who", bLead.Email).OK(t).Says(t, "email:    "+bLead.Email)
	})
}
