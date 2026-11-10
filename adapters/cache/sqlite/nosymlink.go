package sqlite

import (
	"errors"
	"io/fs"
	"os"

	"github.com/thesatellite-ai/lore/model/errs"
)

// sqliteSidecars are the files SQLite opens beside a database file: the
// WAL and its shared-memory index (journal_mode WAL, see fileDSN) and the
// rollback journal SQLite falls back to. Each is written through as
// readily as the main file, so each is checked like it.
var sqliteSidecars = []string{"-wal", "-shm", "-journal"}

// refuseSymlinks refuses to open the SQLite file at abs when it, or one of
// its sidecars, is a symbolic link (E_SYMLINK_DB, as v0.1.17 refused a
// symlinked lore.db).
//
// Why: the repo cache lives inside the repository (.lore/lore-cache.db). A
// cloned repository can commit a symlink there (a .gitignore entry does not
// stop a committed file), and opening through it would create lore's
// tables in, and rewrite, whatever SQLite file it points at, outside the
// repository. The same holds for the files under the lore home. Only the
// final path element is checked: a symlinked parent folder (a home on
// another disk, macOS /var) is the person's own layout and stays allowed.
//
// A missing file is fine (SQLite creates it as a regular file). The check
// runs right before the open, so a link swapped in between is a local race
// that needs write access to the folder already.
func refuseSymlinks(abs string) error {
	for _, p := range append([]string{abs}, sidecarPaths(abs)...) {
		fi, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return errs.Wrap(errs.BadPath, "sqlite cache: cannot check "+p, err)
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return errs.New(errs.SymlinkDB, "refusing to open "+p+": it is a symbolic link").
				WithHint("lore never writes a database through a link; replace " + p + " with a regular file or remove it (a cache file is rebuilt from the row files)")
		}
	}
	return nil
}

// sidecarPaths lists the sidecar file names of the database at abs.
func sidecarPaths(abs string) []string {
	out := make([]string, 0, len(sqliteSidecars))
	for _, s := range sqliteSidecars {
		out = append(out, abs+s)
	}
	return out
}
