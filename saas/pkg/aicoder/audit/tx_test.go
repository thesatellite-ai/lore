package audit

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"dbent"
	"dbent/pkg/dbtemplate"
)

var templateDB string

func TestMain(m *testing.M) { os.Exit(run(m)) }

func run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "audit-")
	if err != nil {
		return 1
	}
	defer os.RemoveAll(dir) // temp fixture
	templateDB = filepath.Join(dir, "t.db")
	if err := dbtemplate.Build(context.Background(), templateDB); err != nil {
		return 1
	}
	return m.Run()
}

func TestAppendVerifyTamper(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lore.db")
	if err := dbtemplate.Copy(templateDB, path); err != nil {
		t.Fatal(err)
	}
	db := dbent.InitDB(path)
	defer db.Close()

	if broken, n, err := Verify(ctx, db); err != nil || broken != "" || n != 0 {
		t.Fatalf("empty chain: %q %d %v", broken, n, err)
	}
	for i := range 5 {
		if err := Append(ctx, db, Entry{ActorID: "human:a", Action: "memories.write", TargetTable: "memories",
			TargetID: "mem_01a10237c9e87e4c843d8045973a65a1", AfterHash: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	if broken, n, err := Verify(ctx, db); err != nil || broken != "" || n != 5 {
		t.Fatalf("intact chain: %q %d %v", broken, n, err)
	}
	recs, err := List(ctx, db, 2)
	if err != nil || len(recs) != 2 || recs[0].AfterHash != "e" {
		t.Fatalf("list newest first: %+v %v", recs, err)
	}
	// Edit a past entry: the NEXT entry's link breaks.
	if _, err := db.Exec(`UPDATE audit_logs SET after_hash = 'forged' WHERE rowid = 2`); err != nil {
		t.Fatal(err)
	}
	broken, _, err := Verify(ctx, db)
	if err != nil || broken == "" {
		t.Fatalf("tamper not detected: %q %v", broken, err)
	}
	var third string
	if err := db.QueryRow(`SELECT id FROM audit_logs WHERE rowid = 3`).Scan(&third); err != nil || broken != third {
		t.Fatalf("break reported at %s, want %s", broken, third)
	}
}

func TestDeletingAnEntryBreaksTheChain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lore.db")
	if err := dbtemplate.Copy(templateDB, path); err != nil {
		t.Fatal(err)
	}
	db := dbent.InitDB(path)
	defer db.Close()
	for range 3 {
		if err := Append(ctx, db, Entry{ActorID: "a", Action: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`DELETE FROM audit_logs WHERE rowid = 2`); err != nil {
		t.Fatal(err)
	}
	if broken, _, err := Verify(ctx, db); err != nil || broken == "" {
		t.Fatalf("deletion not detected: %q %v", broken, err)
	}
}
