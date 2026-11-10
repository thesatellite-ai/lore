package metafilter

import (
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
)

const (
	idClient = "mky_01936f0000007a1b8c2d3e4f5a6ba101"
	idCost   = "mky_01936f0000007a1b8c2d3e4f5a6ba102"
	idPages  = "mky_01936f0000007a1b8c2d3e4f5a6ba105"
	idDeploy = "mky_01936f0000007a1b8c2d3e4f5a6ba106"
	idOther  = "mky_01936f0000007a1b8c2d3e4f5a6ba107"
)

func kid(t *testing.T, s string) ids.ID[ids.MetaKey] {
	t.Helper()
	id, err := ids.Parse[ids.MetaKey](s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mm(t *testing.T, kv ...string) map[ids.ID[ids.MetaKey]]value.JSON {
	t.Helper()
	out := map[ids.ID[ids.MetaKey]]value.JSON{}
	for i := 0; i+1 < len(kv); i += 2 {
		c, err := canon.Compact([]byte(kv[i+1]))
		if err != nil {
			t.Fatal(err)
		}
		j, err := value.JSONOf(c)
		if err != nil {
			t.Fatal(err)
		}
		out[kid(t, kv[i])] = j
	}
	return out
}

func TestFilterMatrix(t *testing.T) {
	keys := []Key{
		{ID: kid(t, idClient), Name: "client", Scope: "project:acme-hub", Type: meta.Type{Kind: value.KindText}},
		{ID: kid(t, idCost), Name: "cost_hours", Scope: "project:acme-hub", Type: meta.Type{Kind: value.KindNumber}},
		{ID: kid(t, idPages), Name: "pages", Scope: "repo", Type: meta.Type{Kind: value.KindList, Elem: value.KindText}},
		{ID: kid(t, idDeploy), Name: "deploy", Scope: "repo", Type: meta.Type{Kind: value.KindJSON}},
		{ID: kid(t, idOther), Name: "client", Scope: "org:agency", Type: meta.Type{Kind: value.KindText}},
	}
	rows := map[string]map[ids.ID[ids.MetaKey]]value.JSON{
		"r1": mm(t, idClient, `{"name":"client","scope":"project:acme-hub","v":"acme"}`, idCost, `{"name":"cost_hours","scope":"project:acme-hub","v":3.5}`,
			idPages, `{"name":"pages","scope":"repo","v":["invoices","billing-export"]}`, idDeploy, `{"name":"deploy","scope":"repo","v":{"region":"eu-central"}}`),
		"r2": mm(t, idClient, `{"name":"client","scope":"project:acme-hub","v":"Acme"}`, idCost, `{"name":"cost_hours","scope":"project:acme-hub","v":7}`),
		"r3": mm(t, idOther, `{"name":"client","scope":"org:agency","v":"acme"}`, idCost, `{"name":"cost_hours","scope":"project:acme-hub","v":"two"}`),
		"r4": mm(t, idCost, `{"conflict":{"base":1,"ours":2,"theirs":3},"name":"cost_hours","scope":"project:acme-hub","strict":true}`),
		"r5": {},
	}
	cases := []struct {
		filters []string
		place   string
		want    []string
	}{
		{[]string{"client=acme"}, "", []string{"r1", "r3"}},
		{[]string{"client=acme"}, "project:acme-hub", []string{"r1"}},
		{[]string{"client=acme"}, "org", []string{"r3"}},
		{[]string{"client~=ACME"}, "", []string{"r1", "r2", "r3"}},
		{[]string{"cost_hours>2"}, "", []string{"r1", "r2"}},
		{[]string{"cost_hours<4"}, "", []string{"r1"}},
		{[]string{"client=acme", "cost_hours>2"}, "", []string{"r1"}},
		{[]string{"pages=invoices"}, "", []string{"r1"}},
		{[]string{"pages!=invoices"}, "", []string{"r2", "r3", "r4", "r5"}},
		{[]string{"deploy.region=eu-central"}, "", []string{"r1"}},
		{[]string{"cost_hours?"}, "", []string{"r1", "r2", "r3"}},
		{[]string{"pages!?"}, "", []string{"r2", "r3", "r4", "r5"}},
		{[]string{"clinet=acme"}, "", nil},
	}
	for _, c := range cases {
		p, warns, err := Compile(c.filters, c.place, "", keys)
		if err != nil {
			t.Fatalf("%v: %v", c.filters, err)
		}
		var got []string
		for _, id := range []string{"r1", "r2", "r3", "r4", "r5"} {
			if ok, _ := p.Match(id, rows[id]); ok {
				got = append(got, id)
			}
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%v place %q = %v, want %v", c.filters, c.place, got, c.want)
		}
		if c.filters[0] == "clinet=acme" && (len(warns) != 1 || !strings.Contains(warns[0], "Did you mean client?")) {
			t.Errorf("warnings = %v", warns)
		}
	}
	// Skipped notes: a text value under a number key, and a conflict.
	p, _, _ := Compile([]string{"cost_hours>2"}, "", "", keys)
	if _, n := p.Match("r3", rows["r3"]); !strings.Contains(string(n), "skipped: cost_hours on r3 is text") {
		t.Errorf("note = %q", n)
	}
	if _, n := p.Match("r4", rows["r4"]); !strings.Contains(string(n), "conflict") {
		t.Errorf("note = %q", n)
	}
	// > on a text key is E_META_OP.
	if _, _, err := Compile([]string{"client>a"}, "", "", keys); err == nil {
		t.Error("client>a compiled")
	}
	// Sort: by cost_hours, missing last; :desc reverses the present ones.
	for _, s := range []struct {
		sort string
		want []string
	}{{"cost_hours", []string{"r1", "r2", "r3", "r4", "r5"}}, {"meta.cost_hours:desc", []string{"r3", "r2", "r1", "r4", "r5"}}} {
		p, _, err := Compile(nil, "", s.sort, keys)
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{"r5", "r4", "r3", "r2", "r1"}
		slices.SortStableFunc(ids, func(a, b string) int { return p.Compare(rows[a], rows[b]) })
		if !slices.Equal(ids[:3], s.want[:3]) {
			t.Errorf("sort %s = %v", s.sort, ids)
		}
	}
}

func TestQuarantine(t *testing.T) {
	ok := mm(t, idClient, `{"name":"client","scope":"repo","v":"acme"}`, idCost, `{"conflict":{"base":1,"ours":2,"theirs":3},"name":"cost","scope":"repo"}`)
	if q := Quarantine(ok); len(q) != 0 {
		t.Errorf("valid meta quarantined: %v", q)
	}
	bad := mm(t, idClient, `{"name":"Client-Name","scope":"repo","v":"acme"}`, idCost, `{"name":"cost","scope":"repo","v":""}`, idPages, `{"name":"pages","scope":"repo"}`)
	q := Quarantine(bad)
	if len(q) != 3 {
		t.Fatalf("problems = %v", q)
	}
}
