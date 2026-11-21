package wire

// devco D-822 (bug hunt H-81): an agent answering "everything about
// samir-nasser" with `resource show --with-refs` missed the newest
// meeting, "Met samir-nasser about fjord: … pricing page round 202": the
// note names the person in plain words, without ${res:…}, and only rows
// holding a reference were listed. lore ask found it by the name.
// --with-refs now lists a row that names the resource or an alias as a
// word (not inside a longer name), unless a resource of another kind has
// that name too; rows a feed imported come after the rows people wrote.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"

	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
)

func TestResourceShowWithRefsNames(t *testing.T) {
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
	memory := func(body, at, source string) string {
		t.Helper()
		in, err := json.Marshal(opsmemory.AddIn{Body: body, At: at, Source: source})
		if err != nil {
			t.Fatal(err)
		}
		return idOf(t, do(opsmemory.OpAdd, string(in)))
	}
	do(opssettings.OpKindAdd, `{"kind":"person","name_format":"dns"}`)
	do(opssettings.OpKindAdd, `{"kind":"team","name_format":"dns"}`)
	do(opssettings.OpResAdd, `{"kind":"person","name":"carol-li"}`)
	do(opssettings.OpResAliasAdd, `{"id":"carol-li","kind":"person","alias":["Caz"]}`)
	do(opssettings.OpResAdd, `{"kind":"person","name":"dan-oh"}`)
	do(opssettings.OpResAdd, `{"kind":"team","name":"dan-oh"}`) // a plain "dan-oh" cannot say which
	met := memory("Met carol-li about fjord: talked about the pricing page round 202.", "2021-05-01", "")
	byAlias := memory("Lunch with CAZ at the lido.", "2021-04-01", "")
	ref := memory("Saw ${res:person/carol-li} about orbit.", "2021-03-01", "")
	feed := memory("Feed slack: carol-li sent 3 emails about orbit.", "2022-01-01", "imported")
	longer := memory("Met carol-lin about orbit.", "2021-06-01", "")
	danPlain := memory("Met dan-oh about orbit.", "2021-06-02", "")

	show := func(in string) opssettings.ResOut {
		t.Helper()
		var got opssettings.ResOut
		if err := json.Unmarshal([]byte(do(opssettings.OpResShow, in)), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := show(`{"id":"carol-li","kind":"person","with_refs":true}`)
	var order []string
	for _, r := range got.ReferencedBy {
		order = append(order, r.Row)
	}
	// written rows newest first, then the feed's row though it is newer;
	// carol-lin is another name
	if want := []string{met, byAlias, ref, feed}; strings.Join(order, ",") != strings.Join(want, ",") || got.ReferencedTotal != 4 {
		t.Fatalf("[D-822] --with-refs rows %v (total %d), want meeting, alias, ref, then the feed %v; not %s (carol-lin)", order, got.ReferencedTotal, want, longer)
	}
	if b := got.ReferencedBy[0]; b.Body != "Met carol-li about fjord: talked about the pricing page round 202." || strings.Join(b.Fields, ",") != "body" {
		t.Errorf("[D-822] the meeting naming carol-li is not printed in full: %+v", b)
	}
	e, _ := a.Registry().Lookup(opssettings.OpResShow)
	res, err := a.Run(background(), e, op.JSONDecoder{Body: []byte(`{"id":"carol-li","kind":"person","with_refs":true,"max_refs":2}`)}, op.Caller{})
	if err != nil {
		t.Fatal(err)
	}
	if txt, _ := res.Text(); !strings.Contains(txt, "referenced by 4 rows, newest first, the rows a feed imported after the rest:") || !strings.Contains(txt, "2 more rows left out; --max-refs 4 prints them all") || !strings.Contains(txt, "pricing page round 202") {
		t.Errorf("[D-822] --with-refs text:\n%s", txt)
	}
	if dan := show(`{"id":"dan-oh","kind":"person","with_refs":true}`); dan.ReferencedTotal != 0 {
		t.Errorf("[D-822] person dan-oh --with-refs listed %+v: a team is named dan-oh too, so %s's plain name is not the person's", dan.ReferencedBy, danPlain)
	}
}
