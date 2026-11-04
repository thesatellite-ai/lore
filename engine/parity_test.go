package engine

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// v017Project is the project id the v0.1.17 goldens below were computed
// with.
const v017Project = "prj_01936f0000017c3d8e4f5a6b7c8d9e00"

// v017Goldens are ids v0.1.17's saas/pkg/aicoder/ids.Deterministic returns
// (computed once with that code from a throwaway main; the bytes must
// never change, or rows written by v0.1.17 and 0.1.18 of the same repo
// stop converging).
var v017Goldens = []struct {
	name   string
	prefix ids.Prefix
	parts  []string
	want   string
}{
	{"tag (project_id, name)", ids.PrefixTag, []string{v017Project, "backend"}, "tag_eb44519e585a7adba24148437fa778e0"},
	{"repo (project_id, mount_name)", ids.PrefixRepo, []string{v017Project, "api"}, "rep_d87d0364b5817a10ad97ac9ef51c9d4c"},
	{"actor (stable_key)", ids.PrefixActor, []string{"khanakia@gmail.com"}, "act_672f19f899487f6ab4f199fb3b2c8403"},
	{"unicode name", ids.PrefixTag, []string{v017Project, "naïve-ünïcode"}, "tag_32105243c54674158ef86d18a299a524"},
}

func TestNaturalIDMatchesV017Bytes(t *testing.T) {
	for _, g := range v017Goldens {
		got, err := NaturalID(g.prefix, g.parts...)
		if err != nil || got.String() != g.want {
			t.Errorf("%s: %s %v, want %s", g.name, got, err, g.want)
		}
	}
	if _, err := NaturalID(ids.PrefixTag); !errs.Has(err, errs.Internal) {
		t.Errorf("no key parts: %v", err)
	}
}

// pinProject writes the golden project into the repo store through
// CreateWithID.
func pinProject(t *testing.T, w *f15World) ids.AnyID {
	t.Helper()
	id, err := ids.ParseAny(v017Project)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.eng.CreateWithID(context.Background(), store.SourceRepo, schema.TableProjects, id, map[string]value.Value{"name": txt("p")}); err != nil {
		t.Fatal(err)
	}
	return id
}

func tagFields(prj ids.AnyID, name string) map[string]value.Value {
	return map[string]value.Value{"project_id": txt(prj.String()), "name": txt(name)}
}

// TestNaturalIDsPerStore: the repo store (no KeyNamespace) writes
// v0.1.17's id; a namespaced store hashes its namespace and the table in,
// so the same key there is another row; a GlobalKey table (actors) keeps
// the v0.1.17 id in every store; a live duplicate key is refused naming
// the existing row; a deleted key may be re-created and gets a fresh id
// (the tombstone keeps the natural one, decision 41cac0c).
func TestNaturalIDsPerStore(t *testing.T) {
	ctx := context.Background()
	const ns = "sto_01936f0000017c3d8e4f5a6b7c8d9e01"
	w := newF15World(t, false)
	// re-attach agency with a namespace: build a second world by hand
	reg := w.eng.reg
	data := map[store.Source]*memstore.Data{store.SourceRepo: memstore.NewData(), agency: memstore.NewData()}
	eng, err := New(
		WithStore(Attached{Name: store.SourceRepo, Backend: data[store.SourceRepo].Open(store.OpenOptions{})}),
		WithStore(Attached{Name: agency, Backend: data[agency].Open(store.OpenOptions{}), KeyNamespace: ns}),
		WithCache(newFakeCache()), WithRegistry(reg), WithScanner(w.eng.scanner), WithActor(testActor), WithAudit(&auditLog{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	w = &f15World{eng: eng, data: data}
	prj := pinProject(t, w)
	pinProjectAgency := func() {
		if _, err = w.eng.CreateWithID(ctx, agency, schema.TableProjects, prj, map[string]value.Value{"name": txt("p")}); err != nil {
			t.Fatal(err)
		}
	}
	pinProjectAgency()

	inRepo, err := w.eng.Create(ctx, store.SourceRepo, schema.TableTags, tagFields(prj, "backend"))
	if err != nil || inRepo.Row.ID.String() != v017Goldens[0].want {
		t.Fatalf("repo tag id %s %v, want the v0.1.17 id", inRepo.Row.ID, err)
	}
	inAg, err := w.eng.Create(ctx, agency, schema.TableTags, tagFields(prj, "backend"))
	if err != nil {
		t.Fatal(err)
	}
	wantAg, _ := NaturalID(ids.PrefixTag, ns, string(schema.TableTags), prj.String(), "backend")
	if inAg.Row.ID != wantAg || inAg.Row.ID == inRepo.Row.ID {
		t.Fatalf("agency tag id %s, want %s (namespaced, not the repo's)", inAg.Row.ID, wantAg)
	}
	found, err := FindByKey(ctx, w.eng, schema.TableTags, map[string]string{"project_id": prj.String(), "name": "backend"})
	if err != nil || len(found) != 2 || found[0].Store != store.SourceRepo || found[1].Store != agency {
		t.Fatalf("FindByKey = %+v %v (nearest first, one per store)", found, err)
	}
	if none, _ := FindByKey(ctx, w.eng, schema.TableTags, map[string]string{"name": "frontend"}); len(none) != 0 {
		t.Fatalf("FindByKey of a missing key = %+v", none)
	}

	act, err := w.eng.Create(ctx, agency, schema.TableActors, map[string]value.Value{
		"kind": txt(schema.ActorKindHuman), "display_name": txt("Aman Bansal"), "stable_key": txt("khanakia@gmail.com"),
	})
	if err != nil || act.Row.ID.String() != v017Goldens[2].want {
		t.Fatalf("actor in a namespaced store: %s %v, want the global v0.1.17 id", act.Row.ID, err)
	}

	_, err = w.eng.Create(ctx, store.SourceRepo, schema.TableTags, tagFields(prj, "backend"))
	if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), inRepo.Row.ID.String()) {
		t.Fatalf("duplicate live key: %v", err)
	}
	if _, err = w.eng.Delete(ctx, Ref{Store: store.SourceRepo, ID: inRepo.Row.ID}); err != nil {
		t.Fatal(err)
	}
	again, err := w.eng.Create(ctx, store.SourceRepo, schema.TableTags, tagFields(prj, "backend"))
	if err != nil || again.Row.ID == inRepo.Row.ID || again.Row.Deleted {
		t.Fatalf("re-create after delete: %s %v (fresh id expected)", again.Row.ID, err)
	}
	if _, tomb, _ := w.eng.Get(ctx, Ref{Store: store.SourceRepo, ID: inRepo.Row.ID}); !tomb.Deleted {
		t.Fatal("the tombstone was resurrected")
	}
	_, err = w.eng.Create(ctx, store.SourceRepo, schema.TableTags, tagFields(prj, "backend"))
	if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), again.Row.ID.String()) {
		t.Fatalf("second re-create must name the live re-created row: %v", err)
	}
}

func TestCreateWithID(t *testing.T) {
	ctx := context.Background()
	w := newF15World(t, false)
	prj := pinProject(t, w)
	cases := []struct {
		name string
		id   ids.AnyID
		want errs.Code
	}{
		{"zero id", ids.AnyID{}, errs.InvalidID},
		{"wrong prefix", mustID(t, ids.PrefixMemory), errs.InvalidID},
		{"already there", prj, errs.Stale},
	}
	for _, c := range cases {
		_, err := w.eng.CreateWithID(ctx, store.SourceRepo, schema.TableProjects, c.id, map[string]value.Value{"name": txt("q")})
		if !errs.Has(err, c.want) {
			t.Errorf("%s: %v, want %s", c.name, err, c.want)
		}
	}
	_, got, err := w.eng.Get(ctx, Ref{ID: prj})
	if err != nil || got.Fields["name"].String() != "p" {
		t.Fatalf("CreateWithID overwrote a row: %v %v", got.Fields, err)
	}
	// a pinned id wins over the natural key
	pin := mustID(t, ids.PrefixTag)
	tg, err := w.eng.CreateWithID(ctx, store.SourceRepo, schema.TableTags, pin, tagFields(prj, "pinned"))
	if err != nil || tg.Row.ID != pin {
		t.Fatalf("pinned tag: %s %v", tg.Row.ID, err)
	}
}

func TestValidStoreName(t *testing.T) {
	for n, want := range map[string]bool{
		"repo": true, "agency": true, "agency/org": true, "agency/acme-hub": true, "me/notes_1": true, "a/b/c": true,
		"": false, "Agency": false, "agency/": false, "agency//x": false, "agency/..": false, "agency/.": true, "agency/./x": false, "agency/x/.": false, "./x": false,
		"../x": false, "agency/../me": false, `agency\x`: false, `agency/a\b`: false, "agency/Org": false,
		"agency/a b": false, "agency/ünï": false, "/agency": false, "agency/a.b": false,
	} {
		if got := validStoreName(n); got != want {
			t.Errorf("validStoreName(%q) = %v, want %v", n, got, want)
		}
	}
}

// policyRow creates a policies row in store n.
func policyRow(t *testing.T, w *f15World, n store.Source, key policy.Key, raw string) {
	t.Helper()
	j, err := value.JSONOf([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.eng.Create(context.Background(), n, schema.TablePolicies, map[string]value.Value{
		"key": txt(string(key)), "value": j, "scope": txt(string(n)),
	}); err != nil {
		t.Fatal(err)
	}
}

// TestStoreSettings reads the settings rows of every attached store: a
// stricter on_detect in another store wins over the repo's looser one;
// a rule turned off at a store level is off; a secret_allows row in any
// store lets that exact finding through; a malformed row fails closed.
func TestStoreSettings(t *testing.T) {
	ctx := context.Background()
	w := newF15World(t, false)
	prj := pinProject(t, w)
	w2 := w.memIn(t, store.SourceRepo, prj, "clean")
	policyRow(t, w, store.SourceRepo, policy.KeyOnDetect, `"ask"`)
	policyRow(t, w, agency, policy.KeyOnDetect, `"refuse"`)
	if set, err := w.eng.Policy(store.SourceRepo); err != nil || set.OnDetect() != policy.OnDetectRefuse {
		t.Fatalf("stricter store policy lost: %v %v", set.OnDetect(), err)
	}
	if _, err := w.eng.Update(ctx, Ref{ID: w2.ID}, map[string]value.Value{"body": txt("key " + fakeKey)}); !errs.Has(err, errs.SecretDetected) {
		t.Fatalf("refuse from agency not applied: %v", err)
	}

	// allowed: an allow row naming this row, field, rule and match
	hash, err := value.NumberOf(strconv.FormatUint(uint64(scan.HashMatch(fakeKey)), 10))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.eng.Create(ctx, agency, schema.TableSecretAllows, map[string]value.Value{
		"row_id": txt(w2.ID.String()), "field": txt("body"), "rule_id": txt(awsRule), "match_hash": hash,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.eng.Update(ctx, Ref{ID: w2.ID}, map[string]value.Value{"body": txt("key " + fakeKey)}); err != nil {
		t.Fatalf("an allowed finding was refused: %v", err)
	}
	other := w.memIn(t, store.SourceRepo, prj, "other")
	if _, err := w.eng.Update(ctx, Ref{ID: other.ID}, map[string]value.Value{"body": txt("key " + fakeKey)}); !errs.Has(err, errs.SecretDetected) {
		t.Fatalf("an allow of another row let this one through: %v", err)
	}

	// rule off at agency's store level: off everywhere
	if _, err := w.eng.Create(ctx, agency, schema.TableSecretRules, map[string]value.Value{
		"rule_id": txt(awsRule), "state": txt(schema.SecretRuleOff),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.eng.Update(ctx, Ref{ID: other.ID}, map[string]value.Value{"body": txt("key " + fakeKey)}); err != nil {
		t.Fatalf("rule off at store level not honoured: %v", err)
	}

	// malformed rows fail closed
	for _, bad := range []struct {
		table  row.Table
		fields map[string]value.Value
	}{
		{schema.TableSecretRules, map[string]value.Value{"rule_id": txt("x")}},
		{schema.TableSecretAllows, map[string]value.Value{"row_id": txt("not-an-id"), "field": txt("body")}},
		{schema.TablePolicies, map[string]value.Value{"value": txt("1")}},
	} {
		wb := newF15World(t, false)
		raw := row.Row{Table: bad.table, ID: mustID(t, mustPrefix(t, wb, bad.table)), HLC: hlc.Next(hlc.HLC{}, time.Now(), testActor), Fields: bad.fields}
		sr, err := store.FromRow(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err = wb.data[agency].Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: sr}}); err != nil {
			t.Fatal(err)
		}
		_, err = wb.eng.Create(ctx, store.SourceRepo, schema.TableProjects, map[string]value.Value{"name": txt("x")})
		if !errs.Has(err, errs.ConfigInvalid) || !strings.Contains(err.Error(), raw.ID.String()) {
			t.Errorf("malformed %s row: %v, want E_CONFIG_INVALID naming it", bad.table, err)
		}
	}
}

func mustPrefix(t *testing.T, w *f15World, table row.Table) ids.Prefix {
	t.Helper()
	def, ok := w.eng.reg.Table(table)
	if !ok {
		t.Fatalf("no table %s", table)
	}
	return def.Prefix
}

// TestStaleMessageMasksSecrets: an E_STALE clash names both values, and a
// value the scanner flags is masked in the message (never repeated).
func TestStaleMessageMasksSecrets(t *testing.T) {
	f := setup(t)
	msg := f.eng.staleErr(f.project, nil, schema.TableDef{}).Error()
	if !strings.Contains(msg, f.project.String()) {
		t.Fatalf("no id: %s", msg)
	}
	shownSecret := f.eng.shown("body", txt("key "+fakeKey))
	if strings.Contains(shownSecret, fakeKey) {
		t.Fatalf("secret repeated: %s", shownSecret)
	}
	if got := f.eng.shown("body", nil); got != "(none)" {
		t.Fatalf("absent value shown as %q", got)
	}
	long := f.eng.shown("body", txt(strings.Repeat("é", staleShownMax+5)))
	if !strings.HasSuffix(long, `…"`) || len([]rune(long)) != staleShownMax+3 {
		t.Fatalf("long value not cut at %d runes: %d", staleShownMax, len([]rune(long)))
	}
}

// m2mRegistry is Builtin with memories.tag_ids' on-delete rule replaced.
func m2mRegistry(t *testing.T, rule schema.OnDelete) *schema.Registry {
	t.Helper()
	defs := schema.Builtin()
	for i, d := range defs {
		if d.Name != schema.TableMemories {
			continue
		}
		for j, r := range d.Relations {
			if r.Kind == schema.ManyToMany && r.Field == schema.FieldTagIDs {
				defs[i].Relations[j].OnDelete = rule
			}
		}
	}
	reg, err := schema.NewRegistry(defs...)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// TestManyToManyDeleteRules: deleting a tag listed in a memory's tag_ids
// acts on the link only: cascade (the built-in) removes the id and keeps
// the memory, restrict refuses, tombstone_only leaves the id.
func TestManyToManyDeleteRules(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		rule     schema.OnDelete
		refused  bool
		keepsTag bool
	}{
		{schema.Cascade, false, false},
		{schema.SetNull, false, false},
		{schema.Restrict, true, true},
		{schema.TombstoneOnly, false, true},
	} {
		w := newF15World(t, false)
		w.eng.reg = m2mRegistry(t, c.rule)
		prj := pinProject(t, w)
		a, err := w.eng.Create(ctx, store.SourceRepo, schema.TableTags, tagFields(prj, "a"))
		if err != nil {
			t.Fatal(err)
		}
		b, err := w.eng.Create(ctx, store.SourceRepo, schema.TableTags, tagFields(prj, "b"))
		if err != nil {
			t.Fatal(err)
		}
		fields := map[string]value.Value{"project_id": txt(prj.String()), "kind": txt(schema.MemoryKindCore), "body": txt("m"), "source_kind": txt("manual")}
		ra, _ := value.RefOf(a.Row.ID)
		rb, _ := value.RefOf(b.Row.ID)
		if fields[schema.FieldTagIDs], err = value.ListOf(value.KindRef, ra, rb); err != nil {
			t.Fatal(err)
		}
		m, err := w.eng.Create(ctx, store.SourceRepo, schema.TableMemories, fields)
		if err != nil {
			t.Fatal(err)
		}
		_, err = w.eng.Delete(ctx, Ref{ID: a.Row.ID})
		if c.refused != errs.Has(err, errs.HasDependents) || (!c.refused && err != nil) {
			t.Errorf("%s: delete err = %v", c.rule, err)
			continue
		}
		_, got, err := w.eng.Get(ctx, Ref{ID: m.Row.ID})
		if err != nil || got.Deleted {
			t.Errorf("%s: the memory itself was touched: %v %+v", c.rule, err, got)
			continue
		}
		list := got.Fields[schema.FieldTagIDs].String()
		if strings.Contains(list, a.Row.ID.String()) != c.keepsTag || !strings.Contains(list, b.Row.ID.String()) {
			t.Errorf("%s: tag_ids = %s", c.rule, list)
		}
		_, tag, _ := w.eng.Get(ctx, Ref{ID: a.Row.ID})
		if tag.Deleted == c.refused {
			t.Errorf("%s: tag deleted = %v", c.rule, tag.Deleted)
		}
	}
}

// TestMove covers PROV5: the id is kept, the destination row carries a
// `moves` entry, the source keeps a tombstone; a rerun after a half move
// only writes the tombstone; a tombstone in the target refuses; a client
// destination refuses secret references and gets no moves entry.
func TestMove(t *testing.T) {
	ctx := context.Background()
	w := newF15World(t, true)
	repoPrj := w.project(t, store.SourceRepo, "client")

	// agency → me: id kept, moves entry, tombstone left.
	m := w.memIn(t, agency, repoPrj, "move me")
	mv, err := w.eng.Move(ctx, Ref{ID: m.ID}, personal)
	if err != nil {
		t.Fatal(err)
	}
	if mv.Row.ID != m.ID || !strings.Contains(mv.Row.Origin.String(), `"moved_from":"agency"`) || !strings.Contains(mv.Row.Origin.String(), `"moved_to":"me"`) {
		t.Fatalf("moved row = %s %s", mv.Row.ID, mv.Row.Origin.String())
	}
	if _, src, _ := w.eng.Get(ctx, Ref{Store: agency, ID: m.ID}); !src.Deleted {
		t.Fatal("source not tombstoned")
	}
	if _, got, gerr := w.eng.Get(ctx, Ref{Store: personal, ID: m.ID}); gerr != nil || got.Deleted {
		t.Fatalf("after move: %v %+v", gerr, got)
	}
	// back into agency: it left agency, so it never returns
	if _, err = w.eng.Move(ctx, Ref{Store: personal, ID: m.ID}, agency); !errs.Has(err, errs.Unsupported) {
		t.Fatalf("return to a store it left: %v", err)
	}
	// already in the target: nothing to move from elsewhere
	if _, err = w.eng.Move(ctx, Ref{ID: m.ID}, personal); !errs.Has(err, errs.NotFound) {
		t.Fatalf("move into the store it is in: %v", err)
	}

	// half move: the copy already landed in me, the source still live.
	h := w.memIn(t, agency, repoPrj, "half")
	raw, err := w.data[agency].Open(store.OpenOptions{}).Get(ctx, schema.TableMemories, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.data[personal].Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: raw}}); err != nil {
		t.Fatal(err)
	}
	if _, err = w.eng.Move(ctx, Ref{Store: agency, ID: h.ID}, personal); err != nil {
		t.Fatalf("rerun after a half move: %v", err)
	}
	if _, src, _ := w.eng.Get(ctx, Ref{Store: agency, ID: h.ID}); !src.Deleted {
		t.Fatal("rerun did not tombstone the source")
	}

	// into the client repo: a secret reference refuses, none moves without
	// a moves entry.
	sec := w.memIn(t, agency, repoPrj, "uses ${secret:deploy_key} to ship")
	if _, err = w.eng.Move(ctx, Ref{ID: sec.ID}, store.SourceRepo); !errs.Has(err, errs.LeakBlocked) || !strings.Contains(err.Error(), "secret:deploy_key") {
		t.Fatalf("secret ref into a client repo: %v", err)
	}
	clean := w.memIn(t, agency, repoPrj, "plain")
	got, err := w.eng.Move(ctx, Ref{ID: clean.ID}, store.SourceRepo)
	if err != nil {
		t.Fatal(err)
	}
	if o := got.Row.Origin.String(); strings.Contains(o, "moves") || strings.Contains(o, string(agency)) || !strings.Contains(o, `"store":"repo"`) {
		t.Fatalf("a client repo row must carry a fresh _o naming only repo: %s", o)
	}

	// an internal project is re-homed to the client repo's one project
	agPrj := w.project(t, agency, "internal-folder")
	inner := w.memIn(t, agency, agPrj, "filed in an internal folder")
	got, err = w.eng.Move(ctx, Ref{ID: inner.ID}, store.SourceRepo)
	if err != nil {
		t.Fatalf("move with an internal project into the client repo: %v", err)
	}
	if p := got.Row.Fields["project_id"].String(); p != repoPrj.String() {
		t.Fatalf("project_id = %s, want the client repo's %s", p, repoPrj)
	}
	// two projects in the client repo: no single place to re-home to
	w.project(t, store.SourceRepo, "second")
	inner2 := w.memIn(t, agency, agPrj, "again")
	if _, err = w.eng.Move(ctx, Ref{ID: inner2.ID}, store.SourceRepo); !errs.Has(err, errs.Unsupported) {
		t.Fatalf("re-home with two client projects: %v", err)
	}

	// read-only destination: refused before anything is read or written
	f := setup(t) // agency is attached read-only there
	ro := f.memory(t, "stays")
	if _, err := f.eng.Move(ctx, Ref{ID: ro.ID}, agency); !errs.Has(err, errs.StoreReadOnly) {
		t.Fatalf("move into a read-only store: %v", err)
	}
	if _, err := f.eng.Move(ctx, Ref{ID: ro.ID}, "nowhere"); !errs.Has(err, errs.NotFound) {
		t.Fatalf("move into an unattached store: %v", err)
	}
}

// TestHasOtherWriterV1Field: a v0.1.17 row names its creator in the
// created_by_actor_id field; a teammate there asks, the caller there does
// not, and a value that is no actor id is ignored.
func TestHasOtherWriterV1Field(t *testing.T) {
	other, err := ids.Parse[ids.Actor]("act_01936f0000007a1b8c2d3e4f5a6b7c8e")
	if err != nil {
		t.Fatal(err)
	}
	v1 := func(s string) row.Row { return row.Row{Fields: map[string]value.Value{v1CreatorField: txt(s)}} }
	for _, c := range []struct {
		name string
		rows []row.Row
		want bool
	}{
		{"no rows", nil, false},
		{"v1 teammate", []row.Row{v1(other.String())}, true},
		{"v1 caller", []row.Row{v1(testActor.String())}, false},
		{"v1 garbage", []row.Row{v1("someone")}, false},
		{"v1 empty", []row.Row{v1("")}, false},
		{"hlc actor only", []row.Row{{HLC: hlc.HLC{Actor: other}}}, true},
		{"caller everywhere", []row.Row{{CreatedBy: testActor, UpdatedBy: testActor, HLC: hlc.HLC{Actor: testActor}}, v1(testActor.String())}, false},
	} {
		if got := HasOtherWriter(c.rows, testActor); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// TestUpdateRetypesStoredFields: a stored row read back has inferred
// values (a json column holding a string reads as Text); an Update that
// patches another field must not fail on it, and the re-typed json value
// stays as it was.
func TestUpdateRetypesStoredFields(t *testing.T) {
	ctx := context.Background()
	w := newF15World(t, false)
	j, err := value.JSONOf([]byte(`"refuse"`))
	if err != nil {
		t.Fatal(err)
	}
	p, err := w.eng.Create(ctx, store.SourceRepo, schema.TablePolicies, map[string]value.Value{
		"key": txt(string(policy.KeyOnDetect)), "value": j, "scope": txt("repo"),
	})
	if err != nil {
		t.Fatal(err)
	}
	up, err := w.eng.Update(ctx, Ref{ID: p.Row.ID}, map[string]value.Value{"scope": txt("store")})
	if err != nil {
		t.Fatalf("update of a row with a json string column: %v", err)
	}
	if up.Row.Fields["value"].Kind() != value.KindJSON || up.Row.Fields["value"].String() != `"refuse"` {
		t.Fatalf("value re-typed to %s %s", up.Row.Fields["value"].Kind(), up.Row.Fields["value"].String())
	}
	// unknown stored field kept, a field that does not parse still reported
	got := retype(mustDef(t, w, schema.TableMemories), map[string]value.Value{"extra": txt("x"), "valid_at": txt("not a date")})
	if got["extra"].String() != "x" || got["valid_at"].Kind() != value.KindText {
		t.Fatalf("retype = %v", got)
	}
}

func mustDef(t *testing.T, w *f15World, table row.Table) schema.TableDef {
	t.Helper()
	def, ok := w.eng.reg.Table(table)
	if !ok {
		t.Fatalf("no table %s", table)
	}
	return def
}
