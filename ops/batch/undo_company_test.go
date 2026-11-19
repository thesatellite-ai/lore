package batch

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/profilestore/profilestoretest"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// companyEnv is a batch Env with rows, before-images and an org roster
// (profilestoretest.Fake) whose admin is not the caller.
type companyEnv struct {
	*profilestoretest.Fake
	evs    []Event
	rows   map[ids.AnyID]row.Row
	src    map[ids.AnyID]store.Source
	priors map[ids.AnyID]Prior
	wrote  int
	seq    int
}

func (e *companyEnv) BatchEvents(context.Context) ([]Event, error) { return e.evs, nil }
func (e *companyEnv) Get(_ context.Context, ref engine.Ref) (store.Source, row.Row, error) {
	r, ok := e.rows[ref.ID]
	if !ok {
		return "", row.Row{}, errs.New(errs.NotFound, "no row")
	}
	return e.src[ref.ID], r, nil
}
func (e *companyEnv) BatchPriors(context.Context, ids.ID[ids.Batch]) (map[ids.AnyID]Prior, error) {
	return e.priors, nil
}
func (e *companyEnv) Revert(_ context.Context, _ engine.Ref, _ hlc.HLC, before row.Row) (engine.Written, error) {
	e.wrote++
	return engine.Written{Row: before}, nil
}
func (e *companyEnv) Restore(context.Context, string) (engine.TrashResult, error) {
	e.wrote++
	return engine.TrashResult{}, nil
}
func (e *companyEnv) Delete(context.Context, engine.Ref) (engine.Written, error) {
	e.wrote++
	return engine.Written{}, nil
}

func ctext(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// newCompanyEnv seeds an org roster naming Ada Admin as its admin; the
// caller is admin when asAdmin, else a member.
func newCompanyEnv(t *testing.T, asAdmin bool) *companyEnv {
	t.Helper()
	admin, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	member, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	caller := member
	if asAdmin {
		caller = admin
	}
	f := profilestoretest.New(caller, time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
	f.Put(row.Row{Table: schema.TableActors, ID: admin.Any(), Fields: map[string]value.Value{
		schema.FieldActorDisplayName: ctext(t, "Ada Admin"), schema.FieldActorHandle: ctext(t, "ada")}})
	per, err := ids.New[ids.ActorPeriod]()
	if err != nil {
		t.Fatal(err)
	}
	f.Put(row.Row{Table: schema.TableActorPeriods, ID: per.Any(), Fields: map[string]value.Value{
		schema.FieldPeriodActor: ctext(t, admin.String()), schema.FieldPeriodOrgRole: ctext(t, "admin")}})
	return &companyEnv{Fake: f, rows: map[ids.AnyID]row.Row{}, src: map[ids.AnyID]store.Source{}, priors: map[ids.AnyID]Prior{}}
}

// rule adds a live rule of severity sev in src and returns it.
func (e *companyEnv) rule(t *testing.T, src store.Source, sev string) row.Row {
	t.Helper()
	e.seq++
	id, err := ids.Natural(ids.PrefixRule, string(src), sev, strconv.Itoa(e.seq))
	if err != nil {
		t.Fatal(err)
	}
	r := row.Row{Table: schema.TableRules, ID: id, Fields: map[string]value.Value{"severity": ctext(t, sev), "body": ctext(t, "a rule")}}
	e.rows[r.ID], e.src[r.ID] = r, src
	return r
}

// TestBatchUndoCompanyRowsAdminOnly guards devco D-664: batch undo took a
// company must rule out of what every repo is told with no role check
// (the one-row and bulk forms refuse a member, D-637/D-659). A member's
// undo of a create batch that added one, or of an edit batch whose undo
// lowers one, is refused naming the row and the admin, before any write;
// an admin's goes on; a project folder of the company store is company
// ground too (devco D-681); another org's folder with no roster here is
// every member's.
func TestBatchUndoCompanyRowsAdminOnly(t *testing.T) {
	ctx := engine.WithConfirmed(context.Background())
	for _, c := range []struct {
		name    string
		asAdmin bool
		src     store.Source
		kind    string // create, or edit (undo puts severity back to should)
		refused bool
	}{
		{"member undoes the add of a company must rule", false, "acme/org", "create", true},
		{"member undoes the raise of a company rule to must", false, "acme/org", "edit", true},
		{"admin undoes the add of a company must rule", true, "acme/org", "create", false},
		// devco D-681: a project folder of the company store is company ground
		{"member undoes the add of a project folder's must rule", false, "agency/web", "create", true},
		{"admin undoes the add of a project folder's must rule", true, "agency/web", "create", false},
		{"member undoes the add of another org's folder's must rule", false, "acme/web", "create", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newCompanyEnv(t, c.asAdmin)
			bid := mustBatch(t)
			r := e.rule(t, c.src, schema.RuleSeverityMust)
			ev := Event{Batch: bid, Kind: bulk.KindImport, Op: "rule.add", Action: actCreate, Store: c.src, Table: schema.TableRules, ID: r.ID}
			if c.kind == "edit" {
				ev.Kind, ev.Op, ev.Action = bulk.KindMeta, "meta.set", actUpdate
				before := r
				before.Fields = map[string]value.Value{"severity": ctext(t, schema.RuleSeverityShould), "body": r.Fields["body"]}
				e.priors[r.ID] = Prior{Store: c.src, Before: before, After: r.HLC}
			}
			e.evs = []Event{ev}
			_, err := ops{env: e}.undo(ctx, UndoIn{ID: bid.String()})
			if c.refused {
				if !errs.Has(err, errs.Refused) || e.wrote != 0 {
					t.Fatalf("devco:D-664: want E_REFUSED with nothing written, got %v (writes %d)", err, e.wrote)
				}
				var le *errs.Error
				if !errors.As(err, &le) || !strings.Contains(le.Message, r.ID.String()) || !strings.Contains(le.Hint, "Ada Admin") || !strings.Contains(le.Hint, "lore batch undo --id "+bid.String()) {
					t.Fatalf("devco:D-664: the refusal names no row, admin or admin's line: %v (hint %q)", err, le.Hint)
				}
				return
			}
			if errs.Has(err, errs.Refused) {
				t.Fatalf("devco:D-664: refused where it may run: %v", err)
			}
		})
	}
}
