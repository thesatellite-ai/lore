package wire

// devco D-764 (owner P-6): ${res:<kind>/<name>} in a note never reached
// row_refs / row_expanded (the resolver read every `/` as name/field), and
// resource graph --up / impact never listed notes at all. Each typed name
// form a resource lookup takes must fill in and be listed under the
// resource, also when the note is written before the resource exists.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire/internal/isolate"

	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
)

func TestResourceMentionsReachGraphUp(t *testing.T) {
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
	memory := func(body string) string {
		t.Helper()
		in, err := json.Marshal(opsmemory.AddIn{Body: body})
		if err != nil {
			t.Fatal(err)
		}
		return idOf(t, do(opsmemory.OpAdd, string(in)))
	}
	do(opssettings.OpKindAdd, `{"kind":"server"}`)
	do(opssettings.OpFieldAdd, `{"kind":"server","field":"ip","type":"ip"}`)
	early := memory("db box is ${res:repo:server/db_prod}, written before it")
	do(opssettings.OpResAdd, `{"kind":"server","name":"web_prod","set":["ip=203.0.113.10"]}`)
	do(opssettings.OpResAdd, `{"kind":"server","name":"db_prod","set":["ip=203.0.113.11"]}`)
	want := map[string]string{ // memory id -> the resource it mentions
		memory("deploy notes for ${res:server/web_prod}"):     "web_prod",
		memory("ip is ${res:server/web_prod/ip}"):             "web_prod",
		memory("bare ${res:web_prod} and ${res:web_prod/ip}"): "web_prod",
		early: "db_prod",
	}
	plain := memory("no reference to web_prod here")
	for _, res := range []string{"web_prod", "db_prod"} {
		for _, op := range []string{opssettings.OpResGraph, opssettings.OpResImpact} {
			in := `{"id":"` + res + `"}`
			if op == opssettings.OpResGraph {
				in = `{"id":"` + res + `","up":true}`
			}
			out, err := run(t, a, op, in, false)
			if err != nil {
				t.Fatalf("%s %s: %v", op, in, err)
			}
			var got opssettings.GraphOut
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatal(err)
			}
			listed := map[string]bool{}
			for _, m := range got.Mentions {
				listed[m.Row] = true
				if m.Node != got.Root || m.Table != "memories" || m.Field != "body" {
					t.Errorf("[D-764] %s %s: mention %+v, want memories.body of %s", op, res, m, got.Root)
				}
			}
			for id, r := range want {
				if (r == res) != listed[id] {
					t.Errorf("[D-764] %s %s: memory %s listed=%v, want %v:\n%s", op, res, id, listed[id], r == res, out)
				}
			}
			if listed[plain] {
				t.Errorf("[D-764] %s %s lists a memory with no reference", op, res)
			}
		}
	}
	for id := range want {
		out := do(opsmemory.OpShow, `{"id":"`+id+`","expand":true}`)
		var shown struct {
			Expanded struct {
				Body string `json:"body"`
			} `json:"expanded"`
		}
		if err := json.Unmarshal([]byte(out), &shown); err != nil || shown.Expanded.Body == "" || strings.Contains(shown.Expanded.Body, "${res:") {
			t.Errorf("[D-764] memory %s did not fill in its reference:\n%s", id, out)
		}
	}
}
