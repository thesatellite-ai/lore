package task

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestTemplateFileUnreadableNamesCauseAndStep (devco L-16, same class as
// mission apply): a --from-file lore cannot open names the file, the
// system's reason and the flag to fix, never only "read <path>".
func TestTemplateFileUnreadableNamesCauseAndStep(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "gone.yaml")
	_, err := readTemplateFile(path)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.InvalidInput {
		t.Fatalf("err = %v, want E_INVALID_INPUT", err)
	}
	if want := "task template: cannot read " + path + ": no such file or directory"; e.Message != want {
		t.Errorf("message %q, want %q", e.Message, want)
	}
	if !strings.Contains(e.Hint, "--from-file") {
		t.Errorf("hint %q names no flag", e.Hint)
	}
}
