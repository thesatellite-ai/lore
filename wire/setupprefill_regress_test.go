package wire_test

// setupprefill_regress_test.go — every value setup.plan fills in by itself
// passes that step's own validator, on several machine shapes, walking a
// new solo setup step by step the way the page does (take the defaults,
// type only what has none, Save).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// rwTyped is what a user types for a required question with no default.
var rwTyped = map[string]json.RawMessage{
	"name":  str("personal"),
	"email": str("person@example.com"),
}

// rwWalkStep fills step's answers the page's way: the plan's own values,
// then rwTyped for a required text with none. missing lists the required
// choices the plan left empty while it filled another answer of the same
// step (realmac NEW-1: the page then refused its own defaults with
// "Personal store is required").
func rwWalkStep(s rwStep) (answers, prefilled rwAnswers, missing []string) {
	prefilled = s.Prefilled()
	answers = rwAnswers{}
	for k, v := range prefilled {
		answers[k] = v
	}
	for _, q := range s.Questions {
		if _, ok := answers[q.ID]; ok || !q.Required {
			continue
		}
		if v, ok := rwTyped[q.ID]; ok {
			answers[q.ID] = v
			continue
		}
		if len(prefilled) > 0 {
			missing = append(missing, q.ID)
		}
		if len(q.Choices.Static) > 0 {
			answers[q.ID] = str(q.Choices.Static[0].Value)
		}
	}
	return answers, prefilled, missing
}

// Finding d01a1fab→891b57d9 (realmac): the Stores step offered a default
// (an inherit / create value, a store url another setup already owns) that
// its own validator then refused, so taking the page's defaults could
// never be saved; the owner saw lore refuse a value it had filled itself.
func TestRegress_AutofilledValuePassesOwnValidator(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	shapes := map[string]rwShape{
		"one account default key":     {GHAccounts: 1, SSH: "default_only"},
		"two accounts aliases no pub": {GHAccounts: 2, SSH: "several", NoPub: true, IncludeIf: 1},
		"second active plain folder":  {GHAccounts: 2, GHActiveSecond: true, SSH: "several", StartPlain: true, IncludeIf: 2},
		"no ssh gh only":              {GHAccounts: 1, SSH: "none"},
		"missing key":                 {GHAccounts: 1, SSH: "missing_key"},
	}
	for name, shape := range shapes {
		t.Run(name, func(t *testing.T) {
			w := newRegressWorld(t, shape)
			// The first account's own repo exists, so the default store has a
			// real target.
			w.AddRemote(w.Accounts[0].Login, "lore-me")
			rwWalkNewSetup(t, w, "personal")
		})
	}
	// A second setup on a machine that already has one: its defaults must
	// validate too and never offer the first setup's own repo.
	t.Run("second setup same account", func(t *testing.T) {
		w := newRegressWorld(t, rwShape{GHAccounts: 2, SSH: "several"})
		a := w.Accounts[0]
		w.AddRemote(a.Login, "lore-me")
		own := "git@github.com:" + a.Login + "/lore-me.git"
		w.WriteConfig(rwPersonalConfig(a.Login, a.Email) + "    personal_store: " + own + "\n")
		if st, ok := w.Plan("work").Step("personal_store"); ok {
			if v := st.Prefilled()["store_url"]; strings.Contains(string(v), own) {
				t.Errorf("the new setup's store url defaults to setup personal's own repo %s", own)
			}
		}
		rwWalkNewSetup(t, w, "work")
	})
}

// rwWalkNewSetup walks every step of a new solo setup, asserting no field
// error ever names a question whose answer the plan filled itself.
func rwWalkNewSetup(t *testing.T, w *rwWorld, setup string) {
	t.Helper()
	done := map[string]bool{}
	for range 20 {
		plan := w.Plan(setup)
		var next *rwStep
		for i, s := range plan.Steps {
			if !done[s.ID] && s.ID != "summary" {
				next = &plan.Steps[i]
				break
			}
		}
		if next == nil {
			if cfg := w.Config(); !strings.Contains(cfg, "name: "+setup) {
				t.Fatalf("walk ended without %s in config.yaml:\n%s", setup, cfg)
			}
			return
		}
		done[next.ID] = true
		answers, prefilled, missing := rwWalkStep(*next)
		for _, q := range missing {
			t.Errorf("step %s: the plan filled %s but left the required %s empty, so Save of the page's defaults is refused", next.ID, mustJSON(prefilled), q)
		}
		out := w.Check(setup, next.ID, answers)
		for _, fe := range out.FieldErrors {
			if _, auto := prefilled[fe.Question]; auto {
				t.Errorf("step %s: the plan filled %s=%s and the step refused it: %s", next.ID, fe.Question, prefilled[fe.Question], fe.Message)
			}
		}
		err := w.Apply(setup, next.ID, answers)
		if err != nil && strings.Contains(err.Error(), "failing check") {
			// The page's "Run it": run each failing check's runnable fix,
			// then Save again.
			for _, c := range w.Check(setup, next.ID, answers).Checks {
				// A lore fix would run a child lore that waits on this App's
				// lock; the walk only runs the outside programs' fixes (gh).
				if c.Status == "fail" && c.Fix.Runnable && len(c.Fix.Command) > 0 && c.Fix.Command[0] != "lore" {
					var run rwNone
					if ferr := rwRun(w, "setup.fix", map[string]string{"setup": setup, "check": c.Check, "target": c.Target}, &run); ferr != nil {
						t.Logf("step %s: fix %s: %v", next.ID, c.Check, ferr)
					}
				}
			}
			w.Check(setup, next.ID, answers)
			err = w.Apply(setup, next.ID, answers)
		}
		if err != nil {
			msg := err.Error()
			for q, v := range prefilled {
				val := strings.Trim(string(v), `"`)
				if len(val) > 2 && strings.Contains(msg, val) {
					t.Errorf("step %s: Save refused the value the plan filled (%s=%s): %v", next.ID, q, val, err)
				}
			}
			t.Errorf("step %s: Save of the page's own defaults refused (answers %s): %v", next.ID, mustJSON(answers), err)
		}
	}
	t.Fatal("walk did not end")
}
