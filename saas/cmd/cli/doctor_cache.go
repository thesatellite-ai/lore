// doctor_cache.go — the cache rows of `lore doctor`: every cache file the
// core keeps (the repo's .lore/lore-cache.db and the store caches under
// ~/.lore/cache) is integrity-checked, and a damaged one is BROKEN with
// the fix. A cache is rebuildable from the row files (spec 3), so the fix
// is always "delete it": the core also moves a file aside by itself when
// it cannot open it, but a file damaged past its first pages opens fine
// and only an integrity check sees it.
package main

import (
	"context"
	"database/sql"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/wire"
)

// doctorCacheDriver is the database/sql name modernc.org/sqlite registers.
const doctorCacheDriver = "sqlite"

// doctorCacheExt ends a cache file name under ~/.lore/cache.
const doctorCacheExt = ".db"

// doctorCacheSubdir is the caches folder under the lore home.
const doctorCacheSubdir = "cache"

// quickCheckOK is PRAGMA quick_check's one-row answer for a sound file.
const quickCheckOK = "ok"

// doctorCaches checks the repo cache (repoTop may be "" when no project
// resolved) and every store cache under home, adding one finding per
// damaged or unreadable file.
func doctorCaches(ctx context.Context, repoTop, home string, r *doctorReport) {
	var files []string
	if repoTop != "" {
		files = append(files, filepath.Join(repoTop, filepath.FromSlash(wire.RepoCacheFile)))
	}
	if home != "" {
		_ = filepath.WalkDir(filepath.Join(home, doctorCacheSubdir), func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, doctorCacheExt) {
				files = append(files, p)
			}
			return nil // an unreadable folder is skipped, never fatal
		})
	}
	for _, f := range files {
		if msg := cacheFinding(ctx, f); msg != "" {
			r.Errors = append(r.Errors, msg)
			r.Status = doctorBroken
		}
	}
}

// cacheFinding returns the doctor line for one cache file, "" when it is
// sound or absent. Damage (IsCorrupt, or quick_check not ok) names the
// fix; a lock or permission problem is reported as it is, without the
// delete advice, because the file may be fine.
func cacheFinding(ctx context.Context, path string) string {
	if _, err := os.Stat(path); err != nil {
		return "" // absent: the next command creates it
	}
	fix := "cache " + path + " is damaged; delete it (and its -wal/-shm files): the next lore command rebuilds it from .lore/data, nothing is lost"
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro"}
	db, err := sql.Open(doctorCacheDriver, u.String())
	if err != nil {
		return "cache " + path + ": cannot open: " + err.Error()
	}
	defer func() { _ = db.Close() }() // read-only handle: nothing to lose on close
	var res string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&res); err != nil {
		if sqlite.IsCorrupt(err) {
			return fix + " (" + err.Error() + ")"
		}
		return "cache " + path + ": cannot check: " + err.Error()
	}
	if res != quickCheckOK {
		return fix + " (quick_check: " + res + ")"
	}
	return ""
}
