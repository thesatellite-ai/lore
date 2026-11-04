package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

const (
	provCommit = "4e6ed1c09b7a2f51d3c8e0a4b6f2d1e9c7a5b3f0"
	provRepo   = "rep_01a1a679f24c759abb49e8625618d86c"
	provMach   = "mch_0cd630f071ab154c3b6a0f44e9366337ec0fb87c4e688d19fc5e84f1bf8dd980"
)

// fakeProv is a Provenance with fixed facts; upstream names the branches
// that have one.
type fakeProv struct {
	upstream map[string]bool
}

func (fakeProv) Place(context.Context) origin.Facts {
	return origin.Facts{Repo: provRepo, Dir: "services/billing", Git: origin.Git{State: origin.GitBranch, Branch: "mira/leaving-acme", Commit: provCommit}}
}

func (p fakeProv) Upstream(_ context.Context, b string) bool { return p.upstream[b] }

func (fakeProv) Writer() (string, ids.MachineID) {
	m, err := ids.ParseMachine(provMach)
	if err != nil {
		panic(err) // a constant: cannot fail
	}
	return "0.1.18", m
}

// newProvWorld is newF15World with a Provenance (repo store a client repo).
func newProvWorld(t *testing.T, p Provenance) *f15World {
	t.Helper()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := scan.NewRegexScanner([]scan.Rule{{ID: awsRule, Regex: `AKIA[0-9A-Z]{16}`, Keywords: []string{"akia"}, Source: scan.SourceBuiltin}})
	if err != nil {
		t.Fatal(err)
	}
	w := &f15World{data: map[store.Source]*memstore.Data{}}
	opts := []Option{WithCache(newFakeCache()), WithRegistry(reg), WithScanner(sc), WithActor(testActor), WithAudit(&auditLog{}), WithProvenance(p)}
	for _, n := range []store.Source{store.SourceRepo, agency, personal} {
		w.data[n] = memstore.NewData()
		opts = append(opts, WithStore(Attached{Name: n, Backend: w.data[n].Open(store.OpenOptions{}), Client: n == store.SourceRepo}))
	}
	if w.eng, err = New(opts...); err != nil {
		t.Fatal(err)
	}
	return w
}

// writeStamp returns r's `_w` as JSON text ("" when absent).
func writeStamp(r row.Row) string {
	if v, ok := r.Reserved[origin.KeyWrite]; ok {
		return v.String()
	}
	return ""
}

// TestOriginAndStampOnOurSide: a row written into our store carries the
// whole place (repo, PROV6 dir, git with branch) and `_w {m, v}` (PROV4,
// OQ7); an update restamps `_w` and keeps `_o` byte for byte. A row in a
// code repo that is not a client repo still gets `{v}` only (Q-R3-9).
func TestOriginAndStampOnOurSide(t *testing.T) {
	ctx := context.Background()
	w := newProvWorld(t, fakeProv{})
	prj := w.project(t, agency, "acme-hub")
	m := w.memIn(t, agency, prj, "ours")
	// no op runs here, so cmd is empty and left out
	want := `{"dir":"services/billing","git":{"branch":"mira/leaving-acme","commit":"` + provCommit + `","state":"branch"},"repo":"` + provRepo + `","store":"agency"}`
	if m.Origin.String() != want {
		t.Fatalf("_o = %s", m.Origin)
	}
	if s := writeStamp(m); s != `{"m":"`+provMach+`","v":"0.1.18"}` {
		t.Fatalf("_w = %s", s)
	}
	up, err := w.eng.Update(ctx, Ref{ID: m.ID}, map[string]value.Value{"body": txt("edited")})
	if err != nil {
		t.Fatal(err)
	}
	if up.Row.Origin.String() != m.Origin.String() || writeStamp(up.Row) == "" {
		t.Fatalf("update: _o %s, _w %s", up.Row.Origin, writeStamp(up.Row))
	}
	own := newF15World(t, false)
	own.eng.prov = fakeProv{}
	r := own.memIn(t, store.SourceRepo, own.project(t, store.SourceRepo, "own"), "own repo row")
	if s := writeStamp(r); s != `{"v":"0.1.18"}` {
		t.Fatalf("own repo _w = %s", s)
	}
}

// TestOriginInClientRepo is PROV8 at create: a row born in the client
// repo keeps dir, repo, commit and state, drops a local-only branch (kept
// only with an upstream) and gets `_w {v}` (Q-R3-3).
func TestOriginInClientRepo(t *testing.T) {
	for _, withUp := range []bool{false, true} {
		w := newProvWorld(t, fakeProv{upstream: map[string]bool{"mira/leaving-acme": withUp}})
		prj := w.project(t, store.SourceRepo, "app")
		m := w.memIn(t, store.SourceRepo, prj, "client row")
		o := m.Origin.String()
		if !strings.Contains(o, `"store":"repo"`) || !strings.Contains(o, `"dir":"services/billing"`) ||
			!strings.Contains(o, `"repo":"`+provRepo+`"`) || !strings.Contains(o, `"commit":"`+provCommit+`"`) || !strings.Contains(o, `"state":"branch"`) {
			t.Fatalf("client _o lost a client-safe key: %s", o)
		}
		if strings.Contains(o, "leaving-acme") != withUp {
			t.Fatalf("upstream=%v but branch kept=%v: %s", withUp, !withUp, o)
		}
		if s := writeStamp(m); s != `{"v":"0.1.18"}` {
			t.Fatalf("client _w = %s", s)
		}
	}
}

// TestMoveIntoClientLeavesNoOriginCopy is PROV7: a row moved into the
// client repo keeps only the client-safe part of its _o (no moves, no
// internal ids, no dir/git of another checkout) and no copy of the
// dropped keys is left in any store (the source keeps a bare tombstone).
func TestMoveIntoClientLeavesNoOriginCopy(t *testing.T) {
	ctx := context.Background()
	w := newProvWorld(t, fakeProv{})
	w.project(t, store.SourceRepo, "app")
	prj := w.project(t, agency, "acme-hub")
	id, err := w.eng.ids.NewID(mustPrefix(t, w, schema.TableMemories))
	if err != nil {
		t.Fatal(err)
	}
	const secretish = "org_0000000000000000000000000000beef"
	born, err := value.JSONOf([]byte(`{"cmd":"memory add","dir":"internal/tools","folder":"prj_0000000000000000000000000000f01d","git":{"branch":"fix/acme-overbilling","commit":"` + provCommit + `","state":"branch"},"org":"` + secretish + `","plugin":"plg_0000000000000000000000000000ab12","repo":"rep_0000000000000000000000000000aaaa","store":"agency"}`))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := value.RefOf(prj)
	if err != nil {
		t.Fatal(err)
	}
	r := row.Row{Table: schema.TableMemories, ID: id, HLC: hlc.Next(hlc.HLC{}, w.eng.wall(), testActor), Origin: born,
		Fields: map[string]value.Value{"project_id": ref, "kind": txt(schema.MemoryKindCore), "body": txt("born elsewhere"), "source_kind": txt("manual")}}
	sr, err := store.FromRow(r)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.data[agency].Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: sr}}); err != nil {
		t.Fatal(err)
	}
	got, err := w.eng.Move(ctx, Ref{ID: id}, store.SourceRepo)
	if err != nil {
		t.Fatal(err)
	}
	if o := got.Row.Origin.String(); o != `{"cmd":"memory add","store":"repo"}` {
		t.Fatalf("client _o = %s", o)
	}
	for name, d := range w.data {
		rows, _, rerr := d.Open(store.OpenOptions{}).Read(ctx, "")
		if rerr != nil {
			t.Fatal(rerr)
		}
		for _, x := range rows {
			doc := string(x.Doc)
			for _, dropped := range []string{secretish, "fix/acme-overbilling", "internal/tools", "plg_", "prj_0000000000000000000000000000f01d", "rep_0000000000000000000000000000aaaa"} {
				if strings.Contains(doc, dropped) {
					t.Fatalf("store %s still holds %s after the move: %s", name, dropped, doc)
				}
			}
		}
	}
}

// TestNoProvenanceKeepsOldShape: without a Provenance the block is
// {cmd, store} and no `_w` is written (embedders and old tests).
func TestNoProvenanceKeepsOldShape(t *testing.T) {
	w := newF15World(t, false)
	prj := w.project(t, store.SourceRepo, "p")
	m := w.memIn(t, store.SourceRepo, prj, "x")
	if strings.Contains(m.Origin.String(), "git") || writeStamp(m) != "" {
		t.Fatalf("_o %s _w %s", m.Origin, writeStamp(m))
	}
}

// TestImportSlotAndBatch: an importer's ctx fills `_o.import` (XQ9) and a
// bulk action's ctx puts its batch on every audit event (XQ1).
func TestImportSlotAndBatch(t *testing.T) {
	w := newProvWorld(t, fakeProv{})
	prj := w.project(t, agency, "acme-hub")
	bid, err := ids.New[ids.Batch]()
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithBatch(WithImport(context.Background(), origin.Import{Source: "jira", ExtID: "AGY-1234"}), Batch{ID: bid, Kind: "import"})
	ref, err := value.RefOf(prj)
	if err != nil {
		t.Fatal(err)
	}
	got, err := w.eng.Create(ctx, agency, schema.TableMemories, map[string]value.Value{"project_id": ref, "kind": txt(schema.MemoryKindCore), "body": txt("imported"), "source_kind": txt("manual")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Row.Origin.String(), `"import":{"ext_id":"AGY-1234","source":"jira"}`) {
		t.Fatalf("_o = %s", got.Row.Origin)
	}
	log, ok := w.eng.audit.(*auditLog)
	if !ok {
		t.Fatal("audit sink")
	}
	last := log.evs[len(log.evs)-1]
	if last.Batch.ID != bid || last.Batch.Kind != "import" {
		t.Fatalf("event batch = %+v", last.Batch)
	}
	if _, ok := BatchOf(context.Background()); ok {
		t.Fatal("a bare ctx has no batch")
	}
}

// TestConfirmed is XQ2: true inside Run with Yes, false without, false
// outside Run.
func TestConfirmed(t *testing.T) {
	f := setup(t)
	var seen []bool
	o := op.Define(op.Spec{Name: "memory.probe", Summary: "probe", Permission: op.PermWrite, Access: op.AccessWrite},
		func(ctx context.Context, _ memAddIn) (memAddOut, error) {
			seen = append(seen, Confirmed(ctx))
			return memAddOut{}, nil
		})
	for _, yes := range []bool{true, false} {
		if _, err := f.eng.Run(context.Background(), o, op.ArgsDecoder{Args: []string{"x"}}, op.Caller{Yes: yes}); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 2 || !seen[0] || seen[1] || Confirmed(context.Background()) {
		t.Fatalf("Confirmed = %v (outside Run %v)", seen, Confirmed(context.Background()))
	}
}
