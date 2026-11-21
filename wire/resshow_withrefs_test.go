package wire

// devco D-792 (owner P-19, bug hunt H-49): "everything about X" took one
// call per note: resource show printed the resource only, graph --up
// listed the notes that mention it by title, and neither took --kind or
// --name. `resource show --kind person --name alice-ng --with-refs` now
// returns the resource plus every row that references it (text
// ${res:…} and --ref pointers), body in full, newest first, capped by
// --max-refs.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"

	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

func TestResourceShowWithRefs(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	a := openWith(t, gitRepo(t), Options{})
	withProject(t, a)
	do := func(name, in string) string {
		t.Helper()
		out, err := run(t, a, name, in, true)
		if err != nil {
			t.Fatalf("%s %s: %v", name, in, err)
		}
		return out
	}
	memory := func(body, at string) string {
		t.Helper()
		in, err := json.Marshal(opsmemory.AddIn{Body: body, At: at})
		if err != nil {
			t.Fatal(err)
		}
		return idOf(t, do(opsmemory.OpAdd, string(in)))
	}
	do(opssettings.OpKindAdd, `{"kind":"person","name_format":"dns"}`)
	do(opssettings.OpKindAdd, `{"kind":"team","name_format":"dns"}`)
	do(opssettings.OpResAdd, `{"kind":"person","name":"alice-ng"}`)
	do(opssettings.OpResAdd, `{"kind":"person","name":"bob-ray"}`)
	do(opssettings.OpResAdd, `{"kind":"team","name":"alice-ng"}`) // the same name, another kind
	oldest := memory("old: ${res:person/alice-ng} was at globex", "2020-01-10")
	newer := memory("${res:person/alice-ng} joins acme-co as CFO\nsecond line: owns the orbit budget", "2021-03-01")
	other := memory("${res:person/bob-ray} runs orbit", "2021-06-01")
	plain := memory("no reference to alice-ng here", "2021-07-01")
	taskOut := do(opstask.OpAdd, `{"title":"send the orbit budget to alice","ref":["person/alice-ng"]}`) // written now: the newest
	i := strings.Index(taskOut, "tsk_")
	if i < 0 || len(taskOut) < i+36 {
		t.Fatalf("task add output has no id: %s", taskOut)
	}
	task := taskOut[i : i+36]

	// the bare name two kinds share is ambiguous; --kind narrows it
	if _, err := run(t, a, opssettings.OpResShow, `{"id":"alice-ng"}`, false); !errs.Has(err, errs.ResourceAmbiguous) {
		t.Fatalf("[D-792] show alice-ng without --kind: err %v, want E_RESOURCE_AMBIGUOUS", err)
	}
	show := func(in string) opssettings.ResOut {
		t.Helper()
		var got opssettings.ResOut
		if err := json.Unmarshal([]byte(do(opssettings.OpResShow, in)), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := show(`{"id":"alice-ng","kind":"person"}`); got.Data.Kind != "person" || got.ReferencedBy != nil || got.ReferencedTotal != 0 {
		t.Fatalf("[D-792] show --kind person without --with-refs: %+v", got)
	}

	got := show(`{"id":"alice-ng","kind":"person","with_refs":true}`)
	var order []string
	for _, r := range got.ReferencedBy {
		order = append(order, r.Row)
	}
	if want := []string{task, newer, oldest}; strings.Join(order, ",") != strings.Join(want, ",") || got.ReferencedTotal != 3 {
		t.Fatalf("[D-792] --with-refs rows %v (total %d), want task, newer, oldest %v (newest first); bob's %s and the plain %s left out", order, got.ReferencedTotal, want, other, plain)
	}
	if b := got.ReferencedBy[1]; b.Body != "${res:person/alice-ng} joins acme-co as CFO\nsecond line: owns the orbit budget" || b.Table != "memories" || strings.Join(b.Fields, ",") != "body" || !strings.HasPrefix(b.At, "2021-03-01") {
		t.Errorf("[D-792] the newer memory is not printed in full with its date: %+v", b)
	}
	if b := got.ReferencedBy[0]; b.Table != "tasks" || strings.Join(b.Relations, ",") != "mentions" || b.Body != "send the orbit budget to alice" {
		t.Errorf("[D-792] the task's --ref pointer is not listed: %+v", b)
	}

	capped := show(`{"id":"alice-ng","kind":"person","with_refs":true,"max_refs":1}`)
	if len(capped.ReferencedBy) != 1 || capped.ReferencedBy[0].Row != task || capped.ReferencedTotal != 3 {
		t.Fatalf("[D-792] --max-refs 1: %+v, want the task only and a total of 3", capped)
	}
	e, _ := a.Registry().Lookup(opssettings.OpResShow)
	res, err := a.Run(background(), e, op.JSONDecoder{Body: []byte(`{"id":"alice-ng","kind":"person","with_refs":true,"max_refs":1}`)}, op.Caller{})
	if err != nil {
		t.Fatal(err)
	}
	if txt, _ := res.Text(); !strings.Contains(txt, "referenced by 3 rows, newest first:") || !strings.Contains(txt, "2 older rows left out; --max-refs 3 prints them all") || !strings.Contains(txt, "send the orbit budget to alice") {
		t.Errorf("[D-792] --with-refs text:\n%s", txt)
	}
	if none := show(`{"id":"bob-ray","with_refs":true}`); none.ReferencedTotal != 1 || none.ReferencedBy[0].Row != other {
		t.Errorf("[D-792] bob-ray --with-refs: %+v, want only %s", none, other)
	}
}
