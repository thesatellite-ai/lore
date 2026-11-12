package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
)

// TestFixOldFilesRemovedOnceMigrated pins B4-7: lore 0.1.17's lore.db and
// backups/ stay while their rows are not migrated (the step says why), go
// once every row they hold is in .lore/data (a dry run only names them),
// and stay when a row of lore.db is in no store here.
func TestFixOldFilesRemovedOnceMigrated(t *testing.T) {
	t.Parallel()
	ctx := background()
	a, repo := repoDBApp(t, `{"_v": 1, "project_id": "`+v17ProjA+`"}`+"\n")
	db := filepath.Join(repo, ".lore", "lore.db")
	backups := filepath.Join(repo, ".lore", "backups")
	if err := os.MkdirAll(backups, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backups, "lore-1.db"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := a.fixOldFiles(ctx, repo, false); st.Changed || !strings.HasPrefix(st.Result, oldFilesKeptPrefix) {
		t.Fatalf("before the migration: %+v", st)
	}
	if _, err := os.Stat(db); err != nil {
		t.Fatal("removed before the migration")
	}
	if _, err := run(t, a, opsmigrate.OpRun, `{}`, false); err != nil {
		t.Fatal(err)
	}
	// a row lore.db holds that no store has (gone without a tombstone)
	// keeps the files
	mem := memPath(repo, v17MemA)
	saved, err := os.ReadFile(mem)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(mem); err != nil {
		t.Fatal(err)
	}
	if err = a.reopenState(); err != nil {
		t.Fatal(err)
	}
	if st := a.fixOldFiles(ctx, repo, false); st.Changed || !strings.Contains(st.Result, "in no store here") {
		t.Fatalf("with a row missing: %+v", st)
	}
	if err = os.WriteFile(mem, saved, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = a.reopenState(); err != nil {
		t.Fatal(err)
	}
	if st := a.fixOldFiles(ctx, repo, true); !st.Changed || !strings.HasPrefix(st.Result, "would remove") {
		t.Fatalf("dry run: %+v", st)
	}
	if _, err = os.Stat(db); err != nil {
		t.Fatal("the dry run removed lore.db")
	}
	if st := a.fixOldFiles(ctx, repo, false); !st.Changed || st.Failed {
		t.Fatalf("run: %+v", st)
	}
	for _, p := range []string{db, backups} {
		if _, err = os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s is still there: %v", p, err)
		}
	}
	if st := a.fixOldFiles(ctx, repo, false); st.Changed || st.Result != "current" {
		t.Fatalf("second run: %+v", st)
	}
}
