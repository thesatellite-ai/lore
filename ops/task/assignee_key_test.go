package task_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// projectedActor writes an actor row as the repo store keeps it (the F18
// projection: id, display_name, kind; no stable_key) under the id v0.1.17
// derives from stableKey, and returns that id.
func projectedActor(t *testing.T, h *opstest.H, stableKey, display, kind string) string {
	t.Helper()
	id, err := identity.ActorID(stableKey)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]value.Value{"display_name": text(t, display), "kind": text(t, kind)}
	if _, err := h.App.CreateWithID(context.Background(), store.SourceRepo, schema.TableActors, id.Any(), fields); err != nil {
		t.Fatal(err)
	}
	return id.String()
}

// TestAssigneeByStableKeyOnProjection is saas/cmd/cli
// TestUC_Tasks_ListAssignee's parity case at the op level: in a plain
// repo the actor rows carry no stable_key (F18 projection), so
// --assignee=agent:review-bot is matched through the id the stable key
// derives (v0.1.17's actor id), the display name still matches any case,
// and an unknown key stays E_NOT_FOUND. Two actors sharing a display name
// are ambiguous (E_INVALID_INPUT naming both), while their stable keys
// each pick one.
func TestAssigneeByStableKeyOnProjection(t *testing.T) {
	h := opstest.New(t)
	bot := projectedActor(t, h, "agent:review-bot", "review-bot", schema.ActorKindAgent)
	twinA := projectedActor(t, h, "agent:twin", "twin", schema.ActorKindAgent)
	twinH := projectedActor(t, h, "human:twin@example.com", "twin", schema.ActorKindHuman)
	byBot := add(h, "for the bot", `"assigned_to":"`+bot+`"`)
	byTwinA := add(h, "for twin agent", `"assigned_to":"`+twinA+`"`)
	byTwinH := add(h, "for twin human", `"assigned_to":"`+twinH+`"`)
	ctx := context.Background()
	for in, want := range map[string]string{
		`{"assignee":"agent:review-bot"}`:       byBot,
		`{"assignee":"Review-Bot"}`:             byBot,
		`{"assignee":"agent:twin"}`:             byTwinA,
		`{"assignee":"human:twin@example.com"}`: byTwinH,
	} {
		if got := listIDs(t, h, ctx, in); len(got) != 1 || got[0] != want {
			t.Errorf("%s: %v, want [%s]", in, got, want)
		}
	}
	h.Fails(opstask.OpList, `{"assignee":"agent:nobody"}`, errs.NotFound)
	msg := h.Fails(opstask.OpList, `{"assignee":"twin"}`, errs.InvalidInput)
	if !strings.Contains(msg, twinA) || !strings.Contains(msg, twinH) {
		t.Fatalf("ambiguous message does not name both: %s", msg)
	}
}
