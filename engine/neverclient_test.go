package engine

import (
	"bytes"
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// ncFields is a valid row of each NeverClient table (TEAM_PROFILES 2.2-2.6)
// for store src of fixture f.
func ncFields(t *testing.T, f *csFixture, src store.Source) map[row.Table]map[string]value.Value {
	t.Helper()
	date := func(s string) value.Value {
		d, err := value.DateOf(s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	roles, err := value.ListOf(value.KindText, txt("dev"))
	if err != nil {
		t.Fatal(err)
	}
	return map[row.Table]map[string]value.Value{
		schema.TableActorContacts: {
			schema.FieldContactActor: txt(f.owner.String()), schema.FieldContactType: txt("email"),
			schema.FieldContactValue: txt("Mira.Patel@Gmail.com"), schema.FieldContactValueNorm: txt("mira.patel@gmail.com"),
			schema.FieldContactIdentity: value.BoolOf(true),
		},
		schema.TableContactTypes: {schema.FieldContactTypeName: txt("bitbucket"), schema.FieldContactTypeKind: txt(schema.ContactKindHandle)},
		schema.TableRoles:        {schema.FieldRoleName: txt("ops")},
		schema.TableActorPeriods: {
			schema.FieldPeriodActor: txt(f.owner.String()), schema.FieldPeriodType: txt(schema.PeriodTypeEmployee),
			schema.FieldPeriodStartedOn: date("2026-01-12"), schema.FieldPeriodEndReason: txt("moved abroad"),
		},
		schema.TableProjectMembers: {
			schema.FieldMemberProject: txt(f.project[src].String()), schema.FieldMemberActor: txt(f.owner.String()),
			schema.FieldMemberRoles: roles, schema.FieldMemberStartedOn: date("2026-01-12"),
		},
		schema.TableInvites: {
			schema.FieldInviteEmail: txt("tomas@agency.example"), schema.FieldInvitePeriodType: txt(schema.PeriodTypeEmployee),
			schema.FieldInviteStatus: txt(schema.InviteStatusPending), schema.FieldInviteExpiresOn: date("2026-11-20"),
		},
	}
}

// TS-3 / TP-7: a NeverClient table's row is refused (E_LEAK_BLOCKED) on a
// create into a client-class store and a move into one, and nothing is
// written there; the same create into an internal store works. Every
// table the registry marks NeverClient is in the matrix.
func TestNeverClientCreateAndMoveRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	probe := csSetup(t)
	var tables []row.Table
	for _, d := range probe.reg.Tables() {
		if d.NeverClient {
			tables = append(tables, d.Name)
		}
	}
	if len(tables) != len(ncFields(t, probe, csInternal)) {
		t.Fatalf("NeverClient tables %v: the matrix covers %d", tables, len(ncFields(t, probe, csInternal)))
	}
	for _, table := range tables {
		t.Run(string(table), func(t *testing.T) {
			f := csSetup(t)
			if _, err := f.eng.Create(ctx, store.SourceRepo, table, ncFields(t, f, store.SourceRepo)[table]); !errs.Has(err, errs.LeakBlocked) {
				t.Fatalf("create into the client repo: err = %v, want E_LEAK_BLOCKED", err)
			}
			w, err := f.eng.Create(ctx, csInternal, table, ncFields(t, f, csInternal)[table])
			if err != nil {
				t.Fatalf("create into the internal store: %v", err)
			}
			if _, err = f.eng.Move(ctx, Ref{Store: csInternal, Table: table, ID: w.Row.ID}, store.SourceRepo); !errs.Has(err, errs.LeakBlocked) {
				t.Fatalf("move into the client repo: err = %v, want E_LEAK_BLOCKED", err)
			}
			if rows := f.repo.Open(store.OpenOptions{}); rows != nil {
				if got, gerr := rows.Get(ctx, table, w.Row.ID); gerr == nil {
					t.Fatalf("a %s row reached the client repo:\n%s", table, got.Doc)
				}
			}
			if _, _, err = f.eng.Get(ctx, Ref{Store: csInternal, Table: table, ID: w.Row.ID}); err != nil {
				t.Fatalf("the refused move lost the internal row: %v", err)
			}
		})
	}
}

// A NeverClient row that is already in a client repo's files (copied by
// hand) is never written again by the engine there: update and delete
// (into the trash) refuse and leave the file's bytes as they were. Purge
// shares delete's write path (engine remove); the memstore backend records
// no purged ids, so it is not driven here.
func TestNeverClientEditOfLeakedRowRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, verb := range []string{"update", "delete"} {
		t.Run(verb, func(t *testing.T) {
			f, org := csSetup(t), csSetup(t) // the row is copied from another org store's files
			w, err := org.eng.Create(ctx, csInternal, schema.TableActorContacts, ncFields(t, org, csInternal)[schema.TableActorContacts])
			if err != nil {
				t.Fatal(err)
			}
			doc, err := org.internal.Open(store.OpenOptions{}).Get(ctx, schema.TableActorContacts, w.Row.ID)
			if err != nil {
				t.Fatal(err)
			}
			repo := f.repo.Open(store.OpenOptions{})
			if err = repo.Write(ctx, []store.Change{{Row: doc}}); err != nil {
				t.Fatal(err)
			}
			if _, err = f.eng.Refresh(ctx, store.SourceRepo); err != nil {
				t.Fatal(err)
			}
			ref := Ref{Store: store.SourceRepo, Table: schema.TableActorContacts, ID: w.Row.ID}
			switch verb {
			case "update":
				_, err = f.eng.Update(ctx, ref, map[string]value.Value{schema.FieldContactLabel: txt("work")})
			case "delete":
				_, err = f.eng.Delete(ctx, ref)
			}
			if !errs.Has(err, errs.LeakBlocked) {
				t.Fatalf("%s of a leaked contact row: err = %v, want E_LEAK_BLOCKED", verb, err)
			}
			got, gerr := f.repo.Open(store.OpenOptions{}).Get(ctx, schema.TableActorContacts, w.Row.ID)
			if gerr != nil || !bytes.Equal(got.Doc, doc.Doc) {
				t.Fatalf("%s rewrote the leaked file (err %v):\n%s", verb, gerr, got.Doc)
			}
		})
	}
}

// TP-7 / F18: the profile fields added to actors (TEAM_PROFILES 2.1)
// are Internal, so an actor written into a client repo keeps none of
// them, while the internal store keeps them all.
func TestNeverClientActorProfileFieldsStripped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := csSetup(t)
	date := func(s string) value.Value {
		d, err := value.DateOf(s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	vals := map[string]value.Value{
		schema.FieldActorPhotoURL: txt("https://example.com/m.png"), schema.FieldActorTimezone: txt("Europe/Istanbul"),
		schema.FieldActorLocation: txt("Ankara, TR"), schema.FieldActorTitle: txt("Backend engineer"), schema.FieldActorBio: txt("likes Go"),
		schema.FieldActorBirthday: date("1990-04-17"), schema.FieldActorStatus: txt(schema.ActorStatusOnLeave),
		schema.FieldActorLeaveFrom: date("2026-06-01"), schema.FieldActorLeaveUntil: date("2026-06-30"),
		schema.FieldActorStatusNote: txt("parental leave"),
	}
	if len(vals) != len(schema.ProfileActorFields()) {
		t.Fatalf("the matrix covers %d of %d profile fields", len(vals), len(schema.ProfileActorFields()))
	}
	for _, src := range []store.Source{store.SourceRepo, csInternal} {
		if _, err := f.eng.Update(ctx, Ref{Store: src, Table: schema.TableActors, ID: f.owner.Any()}, vals); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		got, err := f.data(src).Open(store.OpenOptions{}).Get(ctx, schema.TableActors, f.owner.Any())
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range schema.ProfileActorFields() {
			has := bytes.Contains(got.Doc, []byte(`"`+k+`"`))
			if src == store.SourceRepo && has {
				t.Errorf("client actor file holds %s:\n%s", k, got.Doc)
			}
			if src == csInternal && !has {
				t.Errorf("internal actor file lost %s:\n%s", k, got.Doc)
			}
		}
	}
}

// TEAM_PROFILES 6.3: RebindActor changes the stamping actor before any
// write; after a write it is E_INTERNAL and the actor stays.
func TestRebindActor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := csSetup(t) // csSetup writes; this engine is fresh
	eng := f.engine(t, f.repo.Open(store.OpenOptions{}), nil)
	other := mustActor("act_0a000000000070008000000000000001")
	if err := eng.RebindActor(other); err != nil {
		t.Fatalf("rebind before a write: %v", err)
	}
	if eng.Actor() != other {
		t.Fatalf("actor = %s, want %s", eng.Actor(), other)
	}
	w, err := eng.Create(ctx, csInternal, schema.TableRoles, map[string]value.Value{schema.FieldRoleName: txt("ops")})
	if err != nil {
		t.Fatal(err)
	}
	if w.Row.CreatedBy != other || w.Row.HLC.Actor != other {
		t.Fatalf("row stamped by %s / hlc %s, want %s", w.Row.CreatedBy, w.Row.HLC.Actor, other)
	}
	if err := eng.RebindActor(apPerson); !errs.Has(err, errs.Internal) {
		t.Fatalf("rebind after a write: err = %v, want E_INTERNAL", err)
	}
	if eng.Actor() != other {
		t.Fatalf("a refused rebind changed the actor to %s", eng.Actor())
	}
	if err := eng.RebindActor(ids.ID[ids.Actor]{}); !errs.Has(err, errs.Internal) {
		t.Fatalf("rebind to an empty actor: err = %v", err)
	}
}
