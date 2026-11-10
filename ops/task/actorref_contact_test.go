package task_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// identityContact writes an actor_contacts row of actor (an act_ id) into
// the (internal) repo store: type, the normalised value, identity flag.
func identityContact(t *testing.T, h *opstest.H, actor, typ, norm string, identity bool) {
	t.Helper()
	fields := map[string]value.Value{schema.FieldContactActor: text(t, actor), schema.FieldContactType: text(t, typ),
		schema.FieldContactValue: text(t, norm), schema.FieldContactValueNorm: text(t, norm), schema.FieldContactIdentity: value.BoolOf(identity)}
	if _, err := h.App.Create(context.Background(), store.SourceRepo, schema.TableActorContacts, fields); err != nil {
		t.Fatal(err)
	}
}

// TestActorRefIdentityContact: an actor reference in contact form
// (TEAM_PROFILES 6.3) resolves through the identity contacts after the id,
// stable-key and display-name forms: github:<login> in any case or URL
// form, email:<address> and a bare email name the person; a contact not
// flagged for identity, or an unknown one, stays E_NOT_FOUND; one contact
// held by two people is E_AMBIGUOUS_ID naming both (never the first).
func TestActorRefIdentityContact(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false))
	mira := projectedActor(t, h, "human:mira@agency.example", "Mira Patel", schema.ActorKindHuman)
	omar := projectedActor(t, h, "human:omar@agency.example", "Omar", schema.ActorKindHuman)
	identityContact(t, h, mira, "github", "mirap", true)
	identityContact(t, h, mira, "email", "mira.patel@gmail.com", true)
	identityContact(t, h, mira, "email", "mira.old@gmail.com", false)
	byMira := add(h, "for mira", `"assigned_to":"`+mira+`"`)
	ctx := context.Background()
	for _, ref := range []string{"github:mirap", "github:MiraP", "github:https://github.com/MiraP/", "email:Mira.Patel@gmail.com", "mira.patel@gmail.com"} {
		if got := listIDs(t, h, ctx, `{"assignee":"`+ref+`"}`); len(got) != 1 || got[0] != byMira {
			t.Errorf("%s: %v, want [%s]", ref, got, byMira)
		}
	}
	h.Fails(opstask.OpList, `{"assignee":"email:mira.old@gmail.com"}`, errs.NotFound)
	h.Fails(opstask.OpList, `{"assignee":"github:nobody"}`, errs.NotFound)
	identityContact(t, h, omar, "github", "mirap", true)
	msg := h.Fails(opstask.OpList, `{"assignee":"github:mirap"}`, errs.AmbiguousID)
	if !strings.Contains(msg, mira) || !strings.Contains(msg, omar) {
		t.Fatalf("ambiguous contact does not name both: %s", msg)
	}
}
