package errs

import (
	"errors"
	"io/fs"
	"testing"
)

// TestFileIOOr: a coded inner error keeps its code and hint; a raw
// filesystem error is classified by FileIO; a raw unknown one stays
// E_INTERNAL with a hint naming the path.
func TestFileIOOr(t *testing.T) {
	coded := New(ReadOnlyFS, "x").WithHint("chmod it")
	if e := FileIOOr("open", "/h/.lore", coded); e.Code != ReadOnlyFS || e.Hint != "chmod it" {
		t.Errorf("coded: %+v", e)
	}
	if e := FileIOOr("open", "/h/.lore/state/machine.db", fs.ErrPermission); e.Code != ReadOnlyFS || e.Hint == "" {
		t.Errorf("permission: %+v", e)
	}
	if e := FileIOOr("open", "/h/.lore", errors.New("odd")); e.Code != Internal || e.Hint == "" {
		t.Errorf("unknown: %+v", e)
	}
}
