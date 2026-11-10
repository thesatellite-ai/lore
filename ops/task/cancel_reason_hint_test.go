package task_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestCancelFreeTextReasonNamesTheWhyFlag: the old way of giving a
// --not-a-secret reason on task cancel (free text in --reason) is refused
// with a hint naming --not-a-secret-why, the flag that takes it now.
func TestCancelFreeTextReasonNamesTheWhyFlag(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	id := addID(h, h.Must(opstask.OpAdd, `{"title":"cancel me"}`))
	_, err := h.Run(opstask.OpCancel, `{"id":"`+id+`","reason":"a doc sample, not a key"}`)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.InvalidInput || !strings.Contains(e.Hint, "--not-a-secret-why") {
		t.Fatalf("cancel with a free-text reason: %v (hint %q)", err, func() string {
			if e != nil {
				return e.Hint
			}
			return ""
		}())
	}
	h.Must(opstask.OpCancel, `{"id":"`+id+`","reason":"obsolete"}`)
}
