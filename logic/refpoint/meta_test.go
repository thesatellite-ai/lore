package refpoint

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// metaRow builds a row whose meta holds the given entries (name → raw
// JSON value), sensitive for the names in sens.
func metaRow(t *testing.T, vals map[string]string, sens ...string) row.Row {
	t.Helper()
	r := row.Row{Meta: map[ids.ID[ids.MetaKey]]value.JSON{}}
	for name, raw := range vals {
		id, err := ids.New[ids.MetaKey]()
		if err != nil {
			t.Fatal(err)
		}
		e := meta.Entry{Name: name, Scope: "repo", V: []byte(raw)}
		for _, s := range sens {
			e.Sensitive = e.Sensitive || s == name
		}
		j, err := e.Encode()
		if err != nil {
			t.Fatal(err)
		}
		r.Meta[id] = j
	}
	return r
}

// TestMetaOf (OV13a, META-B7-OBJ): a ${meta:…} target is read from the
// row's own meta entries: a plain key; a dotted or slash path into an
// object value (the longest entry name that prefixes the target wins); a
// string unquoted, any other JSON as written; a missing key or path
// misses; the sensitive flag travels with the value.
func TestMetaOf(t *testing.T) {
	r := metaRow(t, map[string]string{
		"owner":       `"mira"`,
		"smtp":        `{"url":"smtp://mail.example","port":587,"tls":{"on":true}}`,
		"smtp.legacy": `"old.example"`,
		"count":       `3`,
		"tags":        `["a","b"]`,
		"empty":       `""`,
		"büro":        `"köln"`,
		"api.hint":    `"tk-not-printed"`,
	}, "api.hint")
	get := MetaOf(r)
	cases := []struct {
		key       string
		ok        bool
		text      string
		sensitive bool
	}{
		{key: "owner", ok: true, text: "mira"},
		{key: "smtp.url", ok: true, text: "smtp://mail.example"},
		{key: "smtp/url", ok: true, text: "smtp://mail.example"},
		{key: "smtp.port", ok: true, text: "587"},
		{key: "smtp.tls.on", ok: true, text: "true"},
		{key: "smtp/tls", ok: true, text: `{"on":true}`},
		{key: "smtp.legacy", ok: true, text: "old.example"}, // the longer entry name wins
		{key: "smtp", ok: true, text: `{"port":587,"tls":{"on":true},"url":"smtp://mail.example"}`},
		{key: "count", ok: true, text: "3"},
		{key: "tags", ok: true, text: `["a","b"]`},
		{key: "empty", ok: true, text: ""},
		{key: "büro", ok: true, text: "köln"},
		{key: "api.hint", ok: true, text: "tk-not-printed", sensitive: true},
		{key: "missing"},
		{key: "smtp.nope"},
		{key: "owner.inner"}, // a string has no fields
		{key: "tags.0"},      // arrays are not walked
		{key: ""},
	}
	for _, c := range cases {
		v, ok := get(c.key)
		if ok != c.ok || v.Text != c.text || v.Sensitive != c.sensitive {
			t.Errorf("MetaOf(%q) = %+v %v; want %q sensitive=%v ok=%v", c.key, v, ok, c.text, c.sensitive, c.ok)
		}
	}
	if _, ok := MetaOf(row.Row{})("owner"); ok {
		t.Error("a row without meta must miss")
	}
}

// TestMetaExpansionPoints: ${meta:…} is filled only where the row's own
// metadata may appear (show and our private render); a sensitive value is
// masked; a missing key stays as written and is reported dangling; the
// shareable render and a client render keep the reference as written; a
// value holding "${" is escaped so it never expands a second time.
func TestMetaExpansionPoints(t *testing.T) {
	r := metaRow(t, map[string]string{"owner": `"mira"`, "pw": `"hunter2"`, "tpl": `"${var:host}"`}, "pw")
	text := "o=${meta:owner} p=${meta:pw} m=${meta:missing} t=${meta:tpl}"
	for _, c := range []struct {
		point Point
		want  string
	}{
		{Show, "o=mira p=" + Mask + " m=${meta:missing} t=${var:host}"},
		{Private, "o=mira p=" + Mask + " m=${meta:missing} t=${var:host}"},
		{Render, text},
		{Client, text},
	} {
		got, err := Expand(Options{Point: c.point, Meta: MetaOf(r)}, "body", text)
		if err != nil {
			t.Fatalf("%s: %v", c.point, err)
		}
		if got.Text != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.point, got.Text, c.want)
		}
		if c.point == Show {
			states := map[string]State{}
			for _, u := range got.Refs {
				states[u.Ref] = u.State
			}
			if states["${meta:missing}"] != StateDangling || states["${meta:pw}"] != StateMasked || states["${meta:owner}"] != StateExpanded {
				t.Errorf("show states = %v", states)
			}
			for _, u := range got.Refs {
				if u.Value == "hunter2" {
					t.Error("a sensitive value reached the refs report")
				}
			}
		}
	}
	// nil Meta: nothing filled at any point
	if got, err := Expand(Options{Point: Show}, "body", "${meta:owner}"); err != nil || got.Text != "${meta:owner}" {
		t.Fatalf("nil Meta: %q %v", got.Text, err)
	}
}

// TestMetaOfWithRegistry pins METADATA 3.20 on ${meta:…}: a value is
// masked when the entry's label OR the registry (KeyFlags) says
// sensitive, including a field inside an object value; nil flags and a
// nil KeyFlagsFunc fall back to the entry label only.
func TestMetaOfWithRegistry(t *testing.T) {
	r := metaRow(t, map[string]string{"owner": `"mira"`, "vpn": `"probe-reg-1"`, "smtp": `{"url":"probe-reg-2"}`, "pin": `"probe-entry-3"`}, "pin")
	ids := map[string]string{}
	for kid, j := range r.Meta {
		e, err := meta.DecodeEntry(j)
		if err != nil {
			t.Fatal(err)
		}
		ids[e.Name] = kid.String()
	}
	reg := KeyFlagsFunc(func(id string) bool { return id == ids["vpn"] || id == ids["smtp"] })
	text := "o=${meta:owner} v=${meta:vpn} s=${meta:smtp.url} p=${meta:pin}"
	for _, point := range []Point{Show, Private} {
		got, err := Expand(Options{Point: point, Meta: MetaOfWith(r, reg)}, "body", text)
		if err != nil {
			t.Fatal(err)
		}
		want := "o=mira v=" + Mask + " s=" + Mask + " p=" + Mask
		if got.Text != want {
			t.Errorf("%s: %q, want %q", point, got.Text, want)
		}
		for _, u := range got.Refs {
			if strings.Contains(u.Value, "probe") {
				t.Errorf("%s: refs report leaks %q", point, u.Value)
			}
		}
	}
	for name, flags := range map[string]KeyFlags{"nil": nil, "nil func": KeyFlagsFunc(nil)} {
		v, ok := MetaOfWith(r, flags)("vpn")
		if !ok || v.Sensitive || v.Text != "probe-reg-1" {
			t.Errorf("%s flags: %+v %v", name, v, ok)
		}
		if v, _ := MetaOfWith(r, flags)("pin"); !v.Sensitive {
			t.Errorf("%s flags lost the entry label", name)
		}
	}
}
