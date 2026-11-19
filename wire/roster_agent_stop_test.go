package wire

// roster_agent_stop_test.go — devco:S-14: in a repo with no org a person
// stops an agent by naming it the way `lore handoff add --for` does
// (@<label> or its act_ id): actor deactivate finds it, task reassign
// --from finds it, and the stopped agent's own writes are refused.

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/schema"
)

func TestDeactivateAgentByLabelWithoutOrg(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1") // the run may sit in a coding agent: act as the person
	a, _, _ := standaloneTask(t)
	ctx := background()
	if _, err := a.ActorRegister(ctx, opsinfra.ActorRegisterIn{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	bot, err := a.ActorRegister(ctx, opsinfra.ActorRegisterIn{Kind: opsinfra.RegisterKindAgent, Label: "agent-lead-2"})
	if err != nil {
		t.Fatalf("agent register: %v", err)
	}
	// task reassign --from resolves the label, active or not
	if id, ok, _ := a.RosterActorRef(ctx, "@agent-lead-2"); !ok || id != bot.ID {
		t.Fatalf("RosterActorRef(@agent-lead-2) = %q %v, want %s", id, ok, bot.ID)
	}
	out, err := a.ActorDeactivate(ctx, "@agent-lead-2")
	if err != nil || out.ID != bot.ID || out.Kind != schema.ActorKindAgent {
		t.Fatalf("deactivate @agent-lead-2: %+v %v", out, err)
	}
	if txt := out.Text(); strings.Contains(txt, "offboard") || !strings.Contains(txt, "lore agent start --label agent-lead-2") {
		t.Errorf("an agent's deactivate text %q should name agent start, not offboard", txt)
	}
	if id, ok, _ := a.RosterActorRef(ctx, "@agent-lead-2"); !ok || id != bot.ID {
		t.Errorf("after deactivate, RosterActorRef = %q %v, want %s (reassign --from finds a stopped agent)", id, ok, bot.ID)
	}
	// a second deactivate, by act_ id, says it is already stopped
	_, err = a.ActorDeactivate(ctx, "@"+bot.ID)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.StatusChange || !strings.Contains(e.Hint, "lore agent start --label agent-lead-2") {
		t.Fatalf("second deactivate by id: %v, want E_STATUS_CHANGE naming agent start", err)
	}
	// the owner starts it again: active once more
	if _, err := a.ActorRegister(ctx, opsinfra.ActorRegisterIn{Kind: opsinfra.RegisterKindAgent, Label: "agent-lead-2"}); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if out, err := a.ActorDeactivate(ctx, bot.ID); err != nil || out.ID != bot.ID {
		t.Fatalf("deactivate by bare act_ id: %+v %v", out, err)
	}
}

// TestStoppedAgentErr: only the caller's own, deactivated agent row is
// refused; a person, an active agent and someone else's row are not.
func TestStoppedAgentErr(t *testing.T) {
	t.Parallel()
	mk := func(kind string, active bool) row.Row {
		k, _ := value.TextOf(kind)
		l, _ := value.TextOf("agent-lead-2")
		return row.Row{Table: schema.TableActors, Fields: map[string]value.Value{
			actorFieldKind: k, rosterFieldLabel: l, rosterFieldActive: value.BoolOf(active)}}
	}
	if err := stoppedAgentErr(mk(schema.ActorKindAgent, false), true); !errs.Has(err, errs.StatusChange) || !strings.Contains(err.Error(), "agent @agent-lead-2") {
		t.Errorf("stopped agent: %v, want E_STATUS_CHANGE naming it", err)
	}
	for name, c := range map[string]struct {
		r   row.Row
		own bool
	}{
		"active agent":  {mk(schema.ActorKindAgent, true), true},
		"stopped human": {mk(schema.ActorKindHuman, false), true},
		"not own":       {mk(schema.ActorKindAgent, false), false},
	} {
		if err := stoppedAgentErr(c.r, c.own); err != nil {
			t.Errorf("%s: %v, want nil", name, err)
		}
	}
}
