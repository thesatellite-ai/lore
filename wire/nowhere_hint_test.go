package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// TestNowhereReadHintNamesStoreAndSetup (devco:D-510): a read from a
// folder no repo, store clone or setup covers refuses E_NOT_A_STORE with a
// hint naming --store and --setup, never "git init" (before, every read
// but the roster's named only --to and --store).
func TestNowhereReadHintNamesStoreAndSetup(t *testing.T) {
	t.Parallel()
	a := newTestApp(t, t.TempDir())
	e, ok := a.Registry().Lookup("rule.list")
	if !ok {
		t.Fatal("no rule.list")
	}
	_, ran, err := a.runOnDefaultStore(t.Context(), e, op.JSONDecoder{Body: []byte(`{}`)}, op.Caller{})
	if !ran || !errs.Has(err, errs.NotAStore) {
		t.Fatalf("ran %v, err %v; want E_NOT_A_STORE", ran, err)
	}
	hint := errs.EnvelopeOf(err).Error.Hint
	for _, want := range []string{"--store <org>", "--setup <setup>"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint %q does not name %s", hint, want)
		}
	}
	if strings.Contains(hint, "git init") {
		t.Errorf("hint %q says git init", hint)
	}
}
