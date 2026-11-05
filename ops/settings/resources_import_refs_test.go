package settings_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestResourceImportRefMatrix (PC-RES-1, RES 3.20 and 3.4): import writes in
// two passes, so every shape of ref between the rows of one file imports,
// and a ref that cannot resolve refuses the whole file before any write.
// Each case starts from one existing host "old" and checks the peer of every
// named row after the import, or that the host count did not move.
func TestResourceImportRefMatrix(t *testing.T) {
	type row struct{ name, peer string } // peer "" = no ref
	cases := []struct {
		name     string
		rows     []row
		required bool              // peer is a required field
		want     map[string]string // name → peer shown after import
		refuse   string            // part of the refusal; "" = imports
	}{
		{name: "no refs", rows: []row{{"a", ""}, {"b", ""}}, want: map[string]string{"a": "", "b": ""}},
		{name: "a to b, b later in file", rows: []row{{"a", "b"}, {"b", ""}}, want: map[string]string{"a": "b", "b": ""}},
		{name: "b to a, a earlier in file", rows: []row{{"a", ""}, {"b", "a"}}, want: map[string]string{"a": "", "b": "a"}},
		{name: "a and b name each other", rows: []row{{"a", "b"}, {"b", "a"}}, want: map[string]string{"a": "b", "b": "a"}},
		{name: "three row ring", rows: []row{{"a", "b"}, {"b", "c"}, {"c", "a"}}, want: map[string]string{"a": "b", "b": "c", "c": "a"}},
		{name: "ref to an existing row", rows: []row{{"a", "old"}}, want: map[string]string{"a": "old"}},
		{name: "self ref", rows: []row{{"a", "a"}}, want: map[string]string{"a": "a"}},
		{name: "ref to a missing row", rows: []row{{"a", ""}, {"b", "nope"}}, refuse: `"nope"`},
		{name: "required ref chain imports", rows: []row{{"a", "b"}, {"b", "old"}}, required: true, want: map[string]string{"a": "b", "b": "old"}},
		{name: "required ref cycle refused", rows: []row{{"a", "b"}, {"b", "a"}}, required: true, refuse: "cycle of required refs"},
		{name: "required self ref refused", rows: []row{{"a", "a"}}, required: true, refuse: "cycle of required refs"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := opstest.New(t)
			mustAll(t, h,
				[2]string{settings.OpKindAdd, `{"kind":"host"}`},
				[2]string{settings.OpFieldAdd, `{"kind":"host","field":"os","type":"text"}`},
				[2]string{settings.OpFieldAdd, `{"kind":"host","field":"peer","type":"ref","ref_kind":"host"}`},
			)
			if c.required { // "old" exists first, then peer turns required
				h.MustYes(settings.OpResAdd, `{"kind":"host","name":"old"}`)
				h.MustYes(settings.OpResEdit, `{"id":"old","set":["peer=old"]}`)
				h.MustYes(settings.OpFieldEdit, `{"kind":"host","field":"peer","required":true}`)
			} else {
				h.MustYes(settings.OpResAdd, `{"kind":"host","name":"old"}`)
			}
			var parts []string
			for _, r := range c.rows {
				f := `"os":"linux"`
				if r.peer != "" {
					f += `,"peer":"` + r.peer + `"`
				}
				parts = append(parts, `{"kind":"host","name":"`+r.name+`","fields":{`+f+`}}`)
			}
			p := filepath.Join(t.TempDir(), "in.json")
			if err := os.WriteFile(p, []byte(`{"resources":[`+strings.Join(parts, ",")+`]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			in := `{"file":"` + p + `"}`
			if c.refuse != "" {
				if msg := h.Fails(settings.OpResImport, in, errs.ResourceBatch); !strings.Contains(msg, c.refuse) || !strings.Contains(msg, "nothing written") {
					t.Fatalf("refusal %q, want %q and nothing written", msg, c.refuse)
				}
				if _, err := h.RunYes(settings.OpResImport, in); err == nil {
					t.Fatal("import with --yes was not refused")
				}
				if n := countHosts(t, h); n != 1 {
					t.Fatalf("refused import left %d hosts, want 1 (whole file unwritten)", n)
				}
				return
			}
			if s := opstest.Text(h.MustYes(settings.OpResImport, in)); !strings.Contains(s, "imported "+strconv.Itoa(len(c.rows))+" rows") {
				t.Fatalf("import %q", s)
			}
			for n, peer := range c.want {
				s := opstest.Text(h.Must(settings.OpResShow, `{"id":"`+n+`"}`))
				if peer == "" && strings.Contains(s, "peer =") {
					t.Errorf("%s has a peer: %q", n, s)
				}
				if peer != "" && !strings.Contains(s, "peer = "+peer) {
					t.Errorf("%s: want peer = %s in %q", n, peer, s)
				}
				if !strings.Contains(s, "os = linux") {
					t.Errorf("%s lost os: %q", n, s)
				}
			}
			if n := countHosts(t, h); n != 1+len(c.rows) {
				t.Fatalf("hosts %d, want %d", n, 1+len(c.rows))
			}
		})
	}
}
