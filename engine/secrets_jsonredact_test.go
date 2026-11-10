package engine

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestRedactJSONLeaves (BUGFIX-12, SECRETS 19.1): a --secret-ref or
// --secret-unstored answer replaces a match inside one json string value;
// a match in an object key, across a quote, touching an escape sequence,
// or a replacement that would need escaping is refused with
// E_SECRET_DETECTED and the hint names only the answers that work.
func TestRedactJSONLeaves(t *testing.T) {
	t.Parallel()
	secret := "AKIA" + strings.Repeat("Q", 16) // built at run time
	const ref = "${secret:gopass://acme/hub/aws-key}"
	at := func(s, sub string, extra int) scan.Finding {
		i := strings.Index(s, sub)
		if i < 0 {
			t.Fatalf("%q not in %q", sub, s)
		}
		return scan.Finding{RuleID: "aws_access_key", Offset: i, Length: len(sub) + extra}
	}
	ok := []struct{ name, raw, want string }{
		{"value", `{"fld_1":{"name":"access_key","v":"` + secret + `"}}`, `{"fld_1":{"name":"access_key","v":"` + ref + `"}}`},
		{"inside text", `{"a":"key ` + secret + ` end"}`, `{"a":"key ` + ref + ` end"}`},
		{"array item", `{"a":["x","` + secret + `"]}`, `{"a":["x","` + ref + `"]}`},
	}
	for _, c := range ok {
		v, err := value.JSONOf([]byte(c.raw))
		if err != nil {
			t.Fatal(err)
		}
		got, err := redactValueWith(v, -1, []scan.Finding{at(v.String(), secret, 0)}, []string{ref})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		want, err := value.JSONOf([]byte(c.want))
		if err != nil {
			t.Fatal(err)
		}
		if got.String() != want.String() {
			t.Errorf("%s: %s, want %s", c.name, got, want)
		}
	}
	bad := []struct {
		name, raw, sub string
		extra          int
		repl           string
	}{
		{"object key", `{"` + secret + `":"x"}`, secret, 0, ref},
		{"across the closing quote", `{"a":"` + secret + `"}`, secret, 1, ref},
		{"across two values", `{"a":"` + secret + `","b":"y"}`, secret, 6, ref},
		{"touches an escape", `{"a":"\n` + secret + `"}`, `\n` + secret, 0, ref},
		{"replacement needs escaping", `{"a":"` + secret + `"}`, secret, 0, `${secret:gopass://a"b}`},
	}
	for _, c := range bad {
		v, err := value.JSONOf([]byte(c.raw))
		if err != nil {
			t.Fatal(err)
		}
		_, err = redactValueWith(v, -1, []scan.Finding{at(v.String(), c.sub, c.extra)}, []string{c.repl})
		e := errs.EnvelopeOf(err).Error
		if !errs.Has(err, errs.SecretDetected) || strings.Contains(e.Hint, "--secret-ref") || !strings.Contains(e.Hint, "--not-a-secret") {
			t.Errorf("%s: %v hint %q", c.name, err, e.Hint)
		}
	}
}
