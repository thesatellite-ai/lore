package onboard

import "testing"

// TestParseGitURLLocalPath (D1): a local git repository path is a store
// url the setup steps take, in every form lore accepts on the command line.
func TestParseGitURLLocalPath(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"/srv/git/me.git", "~/git/me.git", `C:\git\me.git`, "C:/git/me.git", "/My Stores/me.git"} {
		if u, why, ok := ParseGitURL(in); !ok || u.Scheme != GitSchemePath {
			t.Errorf("ParseGitURL(%q) = %+v, %q, %v; want a local path", in, u, why, ok)
		}
	}
	for _, in := range []string{"local", "me.git", "/srv/a\nb"} {
		if _, _, ok := ParseGitURL(in); ok {
			t.Errorf("ParseGitURL(%q) accepted", in)
		}
	}
}
