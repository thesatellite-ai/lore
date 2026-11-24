package team

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
)

// githubContacts is ann's github contacts as login -> primary, each an
// identity contact (one that is not fails the test).
func githubContacts(t *testing.T, x *fixture, a ids.ID[ids.Actor]) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, c := range x.rowsOf(schema.TableActorContacts, a) {
		if c.Deleted || fieldText(c, schema.FieldContactType) != teamprofile.ContactTypeGitHub {
			continue
		}
		if fieldText(c, schema.FieldContactIdentity) != boolTrue {
			t.Errorf("github contact %s is not an identity contact", fieldText(c, schema.FieldContactValue))
		}
		out[fieldText(c, schema.FieldContactValueNorm)] = fieldText(c, schema.FieldContactPrimary) == boolTrue
	}
	return out
}

// TestMemberEditGitHub (devco:D-959, issue #161): a member's GitHub login
// changes with `lore member edit --github`, and the member's github field
// IS their primary github identity contact (one source of truth).
//   - member add naming another login for a member is refused with what
//     differs and the edit line (it said "nothing to add", exit 0),
//     dry run too, and writes nothing;
//   - member edit --github makes the new login primary, keeps the old one
//     as a non-primary identity contact (past commits still resolve), says
//     so, and leaves the handle derived from the old login alone;
//   - member show / list and the join prefill read the primary contact,
//     not the last github contact in list order (contact add --primary
//     left member show on the old login), both ways;
//   - a login another person holds is E_CONTACT_TAKEN; --meta github=… is
//     refused naming --github (its hint said --name item_github).
func TestMemberEditGitHub(t *testing.T) {
	x := newFixture(t)
	ctx := op.WithConfirmed(x.ctx, true)
	// edits name ann by id: the fake roster resolves ids, not emails
	const email = "ann@agency.example"
	out, err := x.ops().staffAdd(ctx, StaffAddIn{Email: email, Name: "Ann", GitHub: "Ann-Personal"})
	if err != nil {
		t.Fatal(err)
	}
	ann, err := ids.Parse[ids.Actor](out.Added[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.ops().staffAdd(ctx, StaffAddIn{Email: "bob@agency.example", Name: "Bob", GitHub: "bob-gh"}); err != nil {
		t.Fatal(err)
	}
	contacts := func() int { return len(x.e.All(schema.TableActorContacts)) }
	before := contacts()

	// member add with another login: refused, nothing written, the edit named
	for _, dry := range []bool{true, false} {
		_, err := x.ops().staffAdd(ctx, StaffAddIn{Email: email, GitHub: "ann-work", DryRun: dry})
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != errs.InvalidInput || !strings.Contains(e.Message, "GitHub login Ann-Personal, not ann-work") ||
			!strings.Contains(e.Hint, "lore member edit --who "+email+" --github ann-work --yes") {
			t.Fatalf("add with another login (dry run %v) = %v", dry, err)
		}
	}
	if n := contacts(); n != before {
		t.Fatalf("a refused add wrote %d contacts", n-before)
	}
	// the same login (any case, with @) is still "already a member"
	if out, err := x.ops().staffAdd(ctx, StaffAddIn{Email: email, GitHub: "@ann-personal"}); err != nil || len(out.Already) != 1 {
		t.Fatalf("add with the same login = %+v %v", out, err)
	}

	edit := func(login string) StaffWriteOut {
		t.Helper()
		w, err := x.ops().staffEdit(ctx, StaffEditIn{Who: ann.String(), GitHub: login})
		if err != nil {
			t.Fatalf("member edit --github %s: %v", login, err)
		}
		return w
	}
	shown := func() StaffRow {
		t.Helper()
		s, err := x.ops().staffShow(ctx, StaffShowIn{Who: ann.String()})
		if err != nil {
			t.Fatal(err)
		}
		p, ok, err := FindRosterPerson(context.Background(), x.e, []string{email})
		if err != nil || !ok || p.GitHub != s.Member.GitHub {
			t.Errorf("join prefill = %+v %v %v; member show says %q", p, ok, err, s.Member.GitHub)
		}
		return s.Member
	}

	w := edit("ann-work")
	if w.Member.GitHub != "ann-work" || w.Member.Handle != "ann-personal" ||
		!strings.Contains(w.Note, "earlier GitHub login Ann-Personal stays theirs") ||
		!strings.Contains(w.Note, "lore member edit --who "+email+" --handle ann-work --yes") {
		t.Fatalf("edit to ann-work = %+v", w)
	}
	if got := githubContacts(t, x, ann); len(got) != 2 || !got["ann-work"] || got["ann-personal"] {
		t.Fatalf("contacts after the edit = %v; want ann-work primary, ann-personal kept", got)
	}
	if m := shown(); m.GitHub != "ann-work" {
		t.Fatalf("member show after the edit = %q", m.GitHub)
	}
	// back to the first login: it is the older contact, so a reader taking
	// the last github contact in list order shows the wrong one
	w = edit("ann-personal")
	if w.Member.GitHub != "Ann-Personal" || strings.Contains(w.Note, "--handle") {
		t.Fatalf("edit back = %+v", w)
	}
	if got := githubContacts(t, x, ann); len(got) != 2 || !got["ann-personal"] || got["ann-work"] {
		t.Fatalf("contacts after the edit back = %v", got)
	}
	if m := shown(); m.GitHub != "Ann-Personal" {
		t.Fatalf("member show after the edit back = %q; want the primary contact", m.GitHub)
	}
	list, err := x.ops().staffList(ctx, StaffListIn{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range list.Members {
		if m.Email == email && m.GitHub != "Ann-Personal" {
			t.Fatalf("member list says %q", m.GitHub)
		}
	}
	// the same login again changes nothing and says nothing
	if w = edit("ann-personal"); w.Note != "" {
		t.Errorf("a no-op edit noted %q", w.Note)
	}

	// refusals: someone else's login, a bad login, --meta github
	n := contacts()
	_, err = x.ops().staffEdit(ctx, StaffEditIn{Who: ann.String(), GitHub: "bob-gh"})
	wantCode(t, "edit to bob's login", err, errs.ContactTaken)
	_, err = x.ops().staffEdit(ctx, StaffEditIn{Who: ann.String(), GitHub: "not a login"})
	wantCode(t, "edit to a bad login", err, errs.InvalidInput)
	_, err = x.ops().staffEdit(ctx, StaffEditIn{Who: ann.String(), RowMetaFlags: engine.RowMetaFlags{Meta: []string{"github=ann-work"}}})
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.InvalidInput || e.Hint != `lore member edit --who `+ann.String()+` --github "ann-work" --yes` {
		t.Errorf("--meta github = %v", err)
	}
	if got := contacts(); got != n {
		t.Errorf("refused edits wrote %d contacts", got-n)
	}
}
