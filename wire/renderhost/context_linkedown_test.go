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

// TestContextTopicsCountWhatContextReads (devco:D-700): `lore context
// --topics` in a linked repo counted only the render's stores (the linked
// folder) while --for-prompt and rule list read the org's internal store
// too; it now counts exactly the rows loadKnowledge reads, and the
// committed render's index keeps leaving the internal store out.
func TestContextTopicsCountWhatContextReads(t *testing.T) {
	ctx := context.Background()
	for _, client := range []bool{false, true} {
		h := New(Options{App: internalAppAs(t, client), Home: t.TempDir()})
		rows, err := h.loadKnowledge(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		for _, k := range rows {
			if k.kind == "memory" {
				want++
			}
		}
		count := func(lines []topicLine) int {
			n := 0
			for _, l := range lines {
				n += l.Counts["memories"]
			}
			return n
		}
		topics, err := h.contextTopicIndex(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := count(topics); got != want || want != 3 {
			t.Fatalf("client=%v: --topics counts %d memories, context reads %d (want 3: repo, share, inner)", client, got, want)
		}
		rendered, err := h.loadTopicIndex(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := count(rendered); got != 2 {
			t.Fatalf("client=%v: the committed index counts %d memories, want 2 (the internal store stays out of a committed file)", client, got)
		}
	}
}
