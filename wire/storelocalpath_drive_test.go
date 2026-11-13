//go:build !windows

package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestCleanFileURLDrivePathElsewhere (B3-27, B3-28): on a system without
// drives, a Windows drive path (plain or file:///C:/…) is a path that does
// not exist here: E_BAD_PATH naming both store spellings, never scheme "c"
// and never E_INTERNAL.
func TestCleanFileURLDrivePathElsewhere(t *testing.T) {
	for _, in := range []string{`C:\Users\nina\notes.git`, `C:/Users/nina/notes.git`, `file:///C:/Users/nina/notes`} {
		if _, err := cleanFileURL(in); !errs.Has(err, errs.BadPath) {
			t.Errorf("cleanFileURL(%q) = %v, want E_BAD_PATH", in, err)
		}
	}
}
