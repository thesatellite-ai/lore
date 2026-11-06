package techdoc_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
)

// TestTechdoc walks add / list / show / search / delete against
// v0.1.17's real output (2026-11-04): "✓ <id> <name>" on add, list
// lines "<id> <name>[ <base url>]", show lines name / base_url / desc
// (each only when set), record keys with the empty `edges` object.
func TestTechdoc(t *testing.T) {
	h := opstest.New(t)
	if got := opstest.Text(h.Must("techdoc.list", "")); got != "(no techdocs)\n" {
		t.Errorf("empty list %q", got)
	}
	if got := h.JSON(h.Must("techdoc.list", "")); got != `{"schema_version":1,"kind":"techdoc.list","data":[]}` {
		t.Errorf("empty list json %s", got)
	}
	cases := []struct{ name, url, desc string }{
		{"Go spec", "https://go.dev/ref/spec", "the language spec"},
		{"Bare", "", ""},
		{"日本語 docs 🚀", "https://例え.jp/path?q=1&x=ü", ""},
		{"Only desc", "", "described but no url"},
		{strings.Repeat("n", 300), "", strings.Repeat("d", 5000)},
	}
	ids := make([]string, len(cases))
	var listWant strings.Builder
	for i, c := range cases {
		in := `{"name":` + opstest.Quote(c.name)
		if c.url != "" {
			in += `,"base_url":` + opstest.Quote(c.url)
		}
		if c.desc != "" {
			in += `,"description":` + opstest.Quote(c.desc)
		}
		res := h.Must("techdoc.add", in+"}")
		ids[i] = h.ID(res)
		if !strings.HasPrefix(ids[i], "tdc_") {
			t.Fatalf("id %q", ids[i])
		}
		if got, want := opstest.Text(res), "✓ "+ids[i]+" "+c.name+"\n"; got != want {
			t.Errorf("add line %q, want %q", got, want)
		}
		show := h.Must("techdoc.show", `{"id":"`+ids[i]+`"}`)
		want := ids[i] + "\n  name:     " + c.name + "\n"
		if c.url != "" {
			want += "  base_url: " + c.url + "\n"
		}
		if c.desc != "" {
			want += "  desc:     " + c.desc + "\n"
		}
		if got := opstest.Text(show); got != want {
			t.Errorf("show text %q, want %q", got, want)
		}
		keys := []string{"id", "created_at", "updated_at", "project_id", "name"}
		if c.url != "" {
			keys = append(keys, "base_url")
		}
		if c.desc != "" {
			keys = append(keys, "description")
		}
		keys = append(keys, "edges")
		if got := contenttest.Keys(t, h.JSON(show), "data"); strings.Join(got, " ") != strings.Join(keys, " ") {
			t.Errorf("record keys %v, want v0.1.17's %v", got, keys)
		}
		if !strings.Contains(h.JSON(show), `"edges":{}`) {
			t.Errorf("no empty edges object: %s", h.JSON(show))
		}
		listWant.WriteString(ids[i] + " " + c.name)
		if c.url != "" {
			listWant.WriteString(" " + c.url)
		}
		listWant.WriteString("\n")
	}
	if got := opstest.Text(h.Must("techdoc.list", "")); got != listWant.String() {
		t.Errorf("list text\n%q\nwant\n%q", got, listWant.String())
	}
	for _, in := range []string{`{"base_url":"https://x"}`, `{"name":"   "}`, `{"name":"x","bogus":1}`} {
		h.Fails("techdoc.add", in, errs.InvalidInput)
	}
	// search over name, description and base_url
	doc := h.JSON(h.Must("techdoc.search", `{"query":"spec"}`))
	if !strings.Contains(doc, ids[0]) || strings.Count(doc, `"id":`) != 1 {
		t.Errorf("search spec: %s", doc)
	}
	if k := contenttest.Keys(t, doc, "data", "0", "row"); strings.Join(k, " ") != "base_url description name" {
		t.Errorf("hit row keys %v", k)
	}
	if got := opstest.Text(h.Must("techdoc.search", `{"query":"spec"}`)); got != ids[0]+" Go spec\n" {
		t.Errorf("search line %q", got)
	}
	for col, want := range map[string]string{"name": ids[1], "description": ids[3], "base_url": ids[0]} {
		q := map[string]string{"name": "Bare", "description": "described", "base_url": "go"}[col]
		d := h.JSON(h.Must("techdoc.search", `{"query":"`+q+`","columns":["`+col+`"]}`))
		if !strings.Contains(d, want) {
			t.Errorf("--column %s %q: %s", col, q, d)
		}
	}
	h.Fails("techdoc.search", `{"query":"x","columns":["body"]}`, errs.InvalidInput)
	// delete
	h.Fails("techdoc.delete", `{"id":"`+ids[0]+`"}`, errs.NeedsConfirm)
	if got, want := opstest.Text(h.MustYes("techdoc.delete", `{"id":"`+ids[0]+`"}`)), contenttest.DeletedText(ids[0]); got != want {
		t.Errorf("delete line %q", got)
	}
	msg := h.Fails("techdoc.show", `{"id":"`+ids[0]+`"}`, errs.InTrash) // DELETION 3.2
	if !strings.Contains(msg, ids[0]+` is in the trash`) {
		t.Errorf("in-trash message %q", msg)
	}
	if strings.Contains(h.JSON(h.Must("techdoc.search", `{"query":"spec"}`)), ids[0]) {
		t.Error("search finds a deleted techdoc")
	}
	h.Fails("techdoc.show", `{"id":"pat_01a1a5aca7f57659be7320c5f4b7ffff"}`, errs.InvalidID)
}
