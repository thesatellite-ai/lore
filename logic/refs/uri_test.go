package refs

import (
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

func TestReadURI(t *testing.T) {
	r := References("${res:hub_prod/ssh?kind=host} ${var:x:-fb ${var:y}} ${var:r:?set r} ${res:2001:db8::10/open_ports} " +
		"${secret:gopass://acme/hub/db?holder=mch_1&label=mira.mac | env://K|unstored} ${var:a%20b}")
	if len(r) != 6 {
		t.Fatalf("refs = %d", len(r))
	}
	if v, ok := r[0].Option(OptKind); !ok || v != "host" || r[0].Target != "hub_prod/ssh" {
		t.Errorf("res = %+v", r[0])
	}
	if r[1].Target != "x" || r[1].Modifier != ModFallback || r[1].Fallback != "fb ${var:y}" {
		t.Errorf("fallback = %+v", r[1])
	}
	if r[2].Modifier != ModRequired || r[2].Fallback != "set r" {
		t.Errorf("required = %+v", r[2])
	}
	if r[3].Target != "2001:db8::10/open_ports" || r[3].Problem != "" {
		t.Errorf("ipv6 = %+v", r[3])
	}
	a := r[4].Alternates
	if len(a) != 3 || a[0].Manager != "gopass" || a[0].Locator != "acme/hub/db" || a[1].Manager != "env" || a[2].Fixed != TargetUnstored {
		t.Errorf("secret = %+v", r[4])
	}
	if r[5].Target != "a b" {
		t.Errorf("percent = %+v", r[5])
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		in   string
		want errs.Code // "" = valid
	}{
		{"plain ${var:x} ${HOME} ${foo:-bar}", ""},
		{"${var:x?kind=a}", errs.RefSyntax},
		{"${res:${var:h}/region}", errs.RefSyntax},
		{"${secret:nope://x}", errs.RefScheme},
		{"${secret:env://A|env://B|env://C|env://D}", errs.RefSyntax},
		{"${secret:gopass://a?bogus=1}", errs.RefSyntax},
		{"${secret:aws-secrets://a?region=eu-central-1#k}", ""},
		{"${var:x:-${var:y?z=1}}", errs.RefSyntax},
		{"${var:}", errs.RefSyntax},
	}
	for _, tc := range cases {
		err := Validate(tc.in)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("Validate(%q) = %v", tc.in, err)
		case tc.want != "" && !errs.Has(err, tc.want):
			t.Errorf("Validate(%q) = %v, want %s", tc.in, err, tc.want)
		}
	}
}

// chainResolver resolves var names from a map, id = a stable fake per name.
type chainResolver map[string]string

func (c chainResolver) Resolve(r Ref) (Resolved, error) {
	v, ok := c[r.Target]
	if !ok {
		return Resolved{}, ErrUnresolved
	}
	return Resolved{Text: v}, nil
}

func TestExpandChainModifiersAndCaps(t *testing.T) {
	res := chainResolver{"api_host": "api.example", "api_base": "https://${var:api_host}/v2", "a": "${var:b}", "b": "${var:a}"}
	got, err := Expander{Resolver: res}.ExpandFull("u=${var:api_base} f=${var:none:-${var:api_host}} d=${var:none}")
	if err != nil || got.Text != "u=https://api.example/v2 f=api.example d=${var:none}" || len(got.Dangling) != 1 {
		t.Fatalf("chain: %+v %v", got, err)
	}
	// cycle: strict errors with the chain, lenient keeps the reference
	if _, err := (Expander{Resolver: res, Strict: true}).ExpandFull("${var:a}"); !errs.Has(err, errs.RefCycle) || !strings.Contains(err.Error(), "var:a -> var:b -> var:a") {
		t.Fatalf("cycle strict: %v", err)
	}
	if got, err := (Expander{Resolver: res}).ExpandFull("x ${var:a} ${var:api_host}"); err != nil || got.Text != "x ${var:a} api.example" {
		t.Fatalf("cycle lenient: %+v %v", got, err)
	}
	if _, err := (Expander{Resolver: res, Strict: true}).ExpandFull("${var:none:?set none}"); !errs.Has(err, errs.RefRequired) {
		t.Fatalf("required: %v", err)
	}
	// depth: v0 -> v1 -> ... -> v9
	deep := chainResolver{}
	for i := range 10 {
		deep["v"+strconv.Itoa(i)] = "${var:v" + strconv.Itoa(i+1) + "}"
	}
	if _, err := (Expander{Resolver: deep, Strict: true}).ExpandFull("${var:v0}"); !errs.Has(err, errs.RefDepth) {
		t.Fatalf("depth: %v", err)
	}
	// blow-up: each level triples, 3^5 leaves + inner nodes > 256 resolutions
	boom := chainResolver{"l5": strings.Repeat("x", 300)}
	for i := 4; i >= 0; i-- {
		n := "${var:l" + strconv.Itoa(i+1) + "}"
		boom["l"+strconv.Itoa(i)] = n + n + n
	}
	if _, err := (Expander{Resolver: boom, Strict: true}).ExpandFull("${var:l0}"); !errs.Has(err, errs.RefLimit) {
		t.Fatalf("limit: %v", err)
	}
	if got, err := (Expander{Resolver: boom}).ExpandFull("${var:l0}"); err != nil || got.Text != "${var:l0}" {
		t.Fatalf("limit lenient: %+v %v", got.Text[:min(40, len(got.Text))], err)
	}
}
