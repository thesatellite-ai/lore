package hotfix_test

import (
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/hotfix"
)

func add(t *testing.T, h *opstest.H, input string) string {
	t.Helper()
	return h.ID(h.Must(hotfix.OpAdd, input))
}

// TestTextMatchesV017 compares each human line with v0.1.17's for the same
// steps (0.1.17 binary, ids replaced): "⚠ <id> — <title> [<severity>]",
// default severity high, list shows the body, search shows the title;
// archived rows are listed only with --archived (OV30-2 changed v0.1.17).
func TestTextMatchesV017(t *testing.T) {
	h := opstest.New(t)
	r := h.Must(hotfix.OpAdd, `{"title":"Flaky CI","body":"Rerun once"}`)
	if got := opstest.IDPattern(opstest.Text(r)); got != "⚠ hfx_ID — Flaky CI [high]\n" {
		t.Fatalf("add %q", got)
	}
	h1 := h.ID(r)
	if got := opstest.IDPattern(opstest.Text(h.Must(hotfix.OpAdd, `{"title":"Crit","body":"boom","severity":"critical"}`))); got != "⚠ hfx_ID — Crit [critical]\n" {
		t.Fatalf("add critical %q", got)
	}
	steps := []struct{ op, input, want string }{
		{hotfix.OpList, ``, "hfx_ID [high] Rerun once\nhfx_ID [critical] boom\n"},
		{hotfix.OpShow, `{"id":"` + h1 + `"}`, "hfx_ID\n  severity: high\n\nRerun once\n"},
		{hotfix.OpSearch, `{"query":"ci"}`, "hfx_ID [high] Flaky CI\n"},
		{hotfix.OpEdit, `{"id":"` + h1 + `","severity":"low"}`, "✓ hfx_ID updated\n"},
		{hotfix.OpArchive, `{"id":"` + h1 + `"}`, "✓ hfx_ID archived\n"},
		{hotfix.OpList, ``, "hfx_ID [critical] boom\n"},
		{hotfix.OpList, `{"archived":true}`, "hfx_ID [low] Rerun once\nhfx_ID [critical] boom\n"},
	}
	for _, s := range steps {
		if got := opstest.IDPattern(opstest.Text(h.Must(s.op, s.input))); got != s.want {
			t.Fatalf("%s %s:\n%q\nwant\n%q", s.op, s.input, got, s.want)
		}
	}
	if got := (hotfix.ListOut{}).Text(); got != "(no hotfixes)\n" {
		t.Fatalf("empty %q", got)
	}
}

// TestAddRefusals: title required, body required, severity a closed set
// (0.1.18 refuses what v0.1.17 stored as given).
func TestAddRefusals(t *testing.T) {
	h := opstest.New(t)
	for _, sev := range []string{"low", "medium", "high", "critical"} {
		add(t, h, `{"title":"t","body":"b","severity":"`+sev+`"}`)
	}
	before := h.JSON(h.Must(hotfix.OpList, ``))
	for _, c := range []struct {
		input string
		code  errs.Code
	}{
		{`{"body":"boom"}`, errs.InvalidInput},
		{`{"title":"x"}`, errs.EmptyBody},
		{`{"title":"x","body":"b","severity":"bogus"}`, errs.InvalidInput},
		{`{"title":"x","body":"b","severity":"must"}`, errs.InvalidInput},
		{`{"title":"x","body":"b","supersedes":"hfx_01a1a5aca7f57659be7320c5f4b70011"}`, errs.NotFound},
		// A well-formed id of another noun and a malformed id are not
		// hotfix ids at all: E_INVALID_ID (B-ERRS-1/B-ERRS-5), never "no such row".
		{`{"title":"x","body":"b","supersedes":"rul_01a1a5aca7f57659be7320c5f4b70011"}`, errs.InvalidID},
		{`{"title":"x","body":"b","supersedes":"hfx_zz"}`, errs.InvalidID},
	} {
		h.Fails(hotfix.OpAdd, c.input, c.code)
	}
	if h.JSON(h.Must(hotfix.OpList, ``)) != before {
		t.Fatal("a refused add wrote a row")
	}
}

// TestSupersedesAndEdit: the chain mark lands on the old hotfix (severity
// unchanged); edit changes only the given fields.
func TestSupersedesAndEdit(t *testing.T) {
	h := opstest.New(t)
	old := add(t, h, `{"title":"A","body":"a","severity":"medium"}`)
	next := add(t, h, `{"title":"B","body":"b","supersedes":"`+old+`"}`)
	var out struct {
		Data hotfix.Record `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(h.Must(hotfix.OpShow, `{"id":"`+old+`"}`))), &out); err != nil {
		t.Fatal(err)
	}
	if out.Data.SupersededByID != next || out.Data.Severity != "medium" {
		t.Fatalf("old %+v", out.Data)
	}
	h.Fails(hotfix.OpAdd, `{"title":"C","body":"c","supersedes":"`+old+`"}`, errs.InvalidInput)
	h.Fails(hotfix.OpEdit, `{"id":"`+old+`"}`, errs.InvalidInput)
	h.Fails(hotfix.OpEdit, `{"id":"`+old+`","severity":"bogus"}`, errs.InvalidInput)
	h.Must(hotfix.OpEdit, `{"id":"`+old+`","title":"A2"}`)
	if err := json.Unmarshal([]byte(h.JSON(h.Must(hotfix.OpShow, `{"id":"`+old+`"}`))), &out); err != nil {
		t.Fatal(err)
	}
	if out.Data.Title != "A2" || out.Data.Body != "a" || out.Data.Severity != "medium" {
		t.Fatalf("after edit %+v", out.Data)
	}
}
