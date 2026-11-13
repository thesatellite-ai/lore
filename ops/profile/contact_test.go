package profile

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/ops/profilestore/profilestoretest"
	"github.com/thesatellite-ai/lore/schema"
)

// contactsOf is the live contacts of actor id (string) and type.
func contactsOf(f *profilestoretest.Fake, actor, typ string) []row.Row {
	var out []row.Row
	for _, c := range live(f, schema.TableActorContacts) {
		if text(c, schema.FieldContactActor) == actor && text(c, schema.FieldContactType) == typ {
			out = append(out, c)
		}
	}
	return out
}

// primaries counts the primary contacts among cs.
func primaries(cs []row.Row) int {
	n := 0
	for _, c := range cs {
		if flag(c, schema.FieldContactPrimary) {
			n++
		}
	}
	return n
}

// TestContactAddWorkedExample is section 12's contact lines: the first
// of a type becomes primary, --primary moves it (TP-3), the value is kept
// as typed with value_norm beside it, and the same contact again is a
// no-op.
func TestContactAddWorkedExample(t *testing.T) {
	ctx := context.Background()
	f, mira, _ := world(t)
	m := mira.String()
	steps := []ContactAddIn{
		{Actor: "@mira", Type: "email", Value: "mira@acme-agency.com", Label: "work", Primary: true, Identity: true},
		{Actor: "@mira", Type: "email", Value: "Mira.Patel@Gmail.com", Label: "personal", Identity: true},
		{Actor: "@mira", Type: "github", Value: "https://github.com/Mira-Acme/", Label: "work", Primary: true, Identity: true},
		{Actor: "@mira", Type: "github", Value: "@mirap", Label: "personal", Identity: true},
		{Actor: "@mira", Type: "phone", Value: "+90 532 000 0000", Label: "work"},
	}
	for _, in := range steps {
		out, err := contactAdd(ctx, f, in)
		if err != nil {
			t.Fatalf("%+v: %v", in, err)
		}
		if out.Verb != verbAdded || !strings.Contains(out.Text(), "Mira Patel (@mira)") {
			t.Fatalf("out %+v / %q", out, out.Text())
		}
	}
	for typ, n := range map[string]int{"email": 2, "github": 2, "phone": 1} {
		cs := contactsOf(f, m, typ)
		if len(cs) != n || primaries(cs) != 1 {
			t.Fatalf("%s: %d contacts, %d primary", typ, len(cs), primaries(cs))
		}
	}
	gh := contactsOf(f, m, "github")
	for _, c := range gh {
		if text(c, schema.FieldContactValueNorm) == "mira-acme" && (!flag(c, schema.FieldContactPrimary) || text(c, schema.FieldContactValue) != "https://github.com/Mira-Acme/") {
			t.Fatalf("work github %v", c.Fields)
		}
	}
	// again, differently typed: already there, nothing written
	pushes := f.Pushes
	out, err := contactAdd(ctx, f, ContactAddIn{Actor: "@mira", Type: "email", Value: " MIRA.PATEL@gmail.com ", Identity: true})
	if err != nil || out.Verb != verbAlreadyThere || f.Pushes != pushes {
		t.Fatalf("again: %+v %v pushes %d→%d", out, err, pushes, f.Pushes)
	}
	// --primary on the second email moves the primary (TP-3)
	if _, err := contactAdd(ctx, f, ContactAddIn{Actor: "@mira", Type: "email", Value: "mira@home.example.org", Primary: true}); err != nil {
		t.Fatal(err)
	}
	cs := contactsOf(f, m, "email")
	if primaries(cs) != 1 {
		t.Fatalf("emails: %d primaries", primaries(cs))
	}
	for _, c := range cs {
		if flag(c, schema.FieldContactPrimary) && text(c, schema.FieldContactValueNorm) != "mira@home.example.org" {
			t.Fatalf("primary is %s", text(c, schema.FieldContactValueNorm))
		}
	}
	// TP-6: list shows values plain, with the type's link
	list, err := contactList(ctx, f, ContactListIn{Actor: "@mira", Type: "github"})
	if err != nil || len(list.Contacts) != 2 || !list.Contacts[0].Primary || list.Contacts[0].URL != "https://github.com/mira-acme" {
		t.Fatalf("list %+v %v", list, err)
	}
	all, _ := contactList(ctx, f, ContactListIn{Type: "phone"})
	if len(all.Contacts) != 1 || all.Contacts[0].Value != "+90 532 000 0000" {
		t.Fatalf("phone list %+v", all)
	}
	ids, _ := contactList(ctx, f, ContactListIn{Identity: true})
	if len(ids.Contacts) != 4 {
		t.Fatalf("identity list: %d", len(ids.Contacts))
	}
}

// TestContactPrimaryOnePerType: the TP-3 matrix. Whatever order adds,
// edits and --primary come in, each (actor, type) has at most one
// primary, and another person's primary is never touched.
func TestContactPrimaryOnePerType(t *testing.T) {
	ctx := context.Background()
	f, mira, ben := world(t)
	add := func(actor, value string, primary bool) string {
		t.Helper()
		out, err := contactAdd(ctx, f, ContactAddIn{Actor: actor, Type: "phone", Value: value, Primary: primary})
		if err != nil {
			t.Fatal(err)
		}
		return out.Contact.ID
	}
	benPhone := add("@ben", "+1 555 000 0001", false) // first: primary by default
	a := add("@mira", "+1 555 000 0002", false)       // first: primary
	b := add("@mira", "+1 555 000 0003", false)       // not primary
	check := func(step, want string) {
		t.Helper()
		cs := contactsOf(f, mira.String(), "phone")
		if primaries(cs) != 1 {
			t.Fatalf("%s: %d primaries", step, primaries(cs))
		}
		for _, c := range cs {
			if flag(c, schema.FieldContactPrimary) && c.ID.String() != want {
				t.Fatalf("%s: primary %s, want %s", step, c.ID, want)
			}
		}
		if bs := contactsOf(f, ben.String(), "phone"); primaries(bs) != 1 || bs[0].ID.String() != benPhone {
			t.Fatalf("%s: ben's primary changed", step)
		}
	}
	check("defaults", a)
	c := add("@mira", "+1 555 000 0004", true)
	check("add --primary", c)
	if _, err := contactEdit(ctx, f, ContactEditIn{ID: b, Primary: true}); err != nil {
		t.Fatal(err)
	}
	check("edit --primary", b)
	if _, err := contactEdit(ctx, f, ContactEditIn{ID: b, Primary: true}); err != nil {
		t.Fatal(err)
	}
	check("edit --primary again", b)
	if _, err := contactEdit(ctx, f, ContactEditIn{ID: b, NoPrimary: true}); err != nil {
		t.Fatal(err)
	}
	if n := primaries(contactsOf(f, mira.String(), "phone")); n != 0 {
		t.Fatalf("--no-primary: %d primaries", n)
	}
}

// TestContactTaken: the TP-4 identity collision matrix (6.5 target 5).
func TestContactTaken(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		// seed is Ben's contact before Mira's add.
		seed ContactAddIn
		add  ContactAddIn
		code errs.Code // "" = allowed
	}{
		{"same github identity on another person", ContactAddIn{Actor: "@ben", Type: "github", Value: "mirap", Identity: true},
			ContactAddIn{Actor: "@mira", Type: "github", Value: "MiraP", Identity: true}, errs.ContactTaken},
		{"same email identity, other spelling", ContactAddIn{Actor: "@ben", Type: "email", Value: "shared@example.com", Identity: true},
			ContactAddIn{Actor: "@mira", Type: "email", Value: " Shared@Example.COM", Identity: true}, errs.ContactTaken},
		{"held without identity never counts", ContactAddIn{Actor: "@ben", Type: "github", Value: "mirap"},
			ContactAddIn{Actor: "@mira", Type: "github", Value: "mirap", Identity: true}, ""},
		{"not asked for identity", ContactAddIn{Actor: "@ben", Type: "github", Value: "mirap", Identity: true},
			ContactAddIn{Actor: "@mira", Type: "github", Value: "mirap"}, ""},
		{"same value, other type", ContactAddIn{Actor: "@ben", Type: "gitlab", Value: "mirap", Identity: true},
			ContactAddIn{Actor: "@mira", Type: "github", Value: "mirap", Identity: true}, ""},
		{"identity on phone", ContactAddIn{},
			ContactAddIn{Actor: "@mira", Type: "phone", Value: "+90 532 000 0000", Identity: true}, errs.ContactType},
		{"unknown type", ContactAddIn{},
			ContactAddIn{Actor: "@mira", Type: "myspace", Value: "mirap"}, errs.ContactType},
		{"bad email", ContactAddIn{},
			ContactAddIn{Actor: "@mira", Type: "email", Value: "mira-at-example"}, errs.ContactType},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, mira, _ := world(t)
			if c.seed.Actor != "" {
				if _, err := contactAdd(ctx, f, c.seed); err != nil {
					t.Fatal(err)
				}
			}
			before := len(live(f, schema.TableActorContacts))
			_, err := contactAdd(ctx, f, c.add)
			if c.code == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			wantCode(t, c.name, err, c.code)
			if len(live(f, schema.TableActorContacts)) != before || len(contactsOf(f, mira.String(), c.add.Type)) != 0 {
				t.Fatal("a refused add wrote a contact")
			}
		})
	}

	// left people still hold their identity (6.2 step 4): taken too
	f, _, ben := world(t)
	if _, err := contactAdd(ctx, f, ContactAddIn{Actor: "@ben", Type: "github", Value: "bo", Identity: true}); err != nil {
		t.Fatal(err)
	}
	r, _, _ := f.Roster(ctx, true)
	if _, err := r.Update(ctx, schema.TableActors, ben.Any(), map[string]value.Value{schema.FieldActorActive: value.BoolOf(false), schema.FieldActorStatus: txt(t, schema.ActorStatusLeft)}); err != nil {
		t.Fatal(err)
	}
	_, err := contactAdd(ctx, f, ContactAddIn{Actor: "@mira", Type: "github", Value: "bo", Identity: true})
	wantCode(t, "left holder", err, errs.ContactTaken)

	// edit: turning identity on, or changing the value onto a held one
	f, _, _ = world(t)
	if _, err := contactAdd(ctx, f, ContactAddIn{Actor: "@ben", Type: "github", Value: "held", Identity: true}); err != nil {
		t.Fatal(err)
	}
	plain, err := contactAdd(ctx, f, ContactAddIn{Actor: "@mira", Type: "github", Value: "held"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = contactEdit(ctx, f, ContactEditIn{ID: plain.Contact.ID, Identity: true})
	wantCode(t, "edit --identity", err, errs.ContactTaken)
	own, err := contactAdd(ctx, f, ContactAddIn{Actor: "@mira", Type: "github", Value: "mine", Identity: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contactAdd(ctx, f, ContactAddIn{Actor: "@ben", Type: "github", Value: "bens", Identity: true}); err != nil {
		t.Fatal(err)
	}
	_, err = contactEdit(ctx, f, ContactEditIn{ID: own.Contact.ID, Value: "@BENS"})
	wantCode(t, "edit --value onto a held identity", err, errs.ContactTaken)
	_, err = contactEdit(ctx, f, ContactEditIn{ID: plain.Contact.ID, Value: "mine"})
	wantCode(t, "edit --value onto own twin", err, errs.InvalidInput)
}

// TestContactMerge: an identity email whose own actor row exists (a
// duplicate made before the contact was known, 6.4).
func TestContactMerge(t *testing.T) {
	ctx := context.Background()
	f, mira, _ := world(t)
	dup := person(t, f, "mirap", "mira.patel", "mira.patel@gmail.com")
	in := ContactAddIn{Actor: "@mira", Type: "email", Value: "mira.patel@gmail.com", Identity: true}
	_, err := contactAdd(ctx, f, in)
	wantCode(t, "no --merge", err, errs.ContactTaken)
	in.Merge = true
	_, err = contactAdd(ctx, f, in)
	wantCode(t, "--merge without --yes", err, errs.NeedsConfirm)
	if len(live(f, schema.TableActorContacts)) != 0 || f.Pushes != 0 {
		t.Fatal("an unconfirmed merge wrote")
	}
	// confirmed (--yes): the contact is added and the duplicate merged
	out, err := contactAdd(yes(), f, in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Merged != dup.String() || !strings.Contains(out.Text(), "merged "+dup.String()) {
		t.Fatalf("out %+v", out)
	}
	r, _, _ := f.Roster(ctx, false)
	d, _ := r.Get(ctx, schema.TableActors, dup.Any())
	if text(d, schema.FieldActorMergedInto) != mira.String() || flag(d, schema.FieldActorActive) || text(d, schema.FieldActorStatus) != schema.ActorStatusLeft {
		t.Fatalf("dup %v", d.Fields)
	}
	// once merged, the duplicate is the same person: a second identity
	// email on it, or the email again, is no longer taken
	if _, err := contactAdd(ctx, f, ContactAddIn{Actor: "@mira", Type: "github", Value: "mirap", Identity: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := contactAdd(ctx, f, ContactAddIn{Actor: "@mirap", Type: "github", Value: "mirap", Identity: true}); err != nil {
		t.Fatalf("merged duplicate counted as another person: %v", err)
	}
}

// TestContactEditRemove: edit rules, remove to the trash, bad ids.
func TestContactEditRemove(t *testing.T) {
	ctx := context.Background()
	f, mira, _ := world(t)
	out, err := contactAdd(ctx, f, ContactAddIn{Actor: "@mira", Type: "github", Value: "mirap", Label: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	id := out.Contact.ID
	e, err := contactEdit(ctx, f, ContactEditIn{ID: id, Value: "https://github.com/MiraP2", Label: "client-acme", Note: "for the acme repos", Identity: true})
	if err != nil {
		t.Fatal(err)
	}
	if c := e.Contact; c.ID != id || c.ValueNorm != "mirap2" || c.Value != "https://github.com/MiraP2" || c.Label != "client-acme" || !c.Identity || c.Note == "" {
		t.Fatalf("edited %+v", c)
	}
	if _, err := contactEdit(ctx, f, ContactEditIn{ID: id, NoIdentity: true}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		in   ContactEditIn
		code errs.Code
	}{
		{"nothing", ContactEditIn{ID: id}, errs.InvalidInput},
		{"both primary flags", ContactEditIn{ID: id, Primary: true, NoPrimary: true}, errs.InvalidInput},
		{"both identity flags", ContactEditIn{ID: id, Identity: true, NoIdentity: true}, errs.InvalidInput},
		{"not an acn id", ContactEditIn{ID: mira.String(), Label: "x"}, errs.InvalidID},
		{"garbage id", ContactEditIn{ID: "nope", Label: "x"}, errs.InvalidID},
		{"bad value", ContactEditIn{ID: id, Value: "two words"}, errs.ContactType},
	} {
		_, err := contactEdit(ctx, f, c.in)
		wantCode(t, c.name, err, c.code)
	}
	phone, err := contactAdd(ctx, f, ContactAddIn{Actor: "@mira", Type: "phone", Value: "+90 532 000 0000"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = contactEdit(ctx, f, ContactEditIn{ID: phone.Contact.ID, Identity: true})
	wantCode(t, "edit --identity on phone", err, errs.ContactType)

	rm, err := contactRemove(ctx, f, ContactRemoveIn{ID: id})
	if err != nil || rm.Contact.Value != "https://github.com/MiraP2" {
		t.Fatalf("remove %+v %v", rm, err)
	}
	if n := len(contactsOf(f, mira.String(), "github")); n != 0 {
		t.Fatalf("removed contact still live (%d)", n)
	}
	_, err = contactRemove(ctx, f, ContactRemoveIn{ID: id})
	wantCode(t, "remove twice", err, errs.InTrash)
	_, err = contactEdit(ctx, f, ContactEditIn{ID: id, Label: "x"})
	wantCode(t, "edit removed", err, errs.InTrash)
	// re-adding the removed contact makes a new live row
	if _, err := contactAdd(ctx, f, ContactAddIn{Actor: "@mira", Type: "github", Value: "mirap2"}); err != nil {
		t.Fatalf("re-add: %v", err)
	}
}

// TestContactRetiredType: a retired type is out of add (E_CONTACT_TYPE)
// while its contacts still edit and list.
func TestContactRetiredType(t *testing.T) {
	ctx := context.Background()
	f, _, _ := world(t)
	out, err := contactAdd(ctx, f, ContactAddIn{Actor: "@mira", Type: "discord", Value: "mira#1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contactTypeEdit(ctx, f, ContactTypeEditIn{Name: "discord", Retire: true}); err != nil {
		t.Fatal(err)
	}
	_, err = contactAdd(ctx, f, ContactAddIn{Actor: "@mira", Type: "discord", Value: "mira#2"})
	wantCode(t, "add on retired", err, errs.ContactType)
	if _, err := contactEdit(ctx, f, ContactEditIn{ID: out.Contact.ID, Label: "old"}); err != nil {
		t.Fatalf("edit on retired type: %v", err)
	}
	if l, _ := contactList(ctx, f, ContactListIn{Type: "discord"}); len(l.Contacts) != 1 {
		t.Fatalf("list %+v", l)
	}
}

// TestContactLookupTrashMatrix (DELETION 3.2): a contact is referenced by
// its acn_ id (actor.contact.edit, actor.contact.remove; padded with
// spaces too, the id is trimmed). Live: the op runs. Trashed (removed):
// E_IN_TRASH with `lore trash restore <id>` in the hint, the answer every
// trashed row gives. Purged, or an id never written: E_NOT_FOUND.
func TestContactLookupTrashMatrix(t *testing.T) {
	ctx := context.Background()
	const (
		stLive    = "live"
		stTrashed = "trashed"
		stPurged  = "purged"
		stNever   = "never existed"
	)
	refs := []struct {
		name string
		form func(id string) string
	}{
		{"acn_ id", func(id string) string { return id }},
		{"padded acn_ id", func(id string) string { return "  " + id + " " }},
	}
	type doFn func(f Env, ref string) error
	verbs := []struct {
		name string
		do   doFn
	}{
		{"edit", func(f Env, ref string) error {
			_, err := contactEdit(ctx, f, ContactEditIn{ID: ref, Label: "client-acme"})
			return err
		}},
		{"remove", func(f Env, ref string) error {
			_, err := contactRemove(ctx, f, ContactRemoveIn{ID: ref})
			return err
		}},
	}
	want := map[string]errs.Code{stTrashed: errs.InTrash, stPurged: errs.NotFound, stNever: errs.NotFound}
	for _, state := range []string{stLive, stTrashed, stPurged, stNever} {
		for _, rf := range refs {
			for _, v := range verbs {
				t.Run(state+"/"+rf.name+"/"+v.name, func(t *testing.T) {
					f, _, _ := world(t)
					out, err := contactAdd(ctx, f, ContactAddIn{Actor: "@mira", Type: "github", Value: "octo-personal"})
					if err != nil {
						t.Fatal(err)
					}
					id := out.Contact.ID
					switch state {
					case stTrashed, stPurged:
						if _, err := contactRemove(ctx, f, ContactRemoveIn{ID: id}); err != nil {
							t.Fatal(err)
						}
						if state == stPurged {
							aid, perr := ids.ParseAny(id)
							if perr != nil {
								t.Fatal(perr)
							}
							f.Purge(schema.TableActorContacts, aid)
						}
					case stNever:
						other, oerr := contactAdd(ctx, f, ContactAddIn{Actor: "@ben", Type: "github", Value: "octo-work"})
						if oerr != nil {
							t.Fatal(oerr)
						}
						// a well-formed acn_ id no row has: the other
						// contact's id with its last digit changed
						last := other.Contact.ID[len(other.Contact.ID)-1]
						repl := byte('1')
						if last == repl {
							repl = '2'
						}
						id = other.Contact.ID[:len(other.Contact.ID)-1] + string(repl)
					}
					err = v.do(f, rf.form(id))
					if state == stLive {
						if err != nil {
							t.Fatalf("live contact: %v", err)
						}
						return
					}
					wantCode(t, state, err, want[state])
					hint := errs.EnvelopeOf(err).Error.Hint
					if restore := "lore trash restore --target " + id; (state == stTrashed) != strings.Contains(hint, restore) {
						t.Fatalf("%s: hint %q, restore hint wanted only for a trashed contact", state, hint)
					}
				})
			}
		}
	}
}

// counterEnv is a fake that also counts a duplicate's rows (the
// profilestore.ActorRowCounter upgrade wire.App implements).
type counterEnv struct {
	*profilestoretest.Fake
	counts []profilestore.TableCount
	err    error
}

func (c counterEnv) ActorRowCounts(context.Context, ids.AnyID) ([]profilestore.TableCount, error) {
	return c.counts, c.err
}

// TestContactMergePreviewRowCounts: 6.4, the --merge question shows the
// duplicate's row counts per table, says so when there are none or when
// they cannot be read, and never blocks on them.
func TestContactMergePreviewRowCounts(t *testing.T) {
	cases := []struct {
		name string
		env  func(*profilestoretest.Fake) Env
		want string
	}{
		{"counts", func(f *profilestoretest.Fake) Env {
			return counterEnv{Fake: f, counts: []profilestore.TableCount{{Table: "comments", Rows: 3}, {Table: "tasks", Rows: 12}}}
		}, "rows it wrote: comments 3, tasks 12"},
		{"none", func(f *profilestoretest.Fake) Env { return counterEnv{Fake: f} }, "rows it wrote: none in the stores read here"},
		{"unreadable", func(f *profilestoretest.Fake) Env {
			return counterEnv{Fake: f, err: errs.New(errs.Internal, "store x is down")}
		}, "its row counts could not be read here ([E_INTERNAL] store x is down)"},
		{"no upgrade", func(f *profilestoretest.Fake) Env { return f }, "its row counts are not shown by this surface"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, _, _ := world(t)
			person(t, f, "mirap", "mira.patel", "mira.patel@gmail.com")
			in := ContactAddIn{Actor: "@mira", Type: "email", Value: "mira.patel@gmail.com", Identity: true, Merge: true}
			_, err := contactAdd(context.Background(), c.env(f), in)
			wantCode(t, c.name, err, errs.NeedsConfirm)
			var e *errs.Error
			if !errors.As(err, &e) || !strings.HasSuffix(e.Question, "; "+c.want) {
				t.Fatalf("question = %q, want it to end with %q", e.Question, c.want)
			}
			// the counts never block: confirmed, the merge goes through
			if _, err := contactAdd(yes(), c.env(f), in); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestWritesCarryEngineWarnings: the profile writes (openWrite) copy the
// roster's engine warnings into their output; "already there" writes none.
func TestWritesCarryEngineWarnings(t *testing.T) {
	const warn = "W_SCAN_DEGRADED the secret scan ran without its rules file"
	f, _, _ := world(t)
	f.WarnOnWrite = warn
	in := ContactAddIn{Actor: "@mira", Type: "github", Value: "mirap"}
	c, err := contactAdd(context.Background(), f, in)
	if err != nil || len(c.Warnings) != 1 || !strings.HasSuffix(c.Text(), "\n"+warn+"\n") {
		t.Fatalf("contact add %q %v", c.Warnings, err)
	}
	e, err := actorEdit(context.Background(), f, ActorEditIn{Actor: "@mira", Title: "Backend engineer"})
	if err != nil || len(e.Warnings) != 1 || !strings.HasSuffix(e.Text(), "\n"+warn+"\n") {
		t.Fatalf("actor edit %q %v", e.Warnings, err)
	}
	again, err := contactAdd(context.Background(), f, in)
	if err != nil || len(again.Warnings) != 0 {
		t.Fatalf("already there %q %v", again.Warnings, err)
	}
}
