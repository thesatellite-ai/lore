package app

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// noTerminal stands in for a run without a terminal.
func noTerminal() bool { return false }

// TestEditorCommand: $VISUAL wins over $EDITOR (git's order); with neither
// and no terminal, vi is not started where nobody sees
// it: E_INVALID_INPUT naming EDITOR and the no-editor way.
func TestEditorCommand(t *testing.T) {
	t.Setenv(envVisual, "code --wait")
	t.Setenv(envEditor, "nano")
	if got, err := editorCommand(noTerminal); err != nil || got != "code --wait" {
		t.Fatalf("editorCommand = %q, %v; want $VISUAL", got, err)
	}
	t.Setenv(envVisual, " ")
	if got, err := editorCommand(noTerminal); err != nil || got != "nano" {
		t.Fatalf("editorCommand = %q, %v; want $EDITOR", got, err)
	}
	t.Setenv(envEditor, "")
	_, err := editorCommand(noTerminal)
	if !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("no editor and no terminal: %v, want E_INVALID_INPUT", err)
	}
	if h := errs.EnvelopeOf(err).Error.Hint; h == "" {
		t.Errorf("the refusal has no next step")
	}
}

// TestEditorDefaultIsVi: with a terminal and neither variable, vi.
func TestEditorDefaultIsVi(t *testing.T) {
	t.Setenv(envVisual, "")
	t.Setenv(envEditor, "")
	if got, err := editorCommand(func() bool { return true }); err != nil || got != defaultEditor {
		t.Fatalf("editorCommand = %q, %v; want %s", got, err, defaultEditor)
	}
}
