package profilestoretest

import (
	"context"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

func mustText(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestFakeRoster: the refusals of Store.Roster, natural ids that converge,
// "already exists", update and clear, trash, and ResolveActor's forms.
func TestFakeRoster(t *testing.T) {
	ctx := context.Background()
	me, err := ids.Parse[ids.Actor]("act_01890a5d8a2b7c3d9e0f1a2b3c4d5e6f")
	if err != nil {
		t.Fatal(err)
	}
	f := New(me, time.Date(2026, 11, 6, 22, 0, 0, 0, time.UTC))
	f.Put(row.Row{Table: schema.TableActors, ID: me.Any(), Fields: map[string]value.Value{
		"kind": mustText(t, schema.ActorKindHuman), "display_name": mustText(t, "Mira"), "handle": mustText(t, "mira"),
		"stable_key": mustText(t, "human:mira@acme.com"), "active": value.BoolOf(true)}})

	for name, set := range map[string]func(){
		"agent":  func() { f.Agent = true },
		"no org": func() { f.NoOrg = true },
		"client": func() { f.Client = true },
	} {
		*f = Fake{CallerID: f.CallerID, Now: f.Now, rows: f.rows, defs: f.defs}
		set()
		if _, done, err := f.Roster(ctx, true); err == nil {
			t.Errorf("%s: a write roster opened", name)
		} else {
			done()
		}
	}
	*f = Fake{CallerID: f.CallerID, Now: f.Now, rows: f.rows, defs: f.defs, Agent: true}
	if _, done, err := f.Roster(ctx, false); err != nil {
		t.Errorf("an agent read: %v", err)
	} else {
		done()
	}
	f.MayManagePeople = true
	r, done, err := f.Roster(ctx, true)
	if err != nil {
		t.Fatalf("agent with agents.may_manage_people: %v", err)
	}
	defer done()
	if !r.Today().Equal(time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("today %v", r.Today())
	}

	ref, _ := value.RefOf(me.Any())
	fields := map[string]value.Value{schema.FieldContactActor: ref, schema.FieldContactType: mustText(t, "email"),
		schema.FieldContactValue: mustText(t, "Mira@Acme.com"), schema.FieldContactValueNorm: mustText(t, "mira@acme.com")}
	c, err := r.Create(ctx, schema.TableActorContacts, fields)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := ids.Natural(ids.PrefixActorContact, me.String(), "email", "mira@acme.com")
	if c.ID != want {
		t.Errorf("natural id %s want %s", c.ID, want)
	}
	if _, err := r.Create(ctx, schema.TableActorContacts, fields); !errs.Has(err, errs.NameTaken) {
		t.Errorf("second add: %v", err)
	}
	if _, err := r.Create(ctx, schema.TableActorContacts, map[string]value.Value{schema.FieldContactActor: ref}); err == nil {
		t.Error("a contact without its required fields was written")
	}
	up, err := r.Update(ctx, schema.TableActorContacts, c.ID, map[string]value.Value{schema.FieldContactLabel: mustText(t, "work")})
	if err != nil || up.Fields[schema.FieldContactLabel].String() != "work" {
		t.Fatalf("update: %v", err)
	}
	if up, err = r.Update(ctx, schema.TableActorContacts, c.ID, map[string]value.Value{schema.FieldContactLabel: value.Null{}}); err != nil {
		t.Fatal(err)
	} else if _, has := up.Fields[schema.FieldContactLabel]; has {
		t.Error("null did not clear")
	}
	if err := r.Delete(ctx, schema.TableActorContacts, c.ID, "typo"); err != nil {
		t.Fatal(err)
	}
	if list, _ := r.List(ctx, schema.TableActorContacts); len(list) != 0 {
		t.Errorf("trashed row listed: %v", list)
	}
	again, err := r.Create(ctx, schema.TableActorContacts, fields)
	if err != nil || again.ID == c.ID {
		t.Errorf("re-add after delete: %v %v", again.ID, err)
	}
	if err := r.Delete(ctx, schema.TableActors, me.Any(), "x"); !errs.Has(err, errs.Refused) {
		t.Errorf("actor delete: %v", err)
	}
	for _, q := range []string{"@mira", me.String(), "human:mira@acme.com"} {
		if a, err := r.ResolveActor(ctx, q); err != nil || a.ID != me.Any() {
			t.Errorf("resolve %s: %v", q, err)
		}
	}
	if _, err := r.ResolveActor(ctx, "@nobody"); !errs.Has(err, errs.NotFound) {
		t.Errorf("unknown: %v", err)
	}
	r.Push(ctx)
	if f.Pushes != 1 {
		t.Errorf("pushes %d", f.Pushes)
	}
}
