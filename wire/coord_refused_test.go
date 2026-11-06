package wire

import (
	"fmt"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestCoordRuleRefusalsAreRefused pins B-ERRS-4: an agent doing a human
// override, the WIP limit, a poisoned task and a task that is not open are
// refusals by rule or data state (E_REFUSED, exit 23), never bad input;
// the human-override refusal also says a person must act (needs_person).
// Bad input (a bad signal name) stays E_INVALID_INPUT.
func TestCoordRuleRefusalsAreRefused(t *testing.T) {
	agent := &coordWorld{who: identity.Resolved{StableKey: "agent:ci", Kind: identity.KindAgent}}
	err := agent.requireHuman("cancelling a claimed task")
	env := errs.EnvelopeOf(err)
	if env.Error.Code != errs.Refused || env.Error.Exit != 23 || !env.Error.NeedsPerson {
		t.Fatalf("agent override: %+v", env.Error)
	}
	if err := (&coordWorld{who: identity.Resolved{Kind: identity.KindHuman}}).requireHuman("x"); err != nil {
		t.Fatalf("a person may override: %v", err)
	}
	for name, in := range map[string]error{
		"wip":      &signals.WIPError{Max: 1},
		"poisoned": &coord.PoisonedError{Task: "tsk_1", Attempts: 3},
		"not open": fmt.Errorf("claim: %w", coord.ErrNotOpen),
	} {
		got := errs.EnvelopeOf(coordErr(in)).Error
		if got.Code != errs.Refused || got.Exit != 23 {
			t.Errorf("%s: %+v, want E_REFUSED exit 23", name, got)
		}
	}
	if got := errs.CodeOf(coordErr(signals.ErrBadName)); got != errs.InvalidInput {
		t.Errorf("bad name: %s, want E_INVALID_INPUT", got)
	}
}
