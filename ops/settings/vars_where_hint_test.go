package settings_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestVarsWhereNotFoundHasHint (devco:D-387): `lore vars where` of a name
// no place defines is E_NOT_FOUND with a hint that says what to do (list
// the variables, or set it with its named flags). Every refusal needs a
// hint line; this one had none (the sweep's read-only home row, where the
// set before it was refused). Break-verified by dropping the WithHint.
func TestVarsWhereNotFoundHasHint(t *testing.T) {
	t.Parallel()
	h := multiStore(t)
	_, err := h.Run(settings.OpVarsWhere, `{"id":"sweep_host"}`)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.NotFound {
		t.Fatalf("vars where of an unknown name: %v, want E_NOT_FOUND", err)
	}
	for _, want := range []string{"lore vars list", "lore vars set --name sweep_host --value <value>"} {
		if !strings.Contains(e.Hint, want) {
			t.Errorf("hint %q does not say %q", e.Hint, want)
		}
	}
}
