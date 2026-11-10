package refs

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

const (
	varID = "var_01936f3300007d4e8f5a6b7c8d9e0f1a"
	resID = "res_01936f3300007d4e8f5a6b7c8d9e0f1b"
)

// tableResolver is a Resolver over a fixed map; it records every call so a
// test can prove secrets and env never reach it.
type tableResolver struct {
	vals  map[string]Resolved
	calls []Ref
	fail  error
}

func (r *tableResolver) Resolve(ref Ref) (Resolved, error) {
	r.calls = append(r.calls, ref)
	if r.fail != nil {
		return Resolved{}, r.fail
	}
	v, ok := r.vals[ref.String()]
	if !ok {
		return Resolved{}, ErrUnresolved
	}
	return v, nil
}

func anyID(s string) ids.AnyID {
	id, err := ids.ParseAny(s)
	if err != nil {
		panic(err)
	}
	return id
}

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want string // kind:raw parts joined by |
	}{
		{"plain", "text:plain"},
		{"host ${var:db_host} port", "text:host |ref:${var:db_host}|text: port"},
		{"${secret:gopass://acme/hub/db?holder=mch_1&label=x}", "ref:${secret:gopass://acme/hub/db?holder=mch_1&label=x}"},
		{"cost $${not_a_ref} here", "text:cost |escaped:$${not_a_ref}|text: here"},
		{"open ${var:x and never closed", "text:open ${var:x and never closed"},
		{"${no scheme}", "text:${no scheme}"},
		{"${:empty}", "text:${:empty}"},
		{"${jira://HUB-412?field=status}", "ref:${jira://HUB-412?field=status}"},
		{"$${unclosed", "text:$${unclosed"},
		{"${row:" + varID + "}${res:hub_prod.region}", "ref:${row:" + varID + "}|ref:${res:hub_prod.region}"},
	}
	for _, tc := range cases {
		var got []string
		for _, p := range Parse(tc.in) {
			got = append(got, string(p.Kind)+":"+p.Raw)
		}
		if g := strings.Join(got, "|"); g != tc.want {
			t.Errorf("Parse(%q)\n got %s\nwant %s", tc.in, g, tc.want)
		}
	}
	refs := References("a ${var:x} b ${secret:env://K} ${jira://Y}")
	if len(refs) != 3 || refs[0].Scheme != SchemeVar || refs[0].Body != "x" || refs[1].Scheme != SchemeSecret || refs[2].Scheme.Known() {
		t.Fatalf("References = %+v", refs)
	}
}

func TestExpand(t *testing.T) {
	r := &tableResolver{vals: map[string]Resolved{
		"${var:db_host}":         {Text: "db.hub.acme.example", ID: anyID(varID)},
		"${res:hub_prod.region}": {Text: "eu-west-1", ID: anyID(resID)},
		"${row:" + resID + "}":   {Text: "hub_prod", ID: anyID(resID)},
		"${secret:env://K}":      {Text: "LEAKED"},
		"${env:HOME}":            {Text: "/home/leak"},
	}}
	in := "h=${var:db_host} r=${res:hub_prod.region} again=${var:db_host} row=${row:" + resID + "} " +
		"s=${secret:env://K} e=${env:HOME} esc=$${var:db_host} gone=${var:missing} plug=${jira://X}"
	got, gotIDs, err := Expander{Resolver: r}.Expand(in)
	if err != nil {
		t.Fatal(err)
	}
	want := "h=db.hub.acme.example r=eu-west-1 again=db.hub.acme.example row=hub_prod " +
		"s=${secret:env://K} e=${env:HOME} esc=${var:db_host} gone=${var:missing} plug=${jira://X}"
	if got != want {
		t.Fatalf("Expand\n got %s\nwant %s", got, want)
	}
	if strings.Join(gotIDs, ",") != varID+","+resID && strings.Join(gotIDs, ",") != resID+","+varID {
		t.Fatalf("ids = %v", gotIDs)
	}
	if gotIDs[0] > gotIDs[1] {
		t.Fatalf("ids not sorted: %v", gotIDs)
	}
	for _, c := range r.calls {
		if c.Scheme == SchemeSecret || c.Scheme == SchemeEnv || !c.Scheme.Known() {
			t.Errorf("resolver was asked for %s", c)
		}
	}
}

func TestExpandStopsOnResolverFailure(t *testing.T) {
	boom := errs.New(errs.Internal, "resolver broke")
	_, _, err := Expander{Resolver: &tableResolver{fail: boom}}.Expand("${var:x}")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the resolver's error", err)
	}
	got, ids, err := Expander{}.Expand("${var:x}")
	if err != nil || got != "${var:x}" || len(ids) != 0 {
		t.Fatalf("no resolver: %q %v %v", got, ids, err)
	}
}

func TestUnstoredIsASecretRef(t *testing.T) {
	r := References(Unstored)
	if len(r) != 1 || r[0].Scheme != SchemeSecret || r[0].Scheme.Expandable() {
		t.Fatalf("Unstored = %q parses as %+v", Unstored, r)
	}
}

// FuzzParse: never panics, and the parts' raw text is the input.
func FuzzParse(f *testing.F) {
	for _, s := range []string{"", "${var:x}", "$${x}", "${", "$${", "}${a:b}}", "${a:${b:c}}", "$$${var:x}"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		var b strings.Builder
		for _, p := range Parse(s) {
			b.WriteString(p.Raw)
			if p.Kind == PartRef && p.Ref.String() != p.Raw {
				t.Fatalf("ref %q renders as %q", p.Raw, p.Ref.String())
			}
		}
		if b.String() != s {
			t.Fatalf("round trip lost text: %q -> %q", s, b.String())
		}
		got, _, err := Expander{}.Expand(s)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(s, escapeLit) && got != s {
			t.Fatalf("expansion with no resolver and no escape changed text: %q -> %q", s, got)
		}
	})
}
