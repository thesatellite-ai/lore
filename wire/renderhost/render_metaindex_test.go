package renderhost

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/store"
)

// TestMetaContextIndexLines (later 10, A-CTX-12, CONTEXT_BUDGET 6.13):
// `meta edit --context index` puts one render index line per key with
// live rows ("- metadata <key>: N (table n) — lookup"), counted over live
// rows only (a trashed or archived row is not counted); an internal key, a
// key with no rows and a key without the mode print nothing; --context
// none clears it; any other mode is E_INVALID_INPUT.
func TestMetaContextIndexLines(t *testing.T) {
	ctx := context.Background()
	h := opstest.New(t)
	for _, in := range []string{
		`{"name":"client","type":"text"}`,
		`{"name":"tier","type":"text"}`,
		`{"name":"rate","type":"text","internal":true}`,
		`{"name":"area","type":"text"}`,
	} {
		h.MustYes(opssettings.OpMetaAdd, in)
	}
	for _, k := range []string{"client", "tier", "rate"} {
		h.MustYes(opssettings.OpMetaEdit, `{"id":"`+k+`","context":"index"}`)
	}
	for _, bad := range []string{"full", "INDEX", "line"} {
		if _, err := h.RunYes(opssettings.OpMetaEdit, `{"id":"area","context":"`+bad+`"}`); !errs.Has(err, errs.InvalidInput) {
			t.Fatalf("--context %s: %v, want E_INVALID_INPUT", bad, err)
		}
	}
	h.MustYes(opsmemory.OpAdd, `{"body":"one","meta":["client=acme","rate=90","area=billing"]}`)
	h.MustYes(opsmemory.OpAdd, `{"body":"two","meta":["client=beta"]}`)
	h.MustYes("decision.add", `{"title":"d","body":"b","meta":["client=acme"]}`)
	gone := h.ID(h.MustYes(opsmemory.OpAdd, `{"body":"gone","meta":["client=acme"]}`))
	h.MustYes(opsmemory.OpDelete, `{"id":"`+gone+`"}`)
	arch := h.ID(h.MustYes(opsmemory.OpAdd, `{"body":"arch","meta":["client=acme"]}`))
	h.Must(opsmemory.OpArchive, `{"id":"`+arch+`"}`)

	host := New(Options{App: h.App})
	lines := func() string {
		t.Helper()
		got, err := host.metaIndexLines(ctx, []store.Source{store.SourceRepo})
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(got, "")
	}
	want := "- metadata client: 3 (decisions 1, memories 2) — `lore <noun> list --meta client`\n"
	if got := lines(); got != want {
		t.Fatalf("index lines\n%q\nwant\n%q", got, want)
	}
	h.MustYes(opssettings.OpMetaEdit, `{"id":"client","context":"none"}`)
	if got := lines(); got != "" {
		t.Fatalf("after --context none: %q", got)
	}
}
