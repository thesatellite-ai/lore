package dbtemplate

import (
	"context"
	"path/filepath"
	"testing"

	"dbent"
)

func TestBuildAndCopy(t *testing.T) {
	dir := t.TempDir()
	tpl := filepath.Join(dir, "template.db")
	if err := Build(context.Background(), tpl); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "copy.db")
	if err := Copy(tpl, dst); err != nil {
		t.Fatal(err)
	}
	db := dbent.InitDB(dst)
	defer db.Close()
	if err := dbent.QuickCheck(db); err != nil {
		t.Fatalf("copy is not a usable lore DB: %v", err)
	}
	if err := Copy(filepath.Join(dir, "missing.db"), dst); err == nil {
		t.Fatal("missing template must error")
	}
}
