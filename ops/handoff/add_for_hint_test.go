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

// TestAddForUnknownLabelNamesSync (devco:S-5): --for an agent label that a
// teammate started in their own clone (a live row not synced here yet) is
// E_NOT_FOUND whose hint names the real next step, a sync on both sides,
// before registering; it used to say only "lore actor register".
func TestAddForUnknownLabelNamesSync(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, internalRepo)
	_, err := h.Run("handoff.add", `{"for":"@lead-codex-1","body":"the retry work is yours"}`)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.NotFound {
		t.Fatalf("err = %v, want E_NOT_FOUND", err)
	}
	for _, w := range []string{"in another clone, they run `lore sync`, then you run `lore sync`", "lore agent start --label lead-codex-1", "lore actor register"} {
		if !strings.Contains(e.Hint, w) {
			t.Errorf("hint %q lacks %q", e.Hint, w)
		}
	}
	if strings.Index(e.Hint, "lore sync") > strings.Index(e.Hint, "lore actor register") {
		t.Errorf("hint %q names register before the sync", e.Hint)
	}
}
