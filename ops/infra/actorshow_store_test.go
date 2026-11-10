package infra_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestActorShowOwnActorInTwoStores: the caller's actor row sits in the repo
// and in the personal store under one id (the actor row once per store). `actor show <own id>` is one actor, never
// E_AMBIGUOUS_ID (whose hint named --to, a flag actor show has not got):
// it shows the repo's copy; --store me shows the personal copy; --store of
// a store without it is E_NOT_FOUND naming the store; an unknown id is
// still E_NOT_FOUND.
func TestActorShowOwnActorInTwoStores(t *testing.T) {
	h := opstest.WithStores(t, []string{"me", "kb"})
	// The identity layer writes the caller's actor row once per store under
	// one deterministic id (wire actor.go); the harness has a fixed actor,
	// so the two copies are written the same way here.
	own, err := ids.ParseAny(opstest.Actor)
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range []store.Source{store.SourceRepo, "me"} {
		fields := map[string]value.Value{}
		for k, v := range map[string]string{"kind": "human", "display_name": "tomas", "stable_key": "human:tomas@agency.example"} {
			tv, terr := value.TextOf(v)
			if terr != nil {
				t.Fatal(terr)
			}
			fields[k] = tv
		}
		if _, cerr := h.App.CreateWithID(context.Background(), src, schema.TableActors, own, fields); cerr != nil {
			t.Fatalf("actor row in %s: %v", src, cerr)
		}
	}
	repoActors := h.JSON(h.Must(infra.OpActorList, `{"store":"repo"}`))
	meActors := h.JSON(h.Must(infra.OpActorList, `{"store":"me"}`))
	if !strings.Contains(repoActors, opstest.Actor) || !strings.Contains(meActors, opstest.Actor) {
		t.Fatalf("own actor row must be in both stores:\nrepo %s\nme %s", repoActors, meActors)
	}
	show := h.Must(infra.OpActorShow, `{"id":"`+opstest.Actor+`"}`)
	if raw := h.JSON(show); !strings.Contains(raw, `"id":"`+opstest.Actor+`"`) || strings.Contains(raw, `"_source"`) {
		t.Fatalf("default show must be the repo copy: %s", raw)
	}
	if raw := h.JSON(h.Must(infra.OpActorShow, `{"id":"`+opstest.Actor+`","store":"me"}`)); !strings.Contains(raw, `"_source"`) || !strings.Contains(raw, `"me"`) {
		t.Fatalf("--store me must show the personal copy: %s", raw)
	}
	if msg := h.Fails(infra.OpActorShow, `{"id":"`+opstest.Actor+`","store":"kb"}`, errs.NotFound); !strings.Contains(msg, `store "kb"`) {
		t.Fatalf("missing copy: %s", msg)
	}
	h.Fails(infra.OpActorShow, `{"id":"act_01a1a59b891c70b789144cd3e711fb92"}`, errs.NotFound)
}
