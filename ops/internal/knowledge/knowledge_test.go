package knowledge

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// fakeEnv is an in-memory Env with failure injection on Update / Delete,
// for the paths a real engine cannot be made to fail on cue (the supersede
// rollback).
type fakeEnv struct {
	rows       map[store.Source]map[string]row.Row
	order      []store.Source
	project    ids.ID[ids.Project]
	next       int
	failUpdate error
	failDelete error
	deleted    []string
}

const (
	testProject  = "prj_01a1a5aca4ff7540bab22323785faffd"
	otherProject = "prj_01a1a5aca4ff7540bab22323785fa000"
)

// fakeRuleIDs are valid rule ids handed out in order.
var fakeRuleIDs = []string{
	"rul_01a1a5aca7f57659be7320c5f4b70011", "rul_01a1a5aca7f57659be7320c5f4b70012",
	"rul_01a1a5aca7f57659be7320c5f4b70013", "rul_01a1a5aca7f57659be7320c5f4b70014",
	"rul_01a1a5aca7f57659be7320c5f4b70015",
}

var rules = Noun{Name: "rule", Table: schema.TableRules, Prefix: ids.PrefixRule}

func newEnv(t *testing.T) *fakeEnv {
	t.Helper()
	p, err := ids.Parse[ids.Project](testProject)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeEnv{
		rows:    map[store.Source]map[string]row.Row{store.SourceRepo: {}, "team": {}},
		order:   []store.Source{store.SourceRepo, "team"},
		project: p,
	}
}

func (f *fakeEnv) Create(_ context.Context, target store.Source, table row.Table, fields map[string]value.Value) (engine.Written, error) {
	if target == "" {
		target = store.SourceRepo
	}
	id, err := ids.ParseAny(fakeRuleIDs[f.next])
	if err != nil {
		return engine.Written{}, err
	}
	f.next++
	r := row.Row{Table: table, ID: id, HLC: hlc.HLC{Wall: time.Unix(1, 0)}, Fields: maps.Clone(fields)}
	f.rows[target][id.String()] = r
	return engine.Written{Row: r}, nil
}

func (f *fakeEnv) Update(_ context.Context, ref engine.Ref, patch map[string]value.Value) (engine.Written, error) {
	if f.failUpdate != nil {
		return engine.Written{}, f.failUpdate
	}
	r := f.rows[ref.Store][ref.ID.String()]
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

func (f *fakeEnv) Delete(ctx context.Context, ref engine.Ref) (engine.Written, error) {
	if f.failDelete != nil {
		return engine.Written{}, f.failDelete
	}
	src, r, err := f.Get(ctx, ref)
	if err != nil {
		return engine.Written{}, err
	}
	r.Deleted, r.Fields = true, nil
	f.rows[src][ref.ID.String()] = r
	f.deleted = append(f.deleted, ref.ID.String())
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

func (f *fakeEnv) List(_ context.Context, source store.Source, table row.Table) ([]row.Row, error) {
	var out []row.Row
	for _, k := range slices.Sorted(maps.Keys(f.rows[source])) {
		if r := f.rows[source][k]; r.Table == table {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeEnv) Stores() []store.Source { return f.order }

func (f *fakeEnv) Search(_ context.Context, table row.Table, match string, limit int) ([]Hit, error) {
	var out []Hit
	for _, s := range f.order {
		for _, k := range slices.Sorted(maps.Keys(f.rows[s])) {
			r := f.rows[s][k]
			if r.Table == table && !r.Deleted && strings.Contains(Field(r, FieldBody), match) && len(out) < limit {
				out = append(out, Hit{Source: s, ID: r.ID})
			}
		}
	}
	return out, nil
}

func (f *fakeEnv) Project(context.Context) (ids.ID[ids.Project], error) { return f.project, nil }

func (f *fakeEnv) RepoID(_ context.Context, s string) (ids.ID[ids.Repo], error) {
	if s == "web" {
		return ids.Parse[ids.Repo]("rep_01a1a5aca7f57659be7320c5f4b70021")
	}
	return ids.ID[ids.Repo]{}, errs.New(errs.NotFound, "no repo "+s)
}

// TestNounID: only a well-formed id of the noun's own prefix parses; the
// message names the noun and the value.
func TestNounID(t *testing.T) {
	for _, c := range []struct {
		in string
		ok bool
	}{
		{"rul_01a1a5aca7f57659be7320c5f4b70011", true},
		{"  rul_01a1a5aca7f57659be7320c5f4b70011\n", true},
		{"dec_01a1a5aca7f57659be7320c5f4b70011", false},
		{"rul_", false},
		{"rul_01a1a5aca7f57659be7320c5f4b7001", false},
		{"RUL_01A1A5ACA7F57659BE7320C5F4B70011", false},
		{"", false},
		{"nope", false},
		{"rül_01a1a5aca7f57659be7320c5f4b70011", false},
	} {
		id, err := rules.ID(c.in)
		if c.ok {
			if err != nil || id.Prefix() != ids.PrefixRule {
				t.Fatalf("%q: %v", c.in, err)
			}
			continue
		}
		if !errs.Has(err, errs.NotFound) || !strings.Contains(err.Error(), "is not a rule id (rul_…)") {
			t.Fatalf("%q: %v", c.in, err)
		}
	}
}

// TestCut is v0.1.17's trunc: at most n characters, "..." included.
func TestCut(t *testing.T) {
	for _, c := range []struct {
		s    string
		n    int
		want string
	}{
		{"", 60, ""},
		{"short", 60, "short"},
		{strings.Repeat("a", 60), 60, strings.Repeat("a", 60)},
		{strings.Repeat("a", 61), 60, strings.Repeat("a", 57) + "..."},
		{strings.Repeat("b", 200), 80, strings.Repeat("b", 77) + "..."},
		// é is 2 bytes: 57 bytes would split the 29th, so 28 are kept.
		{strings.Repeat("é", 40), 60, strings.Repeat("é", 28) + "..."},
		{"日本語" + strings.Repeat("x", 70), 60, "日本語" + strings.Repeat("x", 48) + "..."},
	} {
		if got := Cut(c.s, c.n); got != c.want || len(got) > c.n || !utf8.ValidString(got) {
			t.Fatalf("Cut(%d chars, %d) = %q", len(c.s), c.n, got)
		}
	}
}

// TestScopePrecedence is v0.1.17's repo_scope precedence over every flag
// combination, and which rows (project-wide, this repo, another repo) each
// scope keeps.
func TestScopePrecedence(t *testing.T) {
	const repo, other = "rep_a", "rep_b"
	for _, c := range []struct {
		repo                          string
		noInherit, masterOnly, allRep bool
		want                          scope
		keeps                         [3]bool // master row, repo row, other repo row
	}{
		{"", false, false, false, scopeMaster, [3]bool{true, false, false}},
		{repo, false, false, false, scopeInherit, [3]bool{true, true, false}},
		{repo, true, false, false, scopeRepoOnly, [3]bool{false, true, false}},
		{"", true, false, false, scopeMaster, [3]bool{true, false, false}},
		{repo, true, true, false, scopeMaster, [3]bool{true, false, false}},
		{repo, true, true, true, scopeAll, [3]bool{true, true, true}},
		{"", false, false, true, scopeAll, [3]bool{true, true, true}},
		{"", false, true, false, scopeMaster, [3]bool{true, false, false}},
	} {
		s := scopeOf(c.repo, c.noInherit, c.masterOnly, c.allRep)
		if s != c.want {
			t.Fatalf("%+v: scope %s", c, s)
		}
		got := [3]bool{s.keeps("", c.repo), s.keeps(repo, c.repo), s.keeps(other, c.repo)}
		if got != c.keeps {
			t.Fatalf("%+v: keeps %v", c, got)
		}
	}
}

// TestInputHelpers: Body (once, not blank), Required, OneOf, Quote, Or and
// the store labels.
func TestInputHelpers(t *testing.T) {
	for _, c := range []struct {
		flag, arg string
		code      errs.Code
	}{
		{"x", "", ""}, {"", "x", ""}, {"x", "y", errs.InvalidInput}, {"", "", errs.EmptyBody}, {" \t\n", "", errs.EmptyBody},
	} {
		b, err := Body(rules, c.flag, c.arg)
		if c.code == "" && (err != nil || b != c.flag+c.arg) || c.code != "" && !errs.Has(err, c.code) {
			t.Fatalf("Body(%q,%q) = %q %v", c.flag, c.arg, b, err)
		}
	}
	if err := Required(rules, "title", " "); !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "--title is required") {
		t.Fatal(err)
	}
	if err := Required(rules, "title", "t"); err != nil {
		t.Fatal(err)
	}
	if OneOf(rules, "severity", "", []string{"must"}) != nil || OneOf(rules, "severity", "must", []string{"must"}) != nil {
		t.Fatal("OneOf refused an allowed value")
	}
	if err := OneOf(rules, "severity", "Must", []string{"must", "may"}); !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "must | may") {
		t.Fatal(err)
	}
	if q := Quote(strings.Repeat("x", 100)); q != `"`+strings.Repeat("x", maxQuoted)+`..."` {
		t.Fatalf("Quote %s", q)
	}
	if q := Quote(strings.Repeat("✓", 30)); !utf8.ValidString(q) || q != `"`+strings.Repeat("✓", 16)+`..."` {
		t.Fatalf("Quote unicode %s", q)
	}
	if Or("", "d") != "d" || Or("v", "d") != "v" {
		t.Fatal("Or")
	}
	for src, want := range map[string][2]string{"": {"", ""}, "repo": {"", ""}, "team": {" [team]", "[team]"}, "me/notes": {" [me/notes]", "[me/notes]"}} {
		if StoreLabel(src) != want[0] || AddLabel(src) != want[1] {
			t.Fatalf("labels of %q: %q %q", src, StoreLabel(src), AddLabel(src))
		}
	}
	if ScopeLabel("") != "master" || ScopeLabel("web") != "repo:web" {
		t.Fatal("ScopeLabel")
	}
	if SourceKey(store.SourceRepo) != "" || SourceKey("team") != "team" {
		t.Fatal("SourceKey")
	}
}

func add(t *testing.T, env *fakeEnv, c Create) string {
	t.Helper()
	if c.Texts == nil {
		c.Texts = map[string]string{FieldBody: "b"}
	}
	_, w, err := Add(context.Background(), env, rules, c)
	if err != nil {
		t.Fatal(err)
	}
	return w.Row.ID.String()
}

// TestSupersedesRefusals: unknown, deleted, foreign-project and already
// replaced rows are refused before anything is written.
func TestSupersedesRefusals(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	old := add(t, env, Create{})
	add(t, env, Create{Supersedes: old})
	gone := add(t, env, Create{})
	if _, err := Delete(ctx, env, rules, gone); err != nil {
		t.Fatal(err)
	}
	foreign := add(t, env, Create{})
	r := env.rows[store.SourceRepo][foreign]
	pv, err := ids.ParseAny(otherProject)
	if err != nil {
		t.Fatal(err)
	}
	if r.Fields[FieldProject], err = value.RefOf(pv); err != nil {
		t.Fatal(err)
	}
	env.rows[store.SourceRepo][foreign] = r
	for _, c := range []struct {
		raw  string
		code errs.Code
	}{
		{old, errs.InvalidInput},
		{gone, errs.NotFound},
		{foreign, errs.NotFound},
		{"rul_01a1a5aca7f57659be7320c5f4b70099", errs.NotFound},
		{"dec_01a1a5aca7f57659be7320c5f4b70011", errs.NotFound},
		{"rul_zz", errs.NotFound},
		{"nope", errs.NotFound},
	} {
		n := env.next
		if _, _, err := Add(ctx, env, rules, Create{Texts: map[string]string{FieldBody: "x"}, Supersedes: c.raw}); !errs.Has(err, c.code) {
			t.Fatalf("%s: %v, want %s", c.raw, err, c.code)
		}
		if env.next != n {
			t.Fatalf("%s: a row was written before the refusal", c.raw)
		}
	}
}

// TestSupersedesRollback: when marking the old row fails, the new row is
// deleted again and the marking error's code returned; when that delete
// fails too, the hint names the left-over row so a person can remove it.
func TestSupersedesRollback(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	old := add(t, env, Create{})
	env.failUpdate = errs.New(errs.Stale, "changed underneath")
	_, _, err := Add(ctx, env, rules, Create{Texts: map[string]string{FieldBody: "v2"}, Supersedes: old})
	if !errs.Has(err, errs.Stale) || !strings.Contains(err.Error(), "nothing was kept") {
		t.Fatalf("rollback: %v", err)
	}
	newID := fakeRuleIDs[1]
	if !slices.Contains(env.deleted, newID) || Field(env.rows[store.SourceRepo][old], FieldSuperseded) != "" {
		t.Fatalf("new row not removed (deleted %v) or old marked", env.deleted)
	}
	env.failDelete = errs.New(errs.StoreLocked, "locked")
	_, _, err = Add(ctx, env, rules, Create{Texts: map[string]string{FieldBody: "v3"}, Supersedes: old})
	if !errs.Has(err, errs.Stale) {
		t.Fatalf("double failure: %v", err)
	}
	var x *errs.Error
	if !errors.As(err, &x) || !strings.Contains(x.Hint, "lore rule delete "+fakeRuleIDs[2]) {
		t.Fatalf("hint does not name the left-over row: %v", err)
	}
}

// TestSupersedesAcrossStores: the old row is marked in the store that holds
// it, and SupersededPatch rides along (a decision's status).
func TestSupersedesAcrossStores(t *testing.T) {
	env := newEnv(t)
	old := add(t, env, Create{To: "team"})
	st, err := value.TextOf("superseded")
	if err != nil {
		t.Fatal(err)
	}
	src, w, err := Add(context.Background(), env, rules, Create{Texts: map[string]string{FieldBody: "v2"}, Supersedes: old, SupersededPatch: map[string]value.Value{"status": st}})
	if err != nil || src != store.SourceRepo {
		t.Fatalf("%s %v", src, err)
	}
	r := env.rows["team"][old]
	if Field(r, FieldSuperseded) != w.Row.ID.String() || Field(r, "status") != "superseded" {
		t.Fatalf("old row %v", r.Fields)
	}
}

// TestAddFields: defaults (source manual, trust 0.5, project), repo and
// created-by refs, empty texts not written.
func TestAddFields(t *testing.T) {
	env := newEnv(t)
	id := add(t, env, Create{Repo: "web", CreatedBy: "act_0e45506e64357c94a87e70ef1c6bbba9", Texts: map[string]string{FieldBody: "b", FieldSourceRef: ""}})
	r := env.rows[store.SourceRepo][id]
	want := map[string]string{FieldSourceKind: DefaultSource, FieldProject: testProject, FieldRepo: "rep_01a1a5aca7f57659be7320c5f4b70021", FieldCreatedBy: "act_0e45506e64357c94a87e70ef1c6bbba9"}
	for k, v := range want {
		if Field(r, k) != v {
			t.Fatalf("%s = %q, want %q", k, Field(r, k), v)
		}
	}
	if Number(r, FieldTrust) != DefaultTrust {
		t.Fatalf("trust %v", Number(r, FieldTrust))
	}
	if _, has := r.Fields[FieldSourceRef]; has {
		t.Fatal("an empty source_ref was written")
	}
	for _, c := range []Create{{Repo: "api"}, {CreatedBy: "nope"}, {CreatedBy: "rul_01a1a5aca7f57659be7320c5f4b70011"}, {Project: "nope"}} {
		c.Texts = map[string]string{FieldBody: "x"}
		if _, _, err := Add(context.Background(), env, rules, c); err == nil {
			t.Fatalf("%+v accepted", c)
		}
	}
}

// TestListOrderAndFilters: id order within each store, stores in
// precedence order, archived and deleted rows dropped unless asked, one
// store only, and a keep func.
func TestListOrderAndFilters(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	a := add(t, env, Create{})
	b := add(t, env, Create{To: "team"})
	c := add(t, env, Create{})
	d := add(t, env, Create{})
	if _, err := Stamp(ctx, env, rules, c, FieldArchivedAt, Now(env)); err != nil {
		t.Fatal(err)
	}
	if _, err := Delete(ctx, env, rules, d); err != nil {
		t.Fatal(err)
	}
	ids := func(items []Item) []string {
		var out []string
		for _, it := range items {
			out = append(out, it.Row.ID.String())
		}
		return out
	}
	for _, x := range []struct {
		archived bool
		only     store.Source
		keep     func(row.Row) bool
		want     []string
	}{
		{false, "", nil, []string{a, b}},
		{true, "", nil, []string{a, c, b}},
		{true, "team", nil, []string{b}},
		{true, "", func(r row.Row) bool { return r.ID.String() != a }, []string{c, b}},
	} {
		items, err := List(ctx, env, rules, Scope{}, x.archived, x.only, x.keep)
		if err != nil {
			t.Fatal(err)
		}
		if got := ids(items); !slices.Equal(got, x.want) {
			t.Fatalf("archived=%v only=%q: %v, want %v", x.archived, x.only, got, x.want)
		}
	}
}

// TestSearchCutsAfterFilter: --limit counts only hits that survive the
// scope / archived filter (v0.1.17 filtered in SQL before LIMIT).
func TestSearchCutsAfterFilter(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	for range 3 {
		id := add(t, env, Create{Texts: map[string]string{FieldBody: "gateway"}})
		if _, err := Stamp(ctx, env, rules, id, FieldArchivedAt, Now(env)); err != nil {
			t.Fatal(err)
		}
	}
	keep := add(t, env, Create{Texts: map[string]string{FieldBody: "gateway"}})
	found, err := Search(ctx, env, rules, "gateway", nil, 1, Scope{}, false)
	if err != nil || len(found) != 1 || found[0].Row.ID.String() != keep {
		t.Fatalf("%v %v", found, err)
	}
	if found, err = Search(ctx, env, rules, "gateway", nil, 0, Scope{}, true); err != nil || len(found) != 4 {
		t.Fatalf("archived: %d %v", len(found), err)
	}
	if _, err := Search(ctx, env, rules, "gateway", []string{"body"}, 0, Scope{}, false); !errs.Has(err, errs.Unsupported) {
		t.Fatalf("--column without a column search: %v", err)
	}
	if _, err := Search(ctx, env, rules, "gateway", []string{"nosuch"}, 0, Scope{}, false); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("--column unknown field: %v", err)
	}
}

// TestRetag: tags added and dropped by name; a dropped tag's legacy id
// goes too; adding an existing tag is a no-op; TagList of none is Null.
func TestRetag(t *testing.T) {
	ctx := context.Background()
	env := newEnv(t)
	mkTag := func(name string) string {
		id, err := engine.NaturalID(ids.PrefixTag, testProject, name)
		if err != nil {
			t.Fatal(err)
		}
		fields := map[string]value.Value{}
		for k, v := range map[string]string{"project_id": testProject, "name": name} {
			if fields[k], err = value.TextOf(v); err != nil {
				t.Fatal(err)
			}
		}
		env.rows[store.SourceRepo][id.String()] = row.Row{Table: schema.TableTags, ID: id, Fields: fields}
		return id.String()
	}
	infra, sec := mkTag("infra"), mkTag("security")
	id := add(t, env, Create{Tags: []string{"infra", "infra"}})
	if got := TagsOf(env.rows[store.SourceRepo][id]); !slices.Equal(got, []string{infra}) {
		t.Fatalf("add tags %v", got)
	}
	if _, _, err := Apply(ctx, env, rules, id, Edit{Tags: []string{"security", "infra"}, Untags: []string{"infra"}}); err != nil {
		t.Fatal(err)
	}
	if got := TagsOf(env.rows[store.SourceRepo][id]); !slices.Equal(got, []string{sec}) {
		t.Fatalf("retag %v", got)
	}
	keep, err := HasTags(ctx, env, []string{"security"})
	if err != nil || !keep(env.rows[store.SourceRepo][id]) {
		t.Fatalf("HasTags %v", err)
	}
	if _, _, err := Apply(ctx, env, rules, id, Edit{Untags: []string{"security"}}); err != nil {
		t.Fatal(err)
	}
	if _, has := env.rows[store.SourceRepo][id].Fields[schema.FieldTagIDs]; has {
		t.Fatal("removing the last tag must clear tag_ids")
	}
	if keep, err := HasTags(ctx, env, nil); keep != nil || err != nil {
		t.Fatal("no names must give a nil keep func")
	}
	if _, _, err := Apply(ctx, env, rules, id, Edit{Tags: []string{"nosuch"}}); !errs.Has(err, errs.NotFound) {
		t.Fatalf("unknown tag: %v", err)
	}
}
