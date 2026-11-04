package memory

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// fakeEnv is an in-memory Env: rows per store, search = substring match.
// It records the last write so tests can check what reached the engine.
type fakeEnv struct {
	rows    map[store.Source]map[string]row.Row
	order   []store.Source
	project ids.ID[ids.Project]
	next    int
	created map[string]value.Value
	patched map[string]value.Value
}

func newFakeEnv(t *testing.T) *fakeEnv {
	t.Helper()
	p, err := ids.Parse[ids.Project]("prj_01a1a5aca4ff7540bab22323785faffd")
	if err != nil {
		t.Fatal(err)
	}
	return &fakeEnv{
		rows:    map[store.Source]map[string]row.Row{store.SourceRepo: {}, "team_kb": {}},
		order:   []store.Source{store.SourceRepo, "team_kb"},
		project: p,
	}
}

// fakeIDs are valid memory ids handed out in order.
var fakeIDs = []string{
	"mem_01a1a5aca7f57659be7320c5f4b70011", "mem_01a1a5aca7f57659be7320c5f4b70012",
	"mem_01a1a5aca7f57659be7320c5f4b70013", "mem_01a1a5aca7f57659be7320c5f4b70014",
}

func (f *fakeEnv) Create(_ context.Context, target store.Source, table row.Table, fields map[string]value.Value) (engine.Written, error) {
	if target == "" {
		target = store.SourceRepo
	}
	if _, ok := f.rows[target]; !ok {
		return engine.Written{}, errs.New(errs.NotFound, "no store "+string(target))
	}
	id, err := ids.ParseAny(fakeIDs[f.next])
	if err != nil {
		return engine.Written{}, err
	}
	f.next++
	f.created = maps.Clone(fields)
	r := row.Row{Table: table, ID: id, HLC: hlc.HLC{Wall: time.Unix(1, 0)}, Fields: fields}
	f.rows[target][id.String()] = r
	return engine.Written{Row: r}, nil
}

func (f *fakeEnv) Update(_ context.Context, ref engine.Ref, patch map[string]value.Value) (engine.Written, error) {
	r := f.rows[ref.Store][ref.ID.String()]
	f.patched = maps.Clone(patch)
	next := maps.Clone(r.Fields)
	for k, v := range patch {
		if v.Kind() == value.KindNull {
			delete(next, k)
			continue
		}
		next[k] = v
	}
	r.Fields = next
	f.rows[ref.Store][ref.ID.String()] = r
	return engine.Written{Row: r}, nil
}

func (f *fakeEnv) Delete(_ context.Context, ref engine.Ref) (engine.Written, error) {
	src, r, err := f.Get(context.Background(), ref)
	if err != nil {
		return engine.Written{}, err
	}
	r.Deleted, r.Fields = true, nil
	f.rows[src][ref.ID.String()] = r
	return engine.Written{Row: r}, nil
}

func (f *fakeEnv) Get(_ context.Context, ref engine.Ref) (store.Source, row.Row, error) {
	for _, s := range f.order {
		if ref.Store != "" && ref.Store != s {
			continue
		}
		if r, ok := f.rows[s][ref.ID.String()]; ok {
			return s, r, nil
		}
	}
	return "", row.Row{}, errs.New(errs.NotFound, "no row "+ref.ID.String())
}

func (f *fakeEnv) List(_ context.Context, source store.Source, _ row.Table) ([]row.Row, error) {
	var out []row.Row
	for _, k := range slices.Sorted(maps.Keys(f.rows[source])) {
		if r := f.rows[source][k]; !r.Deleted {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeEnv) Stores() []store.Source { return f.order }

func (f *fakeEnv) Search(_ context.Context, _ row.Table, match string, limit int) ([]Hit, error) {
	var out []Hit
	for _, s := range f.order {
		for _, k := range slices.Sorted(maps.Keys(f.rows[s])) {
			r := f.rows[s][k]
			if !r.Deleted && strings.Contains(field(r, fieldBody), match) && len(out) < limit {
				out = append(out, Hit{Source: s, ID: r.ID})
			}
		}
	}
	return out, nil
}

func (f *fakeEnv) Project(context.Context) (ids.ID[ids.Project], error) { return f.project, nil }

// fakeRepo is the one repo of the fake project, mount name "web".
const (
	fakeRepo      = "rep_01a1a5aca7f57659be7320c5f4b70021"
	fakeRepoMount = "web"
)

func (f *fakeEnv) RepoID(_ context.Context, s string) (ids.ID[ids.Repo], error) {
	if s == fakeRepoMount || s == fakeRepo {
		return ids.Parse[ids.Repo](fakeRepo)
	}
	return ids.ID[ids.Repo]{}, errs.New(errs.NotFound, "no repo "+s)
}

// direct runs an operation without the engine's Run path: decode then
// Invoke, which is all a unit test of the operation needs.
type direct struct{}

func (direct) Run(ctx context.Context, e op.Entry, d op.Decoder, _ op.Caller) (op.Result, error) {
	in, err := e.Decode(d)
	if err != nil {
		return op.Result{}, err
	}
	return e.Invoke(ctx, in)
}

// call runs the named operation with a JSON input and decodes its output.
func call[Out any](t *testing.T, env *fakeEnv, name, input string) (Out, error) {
	t.Helper()
	reg, err := op.NewRegistry(Ops(env)...)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := reg.Lookup(name)
	if !ok {
		t.Fatalf("no op %s", name)
	}
	var out Out
	res, err := direct{}.Run(context.Background(), e, op.JSONDecoder{Body: []byte(input)}, op.Caller{})
	if err != nil {
		return out, err
	}
	b, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

func TestRegistryIsValid(t *testing.T) {
	if _, err := op.NewRegistry(Ops(newFakeEnv(t))...); err != nil {
		t.Fatal(err)
	}
}

// TestAddBodyForms: the text is given once, as --body or positionally;
// both, neither or blank are refused before anything is written.
func TestAddBodyForms(t *testing.T) {
	cases := []struct {
		name, input string
		code        errs.Code
	}{
		{"flag", `{"body":"use pgx"}`, ""},
		{"positional", `{"text":"use pgx"}`, ""},
		{"both", `{"body":"a","text":"b"}`, errs.InvalidInput},
		{"neither", `{}`, errs.EmptyBody},
		{"blank", `{"body":"  \n"}`, errs.EmptyBody},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newFakeEnv(t)
			_, err := call[WriteOut](t, env, OpAdd, c.input)
			if c.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errs.Has(err, c.code) || env.created != nil {
				t.Fatalf("want %s and no write, got %v (wrote %v)", c.code, err, env.created != nil)
			}
		})
	}
}

// TestAddDefaultsAndTarget: v0.1.17 defaults (kind retrieved, source
// manual), the project ref, tx_at, and --to choosing the store.
func TestAddDefaultsAndTarget(t *testing.T) {
	env := newFakeEnv(t)
	out, err := call[WriteOut](t, env, OpAdd, `{"body":"use pgx","to":"team_kb"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out.Store != "team_kb" || out.ID != fakeIDs[0] {
		t.Fatalf("out = %+v", out)
	}
	want := map[string]string{fieldKind: DefaultKind, fieldSourceKind: DefaultSource, fieldProject: env.project.String(), fieldBody: "use pgx"}
	for k, v := range want {
		if got := env.created[k]; got == nil || got.String() != v {
			t.Fatalf("%s = %v, want %s", k, got, v)
		}
	}
	if _, ok := env.created[fieldTxAt].(value.DateTime); !ok {
		t.Fatal("tx_at not stamped")
	}
	if _, has := env.created[fieldSourceRef]; has {
		t.Fatal("an empty source_ref must not be written")
	}
}

// TestListFiltersAndStores: every store is listed in order; archived rows
// only with --archived; --kind and --store filter.
func TestListFiltersAndStores(t *testing.T) {
	env := newFakeEnv(t)
	mustAdd(t, env, `{"body":"a","kind":"core"}`)
	mustAdd(t, env, `{"body":"b","to":"team_kb"}`)
	id := mustAdd(t, env, `{"body":"c"}`)
	if _, err := call[WriteOut](t, env, OpArchive, `{"id":"`+id+`"}`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		input string
		want  []string
	}{
		{`{}`, []string{"a", "b"}},
		{`{"archived":true}`, []string{"a", "c", "b"}},
		{`{"kind":"core"}`, []string{"a"}},
		{`{"store":"team_kb"}`, []string{"b"}},
	} {
		out, err := call[ListOut](t, env, OpList, c.input)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, it := range out.Data {
			got = append(got, it.Body)
		}
		if !slices.Equal(got, c.want) || out.Count != len(c.want) {
			t.Fatalf("%s: got %v, want %v", c.input, got, c.want)
		}
	}
}

// TestArchiveRoundTrip: archive sets archived_at, unarchive clears it
// (a Null patch), invalidate sets valid_until.
func TestArchiveRoundTrip(t *testing.T) {
	env := newFakeEnv(t)
	id := mustAdd(t, env, `{"body":"x"}`)
	for _, step := range []struct {
		op, field string
		null      bool
	}{{OpArchive, fieldArchivedAt, false}, {OpUnarchive, fieldArchivedAt, true}, {OpInvalidate, fieldValidUntil, false}} {
		if _, err := call[WriteOut](t, env, step.op, `{"id":"`+id+`"}`); err != nil {
			t.Fatal(err)
		}
		v := env.patched[step.field]
		if v == nil || (v.Kind() == value.KindNull) != step.null {
			t.Fatalf("%s: patch %v", step.op, env.patched)
		}
	}
	show, err := call[ShowOut](t, env, OpShow, `{"id":"`+id+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !show.Data.ArchivedAt.IsZero() || show.Data.ValidUntil.IsZero() {
		t.Fatalf("show = %+v", show.Data)
	}
}

// TestEditAndShowErrors: an edit with no field is refused; bad and
// unknown ids are E_INVALID_ID / E_NOT_FOUND; a deleted memory is not shown.
func TestEditAndShowErrors(t *testing.T) {
	env := newFakeEnv(t)
	id := mustAdd(t, env, `{"body":"x"}`)
	if _, err := call[WriteOut](t, env, OpEdit, `{"id":"`+id+`"}`); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("empty edit: %v", err)
	}
	if _, err := call[ShowOut](t, env, OpShow, `{"id":"rul_01a1a5aca7f57659be7320c5f4b70011"}`); !errs.Has(err, errs.InvalidID) {
		t.Fatalf("wrong prefix: %v", err)
	}
	if _, err := call[ShowOut](t, env, OpShow, `{"id":"`+fakeIDs[3]+`"}`); !errs.Has(err, errs.NotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	if _, err := call[WriteOut](t, env, OpEdit, `{"id":"`+id+`","body":"y","kind":"core"}`); err != nil {
		t.Fatal(err)
	}
	if env.patched[fieldBody].String() != "y" || env.patched[fieldKind].String() != "core" || len(env.patched) != 2 {
		t.Fatalf("patch = %v", env.patched)
	}
	if _, err := call[WriteOut](t, env, OpDelete, `{"id":"`+id+`"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := call[ShowOut](t, env, OpShow, `{"id":"`+id+`"}`); !errs.Has(err, errs.NotFound) {
		t.Fatalf("deleted: %v", err)
	}
}

// TestSearch: limits are bounded, archived hits are hidden unless asked,
// an empty query is refused.
func TestSearch(t *testing.T) {
	env := newFakeEnv(t)
	mustAdd(t, env, `{"body":"gateway retries"}`)
	id := mustAdd(t, env, `{"body":"gateway timeouts","to":"team_kb"}`)
	if _, err := call[WriteOut](t, env, OpArchive, `{"id":"`+id+`"}`); err != nil {
		t.Fatal(err)
	}
	out, err := call[SearchOut](t, env, OpSearch, `{"query":"gateway"}`)
	if err != nil || out.Count != 1 || out.Data[0].Store != "" {
		t.Fatalf("search = %+v %v", out, err)
	}
	if out, err = call[SearchOut](t, env, OpSearch, `{"query":"gateway","include_archived":true}`); err != nil || out.Count != 2 {
		t.Fatalf("archived = %+v %v", out, err)
	}
	for _, bad := range []string{`{"query":" "}`, `{"query":"x","limit":-1}`, `{"query":"x","limit":1001}`} {
		if _, err := call[SearchOut](t, env, OpSearch, bad); !errs.Has(err, errs.InvalidInput) {
			t.Fatalf("%s: %v", bad, err)
		}
	}
}

func mustAdd(t *testing.T, env *fakeEnv, input string) string {
	t.Helper()
	out, err := call[WriteOut](t, env, OpAdd, input)
	if err != nil {
		t.Fatal(err)
	}
	return out.ID
}

func mustText(t *testing.T, s string) value.Text {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestRepoScope is v0.1.17's repo scope matrix: a project-wide memory and
// one scoped to repo web, read with every flag combination.
func TestRepoScope(t *testing.T) {
	env := newFakeEnv(t)
	mustAdd(t, env, `{"body":"master"}`)
	mustAdd(t, env, `{"body":"web only","repo":"web"}`)
	if env.created[fieldRepo].String() != fakeRepo {
		t.Fatalf("repo_id = %v", env.created[fieldRepo])
	}
	for _, c := range []struct {
		input string
		want  []string
	}{
		{`{}`, []string{"master"}},
		{`{"repo":"web"}`, []string{"master", "web only"}},
		{`{"repo":"` + fakeRepo + `","no_inherit":true}`, []string{"web only"}},
		{`{"no_inherit":true}`, []string{"master"}},
		{`{"repo":"web","master_only":true}`, []string{"master"}},
		{`{"all_repos":true}`, []string{"master", "web only"}},
	} {
		for _, name := range []string{OpList, OpSearch} {
			input := c.input
			if name == OpSearch {
				input = strings.Replace(input, "{", `{"query":"`+"e"+`",`, 1)
				input = strings.Replace(input, ",}", "}", 1)
			}
			var got []string
			if name == OpList {
				out, err := call[ListOut](t, env, name, input)
				if err != nil {
					t.Fatal(err)
				}
				for _, it := range out.Data {
					got = append(got, it.Body)
				}
			} else {
				out, err := call[SearchOut](t, env, name, input)
				if err != nil {
					t.Fatal(err)
				}
				for _, it := range out.Data {
					got = append(got, it.Row.Body)
				}
			}
			slices.Sort(got)
			if !slices.Equal(got, c.want) {
				t.Fatalf("%s %s: got %v, want %v", name, input, got, c.want)
			}
		}
	}
	if _, err := call[ListOut](t, env, OpList, `{"repo":"api"}`); !errs.Has(err, errs.NotFound) {
		t.Fatalf("unknown repo: %v", err)
	}
	if _, err := call[WriteOut](t, env, OpAdd, `{"body":"x","repo":"api"}`); !errs.Has(err, errs.NotFound) {
		t.Fatalf("add to unknown repo: %v", err)
	}
}
