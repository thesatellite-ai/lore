package resname

import (
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// TestPickNameBeforeAlias: a key resolves through an alias (any case,
// bare or <kind>/<alias>) only when no resource has it as its name or
// id, so an alias never makes a name ambiguous (devco D-817).
func TestPickNameBeforeAlias(t *testing.T) {
	t.Parallel()
	kindName := func(id string) string { return map[string]string{"knd_p": "person", "knd_h": "host"}[id] }
	robert := Candidate{ID: "res_1", KindID: "knd_p", Name: "robert-kim", Src: store.Source("me/work"), Aliases: []string{"Bobby", "Robert Kim"}}
	bobbyHost := Candidate{ID: "res_2", KindID: "knd_h", Name: "bobby", Src: store.Source("me/work")}
	cs := []Candidate{robert, bobbyHost}
	pick := func(key string, in []Candidate) []string {
		var out []string
		for _, c := range Pick(in, key, Forms(key), kindName) {
			out = append(out, c.ID)
		}
		return out
	}
	cases := []struct {
		key  string
		in   []Candidate
		want []string
	}{
		{"robert-kim", cs, []string{"res_1"}},
		{"Bobby", []Candidate{robert}, []string{"res_1"}},
		{"bobby", []Candidate{robert}, []string{"res_1"}},
		{"person/Bobby", []Candidate{robert}, []string{"res_1"}},
		{"work:person/robert kim", []Candidate{robert}, []string{"res_1"}},
		{"host/Bobby", []Candidate{robert}, nil},
		// the host named bobby wins over robert-kim's alias Bobby
		{"bobby", cs, []string{"res_2"}},
		{"nobody", cs, nil},
	}
	for _, c := range cases {
		got := pick(c.key, c.in)
		if len(got) != len(c.want) || (len(got) > 0 && got[0] != c.want[0]) {
			t.Errorf("Pick(%q) = %v, want %v", c.key, got, c.want)
		}
	}
}
