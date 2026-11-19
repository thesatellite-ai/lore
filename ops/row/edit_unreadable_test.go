package row_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/row"
)

// TestEditUnreadableFileNamesTheReason (devco L-16, same class as mission
// apply): lore edit --file on a missing view names the file and the
// system's reason in the message the CLI prints, then the next step.
func TestEditUnreadableFileNamesTheReason(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	id := h.ID(h.MustYes("task.add", `{"title":"ship the fix"}`))
	file := filepath.Join(t.TempDir(), "view.md")
	_, err := h.Run(row.OpEdit, `{"id":"`+id+`","file":`+opstest.Quote(file)+`}`)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.InvalidInput {
		t.Fatalf("err = %v, want E_INVALID_INPUT", err)
	}
	if want := "edit: cannot read " + file + ": no such file or directory"; e.Message != want {
		t.Errorf("message %q, want %q", e.Message, want)
	}
	if !strings.Contains(e.Hint, "lore row markdown --id") {
		t.Errorf("hint %q", e.Hint)
	}
}
