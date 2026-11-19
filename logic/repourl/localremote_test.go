package repourl

import "testing"

// TestLocalRemoteKey pins devco D-706: a path remote's classification key
// is the spelling NormalizeStore gives the path (_local/<path without
// .git>), for a plain path and a file:// url alike; a hosted or relative
// remote has none.
func TestLocalRemoteKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/srv/git/acme/tools.git", "_local/srv/git/acme/tools"},
		{"/srv/git/acme/tools/", "_local/srv/git/acme/tools"},
		{"file:///srv/git/acme/tools.git", "_local/srv/git/acme/tools"},
		{"file://localhost/srv/git/acme/tools", "_local/srv/git/acme/tools"},
		{`C:\git\acme\tools.git`, "_local/C:/git/acme/tools"},
		{"file://otherhost/srv/git/x.git", ""},
		{"../tools.git", ""},
		{"git@github.com:acme-co/tools.git", ""},
		{"buildbox:/srv/git/acme/tools.git", ""},
		{"/", ""},
	}
	for _, c := range cases {
		got, ok := LocalRemoteKey(c.in)
		if ok != (c.want != "") || got != c.want {
			t.Errorf("LocalRemoteKey(%q) = %q, %v; want %q", c.in, got, ok, c.want)
		}
	}
	p, err := Compile("_local/srv/git/acme/*")
	if err != nil || !p.Match("_local/srv/git/acme/tools") {
		t.Fatalf("a _local pattern must compile and match the key: %v", err)
	}
}
