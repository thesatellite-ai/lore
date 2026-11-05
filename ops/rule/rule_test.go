package rule_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/rule"
)

// add runs rule.add and returns the new id.
func add(t *testing.T, h *opstest.H, input string) string {
	t.Helper()
	return h.ID(h.Must(rule.OpAdd, input))
}

// norm is a Text() output with ids replaced (opstest.IDPattern).
func norm(s string) string { return opstest.IDPattern(s) }

// TestTextMatchesV017 drives the commands a person runs and compares each
// human line with what v0.1.17 printed for the same steps (captured with
// /home/khanakia/.local/bin/lore 0.1.17 in a scratch repo, ids replaced).
// The 0.1.18 deltas that are deliberate are not in this test: delete is a
// tombstone confirmed with --yes, and an edit without a field is refused.
func TestTextMatchesV017(t *testing.T) {
	h := opstest.New(t)
	r1 := h.Must(rule.OpAdd, `{"body":"never push on friday","severity":"should","activation":"manual"}`)
	if got, want := norm(opstest.Text(r1)), "✓ rul_ID\n  scope: [master] severity: should\n"; got != want {
		t.Fatalf("add:\n%q\nwant\n%q", got, want)
	}
	id1 := h.ID(r1)
	add(t, h, `{"body":"`+strings.Repeat("a", 70)+`"}`)
	steps := []struct {
		op, input, want string
	}{
		{rule.OpList, ``, "rul_ID [should/manual] never push on friday\nrul_ID [must/always] " + strings.Repeat("a", 57) + "...\n"},
		{rule.OpArchive, `{"id":"` + id1 + `"}`, "✓ rul_ID archived\n"},
		{rule.OpList, ``, "rul_ID [must/always] " + strings.Repeat("a", 57) + "...\n"},
		{rule.OpList, `{"archived":true}`, "rul_ID [should/manual] never push on friday [archived]\nrul_ID [must/always] " + strings.Repeat("a", 57) + "...\n"},
		{rule.OpUnarchive, `{"id":"` + id1 + `"}`, "✓ rul_ID unarchived\n"},
		{rule.OpEdit, `{"id":"` + id1 + `","body":"never push on fridays"}`, "✓ rul_ID updated\n"},
		{rule.OpShow, `{"id":"` + id1 + `"}`, "rul_ID\n  severity:   should\n  activation: manual\n\nnever push on fridays\n"},
		{rule.OpSearch, `{"query":"friday*"}`, "rul_ID [should] never push on fridays\n"},
		{rule.OpSearch, `{"query":"zzzz"}`, "(no matches)\n"},
	}
	for _, s := range steps {
		res := h.Must(s.op, s.input)
		if got := norm(opstest.Text(res)); got != s.want {
			t.Fatalf("%s %s:\n%q\nwant\n%q", s.op, s.input, got, s.want)
		}
	}
	del := h.MustYes(rule.OpDelete, `{"id":"`+id1+`"}`)
	if got := norm(opstest.Text(del)); got != "✓ rul_ID moved to the trash (restore: lore trash restore rul_ID)\n" {
		t.Fatalf("delete: %q", got)
	}
}

// TestEmptyList: v0.1.17 printed "(no rules)" and a JSON envelope with an
// empty data array and no count key.
func TestEmptyList(t *testing.T) {
	h := opstest.New(t)
	res := h.Must(rule.OpList, ``)
	if got := opstest.Text(res); got != "(no rules)\n" {
		t.Fatalf("text %q", got)
	}
	if got := h.JSON(res); got != `{"schema_version":1,"kind":"rule.list","data":[]}` {
		t.Fatalf("json %s", got)
	}
}

// TestJSONKeysMatchV017: the --json list row carries v0.1.17's keys in its
// order (ent Rule JSON), with unset nillable columns omitted.
func TestJSONKeysMatchV017(t *testing.T) {
	h := opstest.New(t)
	add(t, h, `{"body":"never push on friday"}`)
	raw := h.JSON(h.Must(rule.OpList, ``))
	var env struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil || len(env.Data) != 1 {
		t.Fatalf("%s %v", raw, err)
	}
	want := []string{"id", "created_at", "updated_at", "trust_score", "source_kind", "project_id", "body", "activation", "severity", "created_by_actor_id"}
	if got := keys(t, env.Data[0]); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("keys %v\nwant %v", got, want)
	}
	if !strings.HasPrefix(raw, `{"schema_version":1,"kind":"rule.list","count":1,"data":[`) {
		t.Fatalf("envelope %s", raw)
	}
}

// keys lists a JSON object's keys in document order.
func keys(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	var out []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// TestAddRefusals is the add input matrix: every closed-set value is
// accepted, anything else is refused before a write (0.1.18 delta: v0.1.17
// stored "bogus" severities as given; the schema's closed set refuses
// them), and the body is required exactly once.
func TestAddRefusals(t *testing.T) {
	h := opstest.New(t)
	for _, sev := range []string{"must", "should", "may"} {
		for _, act := range []string{"always", "glob", "semantic", "manual"} {
			add(t, h, `{"body":"x `+sev+act+`","severity":"`+sev+`","activation":"`+act+`"}`)
		}
	}
	cases := []struct {
		input string
		code  errs.Code
	}{
		{`{"body":"x","severity":"bogus"}`, errs.InvalidInput},
		{`{"body":"x","severity":"MUST"}`, errs.InvalidInput},
		{`{"body":"x","activation":"bogus"}`, errs.InvalidInput},
		{`{}`, errs.EmptyBody},
		{`{"body":"   "}`, errs.EmptyBody},
		{`{"body":"a","text":"b"}`, errs.InvalidInput},
		{`{"body":"x","tags":["nosuch"]}`, errs.NotFound},
		{`{"body":"x","tags":[""]}`, errs.InvalidInput},
		{`{"body":"x","repo":"nope"}`, errs.NotFound},
		{`{"body":"x","project":"nope"}`, errs.NotFound},
		{`{"body":"x","created_by":"nope"}`, errs.NotFound},
		{`{"body":"x","created_by":"mem_01a1a5aca7f57659be7320c5f4b70011"}`, errs.NotFound},
		{`{"body":"x","supersedes":"nope"}`, errs.NotFound}, // --supersedes: v0.1.17 parity
		{`{"body":"x","supersedes":"rul_zz"}`, errs.NotFound},
		{`{"body":"x","supersedes":"dec_01a1a5aca7f57659be7320c5f4b70011"}`, errs.NotFound},
		{`{"body":"x","supersedes":"rul_01a1a5aca7f57659be7320c5f4b70011"}`, errs.NotFound},
	}
	before := h.Must(rule.OpList, ``)
	for _, c := range cases {
		h.Fails(rule.OpAdd, c.input, c.code)
	}
	if h.JSON(h.Must(rule.OpList, ``)) != h.JSON(before) {
		t.Fatal("a refused add wrote a row")
	}
}

// TestPositionalBodyAndUnicode: the positional text is the body; unicode,
// a reserved word and a large body round-trip unchanged.
func TestPositionalBodyAndUnicode(t *testing.T) {
	h := opstest.New(t)
	for _, body := range []string{"select", "日本語のルール — ünïcödé ✓", strings.Repeat("long body ", 2000) + "end"} {
		id := add(t, h, `{"text":`+opstest.Quote(body)+`}`)
		var show struct {
			Data rule.Record `json:"data"`
		}
		if err := json.Unmarshal([]byte(h.JSON(h.Must(rule.OpShow, `{"id":"`+id+`"}`))), &show); err != nil {
			t.Fatal(err)
		}
		if show.Data.Body != body {
			t.Fatalf("body %q round-tripped as %q", body[:min(len(body), 20)], show.Data.Body[:min(len(show.Data.Body), 20)])
		}
	}
}

// TestIDErrors pins 0.1.18's id answers (later.md integrator4 line on
// TestFix_SupersedesMarksTheOldRow): a malformed id or another noun's id is
// E_INVALID_ID (the core's id check, the same for every noun), a
// well-formed unknown or deleted id is E_NOT_FOUND. v0.1.17 answered all of
// them E_NOT_FOUND with "invalid id" in the message; the code is the
// deliberate delta, the message still names the bad value.
func TestIDErrors(t *testing.T) {
	h := opstest.New(t)
	id := add(t, h, `{"body":"x"}`)
	for _, c := range []struct {
		id   string
		code errs.Code
	}{
		{"nope", errs.NotFound},
		{"", errs.InvalidInput}, // a required field left empty
		{"dec_01a1a679339b7c56896a5b5dd2cace7d", errs.NotFound},
		{"rul_01a1a5aca7f57659be7320c5f4b70099", errs.NotFound},
	} {
		for _, name := range []string{rule.OpShow, rule.OpArchive, rule.OpUnarchive} {
			msg := h.Fails(name, `{"id":"`+c.id+`"}`, c.code)
			if c.id != "" && !strings.Contains(msg, c.id) {
				t.Fatalf("%s %s: message %q does not name the id", name, c.id, msg)
			}
		}
		h.Fails(rule.OpEdit, `{"id":"`+c.id+`","body":"y"}`, c.code)
	}
	h.MustYes(rule.OpDelete, `{"id":"`+id+`"}`)
	for _, name := range []string{rule.OpShow, rule.OpArchive} {
		h.Fails(name, `{"id":"`+id+`"}`, errs.InTrash)
	}
	h.Fails(rule.OpEdit, `{"id":"`+id+`","body":"y"}`, errs.InTrash)
}

// TestDeleteAsksFirst: delete always asks (v0.1.17 wanted --confirm); a
// caller that did not confirm gets E_NEEDS_CONFIRM and the row stays.
func TestDeleteAsksFirst(t *testing.T) {
	h := opstest.New(t)
	id := add(t, h, `{"body":"x"}`)
	h.Fails(rule.OpDelete, `{"id":"`+id+`"}`, errs.NeedsConfirm)
	h.Must(rule.OpShow, `{"id":"`+id+`"}`)
}

// TestEditMatrix: only the given fields change; nothing to change and an
// unknown closed-set value are refused; --rebind-* are exclusive.
func TestEditMatrix(t *testing.T) {
	h := opstest.New(t)
	id := add(t, h, `{"body":"x","severity":"may","activation":"glob","globs":"[\"*.go\"]"}`)
	h.Fails(rule.OpEdit, `{"id":"`+id+`"}`, errs.InvalidInput)
	h.Fails(rule.OpEdit, `{"id":"`+id+`","severity":"bogus"}`, errs.InvalidInput)
	h.Fails(rule.OpEdit, `{"id":"`+id+`","activation":"bogus"}`, errs.InvalidInput)
	h.Fails(rule.OpEdit, `{"id":"`+id+`","rebind_master":true,"rebind_repo":"web"}`, errs.InvalidInput)
	h.Must(rule.OpEdit, `{"id":"`+id+`","severity":"must","source_ref":"pr#1"}`)
	var show struct {
		Data rule.Record `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(h.Must(rule.OpShow, `{"id":"`+id+`"}`))), &show); err != nil {
		t.Fatal(err)
	}
	d := show.Data
	if d.Severity != "must" || d.Activation != "glob" || d.Globs != `["*.go"]` || d.Body != "x" || d.SourceRef != "pr#1" {
		t.Fatalf("after edit %+v", d)
	}
}

// TestSupersedesChain: the old rule points at the new one; a replaced rule
// cannot be superseded again (the chain stays one line); a deleted rule
// cannot be superseded.
func TestSupersedesChain(t *testing.T) {
	h := opstest.New(t)
	old := add(t, h, `{"body":"v1"}`)
	next := add(t, h, `{"body":"v2","supersedes":"`+old+`"}`)
	var show struct {
		Data rule.Record `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(h.Must(rule.OpShow, `{"id":"`+old+`"}`))), &show); err != nil {
		t.Fatal(err)
	}
	if show.Data.SupersededByID != next {
		t.Fatalf("old superseded_by %q, want %s", show.Data.SupersededByID, next)
	}
	msg := h.Fails(rule.OpAdd, `{"body":"v3","supersedes":"`+old+`"}`, errs.InvalidInput)
	if !strings.Contains(msg, "already replaced by "+next) {
		t.Fatalf("msg %q", msg)
	}
	gone := add(t, h, `{"body":"gone"}`)
	h.MustYes(rule.OpDelete, `{"id":"`+gone+`"}`)
	h.Fails(rule.OpAdd, `{"body":"v","supersedes":"`+gone+`"}`, errs.NotFound)
}

// TestTags: --tag on add and list (all must match), --tag/--untag on edit;
// an unknown tag is refused and never minted.
func TestTags(t *testing.T) {
	h := opstest.New(t)
	h.Must("tag.add", `{"name":"infra"}`)
	h.Must("tag.add", `{"name":"security"}`)
	a := add(t, h, `{"body":"a","tags":["infra"]}`)
	add(t, h, `{"body":"b","tags":["infra","security"]}`)
	add(t, h, `{"body":"c"}`)
	for _, c := range []struct {
		input string
		want  int
	}{
		{`{}`, 3},
		{`{"tags":["infra"]}`, 2},
		{`{"tags":["infra","security"]}`, 1},
		{`{"tags":["nosuch"]}`, 0},
	} {
		var out rule.ListOut
		if err := json.Unmarshal([]byte(h.JSON(h.Must(rule.OpList, c.input))), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Data) != c.want {
			t.Fatalf("%s: %d rows, want %d", c.input, len(out.Data), c.want)
		}
	}
	h.Must(rule.OpEdit, `{"id":"`+a+`","tags":["security"],"untags":["infra"]}`)
	var out rule.ListOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(rule.OpList, `{"tags":["security"]}`))), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data) != 2 {
		t.Fatalf("after retag: %d", len(out.Data))
	}
	h.Fails(rule.OpEdit, `{"id":"`+a+`","tags":["nosuch"]}`, errs.NotFound)
	if strings.Contains(h.JSON(h.Must("tag.list", ``)), "nosuch") {
		t.Fatal("an unknown tag was minted")
	}
}

// TestSearchLimitAndArchived: --limit bounds, archived hits only with
// --include-archived, an empty query refused.
func TestSearchLimitAndArchived(t *testing.T) {
	h := opstest.New(t)
	var archived string
	for i := range 5 {
		id := add(t, h, `{"body":"gateway retry `+strings.Repeat("x", i)+`"}`)
		if i == 0 {
			archived = id
		}
	}
	h.Must(rule.OpArchive, `{"id":"`+archived+`"}`)
	count := func(input string) int {
		var out rule.SearchOut
		if err := json.Unmarshal([]byte(h.JSON(h.Must(rule.OpSearch, input))), &out); err != nil {
			t.Fatal(err)
		}
		if out.Count != len(out.Data) {
			t.Fatalf("count %d vs %d", out.Count, len(out.Data))
		}
		return out.Count
	}
	for input, want := range map[string]int{
		`{"query":"gateway"}`:                         4,
		`{"query":"gateway","include_archived":true}`: 5,
		`{"query":"gateway","limit":2}`:               2,
		`{"query":"gateway","limit":1000}`:            4,
		`{"query":"\"gateway retry\""}`:               4,
		`{"query":"nomatch"}`:                         0,
	} {
		if got := count(input); got != want {
			t.Fatalf("%s: %d hits, want %d", input, got, want)
		}
	}
	for _, bad := range []string{`{"query":""}`, `{"query":"  "}`, `{"query":"x","limit":-1}`, `{"query":"x","limit":1001}`} {
		h.Fails(rule.OpSearch, bad, errs.InvalidInput)
	}
}

// TestTagFilterCold: a tagged rule read back cold (every row decoded from
// its row file) still matches list --tag and keeps its tag_ids in --json;
// a retag on the cold App keeps the other tag.
func TestTagFilterCold(t *testing.T) {
	h := opstest.New(t)
	h.Must("tag.add", `{"name":"infra"}`)
	h.Must("tag.add", `{"name":"security"}`)
	id := add(t, h, `{"body":"cold","tags":["infra","security"]}`)
	c := h.ReopenCold()
	var out rule.ListOut
	if err := json.Unmarshal([]byte(c.JSON(c.Must(rule.OpList, `{"tags":["infra"]}`))), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data) != 1 || out.Data[0].ID != id || len(out.Data[0].TagIDs) != 2 {
		t.Fatalf("cold list --tag: %+v", out.Data)
	}
	c.Must(rule.OpEdit, `{"id":"`+id+`","untags":["infra"]}`)
	if err := json.Unmarshal([]byte(c.JSON(c.Must(rule.OpList, `{"tags":["security"]}`))), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data) != 1 || len(out.Data[0].TagIDs) != 1 {
		t.Fatalf("after cold untag: %+v", out.Data)
	}
}
