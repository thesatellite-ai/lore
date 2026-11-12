package onboard

import "testing"

// TestLocalStoreDetail: the local choice is the one personal store `me`;
// the page names the other setups already on it (own url or inherited).
func TestLocalStoreDetail(t *testing.T) {
	me := "file:///h/.lore/stores/_me"
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"no setups", Config{}, LocalStoreURL},
		{"only itself", Config{Setups: []SetupEntry{{Name: "joba", PersonalStore: me}}}, LocalStoreURL},
		{"other on me", Config{Setups: []SetupEntry{{Name: "jobb", PersonalStore: me}}}, LocalStoreURL + " (your one personal store, shared with your setup jobb)"},
		{"two others", Config{SharedStore: me, Setups: []SetupEntry{{Name: "jobb"}, {Name: "home", PersonalStore: me}}}, LocalStoreURL + " (your one personal store, shared with your setups jobb, home)"},
		{"other on a repo", Config{Setups: []SetupEntry{{Name: "jobb", PersonalStore: "git@github.com:pat/notes.git"}}}, LocalStoreURL},
		{"other on none", Config{SharedStore: me, Setups: []SetupEntry{{Name: "jobb", PersonalStore: PersonalStoreNone}}}, LocalStoreURL},
	}
	for _, c := range cases {
		if got := LocalStoreDetail(c.cfg, "joba"); got != c.want {
			t.Errorf("%s: LocalStoreDetail = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestIsDefaultLocalStore: the default local folder is stores/_me (and the
// legacy stores/me a not yet moved config may still name); a folder
// elsewhere is not.
func TestIsDefaultLocalStore(t *testing.T) {
	for in, want := range map[string]bool{
		PersonalStoreLocal:                true,
		"file:///h/.lore/stores/_me":      true,
		"file:///h/.lore/stores/me":       true,
		"file:///h/.lore/stores/me/notes": false,
		"file:///h/notes":                 false,
		"git@github.com:me/notes.git":     false,
	} {
		if got := IsDefaultLocalStore(in); got != want {
			t.Errorf("IsDefaultLocalStore(%q) = %v, want %v", in, got, want)
		}
	}
	if LocalStoreURL != "file://~/.lore/stores/_me" {
		t.Errorf("LocalStoreURL = %q, want file://~/.lore/stores/_me", LocalStoreURL)
	}
}
