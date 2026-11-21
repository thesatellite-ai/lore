package repourl

import "testing"

// TestHostlessPatternSuggests (bug hunt H-112): a one-segment pattern is
// refused with the host/owner/repo shape and the pattern it most likely
// meant, built from what was typed; the command's other patterns fill the
// owner.
func TestHostlessPatternSuggests(t *testing.T) {
	for _, c := range []struct{ raw, suggest, says string }{
		{"client-*", "github.com/<owner>/client-*", "has no host"},
		{"acme", "github.com/acme/*", "has no host"},
		{"GitHub.com", "github.com/<owner>/*", "names only a host"},
		{"/api-*/", "github.com/<owner>/api-*", "has no host"},
	} {
		_, err := Compile(c.raw)
		h := AsHostless(err)
		if h == nil {
			t.Fatalf("%q: %v, want a HostlessError", c.raw, err)
		}
		if h.Suggest != c.suggest {
			t.Errorf("%q: suggest %q, want %q", c.raw, h.Suggest, c.suggest)
		}
		want := "repourl: pattern \"" + c.raw + "\" " + c.says + ": write host/owner/repo (host/owner/prefix* for a group of repos), like " + c.suggest
		if err.Error() != want {
			t.Errorf("%q:\n got %s\nwant %s", c.raw, err, want)
		}
	}
	if _, err := Compile("github.com/acme/*"); AsHostless(err) != nil {
		t.Fatalf("a full pattern refused: %v", err)
	}
	if _, err := Compile("*/acme/x"); err == nil || AsHostless(err) != nil {
		t.Fatalf("a wildcard host: %v, want the host refusal, not HostlessError", err)
	}
	known := []string{"client-*", "github.com/*/x", "git.acme.test/acme/*", "github.com/acme-labs/tools"}
	for _, c := range []struct{ in, want string }{
		{"github.com/<owner>/client-*", "git.acme.test/acme/client-*"},
		{"github.com/<owner>/*", "git.acme.test/acme/*"},
		{"git.acme.test/<owner>/*", "git.acme.test/acme/*"},
		{"gitlab.com/<owner>/*", "gitlab.com/<owner>/*"},
		{"github.com/acme/*", "github.com/acme/*"},
	} {
		if got := SuggestWith(c.in, known); got != c.want {
			t.Errorf("SuggestWith(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
