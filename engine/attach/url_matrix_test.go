package attach

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestStoreURLCredentialMatrix (D24, SQ13, SECRETS 14.2 row 16, OV4): a
// store url a person adds (NormalizeURL) is refused when it carries a
// password or a token-shaped user, in https, ssh:// and scp form; a plain
// ssh login user is kept; an https plain user name is dropped silently.
// The read form (CleanURL) never refuses a credential, it removes it.
// Neither ever prints the credential.
func TestStoreURLCredentialMatrix(t *testing.T) {
	tok := "ghp_" + strings.Repeat("A", 20)
	random := "Zq8xK2pL9mW4vB7nR3tY6uE1" // high-entropy, no known prefix
	cases := []struct {
		name    string
		in      string
		refused bool   // NormalizeURL refuses with E_URL_CREDENTIALS
		norm    string // NormalizeURL result when not refused
		clean   string // CleanURL result
	}{
		{name: "https token user", in: "https://" + tok + "@github.com/a/b.git", refused: true, clean: "https://github.com/a/b.git"},
		{name: "https random token user", in: "https://" + random + "@github.com/a/b.git", refused: true, clean: "https://github.com/a/b.git"},
		{name: "https user+password", in: "https://bob:" + tok + "@github.com/a/b", refused: true, clean: "https://github.com/a/b"},
		{name: "https x-access-token", in: "https://x-access-token:" + tok + "@github.com/a/b", refused: true, clean: "https://github.com/a/b"},
		{name: "ssh token login user", in: "ssh://" + tok + "@github.com/a/b.git", refused: true, clean: "ssh://github.com/a/b.git"},
		{name: "ssh password", in: "ssh://git:" + tok + "@host:2222/a/b.git", refused: true, clean: "ssh://git@host:2222/a/b.git"},
		{name: "scp token user", in: tok + "@github.com:a/b.git", refused: true, clean: "ssh://github.com/a/b.git"},
		{name: "ssh plain login user", in: "ssh://git@github.com/a/b.git", norm: "ssh://git@github.com/a/b.git", clean: "ssh://git@github.com/a/b.git"},
		{name: "scp plain user", in: "git@github.com:a/b.git", norm: "ssh://git@github.com/a/b.git", clean: "ssh://git@github.com/a/b.git"},
		{name: "https plain user", in: "https://mira@github.com/a/b.git", norm: "https://mira@github.com/a/b.git", clean: "https://github.com/a/b.git"},
		{name: "https clean", in: "https://github.com/a/b.git", norm: "https://github.com/a/b.git", clean: "https://github.com/a/b.git"},
		{name: "file url", in: "file:///tmp/store", norm: "file:///tmp/store", clean: "file:///tmp/store"},
		{name: "unicode path", in: "https://github.com/a/büro.git", norm: "https://github.com/a/b%C3%BCro.git", clean: "https://github.com/a/b%C3%BCro.git"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NormalizeURL(c.in)
			switch {
			case c.refused:
				if !errs.Has(err, errs.URLUserinfo) {
					t.Fatalf("NormalizeURL = %q, %v; want E_URL_CREDENTIALS", got, err)
				}
				if strings.Contains(err.Error(), tok) || strings.Contains(err.Error(), random) {
					t.Fatalf("the error printed the credential: %v", err)
				}
			case err != nil || got != c.norm:
				t.Fatalf("NormalizeURL = %q, %v; want %q", got, err, c.norm)
			}
			clean, err := CleanURL(c.in)
			if err != nil || clean != c.clean {
				t.Fatalf("CleanURL = %q, %v; want %q", clean, err, c.clean)
			}
			if again, err := CleanURL(clean); err != nil || again != clean {
				t.Fatalf("CleanURL not idempotent: %q -> %q %v", clean, again, err)
			}
		})
	}
	for _, bad := range []string{"", "no scheme here", "/abs/path"} {
		if _, err := NormalizeURL(bad); !errs.Has(err, errs.StoreScheme) {
			t.Errorf("NormalizeURL(%q) = %v, want E_STORE_SCHEME", bad, err)
		}
		if _, err := CleanURL(bad); !errs.Has(err, errs.StoreScheme) {
			t.Errorf("CleanURL(%q) = %v, want E_STORE_SCHEME", bad, err)
		}
	}
}
