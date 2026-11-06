package wire

// Port of saas/cmd/cli search_senscond_test.go (B11). OV34/OV39-SENSCOND
// (owner, OVERNIGHT Qs 2026-11-05): a field:value term on a sensitive
// resource field and a key:value term on a sensitive metadata key MATCH the
// real value and print it masked (•••); key? is unchanged; the mask itself
// is never a searchable value. Driven through the registered search.run op
// so the fan-out, the unmasked match lens and the masked label are in the
// loop (wire/search.go resourceConds / metaTerms).

import (
	"encoding/json"
	"strings"
	"testing"

	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
)

func TestSearchSensitiveConditionsMatchAndMask(t *testing.T) {
	t.Parallel()
	a := openWith(t, gitRepo(t), Options{})
	withProject(t, a)
	steps := []struct{ op, in string }{
		{opssettings.OpKindAdd, `{"kind":"host"}`},
		{opssettings.OpFieldAdd, `{"kind":"host","field":"region","type":"text"}`},
		{opssettings.OpFieldAdd, `{"kind":"host","field":"vault_tier","type":"text","sensitive":true}`},
		{opssettings.OpResAdd, `{"kind":"host","name":"hub_prod","set":["region=eu","vault_tier=gold7"]}`},
		{opssettings.OpResAdd, `{"kind":"host","name":"hub_dev","set":["region=us","vault_tier=bronze"]}`},
		{opssettings.OpMetaAdd, `{"name":"owner_team","type":"text"}`},
		{opssettings.OpMetaAdd, `{"name":"pin_code","type":"text","sensitive":true}`},
	}
	for _, s := range steps {
		if _, err := run(t, a, s.op, s.in, true); err != nil {
			t.Fatalf("%s %s: %v", s.op, s.in, err)
		}
	}
	memory := func(body string, pairs ...string) {
		out, err := run(t, a, opsmemory.OpAdd, `{"body":"`+body+`"}`, true)
		if err != nil {
			t.Fatal(err)
		}
		id := idOf(t, out)
		for _, p := range pairs {
			if _, err := run(t, a, opssettings.OpMetaSet, `{"id":"`+id+`","pair":"`+p+`"}`, true); err != nil {
				t.Fatalf("meta set %s: %v", p, err)
			}
		}
	}
	memory("deploy notes alpha", "owner_team=core", "pin_code=pz4417")
	memory("deploy notes beta", "owner_team=web")

	cases := []struct {
		name, query string
		want        []string // substrings the hit labels must hold
		never       []string // substrings that must not appear anywhere in the hits
		none        bool     // no hit at all
	}{
		{"sensitive field matches, value masked", "vault_tier:gold7", []string{"hub_prod", "(field vault_tier: •••)"}, []string{"gold7", "hub_dev"}, false},
		{"sensitive field, case-insensitive", "vault_tier:GOLD7", []string{"hub_prod", "vault_tier: •••"}, []string{"GOLD7", "gold7", "hub_dev"}, false},
		{"sensitive field, = form", "vault_tier=bronze", []string{"hub_dev", "vault_tier: •••"}, []string{"bronze", "hub_prod"}, false},
		{"sensitive field, wrong value", "vault_tier:silver", nil, nil, true},
		{"sensitive field, the mask is not a value", "vault_tier:•••", nil, nil, true},
		{"plain field prints the value", "region:eu", []string{"hub_prod", "(field region: eu)"}, []string{"hub_dev"}, false},
		{"sensitive key matches, value masked", "pin_code:pz4417", []string{"deploy notes alpha", "pin_code: •••"}, []string{"pz4417", "beta"}, false},
		{"sensitive key, the mask is not a value", "pin_code:•••", nil, nil, true},
		{"sensitive key, wrong value", "pin_code:9999", nil, nil, true},
		{"sensitive key? unchanged", "pin_code?", []string{"deploy notes alpha", "(pin_code?)"}, []string{"pz4417", "beta"}, false},
		{"plain key prints the value", "owner_team:web", []string{"deploy notes beta", "owner_team: web"}, []string{"alpha"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := json.Marshal(opssearch.RunIn{Query: tc.query})
			if err != nil {
				t.Fatal(err)
			}
			out, err := run(t, a, opssearch.OpRun, string(q), false)
			if err != nil {
				t.Fatal(err)
			}
			var res opssearch.RunOut
			if err = json.Unmarshal([]byte(out), &res); err != nil {
				t.Fatal(err)
			}
			// The hits only: the output echoes the query, which holds the
			// value a sensitive term matched on.
			hits, err := json.Marshal(res.Data.Hits)
			if err != nil {
				t.Fatal(err)
			}
			got := string(hits)
			if tc.none {
				if len(res.Data.Hits) != 0 {
					t.Fatalf("search %q must find nothing: %s", tc.query, got)
				}
				return
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("search %q lacks %q: %s", tc.query, w, got)
				}
			}
			for _, n := range tc.never {
				if strings.Contains(got, n) {
					t.Errorf("search %q must not print %q: %s", tc.query, n, got)
				}
			}
		})
	}
}
