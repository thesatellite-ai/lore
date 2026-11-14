package mission_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestMissionApplyUnreadableFileNamesPathCauseAndStep (devco L-16):
// `lore mission apply --file` on a path lore cannot read is E_INVALID_INPUT
// whose message names the file and the system's reason and whose hint
// names the next step with flags. It used to print a bare "read plan".
func TestMissionApplyUnreadableFileNamesPathCauseAndStep(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	dir := t.TempDir()
	for _, c := range []struct{ path, reason string }{
		{filepath.Join(dir, "missing.yaml"), "no such file or directory"},
		{dir, "is a directory"},
	} {
		_, err := h.Run("mission.apply", `{"file":`+opstest.Quote(c.path)+`}`)
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != errs.InvalidInput {
			t.Fatalf("%s: err %v, want E_INVALID_INPUT", c.path, err)
		}
		if want := "mission apply: cannot read " + c.path + ": " + c.reason; e.Message != want {
			t.Errorf("message %q, want %q", e.Message, want)
		}
		for _, w := range []string{"--file", "lore mission apply --help"} {
			if !strings.Contains(e.Hint, w) {
				t.Errorf("hint %q lacks %q", e.Hint, w)
			}
		}
	}
}
