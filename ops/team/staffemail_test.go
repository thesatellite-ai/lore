package team

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/actorsource"
	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
)

// emailContacts is a person's email contacts as address -> primary, each
// an identity contact (one that is not fails the test).
func emailContacts(t *testing.T, x *fixture, a ids.ID[ids.Actor]) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, c := range x.rowsOf(schema.TableActorContacts, a) {
		if c.Deleted || fieldText(c, schema.FieldContactType) != teamprofile.ContactTypeEmail {
			continue
		}
		if fieldText(c, schema.FieldContactIdentity) != boolTrue {
			t.Errorf("email contact %s is not an identity contact", fieldText(c, schema.FieldContactValue))
		}
		out[fieldText(c, schema.FieldContactValueNorm)] = fieldText(c, schema.FieldContactPrimary) == boolTrue
	}
	return out
}

// actorRow is a's actors row in the fake roster.
func actorRow(t *testing.T, x *fixture, a ids.ID[ids.Actor]) row.Row {
	t.Helper()
	for _, r := range x.e.All(schema.TableActors) {
		if r.ID == a.Any() {
			return r
		}
	}
	t.Fatalf("no actors row %s", a)
	return row.Row{}
}

// TestMemberEditEmail (devco:D-995): `lore member edit --email` changes a
// person's email, the key the roster knows them by, on the same actor
// row: its id, stints and teams stay, stable_key becomes the new address,
// the earlier key joins key_aliases (so a machine that knows only the new
// address finds the row) and stays an identity contact, not primary (so
// past commits still resolve). Before, member edit had no --email: the
// only way was a second actor plus an actor merge. An address another
// person has (their key, an earlier key, an identity contact) is
// E_CONTACT_TAKEN naming them, with nothing written.
func TestMemberEditEmail(t *testing.T) {
	x := newFixture(t)
	ctx := op.WithConfirmed(x.ctx, true)
	const oldEmail, newEmail = "ann@agency.example", "ann@agency.test"
	out, err := x.ops().staffAdd(ctx, StaffAddIn{Email: oldEmail, Name: "Ann", Handle: "ann", Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	ann, err := ids.Parse[ids.Actor](out.Added[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := x.ops().staffAdd(ctx, StaffAddIn{Email: "bob@agency.example", Name: "Bob"})
	if err != nil {
		t.Fatal(err)
	}
	periods := len(x.rowsOf(schema.TableActorPeriods, ann))

	w, err := x.ops().staffEdit(ctx, StaffEditIn{Who: ann.String(), Email: " Ann@Agency.test "})
	if err != nil {
		t.Fatalf("member edit --email: %v", err)
	}
	if w.Member.ID != ann.String() || w.Member.Email != newEmail || w.Member.Handle != "ann" ||
		!strings.Contains(w.Note, "earlier email "+oldEmail+" stays theirs") {
		t.Fatalf("edit = %+v", w)
	}
	got0 := actorRow(t, x, ann)
	if got := fieldText(got0, fieldStableKey); got != humanKeyPrefix+newEmail {
		t.Errorf("stable_key = %q, want the new address", got)
	}
	if got := actorsource.SplitAliases(fieldText(got0, schema.FieldActorKeyAliases)); len(got) != 1 || got[0] != humanKeyPrefix+oldEmail {
		t.Errorf("key_aliases = %v, want the earlier key", got)
	}
	if got := emailContacts(t, x, ann); len(got) != 2 || !got[newEmail] || got[oldEmail] {
		t.Errorf("email contacts = %v; want the new one primary, the old one kept", got)
	}
	if n := len(x.rowsOf(schema.TableActorPeriods, ann)); n != periods {
		t.Errorf("stints %d -> %d; an email change keeps them", periods, n)
	}
	if n := len(x.e.All(schema.TableActors)); n != 3 {
		t.Errorf("%d actors; an email change writes no second person", n)
	}
	// the new key finds the same person
	if s, err := x.ops().staffShow(ctx, StaffShowIn{Who: humanKeyPrefix + newEmail}); err != nil || s.Member.ID != ann.String() {
		t.Errorf("show by the new key = %+v %v", s.Member, err)
	}

	// a second change keeps both earlier keys, oldest first
	if _, err := x.ops().staffEdit(ctx, StaffEditIn{Who: ann.String(), Email: "ann@agency.dev"}); err != nil {
		t.Fatal(err)
	}
	got0 = actorRow(t, x, ann)
	if got := fieldText(got0, schema.FieldActorKeyAliases); got != humanKeyPrefix+oldEmail+actorsource.AliasSep+humanKeyPrefix+newEmail {
		t.Errorf("key_aliases after a second change = %q", got)
	}
	if got := emailContacts(t, x, ann); len(got) != 3 || !got["ann@agency.dev"] || got[newEmail] || got[oldEmail] {
		t.Errorf("email contacts after a second change = %v", got)
	}
	// the same address again changes nothing and says nothing
	if w, err := x.ops().staffEdit(ctx, StaffEditIn{Who: ann.String(), Email: "ann@agency.dev"}); err != nil || w.Note != "" {
		t.Errorf("a no-op email edit = %q %v", w.Note, err)
	}

	// a handle that was the old address's name part is kept, and the note
	// names the edit that changes it, with a handle --handle takes
	cy, err := x.ops().staffAdd(ctx, StaffAddIn{Email: "cy@agency.example", Name: "Cy", Handle: "cy"})
	if err != nil {
		t.Fatal(err)
	}
	w, err = x.ops().staffEdit(ctx, StaffEditIn{Who: cy.Added[0].ID, Email: "cy.new@agency.example"})
	if err != nil || w.Member.Handle != "cy" || !strings.Contains(w.Note, "the handle @cy is unchanged") {
		t.Fatalf("edit of cy = %+v %v", w, err)
	}
	if i := strings.Index(w.Note, "lore member edit --who cy.new@agency.example --handle "); i < 0 || !strings.Contains(w.Note[i:], " --setup agency --yes changes it") {
		t.Errorf("handle note = %q", w.Note)
	}

	// refusals: bob's address, an address that is not one, --meta email
	writes := len(x.e.All(schema.TableActorContacts))
	_, err = x.ops().staffEdit(ctx, StaffEditIn{Who: ann.String(), Email: "bob@agency.example"})
	wantCode(t, "edit to bob's address", err, errs.ContactTaken)
	if err != nil && !strings.Contains(err.Error(), "Bob") {
		t.Errorf("the refusal names nobody: %v", err)
	}
	_, err = x.ops().staffEdit(ctx, StaffEditIn{Who: bob.Added[0].ID, Email: oldEmail})
	wantCode(t, "edit bob to ann's earlier address", err, errs.ContactTaken)
	_, err = x.ops().staffEdit(ctx, StaffEditIn{Who: ann.String(), Email: "not an address"})
	wantCode(t, "edit to a bad address", err, errs.InvalidInput)
	if got := len(x.e.All(schema.TableActorContacts)); got != writes {
		t.Errorf("refused edits wrote %d contacts", got-writes)
	}
	if got := fieldText(actorRow(t, x, ann), fieldStableKey); got != humanKeyPrefix+"ann@agency.dev" {
		t.Errorf("a refused edit changed the key to %q", got)
	}
}
