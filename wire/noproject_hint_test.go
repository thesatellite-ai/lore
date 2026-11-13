package wire

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestNoProjectReadNamesNextStep: A-20, a read in a git repo with no
// .lore/ is refused (it creates nothing there) and the refusal names the
// next step (rule 6), lore init.
func TestNoProjectReadNamesNextStep(t *testing.T) {
	err := noProjectErr(WithNoCreate(context.Background()), "/work/plain")
	e := errs.EnvelopeOf(err).Error
	if !errs.Has(err, errs.NotAStore) || !strings.Contains(e.Message, "a read creates nothing there") || !strings.Contains(e.Hint, "lore init") {
		t.Fatalf("read refusal: %v", err)
	}
}
