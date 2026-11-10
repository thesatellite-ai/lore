package work_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
)

// TestWorkSearchHitsCarryMeta (METADATA 3.17 MQ13, G-CONTENT-3, G-META-10):
// a task, mission, plan or tasklist search hit carries the row's metadata
// as the knowledge nouns' hits do: name → value, "<key>_label" for a
// labelled allowed value, a sensitive value masked (never its text) and
// named in meta_masked; a row without metadata keeps the v0.1.17 hit (no
// meta, no meta_masked key).
func TestWorkSearchHitsCarryMeta(t *testing.T) {
	t.Parallel()
	secret := "zq" + strings.Repeat("7", 6) // a value that must never be printed
	for _, noun := range []string{"task", "mission", "plan", "tasklist"} {
		t.Run(noun, func(t *testing.T) {
			t.Parallel()
			h := opstest.New(t)
			h.MustYes("meta.add", `{"name":"tier","type":"text","values":"low,high"}`)
			h.MustYes("meta.edit", `{"id":"tier","value_label":["high=High priority"]}`)
			h.MustYes("meta.add", `{"name":"vault","type":"text","sensitive":true}`)
			withMeta := h.ID(h.MustYes(noun+".add", `{"title":"needle tagged","body":"b","meta":["tier=high",`+opstest.Quote("vault="+secret)+`]}`))
			plain := h.ID(h.MustYes(noun+".add", `{"title":"needle plain","body":"b"}`))

			raw := h.JSON(h.Must(noun+".search", `{"query":"needle"}`))
			if strings.Contains(raw, secret) {
				t.Fatalf("the sensitive value is in the hits: %s", raw)
			}
			var env struct {
				Data []map[string]json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal([]byte(raw), &env); err != nil {
				t.Fatal(err)
			}
			seen := 0
			for _, hit := range env.Data {
				var id string
				if err := json.Unmarshal(hit["id"], &id); err != nil {
					t.Fatal(err)
				}
				switch id {
				case plain:
					seen++
					if _, ok := hit["meta"]; ok {
						t.Errorf("a row without metadata got a meta key: %s", raw)
					}
					if _, ok := hit["meta_masked"]; ok {
						t.Errorf("a row without metadata got meta_masked: %s", raw)
					}
				case withMeta:
					seen++
					var m map[string]string
					var masked []string
					if err := json.Unmarshal(hit["meta"], &m); err != nil {
						t.Fatalf("meta: %v in %s", err, raw)
					}
					if err := json.Unmarshal(hit["meta_masked"], &masked); err != nil {
						t.Fatalf("meta_masked: %v in %s", err, raw)
					}
					if m["tier"] != "high" || m["tier_label"] != "High priority" {
						t.Errorf("allowed value: %v, want tier=high tier_label=High priority", m)
					}
					if v, ok := m["vault"]; !ok || v == secret || v == "" {
						t.Errorf("sensitive value shown as %q, want it masked", v)
					}
					if _, has := m["vault_label"]; has || len(masked) != 1 || masked[0] != "vault" {
						t.Errorf("meta_masked %v / meta %v, want [vault] and no vault_label", masked, m)
					}
				}
			}
			if seen != 2 {
				t.Fatalf("found %d of the 2 rows: %s", seen, raw)
			}
		})
	}
}
