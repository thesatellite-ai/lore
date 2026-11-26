package search_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
)

// TestStaleFactHitMarkedAndRankedBelowFresh (devco:D-941, bug hunt H-190):
// two rows with the same match, the newer one stating a fact whose
// evidence file changed in git after it was verified: the stale hit
// carries "stale: evidence changed since <day> (<file>)" and comes after
// the fresh one (it came first before, as the newer row of equal score).
func TestStaleFactHitMarkedAndRankedBelowFresh(t *testing.T) {
	h := opstest.New(t)
	h.CommitOn("2026-11-01", map[string]string{"svc/a.go": "package svc\n", "svc/b.go": "package svc\n"})
	fresh := h.ID(h.Must("memory.add", `{"body":"quokka rule alpha holds","meta":["fact_evidence=svc/a.go:1","fact_verified_at=2026-11-02"]}`))
	stale := h.ID(h.Must("memory.add", `{"body":"quokka rule bravo holds","meta":["fact_evidence=svc/b.go:1","fact_verified_at=2026-11-02"]}`))
	h.CommitOn("2026-11-10", map[string]string{"svc/b.go": "package svc\n\n// changed\n"})

	res := h.Must("search.run", `{"query":"quokka"}`)
	var out struct {
		Data struct {
			Hits []struct {
				ID    string `json:"id"`
				Stale string `json:"stale"`
			} `json:"hits"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
		t.Fatal(err)
	}
	hits := out.Data.Hits
	if len(hits) != 2 || hits[0].ID != fresh || hits[1].ID != stale {
		t.Fatalf("want fresh %s then stale %s, got %+v", fresh, stale, hits)
	}
	if hits[0].Stale != "" || hits[1].Stale != "stale: evidence changed since 2026-11-02 (svc/b.go)" {
		t.Fatalf("stale marks: %+v", hits)
	}
	if text := opstest.Text(res); !strings.Contains(text, "[stale: evidence changed since 2026-11-02 (svc/b.go)]") {
		t.Fatalf("text has no stale mark:\n%s", text)
	}
}

// TestStaleOffTopicHitBelowFreshRare (devco:D-1007, set 8 ask 6): a stale
// fact holding three of the question's common words ranked first, above
// a fresh row holding its two rare ones (0.9 of its score was not enough
// against one more word). A stale fact's words now count by their rarity,
// so the fresh row leads.
func TestStaleOffTopicHitBelowFreshRare(t *testing.T) {
	h := opstest.New(t)
	h.CommitOn("2026-11-01", map[string]string{"svc/b.go": "package svc\n"})
	fresh := h.ID(h.Must("memory.add", `{"body":"The quokka exporter writes one gzip file per tenant"}`))
	stale := h.ID(h.Must("memory.add", `{"body":"Billing uploads retry three times, then the failed batch waits","meta":["fact_evidence=svc/b.go:1","fact_verified_at=2026-11-02"]}`))
	for _, w := range []string{"uploads", "retry", "failed"} {
		for j := range 6 {
			h.Must("memory.add", fmt.Sprintf(`{"body":"Note %d on %s"}`, j, w))
		}
	}
	h.CommitOn("2026-11-10", map[string]string{"svc/b.go": "package svc\n\n// changed\n"})

	res := h.Must("search.run", `{"query":"does the quokka exporter retry failed uploads"}`)
	var out struct {
		Data struct {
			Hits []struct {
				ID    string `json:"id"`
				Stale string `json:"stale"`
			} `json:"hits"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, x := range out.Data.Hits {
		pos[x.ID] = i + 1
		if x.ID == stale && x.Stale == "" {
			t.Fatalf("the stale row is not marked stale: %+v", out.Data.Hits)
		}
	}
	if pos[fresh] != 1 || pos[stale] == 0 {
		t.Fatalf("want fresh %s first and stale %s listed, got %+v", fresh, stale, out.Data.Hits)
	}
}
