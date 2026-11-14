package handoff_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestAddWithoutForNamesTheNextStep (devco L-16): handoff add with no
// --for names the cause and a next step with named flags (it used to
// stop at "--for @handle is required").
func TestAddWithoutForNamesTheNextStep(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, internalRepo)
	_, err := h.Run("handoff.add", `{"body":"form is half done"}`)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.InvalidInput {
		t.Fatalf("err = %v, want E_INVALID_INPUT", err)
	}
	if !strings.Contains(e.Message, "--for @handle is required") {
		t.Errorf("message %q", e.Message)
	}
	for _, w := range []string{"lore handoff add --for @handle --body", "lore member list"} {
		if !strings.Contains(e.Hint, w) {
			t.Errorf("hint %q lacks %q", e.Hint, w)
		}
	}
}
