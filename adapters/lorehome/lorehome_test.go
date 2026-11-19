package lorehome

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// fakeEnv is an Env with a fixed LORE_HOME (set or not) and user home.
func fakeEnv(loreHome string, set bool, userHome string) Env {
	return Env{
		Lookup: func(k string) (string, bool) {
			if k == EnvName && set {
				return loreHome, true
			}
			return "", false
		},
		UserHome: func() (string, error) {
			if userHome == "" {
				return "", errors.New("no home")
			}
			return userHome, nil
		},
		TempDir: func() string { return "/tmpdir" },
	}
}

func TestResolveLoreHome(t *testing.T) {
	t.Parallel()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		val      string
		set      bool
		userHome string
		want     string
		source   Source
		bad      bool
	}{
		{name: "unset is the user home's .lore", userHome: "/home/sam", want: "/home/sam/.lore", source: FromDefault},
		{name: "unset with no user home falls back to temp", want: "/tmpdir/" + UnresolvedDir, source: FromFallback},
		{name: "set moves the whole home", val: "/srv/lore-dev", set: true, userHome: "/home/sam", want: "/srv/lore-dev", source: FromEnv},
		{name: "set wins over a missing user home", val: "/srv/lore-dev", set: true, want: "/srv/lore-dev", source: FromEnv},
		{name: "trailing slash is cleaned", val: "/srv/lore-dev/", set: true, want: "/srv/lore-dev", source: FromEnv},
		{name: "a symlink is kept as given", val: link, set: true, want: link, source: FromEnv},
		{name: "empty but set is refused", val: "", set: true, userHome: "/home/sam", bad: true},
		{name: "spaces only is refused", val: "  ", set: true, bad: true},
		{name: "relative is refused", val: "lore-dev", set: true, userHome: "/home/sam", bad: true},
		{name: "dot relative is refused", val: "./lore-dev", set: true, bad: true},
		{name: "tilde is never expanded silently", val: "~/.lore-dev", set: true, userHome: "/home/sam", bad: true},
		{name: "bare tilde is refused", val: "~", set: true, bad: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, err := fakeEnv(tc.val, tc.set, tc.userHome).Resolve()
			if tc.bad {
				if !errs.Has(err, errs.InvalidInput) {
					t.Fatalf("got %+v, %v; want E_INVALID_INPUT", h, err)
				}
				var e *errs.Error
				if !errors.As(err, &e) || e.Hint == "" {
					t.Fatalf("refusal %v has no next step", err)
				}
				return
			}
			if err != nil || h.Dir != filepath.FromSlash(tc.want) || h.Source != tc.source {
				t.Fatalf("got %+v, %v; want %q from %s", h, err, tc.want, tc.source)
			}
		})
	}
}

// TestShown (devco P-1): messages name the home this process uses: ~/.lore
// by default, ~/<rel> for a LORE_HOME under the user's home, the folder
// itself elsewhere; never ~/.lore when LORE_HOME moved it.
func TestShown(t *testing.T) {
	t.Parallel()
	env := func(v string, set bool) Env {
		return Env{
			Lookup:   func(string) (string, bool) { return v, set },
			UserHome: func() (string, error) { return "/home/someone", nil },
			TempDir:  func() string { return "/tmp" },
		}
	}
	for _, c := range []struct {
		v    string
		set  bool
		want string
	}{
		{"", false, "~/.lore"},
		{"/home/someone/.lore-dev", true, "~/.lore-dev"},
		{"/srv/lore-home", true, "/srv/lore-home"},
		{"/home/someone", true, "/home/someone"},
	} {
		if got := env(c.v, c.set).Shown(); got != filepath.ToSlash(c.want) {
			t.Errorf("LORE_HOME=%q (set %v): Shown %q, want %q", c.v, c.set, got, c.want)
		}
	}
}
