package memory_test

import (
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/memory"
)

// TestTextMatchesV017 runs the steps a person runs through the real engine
// and compares every human line with v0.1.17's output for the same steps
// (captured with the 0.1.17 binary, ids replaced): the add line keeps its
// trailing space, show prints trust with two decimals, a superseded
// memory stays listed, search prints "<id> <body>".
func TestTextMatchesV017(t *testing.T) {
	h := opstest.New(t)
	r := h.Must(memory.OpAdd, `{"body":"use pgx not database/sql","kind":"core"}`)
	if got := opstest.IDPattern(opstest.Text(r)); got != "✓ mem_ID \n  scope: [master]\n" {
		t.Fatalf("add %q", got)
	}
	m1 := h.ID(r)
	steps := []struct{ op, input, want string }{
		{memory.OpList, ``, "mem_ID [core] use pgx not database/sql\n"},
		{memory.OpShow, `{"id":"` + m1 + `"}`, "mem_ID\n  kind:   core\n  trust:  0.50\n  source: manual\n\nuse pgx not database/sql\n"},
		{memory.OpAdd, `{"body":"use pgx v5","supersedes":"` + m1 + `"}`, "✓ mem_ID \n  scope: [master]\n"},
		{memory.OpList, ``, "mem_ID [core] use pgx not database/sql\nmem_ID [retrieved] use pgx v5\n"},
		{memory.OpArchive, `{"id":"` + m1 + `"}`, "✓ mem_ID archived\n"},
		{memory.OpUnarchive, `{"id":"` + m1 + `"}`, "✓ mem_ID unarchived\n"},
	}
	for _, s := range steps {
		if got := opstest.IDPattern(opstest.Text(h.Must(s.op, s.input))); got != s.want {
			t.Fatalf("%s %s:\n%q\nwant\n%q", s.op, s.input, got, s.want)
		}
	}
	got := opstest.IDPattern(opstest.Text(h.Must(memory.OpSearch, `{"query":"pgx"}`)))
	if got != "mem_ID use pgx v5\nmem_ID use pgx not database/sql\n" && got != "mem_ID use pgx not database/sql\nmem_ID use pgx v5\n" {
		t.Fatalf("search %q", got)
	}
	h.Fails(memory.OpAdd, `{"body":"again","supersedes":"`+m1+`"}`, errs.InvalidInput)
}

// TestTagsThroughTheEngine: --tag on add / list / edit with real tag rows;
// an unknown tag is refused (0.1.18 delta: v0.1.17 ignored an unknown
// --tag silently) and never minted.
func TestTagsThroughTheEngine(t *testing.T) {
	h := opstest.New(t)
	h.Must("tag.add", `{"name":"infra"}`)
	h.Must("tag.add", `{"name":"security"}`)
	a := h.ID(h.Must(memory.OpAdd, `{"body":"a","tags":["infra"]}`))
	h.Must(memory.OpAdd, `{"body":"b","tags":["infra","security"]}`)
	h.Must(memory.OpAdd, `{"body":"c"}`)
	count := func(input string) int {
		var out memory.ListOut
		if err := json.Unmarshal([]byte(h.JSON(h.Must(memory.OpList, input))), &out); err != nil {
			t.Fatal(err)
		}
		return len(out.Data)
	}
	for in, want := range map[string]int{`{}`: 3, `{"tags":["infra"]}`: 2, `{"tags":["infra","security"]}`: 1, `{"tags":["nosuch"]}`: 0} {
		if got := count(in); got != want {
			t.Fatalf("%s: %d, want %d", in, got, want)
		}
	}
	h.Must(memory.OpEdit, `{"id":"`+a+`","untags":["infra"],"tags":["security"]}`)
	if got := count(`{"tags":["infra"]}`); got != 1 {
		t.Fatalf("after untag: %d", got)
	}
	h.Fails(memory.OpAdd, `{"body":"x","tags":["nosuch"]}`, errs.NotFound)
	h.Fails(memory.OpEdit, `{"id":"`+a+`","tags":["nosuch"]}`, errs.NotFound)
}

// TestDeleteIsATombstone: delete asks first (v0.1.17 --confirm, now
// --yes), then the memory is gone from show, list and search; an empty
// search prints "data": null as v0.1.17 did.
func TestDeleteIsATombstone(t *testing.T) {
	h := opstest.New(t)
	id := h.ID(h.Must(memory.OpAdd, `{"body":"ephemeral gateway"}`))
	h.Fails(memory.OpDelete, `{"id":"`+id+`"}`, errs.NeedsConfirm)
	h.MustYes(memory.OpDelete, `{"id":"`+id+`"}`)
	h.Fails(memory.OpShow, `{"id":"`+id+`"}`, errs.NotFound)
	if got := opstest.Text(h.Must(memory.OpList, ``)); got != "(no memories)\n" {
		t.Fatalf("list %q", got)
	}
	if got := h.JSON(h.Must(memory.OpSearch, `{"query":"gateway"}`)); got != `{"schema_version":1,"kind":"memory.search","query":"gateway","count":0,"data":null}` {
		t.Fatalf("search %s", got)
	}
}

// TestTagFilterCold: a tagged memory read back cold (no cache, fresh home:
// every row decoded from its row file) still matches list --tag, and an
// untag on the cold App drops exactly that tag.
func TestTagFilterCold(t *testing.T) {
	h := opstest.New(t)
	h.Must("tag.add", `{"name":"infra"}`)
	h.Must("tag.add", `{"name":"security"}`)
	id := h.ID(h.Must(memory.OpAdd, `{"body":"cold","tags":["infra","security"]}`))
	c := h.ReopenCold()
	var out memory.ListOut
	if err := json.Unmarshal([]byte(c.JSON(c.Must(memory.OpList, `{"tags":["infra","security"]}`))), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data) != 1 || out.Data[0].ID != id {
		t.Fatalf("cold list --tag: %+v", out.Data)
	}
	c.Must(memory.OpEdit, `{"id":"`+id+`","untags":["infra"]}`)
	for in, want := range map[string]int{`{"tags":["security"]}`: 1, `{"tags":["infra"]}`: 0} {
		if err := json.Unmarshal([]byte(c.JSON(c.Must(memory.OpList, in))), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Data) != want {
			t.Fatalf("after cold untag %s: %d, want %d", in, len(out.Data), want)
		}
	}
}
