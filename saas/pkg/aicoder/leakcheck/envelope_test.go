package leakcheck

import (
	"strings"
	"testing"
)

// TestClientActorEnvelopeMatrix pins which 0.1.18 row-envelope keys a client
// repo's actor copy may carry (envelopeKeyAllowed): bookkeeping in the shape
// that names nothing internal passes, every other shape blocks, through the
// commit check (Check) and the store-less CI check (CheckCI) alike.
func TestClientActorEnvelopeMatrix(t *testing.T) {
	const writer = "act_00000000000000000000000000000abc"
	base := `"_table":"actors","_v":2,"id":"` + actID + `","display_name":"Alice","kind":"human"`
	cases := []struct {
		name  string
		extra string // JSON members added to base ("" = none)
		ok    bool
	}{
		{"bare projection", ``, true},
		{"hlc", `"_hlc":"2026-11-04T12:20:58.731Z/0000/` + writer + `"`, true},
		{"created_by and updated_by act ids", `"created_by":"` + writer + `","updated_by":"` + writer + `"`, true},
		{"origin repo only", `"_o":{"store":"repo"}`, true},
		{"origin repo with cmd", `"_o":{"store":"repo","cmd":"rule add"}`, true},
		{"origin unicode cmd", `"_o":{"store":"repo","cmd":"régle ✓"}`, true},
		{"empty created_by", `"created_by":""`, true},
		{"null origin", `"_o":null`, true},
		{"full v2 envelope", `"_hlc":"x","_o":{"store":"repo","cmd":"rule add"},"created_at":"t","updated_at":"t","created_by":"` + writer + `","updated_by":"` + writer + `"`, true},
		// refusals: each names something internal or has an unexpected shape
		{"origin internal store name", `"_o":{"store":"agency"}`, false},
		{"origin me store", `"_o":{"store":"me"}`, false},
		{"origin sto id", `"_o":{"store":"sto_01a1a679f24c759abb49e8625618d86c"}`, false},
		{"origin empty store", `"_o":{"store":""}`, false},
		{"origin moves list", `"_o":{"store":"repo","moves":[{"moved_from":"agency","moved_to":"repo"}]}`, false},
		{"origin unknown key", `"_o":{"store":"repo","machine":"alice-laptop"}`, false},
		{"origin non-text cmd", `"_o":{"store":"repo","cmd":7}`, false},
		{"origin not an object", `"_o":"repo"`, false},
		{"created_by email", `"created_by":"alice@northwind.example"`, false},
		{"created_by stable key", `"created_by":"human:alice@northwind.example"`, false},
		{"updated_by wrong prefix", `"updated_by":"mem_00000000000000000000000000000abc"`, false},
		{"created_by act id with suffix", `"created_by":"` + writer + `x"`, false},
		{"hlc not text", `"_hlc":{"t":1}`, false},
		{"stable_key", `"stable_key":"human:alice@northwind.example"`, false},
		{"handle", `"handle":"alice"`, false},
		{"meta block", `"meta":{"mtk_1":{"v":"x"}}`, false},
		{"deleted flag", `"_deleted":true`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "{" + base
			if tc.extra != "" {
				body += "," + tc.extra
			}
			body += "}"
			path := ".lore/data/actors/" + actID + ".json"
			fs := Blocking(Check([]Item{{Where: path, Path: path, Content: []byte(body)}}, nil, Options{ClientRepo: true}))
			if got := len(fs) == 0; got != tc.ok {
				t.Fatalf("commit check ok=%v, want %v: %v\n%s", got, tc.ok, fs, body)
			}
			ci := CheckCI(map[string][]byte{path: []byte(body)}, repoPID)
			if got := len(ci) == 0; got != tc.ok {
				t.Fatalf("CI check ok=%v, want %v: %v", got, tc.ok, ci)
			}
			if !tc.ok && !strings.Contains(fs[0].Detail, "{id, display name, kind}") {
				t.Fatalf("the refusal must name the projection: %q", fs[0].Detail)
			}
		})
	}
}

// TestEnvelopeKeysIgnoredOutsideClientRepos: in a repo that is not a client
// repo actor files are the repo's own business, envelope or not.
func TestEnvelopeKeysIgnoredOutsideClientRepos(t *testing.T) {
	path := ".lore/data/actors/" + actID + ".json"
	body := `{"id":"` + actID + `","_o":{"store":"agency"},"stable_key":"human:a@b.example"}`
	if fs := Check([]Item{{Where: path, Path: path, Content: []byte(body)}}, nil, Options{}); len(fs) != 0 {
		t.Fatalf("own repo: no actor finding expected: %v", fs)
	}
}

// TestOriginRuleMatrix pins originIsRepo through both checks: a 0.1.18 row
// file in a client repo must have been written in the repo store or moved
// there last; anything else is a copy of another store's row.
func TestOriginRuleMatrix(t *testing.T) {
	const rowPath = ".lore/data/memories/" + memID + ".json"
	cases := []struct {
		name   string
		origin string // the "_o" member ("" = absent)
		ok     bool
	}{
		{"no provenance (v0.1.17 row)", ``, true},
		{"null provenance", `"_o":null`, true},
		{"written in the repo", `"_o":{"store":"repo","cmd":"memory add"}`, true},
		{"moved into the repo last", `"_o":{"store":"agency","moves":[{"moved_from":"agency","moved_to":"repo"}]}`, true},
		{"moved in then out", `"_o":{"store":"repo","moves":[{"moved_from":"repo","moved_to":"agency"}]}`, false},
		{"written in an org store", `"_o":{"store":"northwind"}`, false},
		{"written in the personal store", `"_o":{"store":"me"}`, false},
		{"empty store", `"_o":{"store":""}`, false},
		{"no store key", `"_o":{"cmd":"memory add"}`, false},
		{"empty moves list falls back to store", `"_o":{"store":"repo","moves":[]}`, true},
		{"bad move entry", `"_o":{"store":"repo","moves":["repo"]}`, false},
		{"not an object", `"_o":"repo"`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"_v":2,"_table":"memories","id":"` + memID + `","body":"x"`
			if tc.origin != "" {
				body += "," + tc.origin
			}
			body += "}"
			ci := CheckCI(map[string][]byte{rowPath: []byte(body)}, repoPID)
			if got := len(ci) == 0; got != tc.ok {
				t.Fatalf("CI ok=%v want %v: %v", got, tc.ok, ci)
			}
			for _, f := range ci {
				if strings.Contains(f.Detail, "northwind") || strings.Contains(f.Detail, "agency") {
					t.Fatalf("a CI finding names no store: %q", f.Detail)
				}
			}
			fs := Blocking(Check([]Item{{Where: rowPath, Path: rowPath, Content: []byte(body)}}, nil, Options{ClientRepo: true}))
			if got := len(fs) == 0; got != tc.ok {
				t.Fatalf("commit check ok=%v want %v: %v", got, tc.ok, fs)
			}
			if own := Check([]Item{{Where: rowPath, Path: rowPath, Content: []byte(body)}}, nil, Options{}); len(own) != 0 {
				t.Fatalf("an own repo's row provenance is its business: %v", own)
			}
		})
	}
}
