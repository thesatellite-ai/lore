package rowfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestNewFolderGetsOrgFloor (devco:D-735): a folder created after the org
// declared min_lore_version (lore store folder add, a first write) gets
// that floor in its new _meta.json, so an older lore is read-only there
// too; without a floor source, or with a version no lore can compare, the
// file declares none, as before.
func TestNewFolderGetsOrgFloor(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name, floor, want string
		source            bool
	}{
		{"org floor declared", "0.1.20", "0.1.20", true},
		{"no floor source", "", "", false},
		{"org without a floor", "", "", true},
		{"unparseable floor ignored", "soon", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			root, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			f, err := root.Folder("webapp")
			if err != nil {
				t.Fatal(err)
			}
			if c.source {
				f.SetFloorSource(func(context.Context) string { return c.floor })
			}
			paths, err := f.Create(ctx)
			if err != nil || len(paths) != 1 {
				t.Fatalf("create: paths=%v err=%v", paths, err)
			}
			doc, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(paths[0])))
			if err != nil {
				t.Fatal(err)
			}
			got, err := FloorOfDoc(doc, paths[0])
			if err != nil || got != c.want {
				t.Fatalf("min_lore_version = %q (err %v), want %q:\n%s", got, err, c.want, doc)
			}
			if d, err := f.Format(ctx); err != nil || d.MinLoreVersion != c.want {
				t.Fatalf("Format = %+v, %v; want floor %q", d, err, c.want)
			}
		})
	}
}
