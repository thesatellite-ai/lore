package mission_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/yamlerr"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/mission"
)

// planLeaks are the strings a plan error must never show: yaml.v3's own
// wording, Go type names of the plan file's structs and yaml tags.
var planLeaks = []string{"not found in type", "cannot unmarshal", "mission.File", "FileTask", "[]string", "!!", "`", "yaml:"}

// planToken is a token-shaped value pasted under the wrong key; built at
// run time so the source holds no secret-looking literal.
func planToken() string { return "ghp_" + strings.Repeat("Q7", 18) }

// wrongKindPlans is the matrix of plan files with a value of the wrong
// kind: top level, nested under mission, in a task entry past the first,
// a list wanted but text or a mapping given, text wanted but a list or a
// mapping given, a token as the bad value, and two problems in one file.
func wrongKindPlans() []struct {
	name, body string
	want       []string
} {
	tok := planToken()
	return []struct {
		name, body string
		want       []string
	}{
		{"mission is a list", "mission: [a]\n", []string{"line 1: mission: expected a mapping (key: value lines), got a list"}},
		{"title is a list", "mission:\n  title: [x]\n", []string{"line 2: mission.title: expected text, got a list"}},
		{"to is a list", "mission:\n  title: X\n  to: [a, b]\n", []string{"line 3: mission.to: expected text, got a list"}},
		{"tasks is a token", "mission:\n  title: X\ntasks: " + tok + "\n", []string{"line 3: tasks: expected a list, got text"}},
		{"second task after is text", "mission:\n  title: X\ntasks:\n  - key: a\n    title: A\n  - key: b\n    title: B\n    after: a\n", []string{"line 8: tasks[1].after: expected a list, got text"}},
		{"task title is a mapping", "mission:\n  title: X\ntasks:\n  - key: a\n    title: {x: 1}\n", []string{"line 5: tasks[0].title: expected text, got a mapping"}},
		{"approvers is a mapping", "mission:\n  title: X\napprovers: {lead: " + tok + "}\n", []string{"line 3: approvers: expected a list, got a mapping"}},
		{"two problems", "mission:\n  title: [x]\napprovers: " + tok + "\n", []string{"line 2: mission.title: expected text, got a list", "line 3: approvers: expected a list, got text"}},
	}
}

// checkPlanMsg fails unless msg holds every want and none of the leaks
// or the token.
func checkPlanMsg(t *testing.T, msg string, want []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(msg, w) {
			t.Errorf("message %q lacks %q", msg, w)
		}
	}
	for _, leak := range append(planLeaks, planToken(), "Q7Q7") {
		if strings.Contains(msg, leak) {
			t.Errorf("message %q leaks %q", msg, leak)
		}
	}
}

// TestMissionApplyWrongKindNamesLineAndKey (YAMLERR-1): `lore mission
// apply` on a plan with a value of the wrong kind is E_INVALID_INPUT whose
// message (what the CLI prints) names the line and the key path, never a
// Go type, yaml's wording or the value, and nothing is written. Unknown
// keys stay ignored (Lenient): a plan with extra keys applies.
func TestMissionApplyWrongKindNamesLineAndKey(t *testing.T) {
	h := opstest.New(t)
	for _, c := range wrongKindPlans() {
		t.Run(c.name, func(t *testing.T) {
			_, err := h.Run("mission.apply", `{"file":`+opstest.Quote(writePlan(t, c.body))+`}`)
			if !errs.Has(err, errs.InvalidInput) {
				t.Fatalf("err %v, want E_INVALID_INPUT", err)
			}
			msg := errs.EnvelopeOf(err).Error.Message
			if !strings.HasPrefix(msg, "plan is not valid: line ") {
				t.Errorf("message %q", msg)
			}
			checkPlanMsg(t, msg, c.want)
			checkPlanMsg(t, err.Error(), c.want)
			var ye *yamlerr.Error
			if !errors.As(err, &ye) {
				t.Errorf("cause is not a yamlerr.Error: %T", errors.Unwrap(err))
			}
		})
	}
	if got := opstest.Text(h.Must("mission.list", `{"all":true}`)); strings.Contains(got, "msn_") {
		t.Fatalf("a refused plan wrote a mission: %q", got)
	}
	extra := strings.Replace(planFile, "  key: invite\n", "  key: invite\n  my key: 1\n  colour: [red]\n", 1)
	extra = strings.Replace(extra, "    timebox: 90m\n", "    timebox: 90m\n    notes: {a: b}\n", 1)
	if txt := opstest.Text(h.Must("mission.apply", `{"file":`+opstest.Quote(writePlan(t, extra))+`}`)); !strings.Contains(txt, "msn_") {
		t.Errorf("a plan with unknown keys: %q", txt)
	}
}

// TestApplyInTargetNameWrongKind: TargetName (read by the Runner before
// the run, BUG-T8) gives the same line + key error for a bad plan, never
// a Go type or the value; a good plan gives its trimmed mission.to, a plan
// without one "", and a missing file an error.
func TestApplyInTargetNameWrongKind(t *testing.T) {
	for _, c := range wrongKindPlans() {
		t.Run(c.name, func(t *testing.T) {
			name, err := mission.ApplyIn{File: writePlan(t, c.body)}.TargetName()
			if err == nil {
				t.Fatalf("no error, name %q", name)
			}
			checkPlanMsg(t, err.Error(), c.want)
		})
	}
	for body, want := range map[string]string{
		"mission:\n  title: X\n  to: \" acme/client1 \"\n": "acme/client1",
		"mission:\n  title: X\n":                           "",
		"":                                                 "",
		"mission:\n  to: kb\n  my key: [1]\n":              "kb",
	} {
		got, err := mission.ApplyIn{File: writePlan(t, body)}.TargetName()
		if err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", body, got, err, want)
		}
	}
	if _, err := (mission.ApplyIn{File: writePlan(t, "") + ".missing"}).TargetName(); err == nil {
		t.Error("a missing file: no error")
	}
}
