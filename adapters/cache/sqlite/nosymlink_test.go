package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestOpenRefusesSymlinkedFiles: every SQLite file lore opens (cache,
// machine store, local state, audit chain) is refused with E_SYMLINK_DB
// when the file or one of its sidecars is a symbolic link, and the SQLite
// file the link points at is left untouched (no lore table created in it).
// A regular file and a symlinked parent folder still open.
func TestOpenRefusesSymlinkedFiles(t *testing.T) {
	f := newFixture(t)
	openers := map[string]func(path string) error{
		"cache": func(p string) error {
			c, err := Open(f.ctx, p, f.reg, f.exp)
			if err == nil {
				err = c.Close()
			}
			return err
		},
		"machine store": func(p string) error {
			m, err := OpenMachineStore(f.ctx, p, false)
			if err == nil {
				err = m.Close()
			}
			return err
		},
		"local state": func(p string) error {
			s, err := OpenLocalState(f.ctx, p)
			if err == nil {
				err = s.Close()
			}
			return err
		},
		"audit chain": func(p string) error {
			a, err := OpenAuditChain(f.ctx, p)
			if err == nil {
				err = a.Close()
			}
			return err
		},
	}
	links := []string{"", "-wal", "-shm", "-journal"}
	for name, open := range openers {
		for _, suffix := range links {
			t.Run(name+"/link"+suffix, func(t *testing.T) {
				dir := t.TempDir()
				victim := filepath.Join(dir, "victim.db")
				seedVictim(t, victim)
				db := filepath.Join(dir, "lore-cache.db")
				if err := os.Symlink(victim, db+suffix); err != nil {
					t.Fatal(err)
				}
				err := open(db)
				if errs.CodeOf(err) != errs.SymlinkDB {
					t.Fatalf("open through a symlinked %q: got %v, want E_SYMLINK_DB", "lore-cache.db"+suffix, err)
				}
				if got := tablesOf(t, victim); len(got) != 1 || got[0] != "precious" {
					t.Fatalf("the linked file was written: tables %v", got)
				}
			})
		}
		t.Run(name+"/regular file and symlinked folder open", func(t *testing.T) {
			real := t.TempDir()
			linked := filepath.Join(t.TempDir(), "home")
			if err := os.Symlink(real, linked); err != nil {
				t.Fatal(err)
			}
			for _, p := range []string{filepath.Join(real, "a.db"), filepath.Join(linked, "b.db")} {
				if err := open(p); err != nil {
					t.Fatalf("open %s: %v", p, err)
				}
			}
		})
	}
}

// seedVictim writes a SQLite file holding one table, "precious".
func seedVictim(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open(driverName, "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE precious(x TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

// tablesOf lists the tables of the SQLite file at path.
func tablesOf(t *testing.T, path string) []string {
	t.Helper()
	db, err := sql.Open(driverName, "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
