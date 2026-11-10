package memory_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
)

// TestJSONEmbeddingNull: v0.1.17's memory show/list --json printed
// `"embedding": null` between the actor ids and `"edges": {}` on every
// row (ent scanned the NULL column into a non-nil pointer); the core
// prints the same key in the same place.
func TestJSONEmbeddingNull(t *testing.T) {
	h := opstest.New(t)
	id := h.ID(h.Must("memory.add", `{"body":"embedding parity"}`))
	want := `"created_by_actor_id":"` + opstest.Actor + `","embedding":null,"edges":{}`
	for name, in := range map[string]string{"memory.show": `{"id":"` + id + `"}`, "memory.list": ``} {
		if got := h.JSON(h.Must(name, in)); !strings.Contains(got, want) {
			t.Errorf("%s: %s\nlacks %s", name, got, want)
		}
	}
}
