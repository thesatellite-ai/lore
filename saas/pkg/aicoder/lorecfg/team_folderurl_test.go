package lorecfg

import (
	"strings"
	"testing"
)

// isFolderStoreURL is the gate that lets a 0.1.18 folder-store url
// (`lore store init me` writes file://<home>/.lore/stores/me) through as
// personal.store; everything it refuses must still pass the git-url check
// or be reported. Matrix over scheme × host × path shape × extras ×
// odd names.
func TestIsFolderStoreURL(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		url  string
		want bool
	}{
		{"file:///home/a/.lore/stores/me", true},
		{"file://localhost/home/a/.lore/stores/me", true},
		{"file:///", true},
		{"file:///home/a/.lore/stores/m%C3%A9", true}, // unicode, escaped
		{"file:///home/a/.lore/stores/mé", true},      // unicode, raw
		{"file:///C:/Users/a/.lore/stores/me", true},  // Windows drive path
		{"file:///home/a/stores/with space", true},
		{"", false},
		{"local", false},
		{"/home/a/.lore/stores/me", false}, // a bare path is not a url
		{"file://host/home/a/me", false},   // remote host
		{"file://user@localhost/home/a/me", false},
		{"file:home/a/me", false},                             // opaque
		{"file://./relative", false},                          // host "."
		{"file:///home/a/../b", false},                        // not clean
		{"file:///home/a/%2E%2E/b", false},                    // escaped .. is still not clean
		{"file:///home/a/me/", false},                         // trailing slash is not clean
		{"file:///home//a", false},                            // double slash
		{"file:///home/a/me?x=1", false},                      // query
		{"file:///home/a/me#frag", false},                     // fragment
		{"FILE:///home/a/me", true},                           // scheme is case-insensitive
		{"https://github.com/a/lore", false},                  // git url: not a folder store
		{"git@github.com:a/lore.git", false},                  // scp-style git url
		{"file:///" + strings.Repeat("d/", 2000) + "x", true}, // huge
		{"file://%zz/x", false},                               // unparsable
	} {
		if got := isFolderStoreURL(c.url); got != c.want {
			t.Errorf("isFolderStoreURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// Through Validate, the way config.yaml is read: personal.store accepts
// "local", empty, a folder-store url and a git url, and reports anything
// else naming the key.
func TestValidate_PersonalStore(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		store string
		ok    bool
	}{
		{"", true},
		{PersonalStoreLocal, true},
		{"file:///home/a/.lore/stores/me", true},
		{"https://github.com/a/lore-personal", true},
		{"git@github.com:a/lore-personal.git", true},
		{"file:///home/a/../me", false},
		{"/home/a/.lore/stores/me", false},
		{"not a url", false},
	} {
		cfg := &Config{Personal: Personal{Store: c.store}}
		err := cfg.Validate()
		if (err == nil) != c.ok {
			t.Errorf("personal.store %q: err = %v, want ok=%v", c.store, err, c.ok)
			continue
		}
		if err != nil && !strings.Contains(err.Error(), "personal.store") {
			t.Errorf("personal.store %q: the error must name the key: %v", c.store, err)
		}
	}
}
