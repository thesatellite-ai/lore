package settings_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// rowDoc reads a row file of table under the repo's .lore/data as a JSON
// object of raw members.
func rowDoc(t *testing.T, h *opstest.H, table, id string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(filepath.Join(h.Repo, ".lore", "data", table, id+".json")))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("row file %s: %v", b, err)
	}
	return doc
}

// fieldRow finds the kind_fields row file whose name is field.
func fieldRow(t *testing.T, h *opstest.H, field string) map[string]json.RawMessage {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(h.Repo, ".lore", "data", schema.TableKindFields, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		id := strings.TrimSuffix(filepath.Base(p), ".json")
		doc := rowDoc(t, h, schema.TableKindFields, id)
		if string(doc["name"]) == `"`+field+`"` {
			return doc
		}
	}
	t.Fatalf("no kind_fields row named %s", field)
	return nil
}

// TestResourceCheckNote (OV32): check --note sets the note on one row and
// on every --where match; a later check without --note and a resource edit
// clear it; show prints "note:"; a row never given a note carries no
// check_note member at all (no null is written).
func TestResourceCheckNote(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"host"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"host","field":"provider","type":"text"}`},
	)
	ids := map[string]string{}
	for _, n := range []string{"h1", "h2", "h3"} {
		ids[n] = h.ID(h.MustYes(settings.OpResAdd, `{"kind":"host","name":"`+n+`","set":["provider=hetzner"]}`))
	}
	h.MustYes(settings.OpResEdit, `{"id":"h3","set":["provider=aws"]}`)
	note := func(n string) (string, bool) {
		raw, ok := rowDoc(t, h, schema.TableResources, ids[n])["check_note"]
		return string(raw), ok
	}

	// No note ever set: a plain check writes no check_note member.
	h.MustYes(settings.OpResCheck, `{"id":"h3"}`)
	if raw, ok := note("h3"); ok {
		t.Errorf("check without a note wrote check_note=%s", raw)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"h3"}`)); strings.Contains(s, "note:") {
		t.Errorf("show of an unnoted row %q", s)
	}

	h.MustYes(settings.OpResCheck, `{"id":"h1","note":"rebooted, disk ok ✓"}`)
	if raw, _ := note("h1"); raw != `"rebooted, disk ok ✓"` {
		t.Errorf("single note %s", raw)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"h1"}`)); !strings.Contains(s, "note: rebooted, disk ok ✓") {
		t.Errorf("show %q", s)
	}
	h.MustYes(settings.OpResCheck, `{"kind":"host","where":["provider=hetzner"],"note":"quarterly audit"}`)
	for _, n := range []string{"h1", "h2"} {
		if raw, _ := note(n); raw != `"quarterly audit"` {
			t.Errorf("bulk note on %s: %s", n, raw)
		}
	}
	if raw, ok := note("h3"); ok {
		t.Errorf("bulk note reached a non-match: %s", raw)
	}

	// A check without --note clears the previous note; so does an edit.
	h.MustYes(settings.OpResCheck, `{"id":"h1"}`)
	if raw, ok := note("h1"); ok && raw != "null" {
		t.Errorf("re-check kept note %s", raw)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"h1"}`)); strings.Contains(s, "note:") {
		t.Errorf("show after re-check %q", s)
	}
	h.MustYes(settings.OpResEdit, `{"id":"h2","set":["provider=ovh"]}`)
	if raw, ok := note("h2"); ok && raw != "null" {
		t.Errorf("edit kept note %s", raw)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"h2"}`)); strings.Contains(s, "note:") {
		t.Errorf("show after edit %q", s)
	}
}

// TestFieldLooseningStamps (RES 3.14, OV32): a confirmed loosening of a
// field (unmark sensitive/internal/strict, clear or change a pattern or
// bound) stamps changed_at and changed_by on the field row; a tightening
// and a plain label edit stamp nothing.
func TestFieldLooseningStamps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, add, edit string
		stamp           bool
	}{
		{"unmark sensitive", `"type":"text","sensitive":true`, `"no_sensitive":true`, true},
		{"unmark internal", `"type":"text","internal":true`, `"no_internal":true`, true},
		{"unmark strict", `"type":"text","strict":true`, `"no_strict":true`, true},
		{"raise max", `"type":"number","max":"10"`, `"max":"20"`, true},
		{"clear pattern", `"type":"text","pattern":"^a"`, `"clear":["pattern"]`, true},
		{"clear min", `"type":"number","min":"1"`, `"clear":["min"]`, true},
		{"mark sensitive (tighten)", `"type":"text"`, `"sensitive":true`, false},
		{"set a first max (tighten)", `"type":"number"`, `"max":"5"`, false},
		{"label only", `"type":"text","sensitive":true`, `"label":"Region"`, false},
		{"relabel (not a loosening)", `"type":"number","default":"3"`, `"label":"F"`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := opstest.New(t)
			seedActor(t, h)
			h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
			h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"f",`+c.add+`}`)
			edit := `{"kind":"host","field":"f",` + c.edit + `}`
			_, err := h.Run(settings.OpFieldEdit, edit)
			if asked := errs.Has(err, errs.NeedsConfirm); asked != c.stamp {
				t.Errorf("asked=%v (err %v), want %v", asked, err, c.stamp)
			}
			h.MustYes(settings.OpFieldEdit, edit)
			doc := fieldRow(t, h, "f")
			_, at := doc["changed_at"]
			by, hasBy := doc["changed_by"]
			if c.stamp {
				if !at || !hasBy || !strings.Contains(string(by), "act_") {
					t.Errorf("no stamp: changed_at %v changed_by %s", at, by)
				}
			} else if at || hasBy {
				t.Errorf("stamped a non-loosening: changed_at %v changed_by %s", at, by)
			}
		})
	}
}

// seedActor writes the harness actor's row into the repo store, as
// ensureActor does for a real identity (the harness has none).
func seedActor(t *testing.T, h *opstest.H) {
	t.Helper()
	id, err := ids.ParseAny(opstest.Actor)
	if err != nil {
		t.Fatal(err)
	}
	name, err := value.TextOf("tester")
	if err != nil {
		t.Fatal(err)
	}
	kind, err := value.TextOf(schema.ActorKindHuman)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]value.Value{"display_name": name, "kind": kind}
	if _, err := h.App.CreateWithID(context.Background(), store.SourceRepo, schema.TableActors, id, fields); err != nil {
		t.Fatal(err)
	}
}

// TestFieldLooseningNoActorRow: a caller with no actor row in any attached
// store (an embedder's fixed actor, no identity) can still confirm a
// loosening: changed_at is stamped and changed_by is left out, instead of
// the edit failing on a dangling actor ref.
func TestFieldLooseningNoActorRow(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"host"}`)
	h.MustYes(settings.OpFieldAdd, `{"kind":"host","field":"f","type":"text","sensitive":true}`)
	h.MustYes(settings.OpFieldEdit, `{"kind":"host","field":"f","no_sensitive":true}`)
	doc := fieldRow(t, h, "f")
	if _, ok := doc["changed_at"]; !ok {
		t.Error("no changed_at")
	}
	if by, ok := doc["changed_by"]; ok {
		t.Errorf("changed_by %s names an actor no store holds", by)
	}
}

// TestFieldEditClear (OV32): --clear removes each rule; clearing pattern,
// min or max asks first; --clear X with --X in one edit and an unknown
// name are refused; clearing a rule that is not set changes nothing.
func TestFieldEditClear(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"svc"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"svc","field":"port","type":"number","min":"1","max":"100","default":"8"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"svc","field":"code","type":"text","pattern":"^[A-Z]+$"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"svc","field":"repo","type":"text"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"svc","field":"git","type":"text","deprecated":true,"replaced_by":"repo"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"svc","field":"expires","type":"date","remind_before":"30d"}`},
	)
	for _, r := range []refusal{
		{settings.OpFieldEdit, `{"kind":"svc","field":"port","clear":["max"],"max":"5"}`, errs.InvalidInput, "--clear max and --max"},
		{settings.OpFieldEdit, `{"kind":"svc","field":"port","clear":["colour"]}`, errs.InvalidInput, "want one of"},
		{settings.OpFieldEdit, `{"kind":"svc","field":"port","clear":["MAX"]}`, errs.InvalidInput, "want one of"},
		{settings.OpFieldEdit, `{"kind":"svc","field":"port","clear":[""]}`, errs.InvalidInput, "want one of"},
	} {
		if _, err := h.RunYes(r.op, r.in); !errs.Has(err, r.code) || !strings.Contains(err.Error(), r.msg) {
			t.Errorf("%s: %v", r.in, err)
		}
	}
	for _, in := range []string{
		`{"kind":"svc","field":"port","clear":["min"]}`,
		`{"kind":"svc","field":"port","clear":["max"]}`,
		`{"kind":"svc","field":"code","clear":["pattern"]}`,
	} {
		if _, err := h.Run(settings.OpFieldEdit, in); !errs.Has(err, errs.NeedsConfirm) {
			t.Errorf("%s must ask: %v", in, err)
		}
	}
	// default, replaced_by and remind_before clear without asking.
	h.Must(settings.OpFieldEdit, `{"kind":"svc","field":"port","clear":["default"]}`)
	h.Must(settings.OpFieldEdit, `{"kind":"svc","field":"git","clear":["replaced_by"]}`)
	h.Must(settings.OpFieldEdit, `{"kind":"svc","field":"expires","clear":["remind_before"]}`)
	h.MustYes(settings.OpFieldEdit, `{"kind":"svc","field":"port","clear":["min","max"]}`)
	h.MustYes(settings.OpFieldEdit, `{"kind":"svc","field":"code","clear":["pattern"]}`)
	// Already clear: clearing again does not ask; it reports nothing to change.
	if msg := h.Fails(settings.OpFieldEdit, `{"kind":"svc","field":"port","clear":["min"]}`, errs.InvalidInput); !strings.Contains(msg, "nothing to change") {
		t.Errorf("clear of an unset rule: %s", msg)
	}
	for f, col := range map[string]string{"port": "min", "code": "pattern", "git": "replaced_by", "expires": "remind_before"} {
		if raw, ok := fieldRow(t, h, f)[col]; ok && string(raw) != "null" {
			t.Errorf("%s.%s still %s", f, col, raw)
		}
	}
	if raw, ok := fieldRow(t, h, "port")["default"]; ok && string(raw) != "null" {
		t.Errorf("port default still %s", raw)
	}
	// The rules are really gone: a value the bounds refused is accepted.
	got := opstest.Text(h.MustYes(settings.OpResAdd, `{"kind":"svc","name":"api","set":["port=500","code=lower"]}`))
	if strings.Contains(got, "default: port") {
		t.Errorf("cleared default still fills: %q", got)
	}
}

// TestDurationBounds (RES 3.1, OV32): a duration field takes --min/--max as
// durations; a bound that does not parse, or min above max, is refused at
// field add; resource add refuses a value outside the bounds and accepts
// the bounds themselves (inclusive).
func TestDurationBounds(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpKindAdd, `{"kind":"job"}`)
	for _, bad := range []string{
		`{"kind":"job","field":"b1","type":"duration","min":"soon"}`,
		`{"kind":"job","field":"b2","type":"duration","max":"12"}`,
		`{"kind":"job","field":"b3","type":"duration","min":"2h","max":"1h"}`,
	} {
		if _, err := h.RunYes(settings.OpFieldAdd, bad); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("%s: want E_INVALID_INPUT, got %v", bad, err)
		}
	}
	h.MustYes(settings.OpFieldAdd, `{"kind":"job","field":"timeout","type":"duration","min":"30s","max":"1h"}`)
	for i, c := range []struct {
		v  string
		ok bool
	}{{"30s", true}, {"1h", true}, {"45m", true}, {"29s", false}, {"1h0m1s", false}, {"2h", false}} {
		_, err := h.RunYes(settings.OpResAdd, `{"kind":"job","name":"j`+string(rune('a'+i))+`","set":["timeout=`+c.v+`"]}`)
		if c.ok && err != nil {
			t.Errorf("timeout=%s: %v", c.v, err)
		}
		if !c.ok && !errs.Has(err, errs.ResourceInvalid) {
			t.Errorf("timeout=%s: want E_RESOURCE_INVALID, got %v", c.v, err)
		}
	}
}

// TestRepointAcrossKinds: repoint from a row of one kind to a row of
// another is E_RESOURCE_KIND_MISMATCH naming both kinds, and writes
// nothing.
func TestRepointAcrossKinds(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"host"}`},
		[2]string{settings.OpKindAdd, `{"kind":"database"}`},
		[2]string{settings.OpKindAdd, `{"kind":"service"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"service","field":"runs_on","type":"ref","ref_kind":"host"}`},
	)
	h.MustYes(settings.OpResAdd, `{"kind":"host","name":"h1"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"database","name":"db1"}`)
	h.MustYes(settings.OpResAdd, `{"kind":"service","name":"api","set":["runs_on=h1"]}`)
	_, err := h.RunYes(settings.OpResRepoint, `{"old":"h1","new":"db1"}`)
	if !errs.Has(err, errs.ResourceKindMismatch) || !strings.Contains(err.Error(), "host") || !strings.Contains(err.Error(), "database") {
		t.Fatalf("repoint across kinds: %v", err)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"api"}`)); !strings.Contains(s, "runs_on = h1") {
		t.Errorf("refused repoint wrote: %q", s)
	}
	if s := opstest.Text(h.Must(settings.OpResShow, `{"id":"h1"}`)); strings.Contains(s, "archived") {
		t.Errorf("refused repoint archived the old row: %q", s)
	}
}
