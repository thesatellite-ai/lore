package settings

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/errs"
)

func TestSecretRefUnknownManager(t *testing.T) {
	if _, err := secretRefValue("host h1: field ssh", "nope://acme/x", ""); !errs.Has(err, errs.RefScheme) {
		t.Fatalf("unknown manager: %v", err)
	}
	if _, err := secretRefValue("host h1: field ssh", "acme/hub/ssh", ""); err != nil {
		t.Fatalf("bare path with the default manager: %v", err)
	}
}

func TestRewriteTextKeepsTheRest(t *testing.T) {
	in := "a $${secret:x} b ${var:h} ${secret:gopass://a/b%20c?holder=mch_1&label=m.x|env://K} ${secret:unstored}"
	out, n := rewriteText(in, func(t refs.SecretTarget) (refs.SecretTarget, bool) {
		if t.Manager != "gopass" {
			return t, false
		}
		return setOption(t, refs.OptLabel, "mira.box"), true
	})
	want := "a $${secret:x} b ${var:h} ${secret:gopass://a/b%20c?holder=mch_1&label=mira.box|env://K} ${secret:unstored}"
	if n != 1 || out != want {
		t.Fatalf("got %d %q\nwant %q", n, out, want)
	}
	if _, n = rewriteText(in, func(t refs.SecretTarget) (refs.SecretTarget, bool) { return t, false }); n != 0 {
		t.Fatal("no-op rewrite changed text")
	}
}
