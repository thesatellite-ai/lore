package identmatch

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
)

func actorID(t *testing.T, s string) ids.ID[ids.Actor] {
	t.Helper()
	a, err := ids.Parse[ids.Actor](v7(s))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func contactID(t *testing.T, s string) ids.AnyID {
	t.Helper()
	c, err := ids.ParseAny(v7(s))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// v7 makes a readable test id a valid UUIDv7 id: version nibble 7 and
// variant nibble 8 (both zero in the ids written here).
func v7(s string) string {
	b := []byte(s)
	const hexStart = 4 // after "act_"
	b[hexStart+12], b[hexStart+16] = '7', '8'
	return string(b)
}

// The cast of the matrix: Mira (two emails, two GitHub logins), Omar, a
// duplicate of Mira made from her personal email before the contact was
// known (merged into her), and a phone contact.
type cast struct {
	mira, omar, dup, ghost ids.ID[ids.Actor]
	actors                 []Actor
	contacts               []Contact
	types                  Types
}

func newCast(t *testing.T) cast {
	c := cast{
		mira:  actorID(t, "act_01000000000000000000000000000001"),
		omar:  actorID(t, "act_02000000000000000000000000000002"),
		dup:   actorID(t, "act_03000000000000000000000000000003"),
		ghost: actorID(t, "act_04000000000000000000000000000004"),
		types: TypesOf(nil),
	}
	c.actors = []Actor{{ID: c.mira}, {ID: c.omar}, {ID: c.dup, MergedInto: c.mira}}
	add := func(id string, a ids.ID[ids.Actor], typ, norm string, identity bool) {
		c.contacts = append(c.contacts, Contact{ID: contactID(t, id), Actor: a, Type: typ, ValueNorm: norm, Identity: identity})
	}
	add("acn_01000000000000000000000000000001", c.mira, "email", "mira@acme-agency.com", true)
	add("acn_01000000000000000000000000000002", c.mira, "email", "mira.patel@gmail.com", true)
	add("acn_01000000000000000000000000000003", c.mira, "github", "mira-acme", true)
	add("acn_01000000000000000000000000000004", c.mira, "github", "mirap", true)
	add("acn_01000000000000000000000000000005", c.mira, "phone", "+905320000000", false)
	add("acn_01000000000000000000000000000006", c.omar, "email", "omar.work@acme-agency.com", false) // not flagged for identity
	add("acn_01000000000000000000000000000007", c.dup, "github", "mira-old", true)                   // held by the merged duplicate
	return c
}

// TEAM_PROFILES 6.2 step 1: every reference form that names an identity
// reads as (type, value_norm); everything else is no key.
func TestIdentmatchKeyOf(t *testing.T) {
	types := TypesOf([]teamprofile.ContactType{{Name: "bitbucket", Kind: schema.ContactKindHandle, Identity: true}})
	cases := []struct {
		ref  string
		want Key
		ok   bool
	}{
		{"human:Mira.Patel@Gmail.com", Key{"email", "mira.patel@gmail.com"}, true},
		{"  mira.patel@gmail.com ", Key{"email", "mira.patel@gmail.com"}, true},
		{"MIRA.PATEL@GMAIL.COM", Key{"email", "mira.patel@gmail.com"}, true},
		{"email:mira.patel@gmail.com", Key{"email", "mira.patel@gmail.com"}, true},
		{"github:MiraP", Key{"github", "mirap"}, true},
		{"github:@mirap", Key{"github", "mirap"}, true},
		{"github:https://github.com/MiraP/", Key{"github", "mirap"}, true},
		{"gitlab:mira", Key{"gitlab", "mira"}, true},
		{"bitbucket:Mira", Key{"bitbucket", "mira"}, true}, // a user-added identity type
		{"phone:+90 532 000 0000", Key{}, false},           // phone is not an identity type
		{"linkedin:mirap", Key{}, false},
		{"slack:mira", Key{}, false},
		{"unknown:mira", Key{}, false},
		{"agent:auto/codex/3f9a1c", Key{}, false},
		{"@mira", Key{}, false},
		{"act_01000000000000000000000000000001", Key{}, false},
		{"Mira Patel", Key{}, false},
		{"human:not-an-email", Key{}, false},
		{"github:two words", Key{}, false},
		{"", Key{}, false},
	}
	for _, tc := range cases {
		got, ok := KeyOf(tc.ref, types)
		if ok != tc.ok || got != tc.want {
			t.Errorf("KeyOf(%q) = %v %v, want %v %v", tc.ref, got, ok, tc.want, tc.ok)
		}
	}
}

// TS-2 / 2.3: a row cannot take identity away from a built-in identity
// type or change its kind; a row can add an identity type and change a
// non-identity built-in.
func TestIdentmatchTypesOf(t *testing.T) {
	types := TypesOf([]teamprofile.ContactType{
		{Name: "github", Kind: schema.ContactKindText, Identity: false},
		{Name: "email", Kind: schema.ContactKindHandle, Identity: false, Retired: true},
		{Name: "phone", Kind: schema.ContactKindPhone, Identity: true},
		{Name: "bitbucket", Kind: schema.ContactKindHandle, Identity: true},
	})
	want := Types{
		"github": {Kind: schema.ContactKindHandle, Identity: true}, "email": {Kind: schema.ContactKindEmail, Identity: true},
		"phone": {Kind: schema.ContactKindPhone, Identity: true}, "bitbucket": {Kind: schema.ContactKindHandle, Identity: true},
	}
	for name, w := range want {
		if types[name] != w {
			t.Errorf("type %s = %+v, want %+v", name, types[name], w)
		}
	}
}

// TP-4: two emails and two logins of one person resolve to the same actor;
// the matrix also covers no match, a non-identity contact, a contact of a
// merged duplicate and a contact read from a client store.
func TestResolveMatrix(t *testing.T) {
	c := newCast(t)
	cases := []struct {
		name string
		ref  string
		want ids.ID[ids.Actor]
	}{
		{"work email", "human:mira@acme-agency.com", c.mira},
		{"personal email (second identity)", "human:Mira.Patel@gmail.com", c.mira},
		{"bare personal email", "mira.patel@gmail.com", c.mira},
		{"work login", "github:mira-acme", c.mira},
		{"personal login", "github:MiraP", c.mira},
		{"duplicate's login follows merged_into", "github:mira-old", c.mira},
		{"identity false never matches", "human:omar.work@acme-agency.com", ids.ID[ids.Actor]{}},
		{"unknown email", "human:someone@else.com", ids.ID[ids.Actor]{}},
		{"same value, other type", "gitlab:mirap", ids.ID[ids.Actor]{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, ok := KeyOf(tc.ref, c.types)
			if !ok {
				t.Fatalf("KeyOf(%q) found no key", tc.ref)
			}
			r := Resolve(key, c.types, c.actors, c.contacts)
			if r.Actor != tc.want || len(r.Ambiguous) != 0 {
				t.Fatalf("Resolve(%s) = %+v, want %s", key, r, tc.want)
			}
			if r.Matched() != !tc.want.IsZero() || (r.Matched() && r.Contact.IsZero()) {
				t.Fatalf("Matched/Contact inconsistent: %+v", r)
			}
		})
	}
}

// 6.5 target 1: a contact with identity false never matches, even when it
// is the only one holding the value.
func TestResolveIdentityFalseNeverMatches(t *testing.T) {
	c := newCast(t)
	r := Resolve(Key{"email", "omar.work@acme-agency.com"}, c.types, c.actors, c.contacts)
	if r.Matched() {
		t.Fatalf("identity false matched: %+v", r)
	}
	// the same contact flagged for identity does
	for i := range c.contacts {
		if c.contacts[i].ValueNorm == "omar.work@acme-agency.com" {
			c.contacts[i].Identity = true
		}
	}
	if r := Resolve(Key{"email", "omar.work@acme-agency.com"}, c.types, c.actors, c.contacts); r.Actor != c.omar {
		t.Fatalf("flagged contact: %+v", r)
	}
}

// 6.5 target 3: contacts read from a client store are never used.
func TestResolveClientStoreContactsIgnored(t *testing.T) {
	c := newCast(t)
	key := Key{"email", "intruder@client.example"}
	contacts := append(slices.Clone(c.contacts), Contact{ID: contactID(t, "acn_09000000000000000000000000000009"), Actor: c.omar, Type: "email", ValueNorm: key.ValueNorm, Identity: true, FromClient: true})
	if r := Resolve(key, c.types, c.actors, contacts); r.Matched() || len(r.Ambiguous) != 0 {
		t.Fatalf("a client-store contact matched: %+v", r)
	}
	// a client-store contact never makes an org match ambiguous either
	contacts = append(contacts, Contact{ID: contactID(t, "acn_09000000000000000000000000000010"), Actor: c.omar, Type: "email", ValueNorm: "mira.patel@gmail.com", Identity: true, FromClient: true})
	if r := Resolve(Key{"email", "mira.patel@gmail.com"}, c.types, c.actors, contacts); r.Actor != c.mira {
		t.Fatalf("client contact changed the org answer: %+v", r)
	}
}

// 6.5 target 4: one identity contact held by two actors gives no match and
// names both (fail closed), never the first one, whatever the order.
func TestResolveAmbiguousFailsClosed(t *testing.T) {
	c := newCast(t)
	clash := Contact{ID: contactID(t, "acn_00000000000000000000000000000001"), Actor: c.omar, Type: "github", ValueNorm: "mirap", Identity: true}
	for _, contacts := range [][]Contact{append(slices.Clone(c.contacts), clash), append([]Contact{clash}, c.contacts...)} {
		r := Resolve(Key{"github", "mirap"}, c.types, c.actors, contacts)
		if r.Matched() || !slices.Equal(r.Ambiguous, []ids.ID[ids.Actor]{c.mira, c.omar}) {
			t.Fatalf("two holders: %+v, want no match naming both", r)
		}
	}
	// the same contact on Mira and on her merged duplicate is one person
	same := Contact{ID: contactID(t, "acn_00000000000000000000000000000002"), Actor: c.dup, Type: "github", ValueNorm: "mirap", Identity: true}
	if r := Resolve(Key{"github", "mirap"}, c.types, c.actors, append(slices.Clone(c.contacts), same)); r.Actor != c.mira {
		t.Fatalf("a merged duplicate made it ambiguous: %+v", r)
	}
}

// A merged_into cycle (a broken merge) never loops.
func TestResolveFollowCycle(t *testing.T) {
	c := newCast(t)
	actors := []Actor{{ID: c.mira, MergedInto: c.dup}, {ID: c.dup, MergedInto: c.mira}}
	if got := Follow(c.dup, actors); got != c.mira {
		t.Fatalf("Follow through a cycle = %s, want %s (stop at the first repeat)", got, c.mira)
	}
	if got := Follow(c.ghost, actors); got != c.ghost {
		t.Fatalf("Follow of an actor without a chain = %s", got)
	}
}

// 3.2 / 6.4 / 6.5 target 5: CheckAdd never moves an identity contact from
// one actor to another; the same contact again is "already there"; an
// existing key actor row (a duplicate) is the --merge case.
func TestIdentmatchCheckAdd(t *testing.T) {
	c := newCast(t)
	keyRow := c.ghost // identity.ActorID("human:new@x") with a row
	cases := []struct {
		name   string
		check  AddCheck
		want   Outcome
		holder ids.ID[ids.Actor]
	}{
		{"free", AddCheck{Target: c.omar, Key: Key{"github", "omar"}}, AddFree, ids.ID[ids.Actor]{}},
		{"taken by another actor", AddCheck{Target: c.omar, Key: Key{"email", "mira.patel@gmail.com"}}, AddTaken, c.mira},
		{"taken beats a key actor row", AddCheck{Target: c.omar, Key: Key{"github", "mirap"}, KeyActor: keyRow}, AddTaken, c.mira},
		{"already the target's", AddCheck{Target: c.mira, Key: Key{"github", "mirap"}}, AddAlready, ids.ID[ids.Actor]{}},
		{"held by the target's merged duplicate", AddCheck{Target: c.mira, Key: Key{"github", "mira-old"}}, AddAlready, ids.ID[ids.Actor]{}},
		{"target is the merged duplicate", AddCheck{Target: c.dup, Key: Key{"github", "mirap"}}, AddAlready, ids.ID[ids.Actor]{}},
		{"non-identity contact is no holder", AddCheck{Target: c.mira, Key: Key{"email", "omar.work@acme-agency.com"}}, AddFree, ids.ID[ids.Actor]{}},
		{"key actor row exists", AddCheck{Target: c.mira, Key: Key{"email", "new@x.example"}, KeyActor: keyRow}, AddMergeable, keyRow},
		{"key actor row is the target", AddCheck{Target: c.mira, Key: Key{"email", "new@x.example"}, KeyActor: c.mira}, AddFree, ids.ID[ids.Actor]{}},
		{"key actor already merged into the target", AddCheck{Target: c.mira, Key: Key{"email", "new@x.example"}, KeyActor: c.dup}, AddFree, ids.ID[ids.Actor]{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := CheckAdd(tc.check, c.types, c.actors, c.contacts)
			if v.Outcome != tc.want || v.Holder != tc.holder {
				t.Fatalf("CheckAdd = %+v, want outcome %d holder %s", v, tc.want, tc.holder)
			}
		})
	}
}

// 6.4: Duplicates names every identity key held by two distinct actors
// (after following merged_into), and nothing else.
func TestDuplicates(t *testing.T) {
	c := newCast(t)
	if d := Duplicates(c.types, c.actors, c.contacts); len(d) != 0 {
		t.Fatalf("clean roster: %+v", d)
	}
	contacts := append(slices.Clone(c.contacts),
		Contact{ID: contactID(t, "acn_05000000000000000000000000000001"), Actor: c.omar, Type: "github", ValueNorm: "mirap", Identity: true},
		Contact{ID: contactID(t, "acn_05000000000000000000000000000002"), Actor: c.dup, Type: "email", ValueNorm: "mira.patel@gmail.com", Identity: true},          // merged: same person
		Contact{ID: contactID(t, "acn_05000000000000000000000000000003"), Actor: c.omar, Type: "phone", ValueNorm: "+905320000000", Identity: true},                // not an identity type
		Contact{ID: contactID(t, "acn_05000000000000000000000000000004"), Actor: c.omar, Type: "email", ValueNorm: "mira@acme-agency.com", Identity: false},        // not flagged
		Contact{ID: contactID(t, "acn_05000000000000000000000000000005"), Actor: c.omar, Type: "github", ValueNorm: "mira-acme", Identity: true, FromClient: true}, // client store
	)
	d := Duplicates(c.types, c.actors, contacts)
	if len(d) != 1 || d[0].Key != (Key{"github", "mirap"}) || !slices.Equal(d[0].Actors, []ids.ID[ids.Actor]{c.mira, c.omar}) || len(d[0].Contacts) != 2 {
		t.Fatalf("Duplicates = %+v, want github:mirap held by mira and omar", d)
	}
}
