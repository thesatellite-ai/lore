package host

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestPersonalHintOnlyWhenNotSetUp pins devco:D-654: "your personal store
// is not set up: run lore store init --name me" was the answer to every
// "no store named me is attached", also for a person whose setup has a
// personal store (whoami showed it); following it made a second one. With
// one configured the hint says how to reach it and never names store init.
func TestPersonalHintOnlyWhenNotSetUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	notAttached := errs.New(errs.NotFound, "no store named me is attached")
	for _, c := range []struct {
		name     string
		personal func(context.Context) string
		want     string
		never    string
	}{
		{"none configured", func(context.Context) string { return "" }, "lore store init --name me", "is set up"},
		{"no hook", nil, "lore store init --name me", "is set up"},
		{"configured", func(context.Context) string { return "file:///home/lee/.lore/stores/_me" }, "--store me", "store init"},
	} {
		h := &liveHost{cfg: LiveConfig{PersonalStore: c.personal}}
		err := h.explainNotFound(ctx, nil, notAttached)
		var e *errs.Error
		if !errors.As(err, &e) {
			t.Fatalf("%s: not an errs.Error: %v", c.name, err)
		}
		if !strings.Contains(e.Hint, c.want) || strings.Contains(e.Hint, c.never) {
			t.Errorf("%s: hint %q, want it to name %q and never %q", c.name, e.Hint, c.want, c.never)
		}
	}
}
