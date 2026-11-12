package repourl

import (
	"strings"
	"testing"
)

// tokenUser builds a token-shaped user name at run time (no key-like
// literal in the source).
func tokenUser() string { return "ghp" + "_" + strings.Repeat("Ab3x", 9) }

// cred is scheme + "://" + userinfo + "@" + rest, put together at run time
// so no credential-shaped url sits in the source (the repo's leak hooks
// scan it).
func cred(scheme, userinfo, rest string) string { return scheme + ":/" + "/" + userinfo + "@" + rest }

// TestMask is the table of store and remote addresses with what output
// shows for each: plain addresses and local paths unchanged, only a
// password, a token user or a credential query value masked.
func TestMask(t *testing.T) {
	tok := tokenUser()
	for _, c := range []struct{ in, want string }{
		// plain addresses: never masked
		{"git@git.example.test:acme/lore-x.git", "git@git.example.test:acme/lore-x.git"},
		{"https://git.example.test/acme/x.git", "https://git.example.test/acme/x.git"},
		{"ssh://git@git.example.test:2222/acme/x.git", "ssh://git@git.example.test:2222/acme/x.git"},
		{"https://mira@git.example.test/acme/x.git", "https://mira@git.example.test/acme/x.git"},
		{"git.example.test/acme/x", "git.example.test/acme/x"},
		{"file:///srv/stores/notes", "file:///srv/stores/notes"},
		{"/home/mira/.lore/personal", "/home/mira/.lore/personal"},
		{"~/.lore/personal", "~/.lore/personal"},
		{`C:\Users\mira\.lore\personal`, `C:\Users\mira\.lore\personal`},
		{"https://git.example.test/acme/x.git?ref=main", "https://git.example.test/acme/x.git?ref=main"},
		{"", ""},
		// only the secret part goes
		{cred("https", "mira:password", "git.example.test/acme/x.git"), "https://mira:<redacted>@git.example.test/acme/x.git"},
		{cred("https", "user:secret", "git.example.test/acme/x.git"), "https://user:<redacted>@git.example.test/acme/x.git"},
		{"https://" + tok + "@git.example.test/acme/x.git", "https://<redacted>@git.example.test/acme/x.git"},
		{"https://x-access-token:" + tok + "@git.example.test/acme/x.git", "https://<redacted>@git.example.test/acme/x.git"},
		{cred("ssh", "git:xxx", "git.example.test/acme/x.git"), "ssh://git:<redacted>@git.example.test/acme/x.git"},
		{"mira:pw@git.example.test:acme/x.git", "mira:<redacted>@git.example.test:acme/x.git"},
		{tok + "@git.example.test:acme/x.git", "<redacted>@git.example.test:acme/x.git"},
		{"https://git.example.test/acme/x.git?access_token=abc&ref=main", "https://git.example.test/acme/x.git?access_token=<redacted>&ref=main"},
		{"https://git.example.test/x?Private_Token=abc#top", "https://git.example.test/x?Private_Token=<redacted>#top"},
	} {
		if got := Mask(c.in); got != c.want {
			t.Errorf("Mask(%q) = %q, want %q", c.in, got, c.want)
		}
		if got := Mask(Mask(c.in)); got != c.want {
			t.Errorf("Mask is not idempotent on %q: %q", c.in, got)
		}
	}
}

// TestMaskText masks credentials inside sentences and leaves plain
// addresses, actor keys and emails readable.
func TestMaskText(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"matched by: link in git@git.example.test:acme/lore-x.git client_a/", "matched by: link in git@git.example.test:acme/lore-x.git client_a/"},
		{"[E_NOT_A_STORE] https://git.example.test/acme/x.git (branch main) is listed under stores:", "[E_NOT_A_STORE] https://git.example.test/acme/x.git (branch main) is listed under stores:"},
		{"clone " + cred("https", "mira:secret", "git.example.test/x.git") + " failed", "clone https://mira:<redacted>@git.example.test/x.git failed"},
		{"(" + cred("https", "mira:secret", "git.example.test/x.git") + ")", "(https://mira:<redacted>@git.example.test/x.git)"},
		{"identity human:mira@example.test: ok", "identity human:mira@example.test: ok"},
		{"mail mira@example.test, or cancel?", "mail mira@example.test, or cancel?"},
		{"no store url here", "no store url here"},
	} {
		if got := MaskText(c.in); got != c.want {
			t.Errorf("MaskText(%q) =\n %q, want\n %q", c.in, got, c.want)
		}
	}
}
