package onboard

import (
	"strings"
	"testing"
)

func TestEnabledSkipsDisabled(t *testing.T) {
	c := Config{Setups: []SetupEntry{{Name: "a"}, {Name: "b", Disabled: true}, {Name: "c"}}}
	got := c.Enabled()
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "c" {
		t.Fatalf("Enabled = %v", got)
	}
	if n := len((Config{}).Enabled()); n != 0 {
		t.Fatalf("empty config: %d", n)
	}
}

func TestNewestTrashed(t *testing.T) {
	trash := []TrashedSetup{
		{Setup: SetupEntry{Name: "x"}, RemovedAt: "2026-01-02T00:00:00Z"},
		{Setup: SetupEntry{Name: "y"}, RemovedAt: "2026-01-03T00:00:00Z"},
		{Setup: SetupEntry{Name: "x"}, RemovedAt: "2026-01-05T00:00:00Z"},
		{Setup: SetupEntry{Name: "x"}, RemovedAt: "2026-01-04T00:00:00Z"},
	}
	for _, tc := range []struct {
		name string
		want int
	}{{"x", 2}, {"y", 1}, {"z", -1}, {"", -1}} {
		if got := NewestTrashed(trash, tc.name); got != tc.want {
			t.Errorf("NewestTrashed(%q) = %d, want %d", tc.name, got, tc.want)
		}
	}
	if NewestTrashed(nil, "x") != -1 {
		t.Fatal("nil trash")
	}
}

func TestPurgeKeepsAlwaysKeepsRepos(t *testing.T) {
	cases := []struct {
		name            string
		t               TrashedSetup
		identity, store string
		want, notWant   []string
	}{
		{"bare", TrashedSetup{Setup: SetupEntry{Name: "s"}}, "", "", []string{"never deletes a repository"}, []string{"identity", "org "}},
		{"identity and store", TrashedSetup{Setup: SetupEntry{Name: "s", PersonalStore: PersonalStoreInherit}}, "Ada Example", "git@github.com:example/lore-me.git",
			[]string{"identity (Ada Example)", "personal store git@github.com:example/lore-me.git", "never deletes"}, []string{"store repo"}},
		{"own store", TrashedSetup{Setup: SetupEntry{Name: "s", PersonalStore: "git@github.com:example/own.git"}}, "", "",
			[]string{"store repo git@github.com:example/own.git"}, nil},
		{"none store", TrashedSetup{Setup: SetupEntry{Name: "s", PersonalStore: PersonalStoreNone}}, "", "", nil, []string{"store repo"}},
		{"org", TrashedSetup{Setup: SetupEntry{Name: "s", Kind: KindMember, Org: "acme"}}, "", "", []string{"the org acme"}, nil},
	}
	for _, tc := range cases {
		got := strings.Join(PurgeKeeps(tc.t, tc.identity, tc.store), "\n")
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: missing %q in\n%s", tc.name, w, got)
			}
		}
		for _, w := range tc.notWant {
			if strings.Contains(got, w) {
				t.Errorf("%s: unexpected %q in\n%s", tc.name, w, got)
			}
		}
		if !strings.HasSuffix(got, "lore never deletes a repository") {
			t.Errorf("%s: the repo line must be last: %s", tc.name, got)
		}
	}
}

func TestValidateNoteLength(t *testing.T) {
	ok := []SetupEntry{{Name: "a", Kind: KindSolo, Note: strings.Repeat("é", NoteMaxLen)}}
	if p := Validate(ok, nil, Known{Home: "/h"}); len(p) != 0 {
		t.Fatalf("a %d-rune note is fine: %v", NoteMaxLen, p)
	}
	bad := []SetupEntry{{Name: "a", Kind: KindSolo, Note: strings.Repeat("x", NoteMaxLen+1)}}
	if p := Validate(bad, nil, Known{Home: "/h"}); len(p) != 1 || !strings.Contains(p[0], "note") {
		t.Fatalf("long note: %v", p)
	}
}

func TestImportBannerWording(t *testing.T) {
	cases := []struct {
		name string
		f    BannerFacts
		want string
	}{
		{"nothing proposed", BannerFacts{IdentityName: "Ada"}, ""},
		{"identity and store", BannerFacts{IdentityName: "Ada Example", PersonalStore: "git@github.com:example/lore-me.git", Proposals: []string{"personal"}},
			"Your lore identity (Ada Example) and personal store (example/lore-me) are still saved in ~/.lore/config.yaml — make a setup from them?"},
		{"identity only", BannerFacts{IdentityName: "Ada", ConfigPath: "~/.lore/config.yaml", Proposals: []string{"personal"}},
			"Your lore identity (Ada) is still saved in ~/.lore/config.yaml — make a setup from it?"},
		{"store only, https", BannerFacts{PersonalStore: "https://github.com/example/notes", Proposals: []string{"personal"}},
			"Your personal store (example/notes) is still saved in ~/.lore/config.yaml — make a setup from it?"},
		{"file store kept whole", BannerFacts{PersonalStore: "file:///srv/lore", Proposals: []string{"personal"}},
			"Your personal store (file:///srv/lore) is still saved in ~/.lore/config.yaml — make a setup from it?"},
		{"two proposals", BannerFacts{IdentityName: "Ada", Proposals: []string{"personal", "acme"}},
			"Your lore identity (Ada) is still saved in ~/.lore/config.yaml — make 2 setups from it?"},
		{"orgs only", BannerFacts{Proposals: []string{"acme"}},
			"Found lore repos and settings in ~/.lore/config.yaml that no setup uses — import 1 setup (acme)?"},
		{"legacy install", BannerFacts{Legacy: true, LegacyFiles: []string{"~/.lore/identity.yaml.migrated"}, IdentityName: "Ada", Proposals: []string{"personal"}},
			"Found an existing lore install (~/.lore/identity.yaml.migrated) — import 1 setup (personal)?"},
		{"legacy without paths", BannerFacts{Legacy: true, Proposals: []string{"personal", "acme"}},
			"Found an existing lore install (~/.lore) — import 2 setups (personal, acme)?"},
	}
	for _, tc := range cases {
		if got := ImportBanner(tc.f); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

func TestImportBannerKey(t *testing.T) {
	base := BannerFacts{IdentityName: "Ada", PersonalStore: "git@github.com:example/lore-me.git", Proposals: []string{"personal"}}
	k := ImportBannerKey(base)
	if len(k) != bannerKeyLen || strings.Trim(k, "0123456789abcdef") != "" {
		t.Fatalf("key %q is not %d hex characters", k, bannerKeyLen)
	}
	if ImportBannerKey(base) != k {
		t.Fatal("key is not stable")
	}
	// The path is not part of the key: the same values elsewhere stay dismissed.
	moved := base
	moved.ConfigPath = "/elsewhere/config.yaml"
	if ImportBannerKey(moved) != k {
		t.Fatal("config path changed the key")
	}
	for name, f := range map[string]BannerFacts{
		"identity": {IdentityName: "Bea", PersonalStore: base.PersonalStore, Proposals: base.Proposals},
		"store":    {IdentityName: "Ada", PersonalStore: "git@github.com:example/other.git", Proposals: base.Proposals},
		"proposal": {IdentityName: "Ada", PersonalStore: base.PersonalStore, Proposals: []string{"personal", "acme"}},
		"legacy":   {Legacy: true, IdentityName: "Ada", PersonalStore: base.PersonalStore, Proposals: base.Proposals},
		// field boundaries count: "ab"+"c" is not "a"+"bc".
		"boundary": {IdentityName: "Ada" + "git@github.com:example", PersonalStore: "/lore-me.git", Proposals: base.Proposals},
	} {
		if ImportBannerKey(f) == k {
			t.Errorf("changing %s kept the key", name)
		}
	}
}
