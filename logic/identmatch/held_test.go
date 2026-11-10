package identmatch

import "testing"

// TestHeld: Held answers a reference from rows Resolve never sees (the
// trashed contacts of a store) by exactly Resolve's matching rule, for
// every reference form that names an identity: the first usable holder by
// id, never an identity-false, client-store, actorless or other-type row.
func TestHeld(t *testing.T) {
	c := newCast(t)
	clientRow := Contact{ID: contactID(t, "acn_09000000000000000000000000000001"), Actor: c.omar, Type: "github", ValueNorm: "client-only", Identity: true, FromClient: true}
	actorless := Contact{ID: contactID(t, "acn_09000000000000000000000000000002"), Type: "github", ValueNorm: "nobody", Identity: true}
	// A second holder of mirap with a lower id: the first by id wins.
	early := Contact{ID: contactID(t, "acn_00000000000000000000000000000009"), Actor: c.omar, Type: "github", ValueNorm: "mirap", Identity: true}
	rows := append(append([]Contact{}, c.contacts...), clientRow, actorless, early)
	cases := []struct {
		name string
		ref  string
		want string // contact id, "" for no holder
	}{
		{"github form", "github:MiraP", early.ID.String()},
		{"github form, one holder", "github:mira-acme", "acn_01000000000000000000000000000003"},
		{"human: form", "human:Mira@Acme-Agency.com", "acn_01000000000000000000000000000001"},
		{"email: form", "email:mira.patel@gmail.com", "acn_01000000000000000000000000000002"},
		{"bare email", "mira.patel@gmail.com", "acn_01000000000000000000000000000002"},
		{"merged duplicate's login (the row, not its actor)", "github:mira-old", "acn_01000000000000000000000000000007"},
		{"identity false", "human:omar.work@acme-agency.com", ""},
		{"client store row", "github:client-only", ""},
		{"no actor", "github:nobody", ""},
		{"other type, same value", "gitlab:mirap", ""},
		{"unknown", "github:stranger", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, ok := KeyOf(tc.ref, c.types)
			if !ok {
				t.Fatalf("KeyOf(%q) found no key", tc.ref)
			}
			got, held := Held(key, c.types, rows)
			if held != (tc.want != "") || (held && got.ID.String() != v7(tc.want)) {
				t.Fatalf("Held(%s) = %v %v, want %q", key, got.ID, held, tc.want)
			}
		})
	}
	if _, held := Held(Key{Type: "github", ValueNorm: "mirap"}, c.types, nil); held {
		t.Fatal("Held over no rows found a holder")
	}
}
