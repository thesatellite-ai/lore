package wire

import (
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestMetaKeyFlags: a key added sensitive, and one made sensitive later,
// are flagged by id; a plain key and an unknown id are not.
func TestMetaKeyFlags(t *testing.T) {
	t.Parallel()
	a := openWith(t, gitRepo(t), Options{})
	withProject(t, a)
	id := func(out string) string {
		var r struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(out), &r); err != nil || r.ID == "" {
			t.Fatalf("%s %v", out, err)
		}
		return r.ID
	}
	must := func(name, in string) string {
		t.Helper()
		out, err := run(t, a, name, in, true)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	born := id(must(settings.OpMetaAdd, `{"name":"vpn","type":"text","sensitive":true}`))
	later := id(must(settings.OpMetaAdd, `{"name":"later","type":"text"}`))
	plain := id(must(settings.OpMetaAdd, `{"name":"plain","type":"text"}`))
	must(settings.OpMetaEdit, `{"id":"later","sensitive":true}`)
	f, err := a.MetaKeyFlags(background())
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]bool{born: true, later: true, plain: false, "mky_00000000000000000000000000000000": false} {
		if got := f.KeySensitive(k); got != want {
			t.Errorf("%s: %v, want %v", k, got, want)
		}
	}
}
