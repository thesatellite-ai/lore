package repourl

import "testing"

// TestNormalizeStoreLocalPath: a store url that is a plain absolute path is
// a git repository on this machine, so it gets a store id; Normalize (the
// repo-identity form) keeps refusing it.
func TestNormalizeStoreLocalPath(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"/srv/git/notes.git":            "_local/srv/git/notes",
		"/srv/git/notes":                "_local/srv/git/notes",
		"/srv/git/notes.git/":           "_local/srv/git/notes",
		"git@github.com:acme/notes.git": "github.com/acme/notes",
	} {
		if got, err := NormalizeStore(in); err != nil || got != want {
			t.Errorf("NormalizeStore(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"/", "/srv/../x"} {
		if got, err := NormalizeStore(bad); err == nil {
			t.Errorf("NormalizeStore(%q) = %q, want an error", bad, got)
		}
	}
	if _, err := Normalize("/srv/git/notes.git"); err == nil {
		t.Error("Normalize accepted a local path as a repo identity")
	}
	if err := CheckFetchable("/srv/git/notes.git"); err != nil {
		t.Errorf("CheckFetchable(local path) = %v", err)
	}
}

// TestIsLocalRemote (devco:O-13): paths and file:// urls are remotes on
// this machine; hosted urls and git's scp-like form are not.
func TestIsLocalRemote(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]bool{
		"/srv/git/kit.git":               true,
		"../kit":                         true,
		"./kit.git":                      true,
		"kit.git":                        false,
		"file:///srv/git/kit.git":        true,
		`C:\git\kit.git`:                 true,
		"https://git.example.test/a/kit": false,
		"ssh://git@git.example.test/a/k": false,
		"git@git.example.test:a/kit.git": false,
		"git.example.test:a/kit.git":     false,
		"":                               false,
	} {
		if got := IsLocalRemote(in); got != want {
			t.Errorf("IsLocalRemote(%q) = %v, want %v", in, got, want)
		}
	}
}
