package onboard

import (
	"errors"
	"strings"
	"testing"
)

// fakeLinks resolves a symlinked prefix: every path under a key is the
// same path under its value (a symlinked ~/www pointing at /mnt/data/www).
func fakeLinks(links map[string]string) Resolver {
	return func(p string) (string, error) {
		for from, to := range links {
			if p == from || strings.HasPrefix(p, from+"/") {
				return to + strings.TrimPrefix(p, from), nil
			}
		}
		return "", errors.New("not a link")
	}
}

const testHome = "/home/mira"

func setupsFor(pairs ...[]string) []SetupEntry {
	var out []SetupEntry
	for _, p := range pairs {
		out = append(out, SetupEntry{Name: p[0], Kind: KindMember, Folders: p[1:]})
	}
	return out
}

func TestMatchFolder(t *testing.T) {
	links := fakeLinks(map[string]string{"/home/mira/www": "/mnt/data/www"})
	cases := []struct {
		name    string
		setups  []SetupEntry
		dir     string
		resolve Resolver
		want    string // winning setup, "" = none
		tie     []string
	}{
		{name: "no setups", dir: "/home/mira/x"},
		{name: "one match", setups: setupsFor([]string{"personal", "~/www/projects/mira/**"}), dir: "/home/mira/www/projects/mira/app", want: "personal"},
		{name: "any depth", setups: setupsFor([]string{"personal", "~/www/**"}), dir: "/home/mira/www/a/b/c/d", want: "personal"},
		{name: "zero depth", setups: setupsFor([]string{"personal", "~/www/**"}), dir: "/home/mira/www", want: "personal"},
		{name: "no match", setups: setupsFor([]string{"personal", "~/www/**"}), dir: "/home/mira/other"},
		{name: "most specific wins", setups: setupsFor([]string{"agency", "~/www/projects/**"}, []string{"acme-hub", "~/www/projects/clients/acme/**"}),
			dir: "/home/mira/www/projects/clients/acme/web", want: "acme-hub"},
		{name: "most specific wins, order reversed", setups: setupsFor([]string{"acme-hub", "~/www/projects/clients/acme/**"}, []string{"agency", "~/www/projects/**"}),
			dir: "/home/mira/www/projects/clients/acme/web", want: "acme-hub"},
		{name: "broader still wins outside", setups: setupsFor([]string{"agency", "~/www/projects/**"}, []string{"acme-hub", "~/www/projects/clients/acme/**"}),
			dir: "/home/mira/www/projects/tomas", want: "agency"},
		{name: "tie refused", setups: setupsFor([]string{"agency", "~/work/**"}, []string{"acme-hub", "~/work/**"}),
			dir: "/home/mira/work/x", tie: []string{"acme-hub", "agency"}},
		{name: "tie at single star", setups: setupsFor([]string{"agency", "~/work/*"}, []string{"acme-hub", "~/work/**"}),
			dir: "/home/mira/work/x", tie: []string{"acme-hub", "agency"}},
		{name: "one setup two globs never ties", setups: setupsFor([]string{"agency", "~/work/**", "~/work/*"}), dir: "/home/mira/work/x", want: "agency"},
		{name: "symlinked dir matches resolved glob", setups: setupsFor([]string{"personal", "/mnt/data/www/projects/**"}),
			dir: "/home/mira/www/projects/app", resolve: links, want: "personal"},
		{name: "symlinked glob matches resolved dir", setups: setupsFor([]string{"personal", "~/www/projects/**"}),
			dir: "/mnt/data/www/projects/app", resolve: links, want: "personal"},
		{name: "symlink: specificity after resolving", setups: setupsFor([]string{"agency", "/mnt/data/www/projects/**"}, []string{"acme-hub", "~/www/projects/clients/acme/**"}),
			dir: "/mnt/data/www/projects/clients/acme/x", resolve: links, want: "acme-hub"},
		{name: "symlink tie across spellings", setups: setupsFor([]string{"agency", "/mnt/data/www/**"}, []string{"acme-hub", "~/www/**"}),
			dir: "/home/mira/www/x", resolve: links, tie: []string{"acme-hub", "agency"}},
		{name: "no resolver: spellings differ", setups: setupsFor([]string{"personal", "/mnt/data/www/projects/**"}),
			dir: "/home/mira/www/projects/app"},
		{name: "question mark segment", setups: setupsFor([]string{"personal", "~/w?/**"}), dir: "/home/mira/ww/a", want: "personal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := MatchFolder(tc.setups, tc.dir, testHome, tc.resolve)
			if len(tc.tie) > 0 {
				if !r.Ambiguous() || r.Found {
					t.Fatalf("want tie %v, got %+v", tc.tie, r)
				}
				for i, m := range r.Tie {
					if m.Setup != tc.tie[i] {
						t.Fatalf("tie[%d] = %s, want %s", i, m.Setup, tc.tie[i])
					}
				}
				return
			}
			if r.Ambiguous() {
				t.Fatalf("unexpected tie %+v", r.Tie)
			}
			got := ""
			if r.Found {
				got = r.Match.Setup
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q (%+v)", got, tc.want, r)
			}
		})
	}
}

func TestSpecificity(t *testing.T) {
	for glob, want := range map[string]int{
		"/home/mira/www/projects/clients/acme/**": 6,
		"/home/mira/www/projects/**":              4,
		"/home/mira/w*/x":                         2,
		"/a/b":                                    2,
		"/**":                                     0,
	} {
		if got := Specificity(glob); got != want {
			t.Errorf("Specificity(%s) = %d, want %d", glob, got, want)
		}
	}
}

func TestOverlapsAndShadows(t *testing.T) {
	setups := setupsFor(
		[]string{"agency", "~/www/projects/**", "~/work/a*"},
		[]string{"acme-hub", "~/www/projects/clients/acme/**", "~/work/b*"},
		[]string{"tomas", "~/www/projects/*"},
	)
	ov := Overlaps(setups, testHome, nil)
	if len(ov) != 1 || ov[0].A.Setup != "agency" || ov[0].B.Setup != "tomas" {
		t.Fatalf("overlaps = %+v, want agency~tomas only (a* and b* never meet)", ov)
	}
	sh := Shadows(setups, "agency", testHome, nil)
	if len(sh) != 1 || sh[0].By.Setup != "acme-hub" || sh[0].By.Glob != "~/www/projects/clients/acme/**" {
		t.Fatalf("shadows = %+v", sh)
	}
	if got := Shadows(setups, "acme-hub", testHome, nil); len(got) != 0 {
		t.Fatalf("acme-hub is shadowed by nothing, got %+v", got)
	}
	if got := LiteralPrefix("~/www/projects/**", testHome); got != "/home/mira/www/projects" {
		t.Fatalf("LiteralPrefix = %s", got)
	}
}
