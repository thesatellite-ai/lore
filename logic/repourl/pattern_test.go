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
	for _, raw := range []string{"", "*/acme/web", "**/web", "github.com/**/x/**", "github.com/ac*/web"} {
		if _, err := Compile(raw); err == nil {
			t.Errorf("Compile(%q) accepted", raw)
		}
	}
}
