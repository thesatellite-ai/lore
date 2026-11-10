package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// sessionWritingSink is cmd/lore's audit sink in small: the first event of
// the process writes this machine's session row through the engine with
// the ctx it was handed (wire chainAudit.Session -> App.CurrentSession).
type sessionWritingSink struct {
	auditLog
	eng     *Engine
	once    sync.Once
	project string
	err     error
}

func (s *sessionWritingSink) Record(ctx context.Context, ev Event) error {
	// setup's own writes come before the engine is bound; the session
	// row's own event is never linked (P3-LOCAL-5: it would re-enter)
	if s.eng == nil || ev.Table == schema.TableSessions {
		return s.auditLog.Record(ctx, ev)
	}
	s.once.Do(func() {
		_, s.err = s.eng.Create(ctx, store.SourceMachine, schema.TableSessions, map[string]value.Value{
			schema.FieldSessionProjectID: txt(s.project),
			schema.FieldSessionStartedAt: value.DateTimeFrom(time.Date(2026, 11, 6, 4, 0, 0, 0, time.UTC)),
		})
	})
	if s.err != nil {
		return s.err
	}
	return s.auditLog.Record(ctx, ev)
}

// TestSecretAuditInStagedBatchStaysOutOfTheBatch (g-meta smoke2 res):
// `lore resource import --not-a-secret --reason … --yes` on cmd/lore failed
// "one batch cannot write to two stores (machine, repo)" because the
// secret audit is recorded mid-write, its sink wrote the session row with
// the staged ctx, and that machine row joined the import's repo batch. The
// secret audit's side writes go straight to their own store: the staged
// repo rows (row + allow entries) commit as one batch, the session row is
// in the machine store, and nothing of the batch is written before Commit.
func TestSecretAuditInStagedBatchStaysOutOfTheBatch(t *testing.T) {
	t.Parallel()
	machine := memstore.NewData()
	sink := &sessionWritingSink{}
	f := setup(t, WithAudit(sink), WithMachineStore(Attached{Name: store.SourceMachine, Backend: machine.Open(store.OpenOptions{})}))
	sink.eng, sink.project = f.eng, f.project.String()
	for _, c := range []struct {
		name   string
		caller op.Caller
		code   errs.Code // non-empty: the staged write is refused with it
	}{
		{name: "not-a-secret confirmed", caller: op.Caller{Yes: true, Secret: op.SecretChoice{NotASecret: true, Reason: "fixture"}}},
		{name: "unstored", caller: op.Caller{Yes: true, Secret: op.SecretChoice{Unstored: true}}},
		{name: "bare yes under ask", caller: op.Caller{Yes: true}, code: errs.NeedsConfirm},
	} {
		t.Run(c.name, func(t *testing.T) {
			sink.once, sink.err = sync.Once{}, nil
			before := len(storedIDs(t, f.eng, store.SourceMachine, schema.TableSessions))
			ctx := Staging(context.WithValue(context.Background(), callKey{}, call{op: "memory.import", caller: c.caller}))
			w, err := f.eng.Create(ctx, store.SourceRepo, schema.TableMemories, f.memFields("key "+fakeKey+" end"))
			if c.code != "" {
				if !errs.Has(err, c.code) {
					t.Fatalf("err = %v, want %s", err, c.code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if storedIDs(t, f.eng, store.SourceRepo, schema.TableMemories)[w.Row.ID] {
				t.Fatal("staged row written before Commit")
			}
			if got := len(storedIDs(t, f.eng, store.SourceMachine, schema.TableSessions)); got != before+1 {
				t.Fatalf("session rows = %d, want %d (written at once, outside the batch)", got, before+1)
			}
			if _, err := Commit(ctx); err != nil {
				t.Fatalf("Commit: %v", err)
			}
			if !storedIDs(t, f.eng, store.SourceRepo, schema.TableMemories)[w.Row.ID] {
				t.Fatal("staged row not written by Commit")
			}
		})
	}
}
