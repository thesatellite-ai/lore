package content

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestQuoteID: short values are quoted as given; long ASCII keeps
// v0.1.17's 48 bytes + "..."; multi-byte values are never cut inside a
// character.
func TestQuoteID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", `""`},
		{"pat_1", `"pat_1"`},
		{strings.Repeat("a", 48), `"` + strings.Repeat("a", 48) + `"`},
		{strings.Repeat("a", 49), `"` + strings.Repeat("a", 48) + `..."`},
		{strings.Repeat("é", 30), `"` + strings.Repeat("é", 24) + `..."`},
		{strings.Repeat("😀", 13), `"` + strings.Repeat("😀", 12) + `..."`},
		{"a" + strings.Repeat("😀", 13), `"a` + strings.Repeat("😀", 11) + `..."`},
	}
	for _, c := range cases {
		got := QuoteID(c.in)
		if got != c.want {
			t.Errorf("QuoteID(%q) = %q, want %q", c.in, got, c.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("QuoteID(%q) is not valid UTF-8", c.in)
		}
	}
}

// TestBody: exactly one of --body and the argument, not blank.
func TestBody(t *testing.T) {
	cases := []struct {
		flag, arg, want string
		ok              bool
	}{
		{"b", "", "b", true},
		{"", "a", "a", true},
		{"  x  ", "", "  x  ", true},
		{"b", "a", "", false},
		{"", "", "", false},
		{" \t\n", "", "", false},
	}
	for _, c := range cases {
		got, err := Body("pattern", c.flag, c.arg)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("Body(%q,%q) = %q, %v", c.flag, c.arg, got, err)
		}
	}
}

// TestScope is v0.1.17's precedence: --all-repos, --master-only,
// --no-inherit (with a repo), --repo, else project-wide; and which
// repo_id each scope keeps.
func TestScope(t *testing.T) {
	cases := []struct {
		repo                            string
		noInherit, master, all          bool
		want                            Scope
		keepsMaster, keepsR, keepsOther bool
	}{
		{"", false, false, false, ScopeMaster, true, false, false},
		{"r", false, false, false, ScopeInherit, true, true, false},
		{"r", true, false, false, ScopeRepoOnly, false, true, false},
		{"", true, false, false, ScopeMaster, true, false, false},
		{"r", true, true, false, ScopeMaster, true, false, false},
		{"r", true, true, true, ScopeAll, true, true, true},
		{"", false, false, true, ScopeAll, true, true, true},
	}
	for _, c := range cases {
		got := ScopeOf(c.repo, c.noInherit, c.master, c.all)
		if got != c.want {
			t.Errorf("ScopeOf(%+v) = %s, want %s", c, got, c.want)
			continue
		}
		if got.Keeps("", c.repo) != c.keepsMaster || got.Keeps("r", c.repo) != c.keepsR || got.Keeps("other", c.repo) != c.keepsOther {
			t.Errorf("%s keeps: master %v r %v other %v", got, got.Keeps("", c.repo), got.Keeps("r", c.repo), got.Keeps("other", c.repo))
		}
	}
}
