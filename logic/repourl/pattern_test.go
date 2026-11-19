package repourl

import "testing"

// TestPatternMatch is the matrix of `*` (one segment), `**` (zero or more)
// and literals against normalised remotes, plus the refused patterns.
func TestPatternMatch(t *testing.T) {
	cases := []struct {
		pat, remote string
		want        bool
	}{
		{"github.com/acme/*", "github.com/acme/web", true},
		{"github.com/acme/*", "github.com/acme/web/sub", false},
		{"github.com/acme/*", "github.com/other/web", false},
		{"github.com/acme/**", "github.com/acme", true},
		{"github.com/acme/**", "github.com/acme/a/b/c", true},
		{"github.com/**/web", "github.com/acme/team/web", true},
		{"github.com/**/web", "github.com/acme/team/api", false},
		{"github.com/acme/web", "github.com/acme/web", true},
		{"github.com/acme/web", "gitlab.com/acme/web", false},
	}
	for _, tc := range cases {
		p, err := Compile(tc.pat)
		if err != nil {
			t.Fatalf("Compile(%q): %v", tc.pat, err)
		}
		if got := p.Match(tc.remote); got != tc.want {
			t.Errorf("%q.Match(%q) = %v, want %v", tc.pat, tc.remote, got, tc.want)
		}
	}
	if (Pattern{}).Match("github.com/acme/web") {
		t.Error("the zero Pattern matched a remote")
	}
}

// TestCompileRefuses: a wildcard host, two `**`, a partial wildcard and an
// empty pattern are refused (a wildcard host would match every company).
func TestCompileRefuses(t *testing.T) {
	for _, raw := range []string{"", "*/acme/web", "**/web", "github.com/**/x/**", "github.com/ac**/web", "git*.com/acme/web"} {
		if _, err := Compile(raw); err == nil {
			t.Errorf("Compile(%q) accepted", raw)
		}
	}
}

// TestPatternStarInsideASegment: `*` may sit inside a segment (prefix-*,
// *-suffix, a-*-b) and never crosses a "/" (devco D-453).
func TestPatternStarInsideASegment(t *testing.T) {
	cases := []struct {
		pat, val string
		want     bool
	}{
		{"github.com/acme/client-*", "github.com/acme/client-shop", true},
		{"github.com/acme/client-*", "github.com/acme/client-", true},
		{"github.com/acme/client-*", "github.com/acme/clients", false},
		{"github.com/acme/client-*", "github.com/acme/client-a/b", false},
		{"github.com/acme/*-api", "github.com/acme/shop-api", true},
		{"github.com/acme/*-api", "github.com/acme/shop-api-old", false},
		{"github.com/acme/a-*-b", "github.com/acme/a-x-b", true},
		{"github.com/acme/a-*-b", "github.com/acme/a-b", false},
		{"github.com/team-*/app", "github.com/team-red/app", true},
		{"github.com/team-*/**", "github.com/team-red/x/y", true},
	}
	for _, c := range cases {
		p, err := Compile(c.pat)
		if err != nil {
			t.Fatalf("Compile(%q): %v", c.pat, err)
		}
		if got := p.Match(c.val); got != c.want {
			t.Errorf("%s matches %s = %v, want %v", c.pat, c.val, got, c.want)
		}
	}
}
