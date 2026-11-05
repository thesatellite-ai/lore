package main

// OV34/OV39-SENSCOND (owner, OVERNIGHT Qs 2026-11-05): a field:value term on
// a sensitive resource field and a key:value term on a sensitive metadata key
// MATCH the real value and print it masked (•••); key? is unchanged; the mask
// itself is never a searchable value. Driven through the real binary so the
// search fan-out, the unmasked match lens and the masked snippet are all in
// the loop (search_global.go resourceFieldTerms / metaSearchTerms).

import (
	"strings"
	"testing"
)

func TestSearchSensitiveConditionsMatchAndMask(t *testing.T) {
	r := newToolRepo(t, nil)
	r.lore("init")
	r.lore("resource", "kind", "add", "host", "--yes")
	r.lore("resource", "field", "add", "host", "region", "--type", "text", "--yes")
	r.lore("resource", "field", "add", "host", "vault_tier", "--type", "text", "--sensitive", "--yes")
	r.lore("resource", "add", "host", "hub_prod", "--set", "region=eu", "--set", "vault_tier=gold7", "--yes")
	r.lore("resource", "add", "host", "hub_dev", "--set", "region=us", "--set", "vault_tier=bronze", "--yes")
	r.lore("meta", "add", "owner_team", "--type", "text", "--yes")
	r.lore("meta", "add", "pin_code", "--type", "text", "--sensitive", "--yes")
	r.lore("memory", "add", "--body", "deploy notes alpha", "--meta", "owner_team=core", "--meta", "pin_code=pz4417", "--yes")
	r.lore("memory", "add", "--body", "deploy notes beta", "--meta", "owner_team=web", "--yes")

	cases := []struct {
		name, query string
		want        []string // substrings every hit list must hold
		never       []string // substrings that must not appear
		none        bool     // no hit at all
	}{
		{"sensitive field matches, value masked", "vault_tier:gold7", []string{"hub_prod", "(field vault_tier: •••)"}, []string{"gold7", "hub_dev"}, false},
		{"sensitive field, case-insensitive", "vault_tier:GOLD7", []string{"hub_prod", "vault_tier: •••"}, []string{"GOLD7", "hub_dev"}, false},
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
			out := hitLines(r.lore("search", tc.query).stdout)
			if tc.none {
				if out != "" {
					t.Fatalf("search %q must find nothing: %s", tc.query, out)
				}
				return
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("search %q lacks %q: %s", tc.query, w, out)
				}
			}
			for _, n := range tc.never {
				if strings.Contains(out, n) {
					t.Errorf("search %q must not print %q: %s", tc.query, n, out)
				}
			}
		})
	}
}

// hitLines keeps a search's hit lines ("[<entity>] <id> …"): the header
// echoes the query, which holds the value a sensitive term matched on.
func hitLines(out string) string {
	var hits []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "[") {
			hits = append(hits, l)
		}
	}
	return strings.Join(hits, "\n")
}
