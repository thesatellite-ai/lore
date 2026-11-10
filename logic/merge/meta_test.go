package merge

import (
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

const (
	mkClient = "mky_01936f0000007a1b8c2d3e4f5a6ba101"
	mkCost   = "mky_01936f0000007a1b8c2d3e4f5a6ba102"
	mkJira   = "mky_01936f0000007a1b8c2d3e4f5a6ba104"
	mkPages  = "mky_01936f0000007a1b8c2d3e4f5a6ba105"
	mkDeploy = "mky_01936f0000007a1b8c2d3e4f5a6ba106"
)

// metaOf builds a meta map from id → entry JSON.
func metaOf(t *testing.T, kv ...string) map[ids.ID[ids.MetaKey]]value.JSON {
	t.Helper()
	out := map[ids.ID[ids.MetaKey]]value.JSON{}
	for i := 0; i+1 < len(kv); i += 2 {
		id, err := ids.Parse[ids.MetaKey](kv[i])
		if err != nil {
			t.Fatal(err)
		}
		c, err := canon.Compact([]byte(kv[i+1]))
		if err != nil {
			t.Fatal(err)
		}
		j, err := value.JSONOf(c)
		if err != nil {
			t.Fatal(err)
		}
		out[id] = j
	}
	return out
}

func withMeta(r row.Row, m map[ids.ID[ids.MetaKey]]value.JSON) row.Row { r.Meta = m; return r }

func entryOf(t *testing.T, r row.Row, id string) string {
	t.Helper()
	k, err := ids.Parse[ids.MetaKey](id)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := r.Meta[k]
	if !ok {
		return ""
	}
	return string(v.Raw())
}

// TestMetaMergeDesignExample is the METADATA 3.12 snippet: jira added,
// pages union, client newer wins, strict cost_hours a conflict; the same
// bytes whichever side is called ours.
func TestMetaMergeDesignExample(t *testing.T) {
	p := `"scope":"project:acme-hub"`
	base := metaOf(t,
		mkClient, `{"name":"client",`+p+`,"v":"acme"}`,
		mkCost, `{"name":"cost_hours",`+p+`,"strict":true,"v":3.5}`,
		mkPages, `{"name":"pages",`+p+`,"v":["invoices"]}`)
	ours := metaOf(t,
		mkClient, `{"name":"client",`+p+`,"v":"acme"}`,
		mkCost, `{"name":"cost_hours",`+p+`,"strict":true,"v":4}`,
		mkJira, `{"name":"jira",`+p+`,"v":"HUB-312"}`,
		mkPages, `{"name":"pages",`+p+`,"v":["invoices","billing-export"]}`)
	theirs := metaOf(t,
		mkClient, `{"name":"client",`+p+`,"v":"acme_gmbh"}`,
		mkCost, `{"name":"cost_hours",`+p+`,"strict":true,"v":5}`,
		mkPages, `{"name":"pages",`+p+`,"v":["invoices","dunning"]}`)
	b := withMeta(live(stamp(0, actorA), text("body"), text("x")), base)
	o := withMeta(live(stamp(1, actorA), text("body"), text("x")), ours)
	th := withMeta(live(stamp(2, actorB), text("body"), text("x")), theirs) // theirs newer
	res, err := Rows(PolicyFor(testDef), b, o, th)
	if err != nil {
		t.Fatal(err)
	}
	if got := entryOf(t, res.Row, mkJira); got == "" {
		t.Error("jira (added on ours) lost")
	}
	if got := entryOf(t, res.Row, mkClient); got != `{"name":"client","scope":"project:acme-hub","v":"acme_gmbh"}` {
		t.Errorf("client = %s", got)
	}
	pages := entryOf(t, res.Row, mkPages)
	for _, want := range []string{"invoices", "billing-export", "dunning"} {
		if !strings.Contains(pages, `"`+want+`"`) {
			t.Errorf("pages %s misses %s", pages, want)
		}
	}
	if got := entryOf(t, res.Row, mkCost); got != `{"conflict":{"base":3.5,"ours":4,"theirs":5},"name":"cost_hours","scope":"project:acme-hub","strict":true}` {
		t.Errorf("cost_hours = %s", got)
	}
	if res.Clean() || len(res.Conflicts) != 1 || res.Conflicts[0].Field != "meta."+mkCost {
		t.Errorf("conflicts = %+v", res.Conflicts)
	}
	if len(res.NewerWins) != 0 { // client changed on theirs only: not a clash
		t.Errorf("newer wins = %v", res.NewerWins)
	}
	if got := MetaConflicts(res.Row); len(got) != 1 || got[0] != mkCost {
		t.Errorf("MetaConflicts = %v", got)
	}
	// Either direction: the merged entries other than the conflict object
	// (which names ours/theirs by role) are byte-identical.
	rev, err := Rows(PolicyFor(testDef), b, th, o)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{mkClient, mkJira} {
		if entryOf(t, res.Row, id) != entryOf(t, rev.Row, id) {
			t.Errorf("%s differs by direction", id)
		}
	}
}

// TestMetaMergeRules covers the remaining 3.12 table rows.
func TestMetaMergeRules(t *testing.T) {
	type tc struct {
		name               string
		base, ours, theirs []string
		key, want          string
		conflict, wins     bool
	}
	cases := []tc{
		{name: "object per field",
			base:   []string{mkDeploy, `{"name":"deploy","scope":"repo","v":{"env":"stag"}}`},
			ours:   []string{mkDeploy, `{"name":"deploy","scope":"repo","v":{"env":"prod"}}`},
			theirs: []string{mkDeploy, `{"name":"deploy","scope":"repo","v":{"env":"stag","region":"eu-central"}}`},
			key:    mkDeploy, want: `{"name":"deploy","scope":"repo","v":{"env":"prod","region":"eu-central"}}`},
		{name: "list emptied removes the key",
			base:   []string{mkPages, `{"name":"pages","scope":"repo","v":["a","b"]}`},
			ours:   []string{mkPages, `{"name":"pages","scope":"repo","v":["b"]}`},
			theirs: []string{mkPages, `{"name":"pages","scope":"repo","v":["a"]}`},
			key:    mkPages, want: ``},
		{name: "removed vs changed: change wins",
			base:   []string{mkClient, `{"name":"client","scope":"repo","v":"acme"}`},
			ours:   []string{},
			theirs: []string{mkClient, `{"name":"client","scope":"repo","v":"acme_gmbh"}`},
			key:    mkClient, want: `{"name":"client","scope":"repo","v":"acme_gmbh"}`},
		{name: "removed vs unchanged: removed",
			base:   []string{mkClient, `{"name":"client","scope":"repo","v":"acme"}`},
			ours:   []string{},
			theirs: []string{mkClient, `{"name":"client","scope":"repo","v":"acme"}`},
			key:    mkClient, want: ``},
		{name: "removed vs changed strict: conflict",
			base:   []string{mkCost, `{"name":"cost","scope":"repo","strict":true,"v":1}`},
			ours:   []string{},
			theirs: []string{mkCost, `{"name":"cost","scope":"repo","strict":true,"v":2}`},
			key:    mkCost, want: `{"conflict":{"base":1,"ours":null,"theirs":2},"name":"cost","scope":"repo","strict":true}`, conflict: true},
		{name: "both changed: newer wins", wins: true,
			base:   []string{mkClient, `{"name":"client","scope":"repo","v":"a"}`},
			ours:   []string{mkClient, `{"name":"client","scope":"repo","v":"b"}`},
			theirs: []string{mkClient, `{"name":"client","scope":"repo","v":"c"}`},
			key:    mkClient, want: `{"name":"client","scope":"repo","v":"c"}`},
		{name: "sensitive label: either side",
			base:   []string{mkClient, `{"name":"client","scope":"repo","v":"a"}`},
			ours:   []string{mkClient, `{"name":"client","scope":"repo","sensitive":true,"v":"a"}`},
			theirs: []string{mkClient, `{"name":"client","scope":"repo","v":"b"}`},
			key:    mkClient, want: `{"name":"client","scope":"repo","sensitive":true,"v":"b"}`},
		{name: "strict list never conflicts",
			base:   []string{mkPages, `{"name":"pages","scope":"repo","strict":true,"v":["a","b"]}`},
			ours:   []string{mkPages, `{"name":"pages","scope":"repo","strict":true,"v":["a"]}`},
			theirs: []string{mkPages, `{"name":"pages","scope":"repo","strict":true,"v":["a","b","c"]}`},
			key:    mkPages, want: `{"name":"pages","scope":"repo","strict":true,"v":["a","c"]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := withMeta(live(stamp(0, actorA)), metaOf(t, c.base...))
			o := withMeta(live(stamp(1, actorA)), metaOf(t, c.ours...))
			th := withMeta(live(stamp(2, actorB)), metaOf(t, c.theirs...))
			res, err := Rows(PolicyFor(testDef), b, o, th)
			if err != nil {
				t.Fatal(err)
			}
			if got := entryOf(t, res.Row, c.key); got != c.want {
				t.Errorf("entry = %s\nwant    %s", got, c.want)
			}
			if slices.Contains(res.NewerWins, "meta."+c.key) != c.wins {
				t.Errorf("newer wins = %v", res.NewerWins)
			}
			if res.Clean() == c.conflict {
				t.Errorf("conflicts = %+v", res.Conflicts)
			}
		})
	}
}
