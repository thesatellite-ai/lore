package metafilter

import (
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/value"
)

const (
	idVPN   = "mky_01936f0000007a1b8c2d3e4f5a6ba121"
	idSCost = "mky_01936f0000007a1b8c2d3e4f5a6ba122"
)

// TestFilterSensitiveComparesStored pins OV34/OV39-SENSCOND for --meta and
// --where: a filter on a sensitive key (entry label or registry) compares
// the STORED value, never the mask: =, ~=, !=, > and << match the real
// value, `key=•••` matches nothing, and no note quotes a value.
func TestFilterSensitiveComparesStored(t *testing.T) {
	keys := []Key{
		{ID: kid(t, idVPN), Name: "vpn", Scope: "repo", Type: meta.Type{Kind: value.KindText}},
		{ID: kid(t, idSCost), Name: "cost", Scope: "repo", Type: meta.Type{Kind: value.KindNumber}},
		{ID: kid(t, idIP), Name: "egress_ip", Scope: "repo", Type: meta.Type{Kind: value.KindIP}},
	}
	rows := map[string]map[string]string{
		"r1": {
			idVPN:   `{"name":"vpn","scope":"repo","sensitive":true,"v":"probe-host-1"}`,
			idSCost: `{"name":"cost","scope":"repo","sensitive":true,"v":42}`,
			idIP:    `{"name":"egress_ip","scope":"repo","sensitive":true,"v":"10.9.8.7"}`,
		},
		"r2": {
			idVPN:   `{"name":"vpn","scope":"repo","sensitive":true,"v":"other"}`,
			idSCost: `{"name":"cost","scope":"repo","sensitive":true,"v":"text-probe-2"}`,
		},
	}
	cases := []struct {
		filter string
		want   []string
	}{
		{"vpn=probe-host-1", []string{"r1"}},
		{"vpn~=PROBE-HOST", []string{"r1"}},
		{"vpn!=probe-host-1", []string{"r2"}},
		{"vpn=•••", nil},
		{"vpn~=•••", nil},
		{"cost>40", []string{"r1"}},
		{"egress_ip<<10.0.0.0/8", []string{"r1"}},
	}
	for _, c := range cases {
		p, _, err := Compile([]string{c.filter}, "", "", keys)
		if err != nil {
			t.Fatalf("%s: %v", c.filter, err)
		}
		var got []string
		for _, id := range []string{"r1", "r2"} {
			var kv []string
			for k, v := range rows[id] {
				kv = append(kv, k, v)
			}
			ok, note := p.Match(id, mm(t, kv...))
			if ok {
				got = append(got, id)
			}
			if strings.Contains(string(note), "probe") {
				t.Errorf("%s: note quotes a sensitive value: %q", c.filter, note)
			}
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s = %v, want %v", c.filter, got, c.want)
		}
	}
}
