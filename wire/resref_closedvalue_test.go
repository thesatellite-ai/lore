package wire

// devco D-949 (public issue #157): a closed value (a field with --values)
// is stored as its val_ id; ${res:<kind>/<name>/<field>} filled in that id where
// resource list prints the value's name. It now fills in the current name,
// also after the value is renamed.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire/internal/isolate"

	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
)

func TestResRefFillsClosedValueName(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	a := openWith(t, gitRepo(t), Options{})
	withProject(t, a)
	do := func(op, in string) string {
		t.Helper()
		out, err := run(t, a, op, in, true)
		if err != nil {
			t.Fatalf("%s %s: %v", op, in, err)
		}
		return out
	}
	do(opssettings.OpKindAdd, `{"kind":"code_repo"}`)
	do(opssettings.OpFieldAdd, `{"kind":"code_repo","field":"status","type":"text","values":"active,archived"}`)
	do(opssettings.OpResAdd, `{"kind":"code_repo","name":"widget_api","set":["status=active"]}`)
	in, err := json.Marshal(opsmemory.AddIn{Body: "widget_api is ${res:code_repo/widget_api/status} today"})
	if err != nil {
		t.Fatal(err)
	}
	id := idOf(t, do(opsmemory.OpAdd, string(in)))
	expanded := func() string {
		t.Helper()
		out := do(opsmemory.OpShow, `{"id":"`+id+`","expand":true}`)
		var shown struct {
			Expanded struct {
				Body string `json:"body"`
			} `json:"expanded"`
		}
		if err := json.Unmarshal([]byte(out), &shown); err != nil {
			t.Fatalf("memory show: %v\n%s", err, out)
		}
		return shown.Expanded.Body
	}
	if got := expanded(); got != "widget_api is active today" {
		t.Errorf("[D-949] ${res:code_repo/widget_api/status} filled in %q, want the value's name (active)", got)
	}
	do(opssettings.OpFieldEdit, `{"kind":"code_repo","field":"status","rename_values":["active=live"]}`)
	if got := expanded(); strings.Contains(got, "val_") || !strings.Contains(got, "is live today") {
		t.Errorf("[D-949] after the rename ${res:code_repo/widget_api/status} filled in %q, want live", got)
	}
}
