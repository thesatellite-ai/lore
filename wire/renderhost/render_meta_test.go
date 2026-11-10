package renderhost

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestRenderMetaLine (METADATA 3.14, MQ13, A-CTX-13) is the "meta: k=v"
// matrix of a rendered row over key kind x render: the committed render
// (LORE.md, may reach a client) shows only keys that are neither internal
// nor sensitive, and leaves out a key whose row is not found (fail
// closed); the private render adds internal values; a sensitive key or an
// entry marked sensitive never shows anywhere. Names sort; a value spanning
// lines is printed on one.
func TestRenderMetaLine(t *testing.T) {
	ctx := context.Background()
	h := opstest.New(t)
	key := func(name string, internal, sensitive bool) ids.ID[ids.MetaKey] {
		t.Helper()
		fields := map[string]value.Value{
			"name": text(t, name), "scope": text(t, "store"), "type": text(t, "text"),
			metaKeyInternalField: value.BoolOf(internal), metaKeySensitiveField: value.BoolOf(sensitive),
		}
		w, err := h.App.Create(ctx, store.SourceRepo, schema.TableMetaKeys, fields)
		if err != nil {
			t.Fatal(err)
		}
		id, err := ids.Parse[ids.MetaKey](w.Row.ID.String())
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	entry := func(raw string) value.JSON {
		t.Helper()
		j, err := value.JSONOf([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	plain := key("client", false, false)
	plain2 := key("area", false, false)
	internal := key("rate", true, false)
	sensitive := key("contact", false, true)
	unknown, err := ids.New[ids.MetaKey]()
	if err != nil {
		t.Fatal(err)
	}
	e := map[ids.ID[ids.MetaKey]]value.JSON{
		plain:     entry(`{"name":"client","scope":"store","v":"acme"}`),
		plain2:    entry(`{"name":"area","scope":"store","v":"billing\nand refunds"}`),
		internal:  entry(`{"name":"rate","scope":"store","v":"90"}`),
		sensitive: entry(`{"name":"contact","scope":"store","v":"jo@example.com"}`),
		unknown:   entry(`{"name":"ghost","scope":"store","v":"boo"}`),
	}
	cases := []struct {
		name      string
		keys      []ids.ID[ids.MetaKey]
		committed string
		private   string
	}{
		{"none", nil, "", ""},
		{"plain only", []ids.ID[ids.MetaKey]{plain}, "meta: client=acme", "meta: client=acme"},
		{"plain sorted, multi-line value on one line", []ids.ID[ids.MetaKey]{plain, plain2}, "meta: area=billing and refunds, client=acme", "meta: area=billing and refunds, client=acme"},
		{"internal", []ids.ID[ids.MetaKey]{internal}, "", "meta: rate=90"},
		{"sensitive", []ids.ID[ids.MetaKey]{sensitive}, "", ""},
		{"unknown key", []ids.ID[ids.MetaKey]{unknown}, "", "meta: ghost=boo"},
		{"all kinds", []ids.ID[ids.MetaKey]{plain, internal, sensitive, unknown}, "meta: client=acme", "meta: client=acme, ghost=boo, rate=90"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := row.Row{Meta: map[ids.ID[ids.MetaKey]]value.JSON{}}
			for _, k := range c.keys {
				r.Meta[k] = e[k]
			}
			mk := newRenderMetaKeys(h.App)
			if got := mk.line(ctx, r, false); got != c.committed {
				t.Fatalf("committed %q, want %q", got, c.committed)
			}
			if got := mk.line(ctx, r, true); got != c.private {
				t.Fatalf("private %q, want %q", got, c.private)
			}
		})
	}
	// An entry marked sensitive on the row hides even under a plain key.
	r := row.Row{Meta: map[ids.ID[ids.MetaKey]]value.JSON{plain: entry(`{"name":"client","scope":"store","sensitive":true,"v":"acme"}`)}}
	mk := newRenderMetaKeys(h.App)
	if got := mk.line(ctx, r, false) + mk.line(ctx, r, true); got != "" {
		t.Fatalf("sensitive entry shown: %q", got)
	}
	// withMetaLine: own line after the body; body alone when nothing shows.
	r = row.Row{Meta: map[ids.ID[ids.MetaKey]]value.JSON{plain: e[plain], internal: e[internal]}}
	if got := mk.withMetaLine(ctx, "use tabs", r, false); got != "use tabs\nmeta: client=acme" {
		t.Fatalf("withMetaLine %q", got)
	}
	if got := mk.withMetaLine(ctx, "", r, false); got != "meta: client=acme" {
		t.Fatalf("withMetaLine empty body %q", got)
	}
	r = row.Row{Meta: map[ids.ID[ids.MetaKey]]value.JSON{internal: e[internal]}}
	if got := mk.withMetaLine(ctx, "use tabs", r, false); got != "use tabs" {
		t.Fatalf("withMetaLine internal-only %q", got)
	}
}

// text is a text value (test helper).
func text(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
