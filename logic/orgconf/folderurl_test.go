package orgconf

import "testing"

// TestIsFolderStoreURL (later 156.2, the v0.1.17 lorecfg isFolderStoreURL
// matrix on the core's copy): personal.store accepts a folder-store url
// only as file:// with an absolute, clean path on this machine (no host
// but localhost, no user, query, fragment or opaque form); anything else
// must be a git url, or Validate reports it.
func TestIsFolderStoreURL(t *testing.T) {
	for in, want := range map[string]bool{
		"file:///srv/lore/me":            true,
		"file://localhost/srv/lore/me":   true,
		"file:///":                       true,
		"file:///srv/lore/me/":           false, // not clean
		"file:///srv/../lore/me":         false,
		"file:///srv//lore":              false,
		"file://otherhost/srv/lore":      false,
		"file://relative/path":           false, // "relative" is a host
		"file:relative/path":             false, // opaque
		"file:///srv/lore?folder=x":      false,
		"file:///srv/lore#frag":          false,
		"file://user@localhost/srv/lore": false,
		"FILE:///srv/lore":               true, // url.Parse lowers the scheme
		"http:///srv/lore":               false,
		"/srv/lore/me":                   false,
		"":                               false,
		"file://%zz/x":                   false, // does not parse
	} {
		if got := isFolderStoreURL(in); got != want {
			t.Errorf("isFolderStoreURL(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestValidatePersonalStoreURL: Validate accepts personal.store as the
// local default, a folder-store url or a git url, and reports a file url
// that is not a clean absolute path on this machine.
func TestValidatePersonalStoreURL(t *testing.T) {
	for in, ok := range map[string]bool{
		PersonalStoreLocal:               true,
		"file:///srv/lore/me":            true,
		"git@github.com:ana/lore-me.git": true,
		"https://github.com/ana/lore-me": true,
		"file://otherhost/srv/lore/me":   false,
		"file:///srv/../lore/me":         false,
		"not a url at all":               false,
	} {
		c := Config{Personal: Personal{Store: in}}
		if err := c.Validate(); (err == nil) != ok {
			t.Errorf("personal.store %q: Validate() = %v, want ok=%v", in, err, ok)
		}
	}
}
