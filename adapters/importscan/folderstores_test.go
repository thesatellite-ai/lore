package importscan

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

// storeURLs is every "root-relative-to-home → url" FolderStores found.
func storeURLs(home string, res StoreScanResult) []string {
	var out []string
	for _, r := range res.Repos {
		rel, _ := filepath.Rel(home, r.Root)
		if len(r.Stores) == 0 {
			out = append(out, filepath.ToSlash(rel)+" (no stores)")
		}
		for _, s := range r.Stores {
			out = append(out, filepath.ToSlash(rel)+" "+s.Name+" "+s.URL)
		}
	}
	return out
}

const storesAcme = "stores:\n  - name: team\n    url: git@github.com:acme/lore.git\n"

// TestFolderStores walks fake trees: which repos' stores.yaml become
// suggestions, across folder shape × repo place × known × file state.
func TestFolderStores(t *testing.T) {
	cases := []struct {
		name    string
		build   func(t *testing.T, home string)
		folders []string
		known   []string
		depth   int
		max     int
		want    []string
		trunc   bool
	}{
		{name: "repo not initialised, stores committed", folders: []string{"~/work"},
			build: func(t *testing.T, h string) { repo(t, filepath.Join(h, "work", "acme", "app"), storesAcme) },
			want:  []string{"work/acme/app team git@github.com:acme/lore.git"}},
		{name: "known repo is walked past", folders: []string{"~/work"}, known: []string{"work/acme/app"},
			build: func(t *testing.T, h string) { repo(t, filepath.Join(h, "work", "acme", "app"), storesAcme) }},
		{name: "hidden, node_modules, vendor skipped", folders: []string{"~/work"},
			build: func(t *testing.T, h string) {
				repo(t, filepath.Join(h, "work", ".cache", "x"), storesAcme)
				repo(t, filepath.Join(h, "work", "node_modules", "x"), storesAcme)
				repo(t, filepath.Join(h, "work", "vendor", "x"), storesAcme)
			}},
		{name: "deeper than depth skipped", folders: []string{"~/work"}, depth: 2,
			build: func(t *testing.T, h string) {
				repo(t, filepath.Join(h, "work", "a", "b"), storesAcme)
				repo(t, filepath.Join(h, "work", "a", "b", "c"), storesAcme)
			},
			want: []string{"work/a/b team git@github.com:acme/lore.git"}},
		{name: "malformed and missing stores.yaml give a repo with no stores", folders: []string{"~/work"},
			build: func(t *testing.T, h string) {
				repo(t, filepath.Join(h, "work", "bad"), "stores: nope\n")
				repo(t, filepath.Join(h, "work", "none"), "")
			},
			want: []string{"work/bad (no stores)", "work/none (no stores)"}},
		{name: "missing, empty and relative folders add nothing", folders: []string{"~/gone", "", "rel/x"}},
		{name: "nested and repeated folders walk once", folders: []string{"~/work", "~/work/acme", "~/work"},
			build: func(t *testing.T, h string) { repo(t, filepath.Join(h, "work", "acme", "app"), storesAcme) },
			want:  []string{"work/acme/app team git@github.com:acme/lore.git"}},
		{name: "repo cap truncates", folders: []string{"~/work"}, max: 2, trunc: true,
			build: func(t *testing.T, h string) {
				for i := range 3 {
					repo(t, filepath.Join(h, "work", "r"+strconv.Itoa(i)), "")
				}
			},
			want: []string{"work/r0 (no stores)", "work/r1 (no stores)"}},
		{name: "folder cap", folders: []string{"~/f1", "~/f2", "~/f3", "~/f4", "~/f5", "~/f6", "~/f7", "~/f8", "~/f9"},
			build: func(t *testing.T, h string) {
				for i := 1; i <= 9; i++ {
					repo(t, filepath.Join(h, "f"+strconv.Itoa(i), "r"), "")
				}
			},
			want: []string{"f1/r (no stores)", "f2/r (no stores)", "f3/r (no stores)", "f4/r (no stores)", "f5/r (no stores)", "f6/r (no stores)", "f7/r (no stores)", "f8/r (no stores)"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			if c.build != nil {
				c.build(t, home)
			}
			var known []string
			for _, k := range c.known {
				known = append(known, filepath.Join(home, k))
			}
			res := FolderStores(context.Background(), StoreScan{Folders: c.folders, Home: home, Depth: c.depth, MaxRepos: c.max, Known: known})
			if got := storeURLs(home, res); !slices.Equal(got, c.want) {
				t.Errorf("stores\n got %q\nwant %q", got, c.want)
			}
			if res.Truncated != c.trunc {
				t.Errorf("truncated %v, want %v", res.Truncated, c.trunc)
			}
		})
	}
}
