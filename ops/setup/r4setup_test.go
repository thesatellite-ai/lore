package setup

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestToolsStepRefusesUnknownToolAsFieldError (devco:J-1): a tools answer
// outside the step's own tool list is a field error naming the allowed
// tools; before, "claude" reached the config writer and came back as a
// broken ~/.lore/config.yaml (E_CONFIG_INVALID).
func TestToolsStepRefusesUnknownToolAsFieldError(t *testing.T) {
	e := newEngine(newPrefillEnv(), onboardCatalog{})
	step := onboard.Step{ID: onboard.StepTools, Questions: []onboard.Question{{ID: onboard.QIDTools, Label: "Tools", Type: onboard.QMulti,
		Choices: onboard.ChoiceSource{Prefill: onboard.PrefillToolsDetected}}}}
	bad := onboard.Answers{onboard.QIDTools: {IsList: true, List: []string{"claude"}}}
	fe := e.validateStep(bg, step, onboard.KindSolo, bad)
	if len(fe) != 1 || fe[0].Question != onboard.QIDTools || fe[0].Rule != onboard.FieldRuleChoice ||
		fe[0].Message != `"claude" is not one of claude_code, codex` {
		t.Fatalf("unknown tool: %+v", fe)
	}
	good := onboard.Answers{onboard.QIDTools: {IsList: true, List: []string{"codex"}}}
	if fe := e.validateStep(bg, step, onboard.KindSolo, good); len(fe) != 0 {
		t.Fatalf("offered tool refused: %+v", fe)
	}
}

// TestJoinStepOnFreshMachineNamesMemberPlan (devco:L-1): the join step of
// a setup that has no kind yet names the member plan and the kind step to
// apply first, never "not in the solo plan" with the solo plan's hint.
func TestJoinStepOnFreshMachineNamesMemberPlan(t *testing.T) {
	e := newEngine(newFakeEnv(), onboardCatalog{})
	_, err := e.Apply(bg, ApplyIn{Setup: "acme", Step: "join", Answers: onboard.Answers{"invite": text("git@git.example:acme/team.git")}})
	if !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("err = %v", err)
	}
	msg := err.Error()
	if strings.Contains(msg, "solo") || !strings.Contains(msg, `step "join" is in the member plan`) || !strings.Contains(msg, `--step kind --answers '{"kind":"member"}'`) {
		t.Fatalf("message: %s", msg)
	}
	var le *errs.Error
	if !errors.As(err, &le) || !strings.Contains(le.Hint, "lore setup join --invite") {
		t.Fatalf("hint: %+v", le)
	}
	// A step of no plan at all keeps the plain refusal.
	if _, err := e.Apply(bg, ApplyIn{Setup: "acme", Step: "nope"}); err == nil || !strings.Contains(err.Error(), `step "nope" is not in the solo plan`) {
		t.Fatalf("unknown step: %v", err)
	}
}

// TestFoldersBeforeJoinSaysSavedForLater (devco:K-1): a step saved on a
// member setup whose join step has not written the entry says it takes
// effect with the join; before, it printed status done and nothing else.
func TestFoldersBeforeJoinSaysSavedForLater(t *testing.T) {
	s := stepState{name: "acme", kind: onboard.KindMember}
	w := pendingEntryWarnings(s, onboard.Step{ID: onboard.StepFolders})
	if len(w) != 1 || !strings.Contains(w[0], "takes effect once its join step is done (lore setup apply --setup acme --step join)") {
		t.Fatalf("member: %q", w)
	}
	if w := pendingEntryWarnings(stepState{name: "acme", kind: onboard.KindAdmin}, onboard.Step{ID: onboard.StepFolders}); len(w) != 1 || !strings.Contains(w[0], "team step") {
		t.Fatalf("admin: %q", w)
	}
	for _, s := range []stepState{{name: "acme", kind: onboard.KindMember, exists: true}, {name: "me", kind: onboard.KindSolo}} {
		if w := pendingEntryWarnings(s, onboard.Step{ID: onboard.StepFolders}); len(w) != 0 {
			t.Fatalf("%+v: %q", s, w)
		}
	}
	if w := pendingEntryWarnings(s, onboard.Step{ID: onboard.StepJoin}); len(w) != 0 {
		t.Fatalf("join itself: %q", w)
	}
}
