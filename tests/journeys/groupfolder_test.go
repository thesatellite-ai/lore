//go:build journeys && !windows

package journeys

// groupfolder_test.go — a repo a group shares stays editable by the group
// (devco D-390). Gus keeps the shop checkout in a group-writable folder
// with no setgid bit, the only mark a shared folder keeps below its top on
// macOS (mkdir there never copies setgid). Under the usual umask 022 lore
// used to write the rows of .lore/data -rw-r--r-- in folders drwxr-xr-x, so
// a teammate in the group could not edit a note. Now every row and folder
// lore writes there is group read/write.

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestJourneyGroupFolderRowsGroupWritable(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	gus := w.Persona("Gus Groupmate", "gus@example.test")
	app := gus.Laptop().Clone(w.Remote("github.com/gus/shop"), "shop")

	w.Step("day1/init-in-group-folder", func() {
		if err := os.Chmod(app.Dir, 0o770); err != nil {
			t.Fatal(err)
		}
		app.Lore("init", "--non-interactive", "--name=shop").OK(t)
		app.Lore("task", "add", "--title", "restock the shelf").OK(t)
		data := filepath.Join(app.Dir, ".lore", "data")
		rows := 0
		_ = filepath.WalkDir(data, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			info, ierr := d.Info()
			if ierr != nil {
				return nil
			}
			want := fs.FileMode(0o060)
			if d.IsDir() {
				want = 0o070
			} else if d.Type().IsRegular() {
				rows++
			}
			if info.Mode().Perm()&want != want {
				t.Errorf("%s is %v; in a group's folder lore's writes are group read/write", p, info.Mode().Perm())
			}
			return nil
		})
		if rows == 0 {
			t.Fatalf("no row files under %s", data)
		}
	})
}
