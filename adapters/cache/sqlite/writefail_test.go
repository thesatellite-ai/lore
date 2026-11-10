package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// ioErrOf makes SQLite fail a write with an SQLITE_IOERR (its rollback
// journal path is a folder), the error a write under a quota gets too.
func ioErrOf(t *testing.T) (string, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c.db")
	db, err := sql.Open(driverName, "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE t(x)"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if err = os.Mkdir(p+"-journal", 0o755); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open(driverName, "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec("INSERT INTO t VALUES(1)")
	if !isWriteIOErr(err) {
		t.Fatalf("no SQLITE_IOERR to test with: %v", err)
	}
	return p, dbErr("commit", err)
}

// TestExplainWrite: a cache write I/O error on a disk with no room left for
// this user is E_DISK_FULL with its hint; with room left it keeps its code
// but names the file and says to check free space and quota (never a bare
// E_INTERNAL); any other error, and nil, pass unchanged.
func TestExplainWrite(t *testing.T) {
	path, ioErr := ioErrOf(t)
	full := func(string) int64 { return 0 }
	roomy := func(string) int64 { return 1 << 40 }
	if got := explainWrite(path, ioErr, full); !errs.Has(got, errs.DiskFull) {
		t.Errorf("no room left: %v, want E_DISK_FULL", got)
	}
	got := explainWrite(path, ioErr, roomy)
	var le *errs.Error
	if !errors.As(got, &le) || !errs.Has(got, errs.Internal) || !strings.Contains(le.Hint, path) || !strings.Contains(le.Hint, "quota") {
		t.Errorf("room left: %v (hint %q), want E_INTERNAL naming %s and quota", got, hintOf(got), path)
	}
	other := errs.New(errs.Internal, "sqlite cache: something else")
	if explainWrite(path, other, full) != other {
		t.Errorf("a non-I/O error was changed")
	}
	if explainWrite(path, nil, full) != nil {
		t.Errorf("nil became an error")
	}
}

func hintOf(err error) string {
	var le *errs.Error
	if errors.As(err, &le) {
		return le.Hint
	}
	return ""
}

// TestEveryFileExplainsItsWrites: the machine store, the audit chain, local
// state and the doctor cache each send their write errors through their own
// fileWrites (named by their own path, with the free-space probe), so a
// write I/O error there is E_DISK_FULL with no room left and otherwise names
// that file and says to check space and quota, as the cache's inTx does.
func TestEveryFileExplainsItsWrites(t *testing.T) {
	ctx := context.Background()
	_, ioErr := ioErrOf(t)
	dir := t.TempDir()
	opened := map[string]func(p string) (fileWrites, func() error, error){
		"machine store": func(p string) (fileWrites, func() error, error) {
			s, err := OpenMachineStore(ctx, p, false)
			if err != nil {
				return fileWrites{}, nil, err
			}
			return s.writes, s.Close, nil
		},
		"audit chain": func(p string) (fileWrites, func() error, error) {
			s, err := OpenAuditChain(ctx, p)
			if err != nil {
				return fileWrites{}, nil, err
			}
			return s.writes, s.Close, nil
		},
		"local state": func(p string) (fileWrites, func() error, error) {
			s, err := OpenLocalState(ctx, p)
			if err != nil {
				return fileWrites{}, nil, err
			}
			return s.writes, s.Close, nil
		},
		"doctor cache": func(p string) (fileWrites, func() error, error) {
			s, err := OpenDoctorCache(ctx, p)
			if err != nil {
				return fileWrites{}, nil, err
			}
			return s.writes, s.Close, nil
		},
	}
	for name, open := range opened {
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".db")
		w, closeFn, err := open(p)
		if err != nil {
			t.Fatalf("%s: open: %v", name, err)
		}
		t.Cleanup(func() { _ = closeFn() })
		if w.path != p || w.free == nil {
			t.Errorf("%s: writes = %+v, want its own path %s and the free-space probe", name, w, p)
			continue
		}
		w.free = func(string) int64 { return 0 }
		if got := w.explain(ioErr); !errs.Has(got, errs.DiskFull) {
			t.Errorf("%s: no room left: %v, want E_DISK_FULL", name, got)
		}
		w.free = func(string) int64 { return 1 << 40 }
		if h := hintOf(w.explain(ioErr)); !strings.Contains(h, p) || !strings.Contains(h, "quota") {
			t.Errorf("%s: room left: hint %q does not name %s and quota", name, h, p)
		}
	}
}
