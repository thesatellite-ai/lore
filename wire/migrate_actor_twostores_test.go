package wire

// migrate_actor_twostores_test.go — owner item 10: one actor id held by
// the repo and by the personal store `me` is one person, never
// E_AMBIGUOUS_ID: `lore migrate run` over a v0.1.17 repo finishes, a
// second run is a no-op, and an unqualified read of the actor answers.

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/ids"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

func TestMigrateRunActorInRepoAndMe(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	repo := gitRepo(t)
	home := t.TempDir()
	meDir := t.TempDir()
	sid, err := ids.Parse[ids.Store]("sto_01a1a59b891c70b789144cd3e711f0aa")
	if err != nil {
		t.Fatal(err)
	}
	if err = folder.Init(meDir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: rowfs.VisibilityInternal}); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, home, "personal:\n  store: "+fileuri.URL(meDir, "")+"\n")
	a := newTestAppHome(t, repo, home)
	withProject(t, a)
	if !slices.Contains(a.Stores(), store.Source("me")) {
		t.Fatalf("stores = %v: me not attached", a.Stores())
	}
	st, err := a.state(background())
	if err != nil {
		t.Fatal(err)
	}
	me := st.actor.Any()
	// v0.1.17 wrote the repo copy (`_v: 1`, named by the email's local
	// part); the personal store's copy carries the git name
	v1Row(t, repo, schema.TableActors, me.String(), `"kind":"human","display_name":"una","stable_key":"`+testEmail+`"`)
	if _, err = st.eng.CreateWithID(background(), "me", schema.TableActors, me, textValues(t, map[string]string{
		"kind": schema.ActorKindHuman, "display_name": "Una Upgrader", "stable_key": testEmail})); err != nil {
		t.Fatal(err)
	}
	def, _ := mustReg(t).Table(schema.TableTasks)
	body, _ := v1Body(t, def, projectID(t, a))
	v1Row(t, repo, schema.TableTasks, string(def.Prefix)+"_01a1a59b891c70b789144cd3e711f001", body)

	if out, rerr := run(t, a, opsmigrate.OpRun, `{}`, true); rerr != nil {
		t.Fatalf("migrate run: %v\n%s", rerr, out)
	}
	out, err := run(t, a, opsmigrate.OpRun, `{}`, true)
	if err != nil {
		t.Fatalf("second migrate run: %v\n%s", err, out)
	}
	if n := migrateOut(t, out).Count; n != 0 {
		t.Fatalf("second run count %d: %s", n, out)
	}
	for _, get := range []func() error{
		func() error { _, _, e := st.eng.Get(background(), engine.Ref{ID: me}); return e },
		func() error { _, _, e := a.Get(background(), engine.Ref{ID: me}); return e },
	} {
		if gerr := get(); gerr != nil {
			t.Fatalf("unqualified actor read: %v", gerr)
		}
	}
}
