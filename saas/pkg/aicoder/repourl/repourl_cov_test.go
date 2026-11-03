package repourl

import "testing"

// String returns the pattern as written after host normalisation, so a
// message quoting it matches what the user typed in the config.
func TestPatternString(t *testing.T) {
	cases := map[string]string{
		"github.com/agency/*":       "github.com/agency/*",
		"/github.com/client/*/":     "github.com/client/*",
		"  github.com/client2/api ": "github.com/client2/api",
		"github.com/agency/**":      "github.com/agency/**",
		"GitHub.COM/agency/*":       "github.com/agency/*",
	}
	for in, want := range cases {
		p, err := Compile(in)
		if err != nil {
			t.Fatalf("Compile(%q): %v", in, err)
		}
		if p.String() != want {
			t.Errorf("Compile(%q).String() = %q, want %q", in, p.String(), want)
		}
		again, err := Compile(p.String())
		if err != nil || again.String() != p.String() {
			t.Errorf("String() must compile back to itself: %q %v", again.String(), err)
		}
	}
	if (Pattern{}).String() != "" {
		t.Fatal("the zero pattern prints empty")
	}
}
