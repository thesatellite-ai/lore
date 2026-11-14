package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestContractorRefusalIsOneLine (J-15): the refusal is E_REFUSED, one
// line naming the org and what a contractor may write.
func TestContractorRefusalIsOneLine(t *testing.T) {
	err := contractorRefusal("acme")
	if !errs.Has(err, errs.Refused) {
		t.Fatalf("code = %v", err)
	}
	e := errs.EnvelopeOf(err).Error
	if strings.Contains(e.Message, "\n") || !strings.Contains(e.Message, "contractor in acme") || !strings.Contains(e.Hint, "--to") {
		t.Errorf("refusal = %q / %q", e.Message, e.Hint)
	}
}
