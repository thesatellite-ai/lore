package repourl

import "testing"

// TestNormalizeStoreDrivePath (B3-27): a drive path store url has the same
// store id in either separator, and is fetchable (a local repository).
func TestNormalizeStoreDrivePath(t *testing.T) {
	t.Parallel()
	for _, in := range []string{`C:\git\notes.git`, `C:/git/notes.git`, `/C:/git/notes.git`} {
		if got, err := NormalizeStore(in); err != nil || got != "_local/C:/git/notes" {
			t.Errorf("NormalizeStore(%q) = %q, %v", in, got, err)
		}
		if err := CheckFetchable(in); err != nil {
			t.Errorf("CheckFetchable(%q) = %v", in, err)
		}
	}
}

// TestShownStore (B3-30): a local store's id reads as its path, a hosted
// one as is.
func TestShownStore(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"_local/srv/git/team":  "/srv/git/team",
		"_local/C:/git/team":   "C:/git/team",
		"github.com/acme/team": "github.com/acme/team",
	} {
		if got := ShownStore(in); got != want {
			t.Errorf("ShownStore(%q) = %q, want %q", in, got, want)
		}
	}
}
