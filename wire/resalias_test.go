package wire

// devco D-817 (owner H-75): a nickname, a full name or a role is a
// first-class alias of a resource. ${res:<alias>} in a note fills in and
// lists the note under the resource (also a note written before the alias
// was added), and search finds the resource by an alias.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire/internal/isolate"

	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
)

func TestResourceAliasRefsAndSearch(t *testing.T) {
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
	do(opssettings.OpKindAdd, `{"kind":"person"}`)
	do(opssettings.OpResAdd, `{"kind":"person","name":"robert_kim"}`)
	early := memory("lunch with ${res:Bobby} next week")
	do(opssettings.OpResAliasAdd, `{"id":"robert_kim","alias":["Bobby","CFO"]}`)
	later := memory("the ${res:person/CFO} signs the budget")
	for _, id := range []string{early, later} {
		out := do(opsmemory.OpShow, `{"id":"`+id+`","expand":true}`)
		var shown struct {
			Expanded struct {
				Body string `json:"body"`
			} `json:"expanded"`
		}
		if err := json.Unmarshal([]byte(out), &shown); err != nil || !strings.Contains(shown.Expanded.Body, "robert_kim") {
			t.Errorf("[D-817] memory %s: ${res:<alias>} did not fill in:\n%s", id, out)
		}
	}
	out, err := run(t, a, opssettings.OpResGraph, `{"id":"Bobby","up":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var g opssettings.GraphOut
	if err := json.Unmarshal([]byte(out), &g); err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, m := range g.Mentions {
		listed[m.Row] = true
	}
	if !listed[early] || !listed[later] {
		t.Errorf("[D-817] graph --up of robert_kim by its alias lists %v, want %s and %s:\n%s", listed, early, later, out)
	}
	hits := do(opssearch.OpRun, `{"query":"CFO"}`)
	if !strings.Contains(hits, "robert_kim") {
		t.Errorf("[D-817] search CFO does not find the person with that alias:\n%s", hits)
	}
}
