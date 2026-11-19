package renderhost

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// TestContextReadsInternalStoresInOwnRepo (devco D-429, D-481): `lore
// context` picks from the attached internal stores too (a repo linked
// with org link --own keeps its notes in the store folder, and
// --for-prompt said "nothing in the store applies here"); so does a
// client repo, whose notes live only in its linked folder (render names
// lore context as the way agents read them).
func TestContextReadsInternalStoresInOwnRepo(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name   string
		client bool
		inner  bool
	}{{"own repo", false, true}, {"client repo", true, true}} {
		t.Run(c.name, func(t *testing.T) {
			h := New(Options{App: internalAppAs(t, c.client), Home: t.TempDir()})
			rows, err := h.loadKnowledge(ctx)
			if err != nil {
				t.Fatal(err)
			}
			got := map[store.Source]bool{}
			for _, k := range rows {
				got[k.r.source] = true
			}
			if !got[store.SourceRepo] || !got["share"] || got["inner"] != c.inner {
				t.Fatalf("context sources %v, want repo and share, inner=%v", got, c.inner)
			}
			res, err := h.contextForPrompt(ctx, "zebra note")
			if err != nil {
				t.Fatal(err)
			}
			var text strings.Builder
			for _, p := range res.picks {
				text.WriteString(p.full)
			}
			if strings.Contains(text.String(), "zebra note in inner") != c.inner {
				t.Fatalf("--for-prompt picks:\n%s\nwant the internal store's note: %v", text.String(), c.inner)
			}
		})
	}
}
