package sqlite

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRecordUseRoundTrip: a record written by RecordUse reads back from
// ReadUses with its repo and uses, and an unchanged record is not rewritten.
func TestRecordUseRoundTrip(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(t.TempDir(), "app")
	cache := filepath.Join(root, StoresDir, "sto_x", "main", "acme.db")
	uses := []Use{UseOf(root, cache, "northwind", false), {Path: "stores/sto_y/local/root.db", Store: "tips", Committed: true}}
	if uses[0].Path != "stores/sto_x/main/acme.db" {
		t.Fatalf("UseOf keeps the path relative to the cache root: %q", uses[0].Path)
	}
	if err := RecordUse(root, repo, uses); err != nil {
		t.Fatal(err)
	}
	p := UseRecordPath(root, repo)
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != useFileMode {
		t.Fatalf("the record is the owner's only: %v", fi.Mode().Perm())
	}
	got, err := ReadUses(root)
	if err != nil || len(got) != 1 {
		t.Fatalf("one record: %v %v", got, err)
	}
	if got[0].Record.Repo != repo || len(got[0].Record.Caches) != 2 || got[0].Record.Caches[1] != uses[1] {
		t.Fatalf("round trip: %+v", got[0].Record)
	}
	old := fi.ModTime()
	if err := os.Chtimes(p, old.Add(-1e9), old.Add(-1e9)); err != nil {
		t.Fatal(err)
	}
	if err := RecordUse(root, repo, uses); err != nil {
		t.Fatal(err)
	}
	if fi2, _ := os.Stat(p); !fi2.ModTime().Equal(old.Add(-1e9)) {
		t.Fatal("an unchanged record is not rewritten")
	}
}

// TestStoreCachesAndRemove: StoreCaches lists only *.db files under stores/
// and combined/ (sizes include WAL sidecars, moved-aside damaged copies are
// never listed); RemoveCache deletes the file, its sidecars and the folders
// it leaves empty, and refuses any path outside the store caches.
func TestStoreCachesAndRemove(t *testing.T) {
	root := t.TempDir()
	write := func(rel string, n int) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("stores/sto_a/main/acme.db", 10)
	write("stores/sto_a/main/acme.db-wal", 5)
	write("stores/sto_a/main/acme.db.corrupt-20260101T000000Z", 7)
	write("stores/sto_b/local/root.db", 3)
	write("combined/abc.db", 4)
	write("prj_0123.db", 9)
	got, err := StoreCaches(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []File{{"combined/abc.db", 4}, {"stores/sto_a/main/acme.db", 15}, {"stores/sto_b/local/root.db", 3}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	for _, bad := range []string{"prj_0123.db", "stores/../prj_0123.db", "/etc/x.db", "stores/sto_a/main/acme.db.corrupt-20260101T000000Z", "repos/x.db"} {
		if err := RemoveCache(root, bad); err == nil {
			t.Fatalf("RemoveCache(%q) must be refused", bad)
		}
	}
	if err := RemoveCache(root, "stores/sto_b/local/root.db"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "stores", "sto_b")); !os.IsNotExist(err) {
		t.Fatal("the folders a removed cache leaves empty go too")
	}
	if err := RemoveCache(root, "stores/sto_a/main/acme.db"); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"stores/sto_a/main/acme.db", "stores/sto_a/main/acme.db-wal"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("%s must be gone", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "stores", "sto_a", "main", "acme.db.corrupt-20260101T000000Z")); err != nil {
		t.Fatal("a moved-aside damaged copy is kept (and so is its folder)")
	}
	if _, err := os.Stat(filepath.Join(root, "prj_0123.db")); err != nil {
		t.Fatal("nothing outside stores/ and combined/ is touched")
	}
	if err := RemoveUseRecord(root, filepath.Join(root, "prj_0123.db")); err == nil {
		t.Fatal("RemoveUseRecord refuses a file that is not a use record")
	}
}
