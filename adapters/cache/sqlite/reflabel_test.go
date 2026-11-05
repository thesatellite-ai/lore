package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestRefLabelIndexed (OV34-REFLABEL, FA-META-6, later 27): a resource
// whose ref field names another resource is also found by the target's
// name: a scalar ref and a list of refs, a target applied after its
// referrer in one batch, a rename re-indexing every referrer (the old name
// no longer finds it), the referrer's own source's copy of the target
// preferred over another source's, and a dangling ref adding nothing.
func TestRefLabelIndexed(t *testing.T) {
	f := newFixture(t)
	c := f.open("c.db")
	kind, upF := must(ids.New[ids.Kind]()), must(ids.New[ids.KindField]())
	web, multi, target, second, dangling := must(ids.New[ids.Resource]()), must(ids.New[ids.Resource]()), must(ids.New[ids.Resource]()), must(ids.New[ids.Resource]()), must(ids.New[ids.Resource]())
	res := func(id ids.ID[ids.Resource], name, up string) store.Row {
		return f.resource(id, kind, name, map[ids.ID[ids.KindField]][2]string{upF: {"upstream", up}})
	}
	q := func(id ids.ID[ids.Resource]) string { return `"` + id.String() + `"` }
	f.apply(c, store.SourceRepo, "1",
		f.kindRow(kind, "server"),
		f.fieldRow(upF, kind, "upstream", "ref", kind),
		res(web, "web_one", q(target)), // referrer first, target later in the batch
		res(multi, "web_two", `[`+q(target)+`,`+q(second)+`]`),
		res(target, "hubdbprod", q(dangling)),
		res(second, "cachebox", `""`),
	)
	hits := func(match string) map[string]bool {
		t.Helper()
		out := map[string]bool{}
		for _, h := range must(c.Search(context.Background(), schema.TableResources, match, 20)) {
			out[h.String()] = true
		}
		return out
	}
	expect := func(step, match string, want ...ids.ID[ids.Resource]) {
		t.Helper()
		got := hits(match)
		if len(got) != len(want) {
			t.Errorf("%s: search %s = %v, want %d hits", step, match, got, len(want))
			return
		}
		for _, w := range want {
			if !got[w.String()] {
				t.Errorf("%s: search %s misses %s", step, match, w)
			}
		}
	}
	expect("target after referrer", "hubdbprod", target, web, multi)
	expect("list ref", "cachebox", second, multi)

	f.apply(c, store.SourceRepo, "2", res(target, "coredb", q(dangling)))
	expect("after rename, new", "coredb", target, web, multi)
	expect("after rename, old", "hubdbprod")

	// another source holds the same target id under another name: the
	// repo referrer keeps the repo copy's name
	f.apply(c, store.Source("agency"), "1", f.kindRow(kind, "server"), f.fieldRow(upF, kind, "upstream", "ref", kind), res(target, "agencyname", `""`))
	f.apply(c, store.SourceRepo, "3", res(web, "web_one", q(target)))
	got := hits("agencyname")
	if got[web.String()] {
		t.Errorf("the repo referrer took another source's label: %v", got)
	}
	if !hits("coredb")[web.String()] {
		t.Error("the repo referrer lost its own source's label")
	}
}

// TestRefIDsMatrix: the res_ ids among a value's text: a bare id, words
// around it, a canonical list (no spaces), a spaced list, duplicates kept
// in order (refLabels dedupes), non-resource ids and broken ids left out.
func TestRefIDsMatrix(t *testing.T) {
	a, b := must(ids.New[ids.Resource]()).String(), must(ids.New[ids.Resource]()).String()
	mem := newMem().String()
	cases := map[string][]string{
		a:                                 {a},
		"see " + a + " now":               {a},
		`["` + a + `","` + b + `"]`:       {a, b},
		`[ "` + a + `", "` + b + `" ]`:    {a, b},
		`["` + a + `","` + a + `"]`:       {a, a},
		mem + " " + a:                     {a},
		"res_nothex res_ " + a[:len(a)-1]: nil,
		"":                                nil,
	}
	for in, want := range cases {
		got := refIDs(in)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("refIDs(%q) = %v, want %v", in, got, want)
		}
	}
}
