package search_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
)

// TestGlobalHitsCarryMetadata (MQ13, G-CONTENT-3): a global search hit of
// a row with metadata carries meta (sensitive values masked, named in
// meta_masked), a hit without metadata carries neither key, and a trashed
// hit of --include-deleted carries none (the trash commands read it); the
// text header counts the tables of the hits it lists, so a search whose
// only hit is in the trash says "across 1 entity type", never 0.
func TestGlobalHitsCarryMetadata(t *testing.T) {
	h := opstest.New(t)
	h.Must("meta.add", `{"name":"vpn_host","type":"text","sensitive":true}`)
	withMeta := h.ID(h.Must("rule.add", `{"body":"quokka with meta","meta":["jira=HUB-1","vpn_host=10.9.8.7"]}`))
	plain := h.ID(h.Must("decision.add", `{"title":"quokka plain","body":"no meta here"}`))
	type hit struct {
		ID         string            `json:"id"`
		InTrash    bool              `json:"in_trash"`
		Meta       map[string]string `json:"meta"`
		MetaMasked []string          `json:"meta_masked"`
	}
	hits := func(in string) (map[string]hit, string) {
		t.Helper()
		res := h.Must("search.run", in)
		js := h.JSON(res)
		var out struct {
			Data struct {
				Hits []hit `json:"hits"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(js), &out); err != nil {
			t.Fatalf("%v: %s", err, js)
		}
		if strings.Contains(js, "10.9.8.7") {
			t.Fatalf("a sensitive value in a hit:\n%s", js)
		}
		m := map[string]hit{}
		for _, x := range out.Data.Hits {
			m[x.ID] = x
		}
		return m, opstest.Text(res)
	}
	got, text := hits(`{"query":"quokka"}`)
	if m := got[withMeta]; m.Meta["jira"] != "HUB-1" || m.Meta["vpn_host"] != "•••" || len(m.MetaMasked) != 1 || m.MetaMasked[0] != "vpn_host" {
		t.Fatalf("hit with metadata: %+v", m)
	}
	if p, ok := got[plain]; !ok || p.Meta != nil || p.MetaMasked != nil {
		t.Fatalf("hit without metadata: %+v (found %v)", p, ok)
	}
	if !strings.Contains(text, "across 2 entity types") {
		t.Fatalf("header:\n%s", text)
	}

	h.MustYes("rule.delete", `{"id":"`+withMeta+`"}`)
	h.MustYes("decision.delete", `{"id":"`+plain+`"}`)
	got, text = hits(`{"query":"quokka","include_deleted":true}`)
	tr, ok := got[withMeta]
	if !ok || !tr.InTrash || tr.Meta != nil || tr.MetaMasked != nil {
		t.Fatalf("trashed hit: %+v (found %v)", tr, ok)
	}
	if !strings.Contains(text, "across 2 entity types") || strings.Contains(text, "across 0") {
		t.Fatalf("header of trash-only hits:\n%s", text)
	}
	h.Must("search.rebuild", `{}`)
	_, text = hits(`{"query":"quokka with meta","include_deleted":true}`)
	if !strings.Contains(text, "across 1 entity type:") {
		t.Fatalf("one trashed hit, header:\n%s", text)
	}
}
