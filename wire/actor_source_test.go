package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/actorsource"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestActorEditGate: on an org roster a person changes their own row, an
// org admin (here the creator of the roster folder's project row) anyone's,
// and anyone else is refused with E_POLICY_DENIED naming who may.
func TestActorEditGate(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	a := orgPeriodsApp(t)
	ctx := background()
	r, done, err := a.Roster(ctx, true)
	defer done()
	if err != nil {
		t.Fatal(err)
	}
	g, ok := r.(profilestore.PersonEditGate)
	if !ok {
		t.Fatal("the profile roster has no edit gate")
	}
	mira, err := ids.ParseAny(periodsHere)
	if err != nil {
		t.Fatal(err)
	}
	err = g.MayEditPerson(ctx, mira)
	if !errs.Has(err, errs.PolicyDenied) {
		t.Fatalf("alice changing Mira's row: %v, want E_POLICY_DENIED", err)
	}
	if e := errs.EnvelopeOf(err).Error; e.Hint == "" {
		t.Errorf("the refusal has no next step: %+v", e)
	}
	if err = g.MayEditPerson(ctx, r.Caller().Any()); err != nil {
		t.Fatalf("alice changing her own row: %v", err)
	}
	// alice creates the roster folder's project row: she started the org.
	pr := r.(*profileRoster)
	if _, err = a.rosterProject(ctx, pr.st, rosterStore, rosterFolderName(rosterStore)); err != nil {
		t.Fatal(err)
	}
	if err = g.MayEditPerson(ctx, mira); err != nil {
		t.Fatalf("the org's creator changing Mira's row: %v", err)
	}
}

// TestEnsureActorRefreshesStaleCopy: the caller's repo copy, written from
// the identity, is rewritten from the roster row once the roster holds
// them (a new version: new name, the roster's sto_ id and HLC as its
// stamp); a current copy is left as it is.
func TestEnsureActorRefreshesStaleCopy(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	a := orgPeriodsApp(t)
	ctx := background()
	if err := a.EnsureActor(ctx, store.SourceRepo); err != nil {
		t.Fatal(err)
	}
	st, err := a.state(ctx)
	if err != nil {
		t.Fatal(err)
	}
	me := st.actor.Any()
	ref := engine.Ref{Store: store.SourceRepo, Table: schema.TableActors, ID: me}
	_, first, err := st.eng.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if got := fieldText(first, schema.FieldActorOriginStore); got != actorsource.OriginOrgPrefix+"agency" {
		t.Fatalf("a new copy from the org identity is stamped %q, want org:agency", got)
	}
	// The roster now holds alice under another name: the source.
	if _, err = st.eng.CreateWithID(ctx, rosterStore, schema.TableActors, me, textValues(t, map[string]string{
		"kind": schema.ActorKindHuman, "display_name": "Alice Roster", "stable_key": testEmail})); err != nil {
		t.Fatal(err)
	}
	rep := a.actorReport(ctx, st, me)
	if rep.Source.Values.Name != "Alice Roster" || rep.Shown().Values.Name != "Alice Roster" {
		t.Fatalf("source %+v shown %+v, want the roster row", rep.Source, rep.Shown())
	}
	if n := rep.Counts(); n.Stale != 1 {
		t.Fatalf("counts %+v, want the repo copy stale", n)
	}
	// The next process's first write to the repo store.
	st.actorMu.Lock()
	delete(st.actorDone, store.SourceRepo)
	st.actorMu.Unlock()
	if err = a.EnsureActor(ctx, store.SourceRepo); err != nil {
		t.Fatal(err)
	}
	_, got, err := st.eng.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	_, roster, err := st.eng.Get(ctx, engine.Ref{Store: rosterStore, Table: schema.TableActors, ID: me})
	if err != nil {
		t.Fatal(err)
	}
	if fieldText(got, actorFieldDisplay) != "Alice Roster" || fieldText(got, schema.FieldActorOriginStore) != orgStoreID ||
		fieldText(got, schema.FieldActorOriginVersion) != roster.HLC.String() {
		t.Fatalf("the refreshed copy: %v", got.Fields)
	}
	if got.HLC.Compare(first.HLC) <= 0 || !got.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("the refresh is not a new version of the same row: hlc %s -> %s, created %s -> %s", first.HLC, got.HLC, first.CreatedAt, got.CreatedAt)
	}
	if fieldText(roster, schema.FieldActorOriginStore) != "" {
		t.Fatal("the source row was stamped")
	}
	// Current now: a further first write leaves it.
	st.actorMu.Lock()
	delete(st.actorDone, store.SourceRepo)
	st.actorMu.Unlock()
	if err = a.EnsureActor(ctx, store.SourceRepo); err != nil {
		t.Fatal(err)
	}
	if _, again, _ := st.eng.Get(ctx, ref); again.HLC != got.HLC {
		t.Fatal("a current copy was rewritten")
	}
}

// TestActorSourceGetOneActor: an unqualified Get of an actor id held in
// two stores answers the source's row (never E_AMBIGUOUS_ID) with the
// source's name.
func TestActorSourceGetOneActor(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	a := orgPeriodsApp(t)
	ctx := background()
	mira, err := ids.ParseAny(periodsHere)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eng.CreateWithID(ctx, store.SourceRepo, schema.TableActors, mira, textValues(t, map[string]string{
		"kind": schema.ActorKindHuman, "display_name": "Mira Old"})); err != nil {
		t.Fatal(err)
	}
	// the engine alone answers the newest copy (the repo's, written last)
	if src, _, gerr := eng.Get(ctx, engine.Ref{ID: mira}); gerr != nil || src != store.SourceRepo {
		t.Fatalf("the engine alone: %s %v, want the newest copy (repo), never E_AMBIGUOUS_ID", src, gerr)
	}
	src, r, err := a.Get(ctx, engine.Ref{ID: mira})
	if err != nil {
		t.Fatal(err)
	}
	if src != rosterStore || fieldText(r, actorFieldDisplay) != "Mira" {
		t.Fatalf("Get answered %s %v, want the roster's Mira", src, r.Fields)
	}
}

// TestIdentityOriginNames: each identity step names its origin with a
// colon (actorsource.IsIdentityOrigin), never like a roster store.
func TestIdentityOriginNames(t *testing.T) {
	a := orgPeriodsApp(t)
	st := &state{}
	for _, step := range []identity.Step{identity.StepOrg, identity.StepTop, identity.StepGit, identity.StepOSUser} {
		st.who = identity.Resolved{Step: step, Org: "agency"}
		if o, _ := a.identityOrigin(background(), st); !actorsource.IsIdentityOrigin(o) {
			t.Errorf("step %s: origin %q reads as a roster store", step, o)
		}
	}
}
