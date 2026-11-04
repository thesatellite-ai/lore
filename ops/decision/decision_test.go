package decision_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/decision"
)

func add(t *testing.T, h *opstest.H, input string) string {
	t.Helper()
	return h.ID(h.Must(decision.OpAdd, input))
}

func show(t *testing.T, h *opstest.H, id string) decision.Record {
	t.Helper()
	var out struct {
		Data decision.Record `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(h.Must(decision.OpShow, `{"id":"`+id+`"}`))), &out); err != nil {
		t.Fatal(err)
	}
	return out.Data
}

// TestTextMatchesV017 compares every human line with v0.1.17's output for
// the same steps (captured with the 0.1.17 binary, ids replaced): the add
// line keeps v0.1.17's trailing space, a superseded decision's status
// becomes superseded, and list shows archived decisions (v0.1.17 had no
// --archived on decision list).
func TestTextMatchesV017(t *testing.T) {
	h := opstest.New(t)
	r := h.Must(decision.OpAdd, `{"title":"Use SQLite","body":"local first"}`)
	if got := opstest.IDPattern(opstest.Text(r)); got != "✓ dec_ID — Use SQLite \n" {
		t.Fatalf("add %q", got)
	}
	d1 := h.ID(r)
	add(t, h, `{"title":"T","body":"b"}`)
	if got := opstest.IDPattern(opstest.Text(h.Must(decision.OpShow, `{"id":"`+d1+`"}`))); got != "dec_ID\n  title:  Use SQLite\n  status: accepted\n\nlocal first\n" {
		t.Fatalf("show %q", got)
	}
	add(t, h, `{"title":"Use Postgres","body":"server","supersedes":"`+d1+`"}`)
	h.Must(decision.OpArchive, `{"id":"`+d1+`"}`)
	want := "dec_ID [superseded] Use SQLite\ndec_ID [accepted] T\ndec_ID [accepted] Use Postgres\n"
	if got := opstest.IDPattern(opstest.Text(h.Must(decision.OpList, ``))); got != want {
		t.Fatalf("list\n%q\nwant\n%q", got, want)
	}
	if got := opstest.IDPattern(opstest.Text(h.Must(decision.OpSearch, `{"query":"sqlite","include_archived":true}`))); got != "dec_ID [superseded] Use SQLite\n" {
		t.Fatalf("search %q", got)
	}
	if got := (decision.ListOut{}).Text(); got != "(no decisions)\n" {
		t.Fatalf("empty %q", got)
	}
}

// TestAddRefusals: title and body are required (v0.1.17: `required
// flag(s) "title"` and "body required"); status is a closed set (0.1.18
// refuses what v0.1.17 stored as given); nothing is written on a refusal.
func TestAddRefusals(t *testing.T) {
	h := opstest.New(t)
	for _, st := range []string{"proposed", "accepted", "superseded", "deprecated"} {
		add(t, h, `{"title":"t","body":"b","status":"`+st+`"}`)
	}
	before := h.JSON(h.Must(decision.OpList, ``))
	for _, c := range []struct {
		input string
		code  errs.Code
	}{
		{`{"body":"no title"}`, errs.InvalidInput},
		{`{"title":"  ","body":"b"}`, errs.InvalidInput},
		{`{"title":"T only"}`, errs.EmptyBody},
		{`{"title":"t","body":"b","status":"bogus"}`, errs.InvalidInput},
		{`{"title":"t","body":"b","status":"Accepted"}`, errs.InvalidInput},
		{`{"title":"t","body":"b","supersedes":"nope"}`, errs.NotFound},
		{`{"title":"t","body":"b","supersedes":"dec_00000000000000000000000000000000"}`, errs.NotFound},
		{`{"title":"t","body":"b","supersedes":"dec_zz"}`, errs.NotFound},
		{`{"title":"t","body":"b","supersedes":"dec_01a1a5aca7f57659be7320c5f4b70011"}`, errs.NotFound},
	} {
		h.Fails(decision.OpAdd, c.input, c.code)
	}
	if h.JSON(h.Must(decision.OpList, ``)) != before {
		t.Fatal("a refused add wrote a row")
	}
}

// TestSupersedesChain: the old decision gets superseded_by_id and status
// superseded in one patch; superseding it again is refused, naming its
// successor (v0.1.17's message and hint).
func TestSupersedesChain(t *testing.T) {
	h := opstest.New(t)
	old := add(t, h, `{"title":"A","body":"a","status":"proposed"}`)
	next := add(t, h, `{"title":"B","body":"b","supersedes":"`+old+`"}`)
	if d := show(t, h, old); d.Status != "superseded" || d.SupersededByID != next {
		t.Fatalf("old after supersede: %+v", d)
	}
	if d := show(t, h, next); d.Status != "accepted" || d.SupersededByID != "" {
		t.Fatalf("new: %+v", d)
	}
	msg := h.Fails(decision.OpAdd, `{"title":"C","body":"c","supersedes":"`+old+`"}`, errs.InvalidInput)
	if !strings.Contains(msg, "that decision was already replaced by "+next) {
		t.Fatalf("msg %q", msg)
	}
	third := add(t, h, `{"title":"C","body":"c","supersedes":"`+next+`"}`)
	if d := show(t, h, next); d.SupersededByID != third || d.Status != "superseded" {
		t.Fatalf("chain link 2: %+v", d)
	}
}

// TestEdit: only given fields change, closed set checked, nothing to
// change refused.
func TestEdit(t *testing.T) {
	h := opstest.New(t)
	id := add(t, h, `{"title":"A","body":"a"}`)
	h.Fails(decision.OpEdit, `{"id":"`+id+`"}`, errs.InvalidInput)
	h.Fails(decision.OpEdit, `{"id":"`+id+`","status":"bogus"}`, errs.InvalidInput)
	if got := opstest.IDPattern(opstest.Text(h.Must(decision.OpEdit, `{"id":"`+id+`","status":"deprecated","title":"A2"}`))); got != "✓ dec_ID updated\n" {
		t.Fatalf("edit %q", got)
	}
	if d := show(t, h, id); d.Status != "deprecated" || d.Title != "A2" || d.Body != "a" {
		t.Fatalf("after edit %+v", d)
	}
}

// TestIDErrors: malformed / other-noun ids are E_INVALID_ID, a well-formed
// unknown id E_NOT_FOUND (see ops/rule TestIDErrors for the v0.1.17 delta).
func TestIDErrors(t *testing.T) {
	h := opstest.New(t)
	h.Fails(decision.OpShow, `{"id":"nope"}`, errs.InvalidID)
	h.Fails(decision.OpShow, `{"id":"rul_01a1a5aca7f57659be7320c5f4b70011"}`, errs.InvalidID)
	h.Fails(decision.OpShow, `{"id":"dec_01a1a5aca7f57659be7320c5f4b70011"}`, errs.NotFound)
	id := add(t, h, `{"title":"A","body":"a"}`)
	h.Fails(decision.OpDelete, `{"id":"`+id+`"}`, errs.NeedsConfirm)
	h.MustYes(decision.OpDelete, `{"id":"`+id+`"}`)
	h.Fails(decision.OpShow, `{"id":"`+id+`"}`, errs.NotFound)
}

// TestTagFilterCold: a tagged decision read back cold (every row decoded
// from its row file) still matches list --tag.
func TestTagFilterCold(t *testing.T) {
	h := opstest.New(t)
	h.Must("tag.add", `{"name":"infra"}`)
	id := add(t, h, `{"title":"A","body":"a","tags":["infra"]}`)
	add(t, h, `{"title":"B","body":"b"}`)
	c := h.ReopenCold()
	var out decision.ListOut
	if err := json.Unmarshal([]byte(c.JSON(c.Must(decision.OpList, `{"tags":["infra"]}`))), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data) != 1 || out.Data[0].ID != id || len(out.Data[0].TagIDs) != 1 {
		t.Fatalf("cold list --tag: %+v", out.Data)
	}
}
