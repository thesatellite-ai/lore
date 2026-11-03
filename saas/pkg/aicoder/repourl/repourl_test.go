package repourl

import "testing"

func TestNormalize(t *testing.T) {
	const want = "github.com/acme/app"
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		// every spelling of one repo collapses to the same form
		{"git@github.com:acme/app.git", want, false},
		{"git@github.com:acme/app", want, false},
		{"git@GitHub.com:acme/app.git/", want, false},
		{"ssh://git@github.com/acme/app.git", want, false},
		{"ssh://git@github.com:22/acme/app", want, false},
		{"https://github.com/acme/app.git", want, false},
		{"https://user:token@GITHUB.com/acme/app/", want, false},
		{"http://github.com/acme/app", want, false},
		{"git://github.com/acme/app.git", want, false},
		{"github.com/acme/app", want, false},
		{"github.com/acme/app.git", want, false},
		{"  github.com/acme/app  ", want, false},
		// path case is kept (GitLab paths are case-sensitive)
		{"https://gitlab.com/Acme/App.git", "gitlab.com/Acme/App", false},
		// nested groups
		{"git@gitlab.com:acme/team/app.git", "gitlab.com/acme/team/app", false},
		// git reads host:2222/x as scp syntax (path "2222/x"), never as a port;
		// lore must read it the same way git does
		{"git.example.com:2222/acme/app", "git.example.com/2222/acme/app", false},
		// refused: no host, local paths, empty, relative segments
		{"", "", true},
		{"/srv/git/app.git", "", true},
		{"./app", "", true},
		{"file:///srv/git/app.git", "", true},
		{"github.com", "", true},
		{"https://github.com/", "", true},
		{"https://github.com/acme/../app", "", true},
		{"https://github.com//app", "", true},
		{"~/notes", "", true},
		{"my host/acme/app", "", true},
	}
	for _, c := range cases {
		got, err := Normalize(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("Normalize(%q) err=%v, wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
		if err == nil {
			again, err2 := Normalize(got)
			if err2 != nil || again != got {
				t.Errorf("Normalize not idempotent for %q: %q, %v", c.in, again, err2)
			}
		}
	}
}

func TestPatternMatch(t *testing.T) {
	cases := []struct {
		pattern string
		remote  string
		match   bool
	}{
		{"github.com/acme/*", "github.com/acme/app", true},
		{"github.com/acme/*", "github.com/acme/team/app", false}, // * is one segment
		{"github.com/acme/*", "github.com/acmex/app", false},
		{"github.com/acme/*", "github.com/acme", false},
		{"github.com/acme/**", "github.com/acme/app", true},
		{"github.com/acme/**", "github.com/acme/team/app", true},
		{"github.com/acme/**", "github.com/acme", true}, // ** may be empty
		{"github.com/**/app", "github.com/acme/team/app", true},
		{"github.com/**/app", "github.com/app", true},
		{"github.com/**/app", "github.com/acme/web", false},
		{"GitHub.com/acme/app", "github.com/acme/app", true},  // host case-insensitive
		{"github.com/Acme/app", "github.com/acme/app", false}, // path case kept
		{"github.com/acme/app.git", "github.com/acme/app", true},
		{"git@github.com:acme/app.git", "github.com/acme/app", true}, // pasted remote
		{"gitlab.com/acme/*", "github.com/acme/app", false},
		{"github.com/*/*", "github.com/acme/app", true},
	}
	for _, c := range cases {
		p, err := Compile(c.pattern)
		if err != nil {
			t.Fatalf("Compile(%q): %v", c.pattern, err)
		}
		if got := p.Match(c.remote); got != c.match {
			t.Errorf("%q.Match(%q) = %v, want %v", c.pattern, c.remote, got, c.match)
		}
	}
}

func TestCompileRefuses(t *testing.T) {
	bad := []string{
		"",
		"github.com",         // no path
		"*/acme/app",         // wildcard host matches every company
		"**/app",             // same
		"github.com/acme*/x", // partial wildcard
		"github.com/**/a/**", // two **
		"github.com//app",    // empty segment
	}
	for _, b := range bad {
		if _, err := Compile(b); err == nil {
			t.Errorf("Compile(%q) accepted, want error", b)
		}
	}
	var zero Pattern
	if zero.Match("github.com/acme/app") {
		t.Error("zero Pattern must match nothing")
	}
}
