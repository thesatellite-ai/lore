package checklist

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestFromBody is the 7.5 parser table: nested bullets, [X], code blocks ignored.
func TestFromBody(t *testing.T) {
	t.Parallel()
	body := "intro\n- [ ] one\n  - [x] nested two\n* [X] three\n```\n- [ ] in code\n```\n- [?] not a box\n- [ ]   \n+ [ ] four"
	got := FromBody(body)
	want := []BodyItem{{"one", false}, {"nested two", true}, {"three", true}, {"four", false}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %+v want %+v", i, got[i], want[i])
		}
	}
}

// TestResolveAndLimits: position vs id addressing skips tombstones; text rules.
func TestResolveAndLimits(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	a, _ := New("a", "a1", "", now)
	b, _ := New("b", "a2", "", now)
	c, _ := New("c", "a3", "", now)
	b.Remove("", now)
	list := []Item{c, b, a}
	if i, err := Resolve(list, "2"); err != nil || list[i].Text != "c" {
		t.Errorf("position 2 = %v %v", i, err)
	}
	if i, err := Resolve(list, b.ID.String()); err != nil || list[i].Text != "b" {
		t.Errorf("by id %v %v", i, err)
	}
	if _, err := Resolve(list, "3"); !errs.Has(err, errs.NotFound) {
		t.Errorf("position 3: %v", err)
	}
	if _, err := Resolve(list, "x"); !errs.Has(err, errs.InvalidInput) {
		t.Errorf("bad ref: %v", err)
	}
	for _, bad := range []string{"", "  ", "a\nb", strings.Repeat("x", MaxText+1), "tab\there"} {
		if _, err := CleanText(bad); err == nil {
			t.Errorf("CleanText(%q) passed", bad)
		}
	}
	a.SetDone(true, "act_x", now)
	if d, n := Progress([]Item{a, b, c}); d != 1 || n != 2 || len(Open([]Item{a, b, c})) != 1 {
		t.Errorf("progress %d/%d", d, n)
	}
	raw, err := Encode([]Item{c, a})
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(raw)
	if err != nil || len(back) != 2 || back[0].Text != "a" || !back[0].Done {
		t.Errorf("round trip %+v %v", back, err)
	}
}
