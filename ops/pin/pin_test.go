package pin_test

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/content/contenttest"
)

// TestPinRefusals (FA-CTX-6; fa-ctx later:46): only must rules and
// critical/high hotfixes take a pin; --context is full or line; a non
// rule/hotfix id or an unknown one is E_NOT_FOUND; a pin sets both fields
// and unpin clears them (also on a row that left the core since).
func TestPinRefusals(t *testing.T) {
	h := opstest.New(t)
	must := h.ID(h.Must("rule.add", `{"body":"b","severity":"must"}`))
	should := h.ID(h.Must("rule.add", `{"body":"b","severity":"should"}`))
	high := h.ID(h.Must("hotfix.add", `{"title":"t","body":"b","severity":"high"}`))
	medium := h.ID(h.Must("hotfix.add", `{"title":"t","body":"b","severity":"medium"}`))
	mem := h.ID(h.Must("memory.add", `{"body":"b"}`))

	for _, c := range []struct {
		in   string
		code errs.Code
	}{
		{`{"id":"` + should + `","context":"full"}`, errs.InvalidInput},
		{`{"id":"` + medium + `","context":"line"}`, errs.InvalidInput},
		{`{"id":"` + must + `","context":"half"}`, errs.InvalidInput},
		{`{"id":"` + must + `","context":""}`, errs.InvalidInput},
		{`{"id":"` + mem + `","context":"full"}`, errs.NotFound},
		{`{"id":"rul_019a0000000070008000000000000099","context":"full"}`, errs.NotFound},
	} {
		h.Fails("pin.set", c.in, c.code)
	}
	show := func(id string) (string, string) {
		doc := h.JSON(h.Must("row.show", `{"id":"`+id+`"}`))
		return contenttest.Str(t, doc, "fields", "context_pin"), contenttest.Str(t, doc, "fields", "context_pinned_at")
	}
	h.Must("pin.set", `{"id":"`+must+`","context":"full"}`)
	h.Must("pin.set", `{"id":"`+high+`","context":"line"}`)
	if p, at := show(must); p != "full" || at == "" {
		t.Fatalf("rule pin %q at %q", p, at)
	}
	if p, _ := show(high); p != "line" {
		t.Fatalf("hotfix pin %q", p)
	}
	h.Must("rule.edit", `{"id":"`+must+`","severity":"should"}`)
	h.Must("pin.unset", `{"id":"`+must+`"}`)
	if p, at := show(must); p != "" || at != "" {
		t.Fatalf("after unpin %q %q", p, at)
	}
}
