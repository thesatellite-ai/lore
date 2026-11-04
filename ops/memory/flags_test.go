package memory

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestSupersedesRefusals (later.md memparity): --supersedes refuses an
// unknown, deleted, other-project or already-replaced memory and a
// malformed id, before anything is written; v0.1.17's messages.
func TestSupersedesRefusals(t *testing.T) {
	env := newFakeEnv(t)
	old := mustAdd(t, env, `{"body":"v1"}`)
	next := mustAdd(t, env, `{"body":"v2","supersedes":"`+old+`"}`)
	if got := env.rows[store.SourceRepo][old].Fields[fieldSuperseded]; got == nil || got.String() != next {
		t.Fatalf("old superseded_by = %v, want %s", got, next)
	}
	gone := mustAdd(t, env, `{"body":"gone"}`)
	if _, err := call[WriteOut](t, env, OpDelete, `{"id":"`+gone+`"}`); err != nil {
		t.Fatal(err)
	}
	foreign := mustAdd(t, env, `{"body":"foreign"}`)
	other, err := ids.ParseAny("prj_01a1a5aca4ff7540bab22323785fa000")
	if err != nil {
		t.Fatal(err)
	}
	r := env.rows[store.SourceRepo][foreign]
	if r.Fields[fieldProject], err = value.RefOf(other); err != nil {
		t.Fatal(err)
	}
	env.rows[store.SourceRepo][foreign] = r
	for _, c := range []struct {
		raw, msg string
		code     errs.Code
	}{
		{old, "that memory was already replaced by " + next, errs.InvalidInput},
		{gone, "no memory with that id in this project", errs.NotFound},
		{foreign, "no memory with that id in this project", errs.NotFound},
		{fakeIDs[7], "no memory with that id in this project", errs.NotFound},
		{"nope", "", errs.InvalidID},
		{"rul_01a1a5aca7f57659be7320c5f4b70011", "", errs.InvalidID},
	} {
		n := env.next
		_, err := call[WriteOut](t, env, OpAdd, `{"body":"x","supersedes":"`+c.raw+`"}`)
		if !errs.Has(err, c.code) || !strings.Contains(err.Error(), c.msg) {
			t.Fatalf("%s: %v, want %s %q", c.raw, err, c.code, c.msg)
		}
		if env.next != n {
			t.Fatalf("%s: written before the refusal", c.raw)
		}
	}
}

// TestSupersedesRollback: marking the old memory fails → the new one is
// tombstoned again and the marking error's code returned; if that delete
// fails as well, the hint names the left-over memory.
func TestSupersedesRollback(t *testing.T) {
	env := newFakeEnv(t)
	old := mustAdd(t, env, `{"body":"v1"}`)
	env.failUpdate = errs.New(errs.Stale, "changed underneath")
	_, err := call[WriteOut](t, env, OpAdd, `{"body":"v2","supersedes":"`+old+`"}`)
	if !errs.Has(err, errs.Stale) || !strings.Contains(err.Error(), "nothing was kept") {
		t.Fatalf("rollback: %v", err)
	}
	if !slices.Contains(env.deleted, fakeIDs[1]) {
		t.Fatalf("new memory not removed: %v", env.deleted)
	}
	env.failDelete = errs.New(errs.StoreLocked, "locked")
	_, err = call[WriteOut](t, env, OpAdd, `{"body":"v3","supersedes":"`+old+`"}`)
	var e *errs.Error
	if !errs.Has(err, errs.Stale) || !errors.As(err, &e) || !strings.Contains(e.Hint, "lore memory delete "+fakeIDs[2]) {
		t.Fatalf("double failure: %v", err)
	}
}

// TestActorFlags: --created-by / --validated-by take act_ ids only; a
// valid one is written as a ref. (v0.1.17 also looked the actor up in
// lore.db and answered E_NOT_FOUND; 0.1.18 checks the id's form here and
// leaves existence to the engine's ref check.)
func TestActorFlags(t *testing.T) {
	const actor = "act_0e45506e64357c94a87e70ef1c6bbba9"
	env := newFakeEnv(t)
	mustAdd(t, env, `{"body":"x","created_by":"`+actor+`","validated_by":" `+actor+` "}`)
	for _, f := range []string{fieldCreatedBy, fieldValidBy} {
		if v := env.created[f]; v == nil || v.Kind() != value.KindRef || v.String() != actor {
			t.Fatalf("%s = %v", f, v)
		}
	}
	for _, in := range []string{
		`{"body":"x","created_by":"nope"}`,
		`{"body":"x","validated_by":"nope"}`,
		`{"body":"x","created_by":"mem_01a1a5aca7f57659be7320c5f4b70011"}`,
		`{"body":"x","validated_by":"` + strings.Repeat("é", 40) + `"}`,
	} {
		n := env.next
		_, err := call[WriteOut](t, env, OpAdd, in)
		if !errs.Has(err, errs.InvalidID) || env.next != n || !utf8.ValidString(err.Error()) {
			t.Fatalf("%s: %v", in, err)
		}
	}
}

// TestProjectFlag: --project by id or name scopes add, list and search;
// an unknown project is E_NOT_FOUND; without --project every project's
// rows are listed (v0.1.17 appended other stores' rows unfiltered).
func TestProjectFlag(t *testing.T) {
	env := newFakeEnv(t)
	proj := map[string]value.Value{fieldName: mustText(t, "demo")}
	pid, err := ids.ParseAny(env.project.String())
	if err != nil {
		t.Fatal(err)
	}
	env.rows[store.SourceRepo][pid.String()] = row.Row{Table: schema.TableProjects, ID: pid, Fields: proj}
	mustAdd(t, env, `{"body":"mine","project":"demo"}`)
	other := mustAdd(t, env, `{"body":"theirs"}`)
	r := env.rows[store.SourceRepo][other]
	op, err := ids.ParseAny("prj_01a1a5aca4ff7540bab22323785fa000")
	if err != nil {
		t.Fatal(err)
	}
	if r.Fields[fieldProject], err = value.RefOf(op); err != nil {
		t.Fatal(err)
	}
	env.rows[store.SourceRepo][other] = r
	for _, c := range []struct {
		input string
		want  []string
	}{
		{`{}`, []string{"mine", "theirs"}},
		{`{"project":"demo"}`, []string{"mine"}},
		{`{"project":"` + env.project.String() + `"}`, []string{"mine"}},
	} {
		out, err := call[ListOut](t, env, OpList, c.input)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, it := range out.Data {
			got = append(got, it.Body)
		}
		slices.Sort(got)
		if !slices.Equal(got, c.want) {
			t.Fatalf("%s: %v, want %v", c.input, got, c.want)
		}
	}
	for _, name := range []string{OpList, OpAdd} {
		if _, err := call[WriteOut](t, env, name, `{"body":"x","project":"nope"}`); name == OpAdd && !errs.Has(err, errs.NotFound) {
			t.Fatalf("%s unknown project: %v", name, err)
		}
	}
	if _, err := call[ListOut](t, env, OpList, `{"project":"nope"}`); !errs.Has(err, errs.NotFound) {
		t.Fatalf("list unknown project: %v", err)
	}
}

// TestRebind: --rebind-repo sets repo_id, --rebind-master clears it, both
// together are refused, an unknown repo is E_NOT_FOUND.
func TestRebind(t *testing.T) {
	env := newFakeEnv(t)
	id := mustAdd(t, env, `{"body":"x"}`)
	if _, err := call[WriteOut](t, env, OpEdit, `{"id":"`+id+`","rebind_repo":"web"}`); err != nil {
		t.Fatal(err)
	}
	if v := env.rows[store.SourceRepo][id].Fields[fieldRepo]; v == nil || v.String() != fakeRepo {
		t.Fatalf("repo_id = %v", v)
	}
	if _, err := call[WriteOut](t, env, OpEdit, `{"id":"`+id+`","rebind_master":true}`); err != nil {
		t.Fatal(err)
	}
	if _, has := env.rows[store.SourceRepo][id].Fields[fieldRepo]; has {
		t.Fatal("rebind-master must clear repo_id")
	}
	if _, err := call[WriteOut](t, env, OpEdit, `{"id":"`+id+`","rebind_master":true,"rebind_repo":"web"}`); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("both: %v", err)
	}
	if _, err := call[WriteOut](t, env, OpEdit, `{"id":"`+id+`","rebind_repo":"api"}`); !errs.Has(err, errs.NotFound) {
		t.Fatalf("unknown repo: %v", err)
	}
}

// TestSearchLimitAfterScope: --limit counts only hits that pass the scope
// filter (v0.1.17 filtered in SQL before LIMIT), so repo-scoped rows ahead
// of the one project-wide hit never starve it.
func TestSearchLimitAfterScope(t *testing.T) {
	env := newFakeEnv(t)
	for range 3 {
		mustAdd(t, env, `{"body":"gateway web","repo":"web"}`)
	}
	want := mustAdd(t, env, `{"body":"gateway master"}`)
	out, err := call[SearchOut](t, env, OpSearch, `{"query":"gateway","limit":1}`)
	if err != nil || out.Count != 1 || out.Data[0].ID != want {
		t.Fatalf("%+v %v", out, err)
	}
	if out, err = call[SearchOut](t, env, OpSearch, `{"query":"gateway","all_repos":true,"limit":2}`); err != nil || out.Count != 2 {
		t.Fatalf("all repos: %+v %v", out, err)
	}
}

// TestMoveNeedsMover: an Env without engine Move answers E_UNSUPPORTED
// instead of pretending.
func TestMoveNeedsMover(t *testing.T) {
	env := newFakeEnv(t)
	id := mustAdd(t, env, `{"body":"x"}`)
	if _, err := call[WriteOut](t, env, OpMove, `{"id":"`+id+`","to":"team_kb"}`); !errs.Has(err, errs.Unsupported) {
		t.Fatalf("move: %v", err)
	}
}

// TestQuoteIDNeverSplitsARune: a long bad id is cut on a character
// boundary in the error.
func TestQuoteIDNeverSplitsARune(t *testing.T) {
	for _, s := range []string{strings.Repeat("x", 100), strings.Repeat("é", 40), strings.Repeat("🙂", 20), "short"} {
		q := quoteID(s)
		if !utf8.ValidString(q) || len(q) > maxQuotedID+len(ellipsis)+2 {
			t.Fatalf("quoteID(%d bytes) = %q", len(s), q)
		}
	}
}
