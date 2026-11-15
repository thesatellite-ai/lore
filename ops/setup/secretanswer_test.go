package setup

import (
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
)

// fakeAnswerToken is a synthetic GitHub token, built at run time.
func fakeAnswerToken() string { return "gh" + "p_" + "aB3dE9fG1hJ2kL8mN4pQ7rS5tU6vW0xY1zC2" }

// TestApplyRefusesSecretAnswerBeforeAnyWrite (devco R-17): an identity
// answer that looks like a secret is refused with the other field rules:
// the applier (which writes config.yaml) never runs, no progress row keeps
// the value, the refusal names the rule and hash only, and its hint says
// to edit the answer instead of naming row-write flags setup apply lacks.
func TestApplyRefusesSecretAnswerBeforeAnyWrite(t *testing.T) {
	env, e := started(t)
	tok := fakeAnswerToken()
	_, err := e.Apply(bg, ApplyIn{Setup: "personal", Step: "identity", Answers: onboard.Answers{"email": text("m" + tok + "@agency.example")}})
	if !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("err = %v, want E_INVALID_INPUT", err)
	}
	env2 := errs.EnvelopeOf(err).Error
	if strings.Contains(env2.Message+env2.Hint, tok) || !strings.Contains(env2.Message, "looks like a secret") {
		t.Fatalf("refusal %q / %q", env2.Message, env2.Hint)
	}
	if !strings.Contains(env2.Hint, "edit that answer") || strings.Contains(env2.Hint, "--secret-") || strings.Contains(env2.Hint, "--not-a-secret") {
		t.Fatalf("hint names flags setup apply lacks: %q", env2.Hint)
	}
	if slices.Contains(env.applier.calls, onboard.StepIdentity) {
		t.Fatal("applier ran: the refused value reached config.yaml")
	}
	if p, ok := env.store.progress["personal"][onboard.StepIdentity]; ok && strings.Contains(p.Answers["email"].Text, tok) {
		t.Fatal("progress row kept the refused value")
	}
	out, err := e.Check(bg, CheckIn{Setup: "personal", Step: "identity", Answers: onboard.Answers{"email": text("m" + tok + "@agency.example")}})
	if err != nil || len(out.FieldErrors) != 1 || out.FieldErrors[0].Rule != ruleSecretAnswer || strings.Contains(out.FieldErrors[0].Message, tok) {
		t.Fatalf("check: %v %+v", err, out.FieldErrors)
	}
}

// TestNewSetupDoesNotTakeTheDefault (devco R-17): a solo setup made on a
// machine that already has a setup (a team one, never marked default)
// starts with default off; the first setup of the machine starts with it on.
func TestNewSetupDoesNotTakeTheDefault(t *testing.T) {
	team := onboard.SetupEntry{Name: "initech", Kind: onboard.KindAdmin}
	if takesDefault(onboard.Config{Setups: []onboard.SetupEntry{team}}, "") {
		t.Fatal("a new solo setup took the default over the team setup")
	}
	if !takesDefault(onboard.Config{}, "") || !takesDefault(onboard.Config{Setups: []onboard.SetupEntry{{Name: "personal"}}}, "personal") {
		t.Fatal("the first setup of the machine is not offered the default")
	}
	out, err := newEngine(newPrefillEnv(team), onboardCatalog{}).Plan(bg, PlanIn{Kind: string(onboard.KindSolo), Setup: "side"})
	if err != nil {
		t.Fatal(err)
	}
	if k := planStepOf(t, out, onboard.StepKind); k.Values[onboard.QIDDefault].Bool {
		t.Fatalf("kind values = %+v", k.Values)
	}
}
