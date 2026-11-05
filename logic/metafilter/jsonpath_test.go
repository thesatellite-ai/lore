package metafilter

import (
	"errors"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
)

const (
	idSpec = "mky_01936f0000007a1b8c2d3e4f5a6ba111"
	idIP   = "mky_01936f0000007a1b8c2d3e4f5a6ba112"
	idNet  = "mky_01936f0000007a1b8c2d3e4f5a6ba113"
	idIPs  = "mky_01936f0000007a1b8c2d3e4f5a6ba114"
	idName = "mky_01936f0000007a1b8c2d3e4f5a6ba115"
)

func isMetaOp(err error) bool {
	var e *errs.Error
	return errors.As(err, &e) && e.Code == errs.MetaOp
}

// TestParsePath pins the 3.4 path grammar: $.field, $[n], $[*], $."odd",
// and every malformed shape is E_META_OP.
func TestParsePath(t *testing.T) {
	good := []struct {
		p    string
		want []pathStep
	}{
		{"$", nil},
		{"$.a", []pathStep{{field: "a", index: -1}}},
		{"$.a.b", []pathStep{{field: "a", index: -1}, {field: "b", index: -1}}},
		{"$[0]", []pathStep{{index: 0}}},
		{"$[12]", []pathStep{{index: 12}}},
		{"$[*]", []pathStep{{all: true, index: -1}}},
		{"$.items[*].sku", []pathStep{{field: "items", index: -1}, {all: true, index: -1}, {field: "sku", index: -1}}},
		{`$."ship-to".city`, []pathStep{{field: "ship-to", index: -1}, {field: "city", index: -1}}},
		{`$."a.b"`, []pathStep{{field: "a.b", index: -1}}},
		{`$."ünï"`, []pathStep{{field: "ünï", index: -1}}},
		{"$[0][1]", []pathStep{{index: 0}, {index: 1}}},
	}
	for _, c := range good {
		got, err := parsePath(c.p)
		if err != nil {
			t.Errorf("%q: %v", c.p, err)
			continue
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%q = %+v, want %+v", c.p, got, c.want)
		}
	}
	for _, p := range []string{"", "a", ".a", "$.", "$..a", "$[", "$[x]", "$[-1]", "$[]", `$."open`, "$a", "$.a]", "$ .a"} {
		if _, err := parsePath(p); !isMetaOp(err) {
			t.Errorf("%q: err %v, want E_META_OP", p, err)
		}
	}
}

// TestJSONPathAndRange runs key$path filters and << through Compile and
// Match (P2-META-13): [*] matches any element, $[n] one position, leaves
// typed by inference; << needs an ip/cidr key or a json leaf, an ip inside
// the range or a narrower cidr matches.
func TestJSONPathAndRange(t *testing.T) {
	keys := []Key{
		{ID: kid(t, idSpec), Name: "spec", Scope: "repo", Type: meta.Type{Kind: value.KindJSON}},
		{ID: kid(t, idIP), Name: "egress_ip", Scope: "repo", Type: meta.Type{Kind: value.KindIP}},
		{ID: kid(t, idNet), Name: "subnet", Scope: "repo", Type: meta.Type{Kind: value.KindCIDR}},
		{ID: kid(t, idIPs), Name: "ips", Scope: "repo", Type: meta.Type{Kind: value.KindList, Elem: value.KindIP}},
		{ID: kid(t, idName), Name: "owner", Scope: "repo", Type: meta.Type{Kind: value.KindText}},
	}
	rows := map[string]map[string]string{
		"r1": {
			idSpec: `{"name":"spec","scope":"repo","v":{"items":[{"sku":"AB-1","qty":2},{"sku":"CD-2","qty":9}],"ship-to":{"city":"Pune"},"hosts":["10.1.2.3"]}}`,
			idIP:   `{"name":"egress_ip","scope":"repo","v":"10.2.3.4"}`,
			idNet:  `{"name":"subnet","scope":"repo","v":"10.4.0.0/16"}`,
			idIPs:  `{"name":"ips","scope":"repo","v":["192.168.1.5","8.8.8.8"]}`,
		},
		"r2": {
			idSpec: `{"name":"spec","scope":"repo","v":{"items":[{"sku":"CD-2","qty":1}],"ship-to":{"city":"Delhi"}}}`,
			idIP:   `{"name":"egress_ip","scope":"repo","v":"172.16.0.1"}`,
			idNet:  `{"name":"subnet","scope":"repo","v":"10.0.0.0/7"}`,
			idIPs:  `{"name":"ips","scope":"repo","v":["8.8.4.4"]}`,
		},
		"r3": {},
	}
	cases := []struct {
		filter string
		want   []string
	}{
		{"spec$.items[*].sku=AB-1", []string{"r1"}},
		{"spec$.items[*].sku=CD-2", []string{"r1", "r2"}},
		{"spec$.items[0].sku=CD-2", []string{"r2"}},
		{"spec$.items[1].sku=CD-2", []string{"r1"}},
		{"spec$.items[5].sku=CD-2", nil},
		{"spec$.items[*].qty>5", []string{"r1"}},
		{"spec$.items[*].qty<=1", []string{"r2"}},
		{`spec$."ship-to".city=Pune`, []string{"r1"}},
		{`spec$."ship-to".city~=delhi`, []string{"r2"}},
		{"spec$.hosts[*]<<10.0.0.0/8", []string{"r1"}},
		{"spec$.missing=x", nil},
		{"egress_ip<<10.0.0.0/8", []string{"r1"}},
		{"egress_ip<<172.16.0.0/12", []string{"r2"}},
		{"egress_ip<<10.2.3.4/32", []string{"r1"}},
		{"egress_ip<<10.2.3.99/8", []string{"r1"}}, // host bits masked
		{"subnet<<10.0.0.0/8", []string{"r1"}},     // narrower cidr inside; /7 is wider
		{"subnet<<0.0.0.0/0", []string{"r1", "r2"}},
		{"ips<<192.168.0.0/16", []string{"r1"}}, // any element
		{"ips<<8.8.0.0/16", []string{"r1", "r2"}},
		{"ips<<2001:db8::/32", nil}, // v6 range never holds a v4 address
	}
	for _, c := range cases {
		p, _, err := Compile([]string{c.filter}, "", "", keys)
		if err != nil {
			t.Errorf("%s: %v", c.filter, err)
			continue
		}
		var got []string
		for _, id := range []string{"r1", "r2", "r3"} {
			var kv []string
			for k, v := range rows[id] {
				kv = append(kv, k, v)
			}
			if ok, _ := p.Match(id, mm(t, kv...)); ok {
				got = append(got, id)
			}
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s = %v, want %v", c.filter, got, c.want)
		}
	}
	for _, f := range []string{
		"owner<<10.0.0.0/8",     // text key
		"egress_ip<<10.0.0.0",   // not a range
		"egress_ip<<nonsense/8", // bad range
		"egress_ip<<10.0.0.0/33",
		"spec$.items[x]=1", // bad path
		"spec$..a=1",
	} {
		if _, _, err := Compile([]string{f}, "", "", keys); !isMetaOp(err) {
			t.Errorf("%s: err %v, want E_META_OP", f, err)
		}
	}
}
