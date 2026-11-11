package wire

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/internal/testenv"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
)

// TestSQLIncludeDeletedWithTMPDIRGone: `lore sql --include-deleted`
// answers when TMPDIR names a folder that is gone: the deleted-rows cache
// goes to lore's own folder.
func TestSQLIncludeDeletedWithTMPDIRGone(t *testing.T) {
	a := lensRepo(t, func(context.Context) (bool, error) { return false, nil })
	out, err := run(t, a, opsmemory.OpAdd, `{"body":"trash me"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = run(t, a, opsmemory.OpDelete, `{"id":"`+idOf(t, out)+`"}`, true); err != nil {
		t.Fatal(err)
	}
	testenv.SetTemp(t, filepath.Join(t.TempDir(), "gone"))
	if _, err := a.SQL(background(), opssql.Request{Query: "SELECT 1", IncludeDeleted: true}); err != nil {
		t.Fatalf("sql --include-deleted with TMPDIR gone: %v", err)
	}
}
