package onboard

import (
	"reflect"
	"testing"
)

// TestLeftovers is the matrix of who uses the top-level identity and
// personal.store: absent, unused, used by an inheriting / naming /
// disabled setup, not used by a member or by a setup with its own; and
// who left it: a purge (a tombstone) or the migration, never you.
func TestLeftovers(t *testing.T) {
	t.Parallel()
	const url = "file:///tmp/me"
	id := SetupIdentity{Key: "human:a@example.com", Name: "A"}
	idLeft := Leftover{Kind: LeftoverIdentity, Key: "identity", Value: "A (human:a@example.com)"}
	stLeft := Leftover{Kind: LeftoverPersonalStore, Key: "personal.store", Value: url}
	// gone is the tombstone a purge of a setup that used both left.
	gone := []PurgedSetup{{Name: "old", Kind: KindSolo, IdentitySHA: IdentityFingerprint(id.Key), StoreSHA: StoreFingerprint(url)}}
	solo := func(key, ps string) SetupEntry {
		return SetupEntry{Name: "s", Kind: KindSolo, Identity: SetupIdentity{Key: key}, PersonalStore: ps}
	}
	cases := []struct {
		name string
		c    Config
		want []Leftover
	}{
		{"nothing set", Config{}, []Leftover{}},
		{"both set, no setups (purged without clears)", Config{TopIdentity: id, SharedStore: url, Purged: gone}, []Leftover{idLeft, stLeft}},
		// Set by hand (identity set, store init me), nothing purged: yours.
		{"both set by hand, no setups", Config{TopIdentity: id, SharedStore: url}, []Leftover{}},
		{"a purge of another identity and store", Config{TopIdentity: id, SharedStore: url,
			Purged: []PurgedSetup{{Name: "x", Kind: KindSolo, IdentitySHA: IdentityFingerprint("human:z@example.com"), StoreSHA: StoreFingerprint("file:///tmp/z")}}}, []Leftover{}},
		{"the migration's identity", Config{TopIdentity: id, MigratedIdentityKey: "human:A@example.com"}, []Leftover{idLeft}},
		{"an identity set after the migration", Config{TopIdentity: id, MigratedIdentityKey: "human:old@example.com"}, []Leftover{}},
		{"both cleared by the purge", Config{}, []Leftover{}},
		{"solo with the same identity, inheriting", Config{TopIdentity: id, SharedStore: url, Setups: []SetupEntry{solo(id.Key, "")}}, []Leftover{}},
		{"solo with no identity uses the top one", Config{TopIdentity: id, Setups: []SetupEntry{solo("", PersonalStoreNone)}}, []Leftover{}},
		{"solo with another identity and its own store", Config{TopIdentity: id, SharedStore: url, Purged: gone, Setups: []SetupEntry{solo("human:b@example.com", "file:///tmp/other")}}, []Leftover{idLeft, stLeft}},
		{"explicit inherit uses the store", Config{SharedStore: url, Setups: []SetupEntry{solo("human:b@example.com", PersonalStoreInherit)}}, []Leftover{}},
		{"same url named uses the store", Config{SharedStore: url, Setups: []SetupEntry{solo("human:b@example.com", url)}}, []Leftover{}},
		{"none leaves the store over", Config{SharedStore: url, Purged: gone, Setups: []SetupEntry{solo("human:b@example.com", PersonalStoreNone)}}, []Leftover{stLeft}},
		{"member never uses the top identity", Config{TopIdentity: id, Purged: gone, Setups: []SetupEntry{{Name: "m", Kind: KindMember, Org: "acme", PersonalStore: PersonalStoreNone}}}, []Leftover{idLeft}},
		{"disabled setup still counts", Config{TopIdentity: id, SharedStore: url, Setups: []SetupEntry{func() SetupEntry { e := solo(id.Key, ""); e.Disabled = true; return e }()}}, []Leftover{}},
		{"trashed setup does not count", Config{TopIdentity: id, SharedStore: url, Purged: gone, Trash: []TrashedSetup{{Setup: solo(id.Key, "")}}}, []Leftover{idLeft, stLeft}},
		{"trashed, never purged: restorable, not left", Config{TopIdentity: id, SharedStore: url, Trash: []TrashedSetup{{Setup: solo(id.Key, "")}}}, []Leftover{}},
		{"identity with a key only", Config{TopIdentity: SetupIdentity{Key: "human:a@example.com"}, Purged: gone}, []Leftover{{Kind: LeftoverIdentity, Key: "identity", Value: "human:a@example.com"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := Leftovers(c.c); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Leftovers = %#v, want %#v", got, c.want)
			}
		})
	}
}

// TestLeftoverKinds pins the closed set and its config keys.
func TestLeftoverKinds(t *testing.T) {
	t.Parallel()
	if got := LeftoverIdentity.ConfigKey(); got != "identity" {
		t.Fatal(got)
	}
	if got := LeftoverPersonalStore.ConfigKey(); got != "personal.store" {
		t.Fatal(got)
	}
	if LeftoverKind("orgs").Valid() || !LeftoverPersonalStore.Valid() {
		t.Fatal("Valid is wrong")
	}
	if !reflect.DeepEqual(LeftoverIdentity.EnumValues(), []string{"identity", "personal_store"}) {
		t.Fatal("EnumValues")
	}
}
