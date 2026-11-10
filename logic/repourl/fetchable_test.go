package repourl

import (
	"errors"
	"testing"
)

// TestCheckFetchable: the store url forms git can fetch pass; the bare
// host/path id form, a relative or absolute local path and a blank value
// are refused (D-DOCS-1).
func TestCheckFetchable(t *testing.T) {
	for _, c := range []struct {
		in   string
		want error
	}{
		{"git@github.com:agency/notes", nil},
		{"git@github.com:agency/notes.git", nil},
		{"ssh://git@github.com/agency/notes", nil},
		{"https://github.com/agency/notes", nil},
		{"http://git.internal.example/agency/notes", nil},
		{"file:///srv/lore/me", nil},
		{"  https://github.com/agency/notes  ", nil},
		{"github.com/agency/notes", ErrNotFetchable},
		{"github.com/agency/notes.git", ErrNotFetchable},
		{"GitHub.com/Agency/Notes", ErrNotFetchable},
		{"agency/notes", ErrNotFetchable},
		{"/srv/lore/me", ErrNotFetchable},
		{"./notes", ErrNotFetchable},
		{"", ErrEmpty},
		{"   ", ErrEmpty},
	} {
		if got := CheckFetchable(c.in); !errors.Is(got, c.want) || (c.want == nil && got != nil) {
			t.Errorf("CheckFetchable(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
