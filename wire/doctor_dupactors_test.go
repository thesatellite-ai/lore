package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	opsprofile "github.com/thesatellite-ai/lore/ops/profile"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// dupOldEmail is the address the person used before the email change.
const dupOldEmail = "alice.old@example.com"

// dupCreate writes an actors row of id into src with fields.
func dupCreate(t *testing.T, a *App, src store.Source, key string, fields map[string]string) ids.AnyID {
	t.Helper()
	id, err := identity.ActorID(key)
	if err != nil {
		t.Fatal(err)
	}
	st, err := a.state(background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.eng.CreateWithID(background(), src, schema.TableActors, id.Any(), textValues(t, fields)); err != nil {
		t.Fatal(err)
	}
	return id.Any()
}

// TestDoctorDuplicateActorsOrg: the person's actor from before an email
// change sits on the org roster beside the current one, under the same
// name: doctor warns, names both and the merge with the old email; the
// fix it names merges the old actor, and doctor is clean after it. An
// agent of the same display and a merged actor are never paired.
func TestDoctorDuplicateActorsOrg(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	a := orgPeriodsApp(t)
	ctx := background()
	if err := a.EnsureActor(ctx, rosterStore); err != nil {
		t.Fatal(err)
	}
	st, err := a.state(ctx)
	if err != nil {
		t.Fatal(err)
	}
	me, name := st.actor.Any(), st.who.DisplayName
	if f, err := a.DoctorDuplicateActors(ctx); err != nil || len(f) != 0 {
		t.Fatalf("one actor per person: %v %v, want no finding", f, err)
	}
	old := dupCreate(t, a, rosterStore, "human:"+dupOldEmail, map[string]string{"kind": schema.ActorKindHuman, "display_name": name, "stable_key": "human:" + dupOldEmail})
	dupCreate(t, a, rosterStore, "agent:auto/claude_code/abc123", map[string]string{"kind": schema.ActorKindAgent, "display_name": name})

	f, err := a.DoctorDuplicateActors(ctx)
	if err != nil || len(f) != 1 {
		t.Fatalf("doctor duplicate_actors: %v %v, want one finding", f, err)
	}
	want := "lore actor contact add --actor " + me.String() + " --type email --value " + dupOldEmail + " --identity --merge"
	if f[0].Level != DoctorWarning || f[0].Fix != want || !strings.Contains(f[0].Message, old.String()) || !strings.Contains(f[0].Message, me.String()) {
		t.Fatalf("finding %+v, want a warning naming %s and %s with fix %q", f[0], me, old, want)
	}

	// The fix, as the person runs it after the question (--yes).
	var add op.Entry
	for _, e := range opsprofile.Ops(a) {
		if e.Spec().Name == profilestore.OpContactAdd {
			add = e
		}
	}
	body := `{"actor":"` + me.String() + `","type":"email","value":"` + dupOldEmail + `","identity":true,"merge":true}`
	if _, err = a.Run(ctx, add, op.JSONDecoder{Body: []byte(body)}, op.Caller{Yes: true}); err != nil {
		t.Fatalf("the named fix: %v", err)
	}
	if f, err := a.DoctorDuplicateActors(ctx); err != nil || len(f) != 0 {
		t.Fatalf("after the merge: %v %v, want no finding", f, err)
	}
}

// TestDoctorDuplicateActorsNoOrg: in a repo with no org the pair is a
// note (the merge needs an org store), naming both actors.
func TestDoctorDuplicateActorsNoOrg(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	repo := gitRepo(t)
	home := srcHome(t, "identity:\n  key: human:una.new@example.test\n  name: Una Upgrader\n")
	a, err := New(Options{RepoDir: repo, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	ctx := background()
	withProject(t, a)
	if err = a.EnsureActor(ctx, store.SourceRepo); err != nil {
		t.Fatal(err)
	}
	old := dupCreate(t, a, store.SourceRepo, "human:una@example.test", map[string]string{"kind": schema.ActorKindHuman, "display_name": "una upgrader"})
	f, err := a.DoctorDuplicateActors(ctx)
	if err != nil || len(f) != 1 {
		t.Fatalf("doctor duplicate_actors: %v %v, want one finding", f, err)
	}
	if f[0].Level != DoctorNote || f[0].Fix != "" || !strings.Contains(f[0].Message, old.String()) || !strings.Contains(f[0].Message, "lore org create") {
		t.Fatalf("finding %+v, want a note naming %s and the org it needs", f[0], old)
	}
}
