package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// list builds a text list policy value.
func list(t *testing.T, items ...string) value.Value {
	t.Helper()
	vs := make([]value.Value, len(items))
	for i, s := range items {
		vs[i] = txt(s)
	}
	l, err := value.ListOf(value.KindText, vs...)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// polID is a fixed pol_ id a fixed policy entry names as its source.
func polID(t *testing.T) ids.AnyID {
	t.Helper()
	id, err := ids.ParseAny("pol_01936f0000007a1b8c2d3e4f5a6b7c8d")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// metaEditIn is a test input with a flag that changes the meaning
// (AGENT_IDENTITY_DESIGN 8.2: "meta.edit.merge_into").
type metaEditIn struct {
	Body      string `arg:"0" json:"body"`
	MergeInto string `flag:"merge-into" json:"merge_into,omitempty"`
	Force     bool   `flag:"force" json:"force,omitempty"`
}

// metaEditOp is a write op named meta.edit that stores one memory.
func metaEditOp(f *fixture) *op.Op[metaEditIn, memAddOut] {
	return op.Define(op.Spec{
		Name: "meta.edit", Summary: "edit", Permission: op.PermWrite, Access: op.AccessWrite,
		Errors: []errs.Code{errs.PolicyDenied},
	}, func(ctx context.Context, in metaEditIn) (memAddOut, error) {
		w, err := f.eng.Create(ctx, "", schema.TableMemories, f.memFields(in.Body))
		if err != nil {
			return memAddOut{}, err
		}
		return memAddOut{ID: w.Row.ID.String()}, nil
	})
}

// TestPersonOnlyMatrix (AGENTS POLICY, spec 9.2 POLICY): an operation
// agents.person_only lists at ANY level refuses an agent caller with
// E_POLICY_DENIED and writes nothing; a person is never refused; a
// "<op>.<flag>" item refuses only when that flag is set; an item naming
// another op refuses nothing.
func TestPersonOnlyMatrix(t *testing.T) {
	t.Parallel()
	const agent, person = "codex", ""
	cases := []struct {
		name   string
		level  policy.Level
		items  []string // nil: no policy row
		caller string   // agent name, "" = a person
		flags  map[string][]string
		denied bool
	}{
		{name: "no policy, agent", caller: agent},
		{name: "empty list, agent", level: policy.LevelRepo, items: []string{}, caller: agent},
		{name: "listed at repo, agent", level: policy.LevelRepo, items: []string{"meta.edit"}, caller: agent, denied: true},
		{name: "listed at repo, person", level: policy.LevelRepo, items: []string{"meta.edit"}, caller: person},
		{name: "listed at org, agent", level: policy.LevelOrg, items: []string{"meta.edit"}, caller: agent, denied: true},
		{name: "listed only in personal, agent", level: policy.LevelPersonal, items: []string{"meta.edit"}, caller: agent, denied: true},
		{name: "other op listed, agent", level: policy.LevelRepo, items: []string{"move", "policy.set"}, caller: agent},
		{name: "prefix of the name is not the name", level: policy.LevelRepo, items: []string{"meta"}, caller: agent},
		{name: "text flag item, flag set", level: policy.LevelRepo, items: []string{"meta.edit.merge_into"}, caller: agent,
			flags: map[string][]string{"merge-into": {"mky_x"}}, denied: true},
		{name: "text flag item, flag unset", level: policy.LevelRepo, items: []string{"meta.edit.merge_into"}, caller: agent},
		{name: "text flag item, person", level: policy.LevelRepo, items: []string{"meta.edit.merge_into"}, caller: person,
			flags: map[string][]string{"merge-into": {"mky_x"}}},
		{name: "bool flag item, set", level: policy.LevelRepo, items: []string{"meta.edit.force"}, caller: agent,
			flags: map[string][]string{"force": {"true"}}, denied: true},
		{name: "bool flag item, unset", level: policy.LevelRepo, items: []string{"meta.edit.force"}, caller: agent},
		{name: "empty flag part", level: policy.LevelRepo, items: []string{"meta.edit."}, caller: agent,
			flags: map[string][]string{"force": {"true"}}},
		{name: "unicode item", level: policy.LevelRepo, items: []string{"méta.edit"}, caller: agent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var entries []policy.Entry
			if c.items != nil {
				entries = append(entries, policy.Entry{Level: c.level, Key: policy.KeyAgentsPersonOnly, Value: list(t, c.items...), Source: polID(t)})
			}
			set, err := policy.Resolve(entries)
			if err != nil {
				t.Fatal(err)
			}
			f := setup(t, WithSettings(fixedSettings{set: set}))
			_, err = f.eng.Run(context.Background(), metaEditOp(f), op.ArgsDecoder{Args: []string{"note"}, Flags: c.flags},
				op.Caller{Actor: testActor, Agent: c.caller, Yes: true})
			rows, lerr := f.eng.List(context.Background(), store.SourceRepo, schema.TableMemories)
			if lerr != nil {
				t.Fatal(lerr)
			}
			if !c.denied {
				if err != nil || len(rows) != 1 {
					t.Fatalf("want allowed: err %v, rows %d", err, len(rows))
				}
				return
			}
			if !errs.Has(err, errs.PolicyDenied) || len(rows) != 0 {
				t.Fatalf("want E_POLICY_DENIED and nothing written: err %v, rows %d", err, len(rows))
			}
			if !errs.EnvelopeOf(err).Error.NeedsPerson {
				t.Fatalf("the refusal must set needs_person (the agent asks its person): %v", err)
			}
			msg := err.Error()
			if !strings.Contains(msg, string(policy.KeyAgentsPersonOnly)) || !strings.Contains(msg, polID(t).String()) || !strings.Contains(msg, c.caller) {
				t.Fatalf("the refusal must name the key, the row and the agent: %s", msg)
			}
		})
	}
}

// TestPersonOnlyFromStoredRowsFailsClosed: the default Settings read the
// policies rows the stores hold, so a person_only row written through the
// engine refuses the next agent run; a malformed policies row refuses an
// agent run (E_CONFIG_INVALID) instead of skipping the check.
func TestPersonOnlyFromStoredRowsFailsClosed(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	val, err := value.JSONOf([]byte(`["meta.edit"]`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.eng.Create(ctx, "", schema.TablePolicies, map[string]value.Value{
		"key": txt(string(policy.KeyAgentsPersonOnly)), "value": val, "scope": txt("repo"),
	}); err != nil {
		t.Fatal(err)
	}
	agentCall := op.Caller{Actor: testActor, Agent: "claude_code", Yes: true}
	if _, err = f.eng.Run(ctx, metaEditOp(f), op.ArgsDecoder{Args: []string{"x"}}, agentCall); !errs.Has(err, errs.PolicyDenied) {
		t.Fatalf("a stored person_only row must refuse the agent: %v", err)
	}
	if _, err = f.eng.Run(ctx, metaEditOp(f), op.ArgsDecoder{Args: []string{"x"}}, op.Caller{Actor: testActor, Yes: true}); err != nil {
		t.Fatalf("a person runs it: %v", err)
	}

	g := setup(t)
	bad, err := value.JSONOf([]byte(`"x"`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.eng.Create(ctx, "", schema.TablePolicies, map[string]value.Value{
		"key": txt("agents.no_such_key"), "value": bad, "scope": txt("repo"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = g.eng.Run(ctx, metaEditOp(g), op.ArgsDecoder{Args: []string{"x"}}, agentCall); !errs.Has(err, errs.ConfigInvalid) {
		t.Fatalf("a broken policy row must fail closed for an agent: %v", err)
	}
}

// resourceFixture creates a kind row and returns a builder of resources
// rows whose values hold n bytes of text.
func resourceFixture(t *testing.T, f *fixture) func(n int) map[string]value.Value {
	t.Helper()
	w, err := f.eng.Create(context.Background(), "", schema.TableKinds, map[string]value.Value{"name": txt("host")})
	if err != nil {
		t.Fatal(err)
	}
	kind := w.Row.ID
	return func(n int) map[string]value.Value {
		ref, rerr := value.RefOf(kind)
		if rerr != nil {
			t.Fatal(rerr)
		}
		js, jerr := value.JSONOf([]byte(`{"note":"` + strings.Repeat("x", n) + `"}`))
		if jerr != nil {
			t.Fatal(jerr)
		}
		return map[string]value.Value{"kind": ref, "name": txt("web-1"), "fields": js}
	}
}

// TestResourcesMaxBytesMatrix (RQ4, RESOURCES_DESIGN 3.15): a resources
// row whose values are over resources.max_bytes is refused at write,
// create and edit alike; the default is 16 KB; the stricter (smaller)
// level wins; other tables are not measured.
func TestResourcesMaxBytesMatrix(t *testing.T) {
	t.Parallel()
	limits := func(t *testing.T, ls map[policy.Level]int64) policy.Set {
		t.Helper()
		var es []policy.Entry
		for l, n := range ls {
			es = append(es, policy.Entry{Level: l, Key: policy.KeyResourcesMaxBytes, Value: value.Int(n)})
		}
		s, err := policy.Resolve(es)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	cases := []struct {
		name   string
		levels map[policy.Level]int64 // nil: defaults (16 KB)
		size   int
		ok     bool
	}{
		{name: "default, small", size: 100, ok: true},
		{name: "default, just under 16 KB", size: 16*1024 - 200, ok: true},
		{name: "default, over 16 KB", size: 17 * 1024},
		{name: "org 512, 400 fits", levels: map[policy.Level]int64{policy.LevelOrg: 512}, size: 400, ok: true},
		{name: "org 512, 600 refused", levels: map[policy.Level]int64{policy.LevelOrg: 512}, size: 600},
		{name: "repo looser than org: org wins", levels: map[policy.Level]int64{policy.LevelRepo: 4096, policy.LevelOrg: 512}, size: 600},
		{name: "personal stricter than repo: personal wins", levels: map[policy.Level]int64{policy.LevelRepo: 4096, policy.LevelPersonal: 256}, size: 300},
		{name: "zero limit refuses any resource", levels: map[policy.Level]int64{policy.LevelRepo: 0}, size: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var opts []Option
			if c.levels != nil {
				opts = append(opts, WithSettings(fixedSettings{set: limits(t, c.levels)}))
			}
			f := setup(t, opts...)
			res := resourceFixture(t, f)
			_, err := f.eng.Create(context.Background(), "", schema.TableResources, res(c.size))
			if c.ok != (err == nil) {
				t.Fatalf("size %d: ok want %v, err %v", c.size, c.ok, err)
			}
			if !c.ok && (!errs.Has(err, errs.ResourceInvalid) || !strings.Contains(err.Error(), string(policy.KeyResourcesMaxBytes))) {
				t.Fatalf("want E_RESOURCE_INVALID naming the key: %v", err)
			}
		})
	}

	t.Run("edit that grows past the limit", func(t *testing.T) {
		f := setup(t, WithSettings(fixedSettings{set: limits(t, map[policy.Level]int64{policy.LevelOrg: 512})}))
		res := resourceFixture(t, f)
		w, err := f.eng.Create(context.Background(), "", schema.TableResources, res(100))
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.eng.Update(context.Background(), Ref{ID: w.Row.ID}, map[string]value.Value{"fields": res(1000)["fields"]})
		if !errs.Has(err, errs.ResourceInvalid) {
			t.Fatalf("an edit over the limit must be refused: %v", err)
		}
		_, got, err := f.eng.Get(context.Background(), Ref{ID: w.Row.ID})
		if err != nil || len(got.Fields["fields"].String()) > 512 {
			t.Fatalf("the refused edit was stored: %v", err)
		}
	})
	t.Run("other tables are not measured", func(t *testing.T) {
		f := setup(t, WithSettings(fixedSettings{set: limits(t, map[policy.Level]int64{policy.LevelOrg: 16})}))
		f.memory(t, strings.Repeat("long memory body ", 100))
	})
}

// TestPersonalRuleOffCoversOnlyOwnCaptures (BATCH 8): a secret rule turned
// off in the personal store applies to writes stamped as the engine's own
// actor, never to a write a surface stamps as someone else; an off at any
// other level applies to every writer (D2/D9).
func TestPersonalRuleOffCoversOnlyOwnCaptures(t *testing.T) {
	t.Parallel()
	other, err := ids.Parse[ids.Actor]("act_01936f0000007a1b8c2d3e4f5a6b7c99")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		level policy.Level
		actor ids.ID[ids.Actor]
		pass  bool
	}{
		{name: "personal off, own write", level: policy.LevelPersonal, actor: testActor, pass: true},
		{name: "personal off, zero caller actor is own", level: policy.LevelPersonal, pass: true},
		{name: "personal off, someone else's write", level: policy.LevelPersonal, actor: other},
		{name: "repo off, someone else's write", level: policy.LevelRepo, actor: other, pass: true},
		{name: "org off, own write", level: policy.LevelOrg, actor: testActor, pass: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			off := policy.ResolveRules([]policy.RuleEntry{{Level: c.level, RuleID: awsRule, State: schema.SecretRuleOff}})
			f := setup(t, WithSettings(fixedSettings{set: policy.Defaults(), rules: off}))
			_, err := f.eng.Run(context.Background(), memAddOp(f, op.ConfirmRule{}), op.ArgsDecoder{Args: []string{"key " + fakeKey}},
				op.Caller{Actor: c.actor})
			if c.pass != (err == nil) {
				t.Fatalf("pass want %v, err %v", c.pass, err)
			}
			if !c.pass && !errs.Has(err, errs.NeedsConfirm) {
				t.Fatalf("the rule must still ask: %v", err)
			}
		})
	}
}

// countingScanner records Refresh calls (engine.RefreshingScanner).
type countingScanner struct {
	degradedScanner
	refreshed int
	err       error
}

func (c *countingScanner) Refresh(context.Context) error { c.refreshed++; return c.err }

// TestScannerRefreshedBeforeEachWrite: a RefreshingScanner is refreshed
// before every write's scan, and a failed refresh fails the write (a rule
// row that cannot be read never lets a write through unscanned).
func TestScannerRefreshedBeforeEachWrite(t *testing.T) {
	t.Parallel()
	inner, err := scan.NewRegexScanner(nil)
	if err != nil {
		t.Fatal(err)
	}
	sc := &countingScanner{degradedScanner: degradedScanner{inner}}
	f := setup(t, WithScanner(sc))
	before := sc.refreshed
	f.memory(t, "one")
	f.memory(t, "two")
	if sc.refreshed-before != 2 {
		t.Fatalf("refreshed %d times for 2 writes", sc.refreshed-before)
	}
	sc.err = errs.New(errs.ConfigInvalid, "bad rule row")
	if _, err := f.eng.Create(context.Background(), "", schema.TableMemories, f.memFields("three")); !errs.Has(err, errs.ConfigInvalid) {
		t.Fatalf("a failed refresh must fail the write: %v", err)
	}
}

// TestConfirmedFindingBecomesAnAllowEntry (SQ2): a write whose finding the
// caller confirms as not a secret (on_detect ask, then --yes) stores one
// secret_allows row in the same store, in the same batch, naming the row,
// the field, the rule and the match hash (never the value). An edit of the
// row keeping that match asks nothing; an edit adding a DIFFERENT match
// still asks.
func TestConfirmedFindingBecomesAnAllowEntry(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	o := memAddOp(f, op.ConfirmRule{})
	res, err := f.eng.Run(ctx, o, op.ArgsDecoder{Args: []string{"key " + fakeKey}}, op.Caller{Actor: testActor, Yes: true,
		Secret: op.SecretChoice{NotASecret: true, Reason: "docs sample"}}) // FB-SEC-1: only a typed reason accepts
	if err != nil {
		t.Fatal(err)
	}
	out, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	memID := strings.TrimSuffix(strings.TrimPrefix(string(out), `{"id":"`), `"}`)
	allows, err := f.eng.List(ctx, store.SourceRepo, schema.TableSecretAllows)
	if err != nil || len(allows) != 1 {
		t.Fatalf("want one allow row: %v %v", allows, err)
	}
	a := allows[0]
	if textOf(a, allowRowID) != memID || textOf(a, allowField) != "body" || textOf(a, allowRule) != awsRule ||
		a.Fields[allowHash].String() != value.Int(int64(scan.HashMatch(fakeKey))).String() {
		t.Fatalf("allow row fields: %v (row %s)", a.Fields, memID)
	}
	if !strings.HasPrefix(textOf(a, allowReason), "docs sample ("+allowReasonPrefix) || strings.Contains(textOf(a, allowReason), fakeKey) {
		t.Fatalf("reason: %q", textOf(a, allowReason))
	}
	id, err := ids.ParseAny(memID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.eng.Update(ctx, Ref{ID: id}, map[string]value.Value{"body": txt("key " + fakeKey + " still here")}); err != nil {
		t.Fatalf("the accepted match must not ask again: %v", err)
	}
	other := "AKIA" + strings.Repeat("Z", 16)
	if _, err = f.eng.Update(ctx, Ref{ID: id}, map[string]value.Value{"body": txt("key " + fakeKey + " and " + other)}); !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("a different match must still ask: %v", err)
	}
	if n, _ := f.eng.List(ctx, store.SourceRepo, schema.TableSecretAllows); len(n) != 1 {
		t.Fatalf("a refused edit must add no allow row: %d", len(n))
	}
}

// TestEnforceMovePlace: a move of a resource obeys resources.allowed_in of
// the target, by the target's place class; other tables never (RQ2).
func TestEnforceMovePlace(t *testing.T) {
	t.Parallel()
	storeOnly, err := policy.Resolve([]policy.Entry{{Level: policy.LevelOrg, Key: policy.KeyResourcesAllowedIn, Value: list(t, policy.PlaceStore)}})
	if err != nil {
		t.Fatal(err)
	}
	res := schema.TableDef{Name: schema.TableResources}
	for _, c := range []struct {
		name   string
		pol    policy.Set
		def    schema.TableDef
		to     store.Source
		client bool
		refuse bool
	}{
		{"default allows a client repo", policy.Defaults(), res, "acme_app", true, false},
		{"store only: repo refused", storeOnly, res, store.SourceRepo, false, true},
		{"store only: client refused", storeOnly, res, "acme_app", true, true},
		{"store only: a store passes", storeOnly, res, "agency", false, false},
		{"me always refused", policy.Defaults(), res, storeMe, false, true},
		{"another table is not checked", storeOnly, schema.TableDef{Name: schema.TableMemories}, store.SourceRepo, false, false},
	} {
		err := enforceMovePlace(c.pol, c.def, c.to, c.client)
		if got := errs.Has(err, errs.ResourcePlace); got != c.refuse {
			t.Errorf("%s: refused=%v want %v (%v)", c.name, got, c.refuse, err)
		}
	}
}
