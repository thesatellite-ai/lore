package task_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestPlaceTwoFlagsNamesTheNextStep (devco L-16): two placement flags on
// task edit are refused with the cause (they each place the task) and a
// hint that keeps one, with its value shape.
func TestPlaceTwoFlagsNamesTheNextStep(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false))
	id := add(h, "a", "")
	_, err := h.Run(opstask.OpEdit, `{"id":"`+id+`","first":true,"last":true}`)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.InvalidInput {
		t.Fatalf("err = %v, want E_INVALID_INPUT", err)
	}
	if !strings.Contains(e.Message, "give only one") {
		t.Errorf("message %q", e.Message)
	}
	if !strings.Contains(e.Hint, "--before <tsk_id>") || !strings.Contains(e.Hint, "--first") {
		t.Errorf("hint %q", e.Hint)
	}
}
