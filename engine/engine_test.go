package engine

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// fakeCache is the in-package store.Cache: rows and a cursor per source.
type fakeCache struct {
	mu      sync.Mutex
	rows    map[store.Source]map[store.Key]store.Row
	cursors map[store.Source]store.Cursor
}

func newFakeCache() *fakeCache {
	return &fakeCache{rows: map[store.Source]map[store.Key]store.Row{}, cursors: map[store.Source]store.Cursor{}}
}

func (c *fakeCache) Apply(_ context.Context, src store.Source, rows []store.Row, next store.Cursor) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rows[src] == nil {
		c.rows[src] = map[store.Key]store.Row{}
	}
	for _, r := range rows {
		if old, ok := c.rows[src][r.Key()]; ok && old.HLC.Compare(r.HLC) > 0 {
			continue // an older copy never replaces a newer one
		}
		c.rows[src][r.Key()] = r
	}
	c.cursors[src] = next
	return nil
}

func (c *fakeCache) Cursor(_ context.Context, src store.Source) (store.Cursor, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cursors[src], nil
}

func (c *fakeCache) Rebuild(_ context.Context, src store.Source) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.rows, src)
	delete(c.cursors, src)
	return nil
}

func (c *fakeCache) Close() error { return nil }

func (c *fakeCache) get(t row.Table, id ids.AnyID) (store.Row, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.rows[store.SourceRepo][store.Key{Table: t, ID: id}]
	return r, ok
}

// auditLog records events.
type auditLog struct {
	mu  sync.Mutex
	evs []Event
}

func (a *auditLog) Record(_ context.Context, ev Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.evs = append(a.evs, ev)
	return nil
}

func (a *auditLog) events() []Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.evs)
}

// fixedSettings returns one policy for every store.
type fixedSettings struct {
	set    policy.Set
	rules  policy.Rules
	allows []scan.Allow
}

func (s fixedSettings) Policy(context.Context, store.Source) (policy.Set, policy.Rules, error) {
	return s.set, s.rules, nil
}
func (s fixedSettings) Allows(context.Context, store.Source) ([]scan.Allow, error) {
	return s.allows, nil
}

// awsRule is the one scanner rule of these tests.
const awsRule = "aws-access-token"

// fakeKey is built at run time so no key-shaped literal sits in the source.
var fakeKey = "AKIA" + strings.Repeat("Q", 16)

var testActor = func() ids.ID[ids.Actor] {
	a, err := ids.Parse[ids.Actor]("act_01936f0000007a1b8c2d3e4f5a6b7c8d")
	if err != nil {
		panic(err)
	}
	return a
}()

const agency store.Source = "agency"

type fixture struct {
	eng     *Engine
	cache   *fakeCache
	audit   *auditLog
	repo    *memstore.Data
	agency  *memstore.Data
	project ids.AnyID
}

func setup(t *testing.T, extra ...Option) *fixture {
	t.Helper()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := scan.NewRegexScanner([]scan.Rule{{ID: awsRule, Regex: `AKIA[0-9A-Z]{16}`, Keywords: []string{"akia"}, Source: scan.SourceBuiltin}})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{cache: newFakeCache(), audit: &auditLog{}, repo: memstore.NewData(), agency: memstore.NewData()}
	opts := append([]Option{
		WithStore(Attached{Name: store.SourceRepo, Backend: f.repo.Open(store.OpenOptions{})}),
		// The handle itself is writable: read_only is the engine's decision
		// (stores.yaml), so the tests prove the engine enforces it.
		WithStore(Attached{Name: agency, Backend: f.agency.Open(store.OpenOptions{}), ReadOnly: true}),
		WithCache(f.cache), WithRegistry(reg), WithScanner(sc), WithActor(testActor), WithAudit(f.audit),
	}, extra...)
	if f.eng, err = New(opts...); err != nil {
		t.Fatal(err)
	}
	w, err := f.eng.Create(context.Background(), "", schema.TableProjects, map[string]value.Value{"name": txt("acme-hub")})
	if err != nil {
		t.Fatal(err)
	}
	f.project = w.Row.ID
	return f
}

func txt(s string) value.Value {
	v, err := value.TextOf(s)
	if err != nil {
		panic(err)
	}
	return v
}

func (f *fixture) memory(t *testing.T, body string) row.Row {
	t.Helper()
	w, err := f.eng.Create(context.Background(), "", schema.TableMemories, f.memFields(body))
	if err != nil {
		t.Fatal(err)
	}
	return w.Row
}

func (f *fixture) memFields(body string) map[string]value.Value {
	return map[string]value.Value{"project_id": txt(f.project.String()), "kind": txt(schema.MemoryKindCore), "body": txt(body), "source_kind": txt("manual")}
}

func TestNewReportsEveryMissingDependency(t *testing.T) {
	t.Parallel()
	_, err := New()
	if !errs.Has(err, errs.ConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
	for _, w := range []string{"cache", "registry", "scanner", "actor", "audit sink", "a store"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error does not name %s: %v", w, err)
		}
	}
}

func TestCreateStampsWritesCachesAudits(t *testing.T) {
	t.Parallel()
	f := setup(t)
	r := f.memory(t, "staging deploys need the vpn")
	if r.ID.Prefix() != ids.PrefixMemory || r.HLC.IsZero() || r.CreatedBy != testActor || r.UpdatedAt.IsZero() || r.Origin.IsZero() {
		t.Fatalf("not stamped: %+v", r)
	}
	if r.Fields["project_id"].Kind() != value.KindRef {
		t.Fatalf("project_id kind %s, want ref (validated and typed)", r.Fields["project_id"].Kind())
	}
	if _, err := f.repo.Open(store.OpenOptions{}).Get(context.Background(), schema.TableMemories, r.ID); err != nil {
		t.Fatalf("not in the backend: %v", err)
	}
	if _, ok := f.cache.get(schema.TableMemories, r.ID); !ok {
		t.Fatal("not applied to the cache")
	}
	evs := f.audit.events()
	if last := evs[len(evs)-1]; last.Action != ActionCreate || last.ID != r.ID || last.Store != store.SourceRepo {
		t.Fatalf("audit = %+v", last)
	}
}

func TestValidationAndRefCheck(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	_, err := f.eng.Create(ctx, "", schema.TableMemories, map[string]value.Value{"body": txt("x"), "colour": txt("red")})
	if !errs.Has(err, errs.ResourceInvalid) || !strings.Contains(err.Error(), "allowed:") {
		t.Fatalf("unknown field: %v", err)
	}
	missing := f.memFields("x")
	missing["project_id"] = txt("prj_01936f0000017c3d8e4f5a6b7c8d9e00")
	if _, err = f.eng.Create(ctx, "", schema.TableMemories, missing); !errs.Has(err, errs.NotFound) {
		t.Fatalf("dangling ref accepted: %v", err)
	}
	marked := f.memFields("<<<<<<< a\nx\n=======\ny\n>>>>>>> b")
	if _, err = f.eng.Create(ctx, "", schema.TableMemories, marked); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("conflict markers stored: %v", err)
	}
	rows, err := f.eng.List(ctx, store.SourceRepo, schema.TableMemories)
	if err != nil || len(rows) != 0 {
		t.Fatalf("a refused write left rows: %d %v", len(rows), err)
	}
}

func TestReadOnlyStoreRefused(t *testing.T) {
	t.Parallel()
	f := setup(t)
	_, err := f.eng.Create(context.Background(), agency, schema.TableProjects, map[string]value.Value{"name": txt("x")})
	if !errs.Has(err, errs.StoreReadOnly) {
		t.Fatalf("err = %v, want E_STORE_READ_ONLY", err)
	}
	if rows, _, _ := f.agency.Open(store.OpenOptions{}).Read(context.Background(), ""); len(rows) != 0 {
		t.Fatal("read-only store was written")
	}
}

func TestSecretRefusedByPolicy(t *testing.T) {
	t.Parallel()
	refuse, err := policy.Resolve([]policy.Entry{{Level: policy.LevelOrg, Key: policy.KeyOnDetect, Value: txt("refuse")}})
	if err != nil {
		t.Fatal(err)
	}
	f := setup(t, WithSettings(fixedSettings{set: refuse}))
	_, err = f.eng.Create(context.Background(), "", schema.TableMemories, f.memFields("key "+fakeKey))
	if !errs.Has(err, errs.SecretDetected) {
		t.Fatalf("err = %v, want E_SECRET_DETECTED", err)
	}
	if strings.Contains(err.Error(), fakeKey) {
		t.Fatal("the error repeats the secret")
	}
	if rows, _ := f.eng.List(context.Background(), store.SourceRepo, schema.TableMemories); len(rows) != 0 {
		t.Fatal("a refused secret was stored")
	}
}

func TestSecretRuleOffAndAllowed(t *testing.T) {
	t.Parallel()
	off := policy.ResolveRules([]policy.RuleEntry{{Level: policy.LevelRepo, RuleID: awsRule, State: schema.SecretRuleOff}})
	f := setup(t, WithSettings(fixedSettings{set: policy.Defaults(), rules: off}))
	if _, err := f.eng.Create(context.Background(), "", schema.TableMemories, f.memFields("key "+fakeKey)); err != nil {
		t.Fatalf("rule turned off still blocks: %v", err)
	}
}

func TestSecretRedacted(t *testing.T) {
	t.Parallel()
	red, err := policy.Resolve([]policy.Entry{{Level: policy.LevelRepo, Key: policy.KeyOnDetect, Value: txt("redact")}})
	if err != nil {
		t.Fatal(err)
	}
	f := setup(t, WithSettings(fixedSettings{set: red}))
	r := f.memory(t, "key "+fakeKey+" end")
	if got := r.Fields["body"].String(); got != "key "+refs.Unstored+" end" {
		t.Fatalf("body = %q", got)
	}
	evs := f.audit.events()
	if len(evs[len(evs)-1].SecretsRedacted) != 1 {
		t.Fatal("redaction not audited")
	}
}

// memAdd is a test operation that writes through the engine.
type memAddIn struct {
	Body string `arg:"0" json:"body"`
	To   string `flag:"to" json:"to,omitempty"`
}

type memAddOut struct {
	ID string `json:"id"`
}

func memAddOp(f *fixture, confirm op.ConfirmRule) *op.Op[memAddIn, memAddOut] {
	return op.Define(op.Spec{
		Name: "memory.add", Summary: "add", Permission: op.PermWrite, Access: op.AccessWrite,
		Errors: []errs.Code{errs.SecretDetected, errs.NeedsConfirm}, Confirm: confirm,
	}, func(ctx context.Context, in memAddIn) (memAddOut, error) {
		w, err := f.eng.Create(ctx, "", schema.TableMemories, f.memFields(in.Body))
		if err != nil {
			return memAddOut{}, err
		}
		return memAddOut{ID: w.Row.ID.String()}, nil
	})
}

// yesConfirmer answers every question.
type yesConfirmer struct {
	answer bool
	asked  []string
}

func (c *yesConfirmer) Confirm(_ context.Context, q string) (bool, error) {
	c.asked = append(c.asked, q)
	return c.answer, nil
}

func TestSecretAskNeedsConfirmThenAccepted(t *testing.T) {
	t.Parallel()
	f := setup(t) // default policy: ask
	ctx := context.Background()
	body := "key " + fakeKey
	o := memAddOp(f, op.ConfirmRule{})
	_, err := f.eng.Run(ctx, o, op.ArgsDecoder{Args: []string{body}}, op.Caller{Actor: testActor})
	if !errs.Has(err, errs.NeedsConfirm) || strings.Contains(err.Error(), fakeKey) {
		t.Fatalf("err = %v, want E_NEEDS_CONFIRM without the secret", err)
	}
	if _, err = f.eng.Create(ctx, "", schema.TableMemories, f.memFields(body)); !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("direct write outside Run must ask too: %v", err)
	}
	// FB-SEC-1: a bare --yes is no choice; it still asks.
	if _, err = f.eng.Run(ctx, o, op.ArgsDecoder{Args: []string{body}}, op.Caller{Actor: testActor, Yes: true}); !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("bare --yes must still ask: %v", err)
	}
	res, err := f.eng.Run(ctx, o, op.ArgsDecoder{Args: []string{body}}, op.Caller{Actor: testActor, Yes: true,
		Secret: op.SecretChoice{NotASecret: true, Reason: "aws docs sample"}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := res.JSON()
	if err != nil || !strings.Contains(string(out), "mem_") {
		t.Fatalf("result %s %v", out, err)
	}
	evs := f.audit.events()
	if ev := evs[len(evs)-1]; len(ev.SecretsAccepted) != 1 || ev.Op != "memory.add" || strings.Contains(ev.SecretsAccepted[0].Preview, fakeKey) {
		t.Fatalf("acceptance not audited safely: %+v", ev)
	}
}

func TestRunConfirmRuleAndInteractive(t *testing.T) {
	t.Parallel()
	c := &yesConfirmer{answer: true}
	f := setup(t, WithConfirmer(c))
	ctx := context.Background()
	o := memAddOp(f, op.ConfirmRule{Always: true})
	if _, err := f.eng.Run(ctx, o, op.ArgsDecoder{Args: []string{"hello"}}, op.Caller{}); !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("non-interactive without yes: %v", err)
	}
	if _, err := f.eng.Run(ctx, o, op.ArgsDecoder{Args: []string{"hello"}}, op.Caller{Interactive: true}); err != nil || len(c.asked) != 1 {
		t.Fatalf("interactive caller not asked: %v %v", err, c.asked)
	}
	c.answer = false
	if _, err := f.eng.Run(ctx, o, op.ArgsDecoder{Args: []string{"hello"}}, op.Caller{Interactive: true}); !errs.Has(err, errs.Cancelled) {
		t.Fatalf("a no must cancel: %v", err)
	}
	ro := memAddOp(f, op.ConfirmRule{})
	_, err := f.eng.Run(ctx, ro, op.ArgsDecoder{Args: []string{"x"}, Flags: map[string][]string{"to": {"agency"}}}, op.Caller{Yes: true})
	if !errs.Has(err, errs.StoreReadOnly) {
		t.Fatalf("Run to a read-only store: %v", err)
	}
	_, err = f.eng.Run(ctx, ro, op.ArgsDecoder{Args: []string{"x"}, Flags: map[string][]string{"to": {"nowhere"}}}, op.Caller{Yes: true})
	if !errs.Has(err, errs.NotFound) {
		t.Fatalf("Run to an unknown store: %v", err)
	}
}

func TestUpdateMergesStaleWrites(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	r := f.memory(t, "first body")
	// A second process (another handle on the same data) edits the row
	// between this engine's read and write: simulate by writing straight to
	// the backend after reading.
	other := f.repo.Open(store.OpenOptions{})
	cur, err := other.Get(ctx, schema.TableMemories, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := cur.Decode()
	if err != nil {
		t.Fatal(err)
	}
	theirs.Fields["kind"] = txt(schema.MemoryKindArchival)
	theirs.HLC = hlc.Next(theirs.HLC, time.Now(), testActor)
	sr, err := store.FromRow(theirs)
	if err != nil {
		t.Fatal(err)
	}
	stale := &staleOnce{Backend: f.repo.Open(store.OpenOptions{}), before: func() error {
		return other.Write(ctx, []store.Change{{Row: sr, Expect: cur.HLC}})
	}}
	f2 := rewire(t, f, stale)
	w, err := f2.Update(ctx, Ref{ID: r.ID}, map[string]value.Value{"body": txt("second body")})
	if err != nil {
		t.Fatal(err)
	}
	if w.Row.Fields["body"].String() != "second body" || w.Row.Fields["kind"].String() != schema.MemoryKindArchival {
		t.Fatalf("lost an edit: %v", w.Row.Fields)
	}
	if evs := f.audit.events(); !evs[len(evs)-1].Merged {
		t.Fatal("merge not audited")
	}
}

// staleOnce runs before() once just before the first Write, so the write
// meets a row someone else changed.
type staleOnce struct {
	store.Backend
	once   sync.Once
	before func() error
}

func (s *staleOnce) Write(ctx context.Context, cs []store.Change) error {
	var err error
	s.once.Do(func() { err = s.before() })
	if err != nil {
		return err
	}
	return s.Backend.Write(ctx, cs)
}

// rewire builds a second engine over f's cache and audit with repo served
// by b.
func rewire(t *testing.T, f *fixture, b store.Backend) *Engine {
	t.Helper()
	e, err := New(
		WithStore(Attached{Name: store.SourceRepo, Backend: b}),
		WithCache(f.cache), WithRegistry(f.eng.reg), WithScanner(f.eng.scanner), WithActor(testActor), WithAudit(f.audit),
	)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestUpdateTextClashIsStaleWithBothValues(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	r := f.memory(t, "base")
	other := f.repo.Open(store.OpenOptions{})
	cur, err := other.Get(ctx, schema.TableMemories, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := cur.Decode()
	if err != nil {
		t.Fatal(err)
	}
	theirs.Fields["body"] = txt("their body")
	theirs.HLC = hlc.Next(theirs.HLC, time.Now(), testActor)
	sr, err := store.FromRow(theirs)
	if err != nil {
		t.Fatal(err)
	}
	e2 := rewire(t, f, &staleOnce{Backend: f.repo.Open(store.OpenOptions{}), before: func() error {
		return other.Write(ctx, []store.Change{{Row: sr, Expect: cur.HLC}})
	}})
	_, err = e2.Update(ctx, Ref{ID: r.ID}, map[string]value.Value{"body": txt("my body")})
	if !errs.Has(err, errs.Stale) {
		t.Fatalf("err = %v, want E_STALE", err)
	}
	var c *Conflict
	if !errors.As(err, &c) || len(c.Fields) != 1 || c.Fields[0].Newer == nil || c.Fields[0].Older == nil {
		t.Fatalf("conflict detail missing: %+v", c)
	}
	both := c.Fields[0].Newer.String() + "|" + c.Fields[0].Older.String()
	if !strings.Contains(both, "my body") || !strings.Contains(both, "their body") {
		t.Fatalf("both values not reported: %s", both)
	}
	_, got, err := f.eng.Get(ctx, Ref{ID: r.ID})
	if err != nil || got.Fields["body"].String() != "their body" {
		t.Fatalf("stored body = %v %v (their write must stand)", got.Fields["body"], err)
	}
}

// TestConcurrentUpdatesNeverLoseAnEdit runs parallel updates of distinct
// fields of one row: each either lands (cleanly merged) or fails with
// E_STALE, and every one that landed is in the final row.
func TestConcurrentUpdatesNeverLoseAnEdit(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	r := f.memory(t, "body")
	type edit struct {
		field string
		val   value.Value
	}
	edits := []edit{
		{"kind", txt(schema.MemoryKindEpisodic)},
		{"body", txt("new body")},
		{"repo_id", value.Null{}},
		{"valid_at", txt("2026-11-05T00:00:00Z")},
	}
	var wg sync.WaitGroup
	results := make([]error, len(edits))
	for i, ed := range edits {
		wg.Go(func() {
			_, results[i] = f.eng.Update(ctx, Ref{ID: r.ID}, map[string]value.Value{ed.field: ed.val})
		})
	}
	wg.Wait()
	_, final, err := f.eng.Get(ctx, Ref{ID: r.ID})
	if err != nil {
		t.Fatal(err)
	}
	landed := 0
	for i, ed := range edits {
		switch {
		case results[i] == nil:
			landed++
			if ed.val.Kind() == value.KindNull {
				continue
			}
			got, ok := final.Fields[ed.field]
			if !ok {
				t.Errorf("edit of %s landed but is lost", ed.field)
				continue
			}
			want := ed.val.String()
			if ed.field == "valid_at" {
				want = "2026-11-05T00:00:00.000000000Z"
			}
			if got.String() != want {
				t.Errorf("edit of %s landed but final has %q", ed.field, got.String())
			}
		case errs.Has(results[i], errs.Stale):
		default:
			t.Errorf("edit of %s: unexpected error %v", ed.field, results[i])
		}
	}
	if landed == 0 {
		t.Fatal("no edit landed")
	}
	merged := 0
	for _, ev := range f.audit.events() {
		if ev.Merged {
			merged++
		}
	}
	t.Logf("%d of %d edits landed, %d through a merge", landed, len(edits), merged)
}

// hardDelete is the tombstone write purge runs (remove), on a backend with
// no purged-id list: the relation rules below act there, while the soft
// delete leaves referrers alone (DELETION D8).
func hardDelete(ctx context.Context, e *Engine, ref Ref) (Written, error) {
	src, cur, err := e.locate(ctx, ref)
	if err != nil {
		return Written{}, err
	}
	if cur.Deleted {
		return Written{}, errs.New(errs.NotFound, "already deleted")
	}
	return e.remove(ctx, src, cur, false)
}

func TestPurgeKeepsTombstoneAndAppliesOnDelete(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	m := f.memory(t, "to delete")
	w, err := hardDelete(ctx, f.eng, Ref{ID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !w.Row.Deleted {
		t.Fatal("no tombstone")
	}
	_, got, err := f.eng.Get(ctx, Ref{ID: m.ID})
	if err != nil || !got.Deleted {
		t.Fatalf("tombstone not kept: %v %+v", err, got)
	}
	if _, err = f.eng.Update(ctx, Ref{ID: m.ID}, map[string]value.Value{"body": txt("back")}); !errs.Has(err, errs.NotFound) {
		t.Fatalf("edited a tombstone: %v", err)
	}
	if c, ok := f.cache.get(schema.TableMemories, m.ID); !ok || !c.Deleted {
		t.Fatal("tombstone not applied to the cache")
	}

	// memories.project_id → projects is restrict: a live memory blocks
	// deleting its project; the tombstoned one above does not count.
	if _, err = hardDelete(ctx, f.eng, Ref{ID: f.project}); err != nil {
		t.Fatalf("a tombstoned dependent blocked the delete: %v", err)
	}
	f2 := setup(t)
	f2.memory(t, "stays")
	if _, err = hardDelete(ctx, f2.eng, Ref{ID: f2.project}); !errs.Has(err, errs.HasDependents) {
		t.Fatalf("restrict not applied: %v", err)
	}
	if _, got, _ := f2.eng.Get(ctx, Ref{ID: f2.project}); got.Deleted {
		t.Fatal("a refused delete wrote a tombstone")
	}

	// memories.repo_id → repos is set_null: deleting the repo clears it.
	repo, err := f2.eng.Create(ctx, "", schema.TableRepos, map[string]value.Value{"project_id": txt(f2.project.String()), "mount_name": txt("backend")})
	if err != nil {
		t.Fatal(err)
	}
	fields := f2.memFields("in a repo")
	fields["repo_id"] = txt(repo.Row.ID.String())
	inRepo, err := f2.eng.Create(ctx, "", schema.TableMemories, fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = hardDelete(ctx, f2.eng, Ref{ID: repo.Row.ID}); err != nil {
		t.Fatal(err)
	}
	_, cleared, err := f2.eng.Get(ctx, Ref{ID: inRepo.Row.ID})
	if err != nil || cleared.Deleted {
		t.Fatalf("set_null dependent: %v %+v", err, cleared)
	}
	if _, still := cleared.Fields["repo_id"]; still {
		t.Fatal("set_null did not clear repo_id")
	}
}

func TestPurgeCascades(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	key, err := f.eng.Create(ctx, "", schema.TableMetaKeys, map[string]value.Value{"name": txt("client"), "scope": txt("repo"), "type": txt("text")})
	if err != nil {
		t.Fatal(err)
	}
	val, err := f.eng.Create(ctx, "", schema.TableMetaValues, map[string]value.Value{"key_id": txt(key.Row.ID.String()), "name": txt("acme")})
	if err != nil {
		t.Fatal(err)
	}
	w, err := hardDelete(ctx, f.eng, Ref{ID: key.Row.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Also) != 1 || w.Also[0].Row.ID != val.Row.ID || !w.Also[0].Row.Deleted {
		t.Fatalf("cascade = %+v", w.Also)
	}
}

func TestRefreshPicksUpRowsWrittenToTheBackend(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	// A row written straight to the backend (a git pull, another process).
	id, err := uuidIDs{}.NewID(ids.PrefixProject)
	if err != nil {
		t.Fatal(err)
	}
	h := hlc.Next(hlc.HLC{}, time.Now(), testActor)
	sr, err := store.FromRow(row.Row{Table: schema.TableProjects, ID: id, HLC: h, Fields: map[string]value.Value{"name": txt("pulled")}})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.repo.Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: sr}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.cache.get(schema.TableProjects, id); ok {
		t.Fatal("cache saw the row before Refresh")
	}
	n, err := f.eng.Refresh(ctx, store.SourceRepo)
	if err != nil || n == 0 {
		t.Fatalf("refresh: %d %v", n, err)
	}
	if _, ok := f.cache.get(schema.TableProjects, id); !ok {
		t.Fatal("refresh did not apply the row")
	}
	if n, err := f.eng.Refresh(ctx, store.SourceRepo); err != nil || n != 0 {
		t.Fatalf("second refresh re-read %d rows (%v): the cursor did not move", n, err)
	}
	if _, err := f.eng.Sync(ctx, store.SourceRepo); err != nil {
		t.Fatal(err)
	}
}

func TestAmbiguousID(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	sr, err := f.repo.Open(store.OpenOptions{}).Get(ctx, schema.TableProjects, f.project)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.agency.Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: sr}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.eng.Get(ctx, Ref{ID: f.project}); !errs.Has(err, errs.AmbiguousID) {
		t.Fatalf("err = %v, want E_AMBIGUOUS_ID", err)
	}
	if src, _, err := f.eng.Get(ctx, Ref{Store: agency, ID: f.project}); err != nil || src != agency {
		t.Fatalf("named store: %s %v", src, err)
	}
}

// degradedScanner reports every scan as degraded.
type degradedScanner struct{ scan.Scanner }

func (d degradedScanner) ScanReport(field, text string) scan.Report {
	return scan.Report{Findings: d.Scan(field, text), Degraded: true, Note: "field over the size gate: only lore's rules ran"}
}

func TestDegradedScanWarns(t *testing.T) {
	t.Parallel()
	f := setup(t)
	e := rewire(t, f, f.repo.Open(store.OpenOptions{}))
	e.scanner = degradedScanner{f.eng.scanner}
	w, err := e.Create(context.Background(), "", schema.TableProjects, map[string]value.Value{"name": txt("big")})
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Warnings) != 1 || w.Warnings[0].Code != WarnScanDegraded || w.Warnings[0].Field != "name" {
		t.Fatalf("warnings = %+v", w.Warnings)
	}
}

func TestHasOtherWriter(t *testing.T) {
	t.Parallel()
	other, err := ids.Parse[ids.Actor]("act_01936f0000007a1b8c2d3e4f5a6b7c8e")
	if err != nil {
		t.Fatal(err)
	}
	mine := row.Row{CreatedBy: testActor, UpdatedBy: testActor}
	unknown := row.Row{}
	if HasOtherWriter([]row.Row{mine, unknown}, testActor) {
		t.Fatal("a solo repo must not ask")
	}
	if !HasOtherWriter([]row.Row{mine, {UpdatedBy: other}}, testActor) {
		t.Fatal("another writer must ask")
	}
}

// TestAmbiguousIDHintNamesACommandThatTakesIt pins B1-5: the hint of
// E_AMBIGUOUS_ID names `lore row move <id> --to <store>` (which takes
// --to and settles the duplicate), never a bare "--to" that show, edit or
// migrate run refuse.
func TestAmbiguousIDHintNamesACommandThatTakesIt(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	sr, err := f.repo.Open(store.OpenOptions{}).Get(ctx, schema.TableProjects, f.project)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.agency.Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: sr}}); err != nil {
		t.Fatal(err)
	}
	_, _, err = f.eng.Get(ctx, Ref{ID: f.project})
	var e *errs.Error
	if !errors.As(err, &e) || !strings.Contains(e.Hint, "lore row move "+f.project.String()+" --to ") {
		t.Fatalf("hint of %v", err)
	}
}
