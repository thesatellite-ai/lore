//go:build journeys && !windows

package journeys

// rohome_write_test.go — a lore home that went read-only (devco:U-4): a
// home restored from a backup without write bits. Reads keep working; a
// write is refused before anything is written, naming the home and the
// fix; following the hint makes the same write work. Before, the write
// stored its row and then failed E_INTERNAL on its audit record, and a
// home holding store clones failed every command, reads included.

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestJourneyReadOnlyHomeWrites(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root writes through any mode")
	}
	w := NewWorld(t)
	rin := w.Persona("Rin Backup", "rin@restored.example")
	lap := rin.Laptop()
	site := w.Remote("github.com/rin-dev/tools")
	repo := lap.Clone(site, "tools")
	home := filepath.Join(lap.Home, ".lore")
	chmodTree := func(on bool) {
		_ = filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			if fi, ierr := d.Info(); ierr == nil {
				mode := fi.Mode().Perm() &^ 0o222
				if on {
					mode = fi.Mode().Perm() | 0o200
				}
				_ = os.Chmod(p, mode)
			}
			return nil
		})
	}

	w.Step("day1/notes-while-writable", func() {
		lap.LoreIn(repo, "init").OK(t)
		lap.LoreIn(repo, "memory", "add", "--body", "the restore job runs at night").OK(t)
	})

	w.Step("day2/home-read-only", func() {
		chmodTree(false)
		defer chmodTree(true) // the invariants after the step write the home
		lap.LoreIn(repo, "memory", "list").OK(t).Says(t, "restore job")
		lap.LoreIn(repo, "context", "--for-file", "README.md").OK(t)
		lap.LoreIn(repo, "memory", "add", "--body", "written into a read-only home").
			Refused(t, "E_READ_ONLY_FS", "lore cannot write its home "+home, "nothing was changed", "chmod -R u+rwX "+home, "LORE_HOME")
		lap.LoreIn(repo, "render").Refused(t, "E_READ_ONLY_FS", "chmod -R u+rwX "+home)
		lap.LoreIn(repo, "memory", "list").OK(t).Never(t, "written into a read-only home")
	})

	w.Step("day2/hint-followed", func() {
		// the hint's chmod (the deferred chmodTree(true) above did it)
		lap.LoreIn(repo, "memory", "add", "--body", "written after the chmod").OK(t)
		lap.LoreIn(repo, "memory", "list").OK(t).Says(t, "written after the chmod")
	})
}
