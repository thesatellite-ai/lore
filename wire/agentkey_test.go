package wire

import (
	"context"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/logic/agentdetect"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// actorsOnly is a backend holding a few actors rows; settleAgentKey only
// calls Get, so the embedded nil Backend is never reached.
type actorsOnly struct {
	store.Backend
	rows map[ids.AnyID]store.Row
}

func (b actorsOnly) Get(_ context.Context, _ row.Table, id ids.AnyID) (store.Row, error) {
	if r, ok := b.rows[id]; ok {
		return r, nil
	}
	return store.Row{}, store.ErrNotFound
}

// agentRow is an agents actors row with id = key's deterministic id and
// the given owner.
func agentRow(t *testing.T, key string, owner ids.ID[ids.Actor]) (ids.AnyID, store.Row) {
	t.Helper()
	id, err := identity.ActorID(key)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := textFields(map[string]string{actorFieldKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if fields[rosterFieldOwner], err = value.RefOf(owner.Any()); err != nil {
		t.Fatal(err)
	}
	r, err := store.FromRow(row.Row{Table: schema.TableActors, ID: id.Any(), HLC: hlc.Next(hlc.HLC{}, time.Now(), owner), Fields: fields})
	if err != nil {
		t.Fatal(err)
	}
	return id.Any(), r
}

// TestSettleAgentKey pins the 6.1 collision rule (OQ9): another owner on
// the 6-hex key widens to 10; my own 6-hex row, or no row, keeps 6; my
// 10-hex row keeps 10 even where the colliding row is not visible; an
// explicit LORE_ACTOR key is never touched.
func TestSettleAgentKey(t *testing.T) {
	ctx := context.Background()
	env := map[string]string{"CODEX_THREAD_ID": "t-1"}
	who, err := identity.Resolve(ctx, identity.Inputs{Home: t.TempDir(), Getenv: func(k string) string { return env[k] },
		Config: identity.Config{Identity: identity.Entry{Key: "human:tomas@agency.example", Name: "tomas"}},
		Detect: func() (agentdetect.Result, error) {
			return agentdetect.Detect(agentdetect.Inputs{Getenv: func(k string) string { return env[k] }, TTY: true})
		}})
	if err != nil {
		t.Fatal(err)
	}
	me, _ := identity.ActorID(who.Owner.StableKey)
	other, _ := identity.ActorID("human:someone@else.example")
	wide, _, _ := identity.WidenAgentKey(who)
	short, _ := identity.ActorID(who.StableKey)
	shortID, shortOther := agentRow(t, who.StableKey, other)
	_, shortMine := agentRow(t, who.StableKey, me)
	wideID, wideMine := agentRow(t, wide.StableKey, me)

	cases := []struct {
		name string
		rows map[ids.AnyID]store.Row
		want string
	}{
		{"no row", nil, who.StableKey},
		{"my 6-hex row", map[ids.AnyID]store.Row{shortID: shortMine}, who.StableKey},
		{"another owner holds 6 hex", map[ids.AnyID]store.Row{shortID: shortOther}, wide.StableKey},
		{"my 10-hex row already exists", map[ids.AnyID]store.Row{wideID: wideMine}, wide.StableKey},
	}
	for _, c := range cases {
		st := &state{who: who, actor: short}
		if err := st.settleAgentKey(ctx, map[store.Source]store.Backend{store.SourceRepo: actorsOnly{rows: c.rows}}); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		wantID, _ := identity.ActorID(c.want)
		if st.who.StableKey != c.want || st.actor != wantID {
			t.Fatalf("%s: key %q actor %s, want %q", c.name, st.who.StableKey, st.actor, c.want)
		}
	}

	explicit := identity.Resolved{StableKey: "agent:frontend_2", Kind: identity.KindAgent, Step: identity.StepEnv}
	eid, _ := identity.ActorID(explicit.StableKey)
	st := &state{who: explicit, actor: eid}
	if err := st.settleAgentKey(ctx, map[store.Source]store.Backend{store.SourceRepo: actorsOnly{rows: map[ids.AnyID]store.Row{eid.Any(): shortOther}}}); err != nil || st.who.StableKey != explicit.StableKey {
		t.Fatalf("explicit key touched: %q %v", st.who.StableKey, err)
	}
}
