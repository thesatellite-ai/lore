package wire_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
)

// TestMetaStatusLines (later 6, FA-META-11): the sync status metadata
// lines name a registry past meta.max_keys_per_registry, and none at the
// cap; no quarantined row gives no quarantine line.
func TestMetaStatusLines(t *testing.T) {
	h := opstest.New(t)
	for _, k := range []string{"a_key", "b_key", "c_key"} {
		h.MustYes("meta.add", `{"name":"`+k+`","type":"text"}`)
	}
	h.MustYes("policy.set", `{"key":"meta.max_keys_per_registry","value":"3"}`)
	if q, over := h.App.MetaStatusLines(t.Context()); len(q) != 0 || len(over) != 0 {
		t.Fatalf("at cap: %v %v", q, over)
	}
	h.MustYes("policy.set", `{"key":"meta.max_keys_per_registry","value":"2"}`)
	q, over := h.App.MetaStatusLines(t.Context())
	if len(q) != 0 || len(over) != 1 || !strings.HasPrefix(over[0], "this repo: 3 key rows, past meta.max_keys_per_registry 2") {
		t.Fatalf("over cap: %v %v", q, over)
	}
}
