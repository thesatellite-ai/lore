package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

type liftKey struct{}

// TestRefuseWritesUnlessLifts (J-15): a refused store refuses every write
// but one whose ctx the lift answers true for.
func TestRefuseWritesUnlessLifts(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	refusal := errs.New(errs.Refused, "refused here")
	f.eng.RefuseWritesUnless(store.SourceRepo, refusal, func(ctx context.Context) bool { return ctx.Value(liftKey{}) != nil })
	if _, err := f.eng.Create(ctx, store.SourceRepo, schema.TableMemories, f.memFields("plain")); !errs.Has(err, errs.Refused) {
		t.Fatalf("plain write = %v, want E_REFUSED", err)
	}
	if _, err := f.eng.Create(context.WithValue(ctx, liftKey{}, true), store.SourceRepo, schema.TableMemories, f.memFields("lifted")); err != nil {
		t.Fatalf("lifted write = %v", err)
	}
}
