package sqlite

import (
	"errors"
	"fmt"
	"io/fs"
	"syscall"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestExplainDiskFull (B11 port of saas/cmd/cli guard_cli_test.go
// TestExplainStorageError): SQLite's "database or disk is full" and ENOSPC
// become E_DISK_FULL (exit 23) with a hint, wrapped or not and even inside
// an E_INTERNAL; unrelated errors, a CANTOPEN, and an error with its own
// lore code pass through unchanged.
func TestExplainDiskFull(t *testing.T) {
	t.Parallel()
	sqliteFull := errors.New("create memory: database or disk is full (13)") // the driver's text, flattened
	for _, tc := range []struct {
		name string
		err  error
		full bool
	}{
		{"sqlite full text", sqliteFull, true},
		{"wrapped sqlite full", fmt.Errorf("cache apply: %w", sqliteFull), true},
		{"inside E_INTERNAL", errs.Wrap(errs.Internal, "write failed", sqliteFull), true},
		{"ENOSPC", &fs.PathError{Op: "write", Path: "/repo/.lore/data/x.json", Err: syscall.ENOSPC}, true},
		{"EDQUOT", &fs.PathError{Op: "write", Path: "/repo/.lore/data/x.json", Err: syscall.EDQUOT}, true},
		{"EFBIG", &fs.PathError{Op: "write", Path: "/repo/.lore/data/x.json", Err: syscall.EFBIG}, true},
		{"other", errors.New("some other failure"), false},
		{"cantopen", errors.New("unable to open database file (14)"), false},
		{"own code wins", errs.Wrap(errs.ReadOnlyFS, "read-only", sqliteFull), false},
	} {
		got := ExplainDiskFull(tc.err)
		if !tc.full {
			if !errors.Is(got, tc.err) || got.Error() != tc.err.Error() {
				t.Errorf("%s: changed to %v", tc.name, got)
			}
			continue
		}
		var e *errs.Error
		if !errors.As(got, &e) || e.Code != errs.DiskFull || e.Hint == "" || errs.DefOf(e.Code).Exit != 23 {
			t.Errorf("%s: %v, want E_DISK_FULL exit 23 with a hint", tc.name, got)
		}
		if !errors.Is(got, tc.err) {
			t.Errorf("%s: the cause is lost", tc.name)
		}
	}
	if ExplainDiskFull(nil) != nil {
		t.Fatal("nil stays nil")
	}
}
