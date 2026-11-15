package setup

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/scanner/gitleaks"
	"github.com/thesatellite-ai/lore/logic/onboard"
)

// ruleSecretAnswer is the FieldError rule of an answer that looks like a
// secret (secretAnswerErrors).
const ruleSecretAnswer = "no_secret"

// secretAnswerHint is the refusal's next step: what works for a setup
// answer. The write flags of a row (--secret-ref, --secret-unstored,
// --not-a-secret) do not exist on setup apply: an answer is saved as
// typed into ~/.lore/config.yaml, so the only way on is a different answer.
const secretAnswerHint = "edit that answer so it holds no secret (setup answers are saved as typed in ~/.lore/config.yaml) and run the same `lore setup apply --setup <name> --step <step> --answers '{…}'` again"

// secretAnswerErrors refuses every answer the default secret rules flag,
// one FieldError per question, naming the rule and the hash, never the
// value. Questions that already have a field error are left to it (a
// store url with a token already says the token is never stored), and an
// invite line is skipped: it is used once and never saved.
//
// Why (devco R-17): an identity answer holding a token was refused by the
// progress row's secret check only after the step's apply had already
// written it into config.yaml, and the refusal named row-write flags that
// setup apply does not have. Judged with the other field rules, before
// any check or write, a refused answer is never stored anywhere.
func secretAnswerErrors(step onboard.Step, answers onboard.Answers, have []onboard.FieldError) []onboard.FieldError {
	var out []onboard.FieldError
	for _, q := range step.Questions {
		a, given := answers[q.ID]
		if !given || q.Type == onboard.QInvite || q.Type == onboard.QBool {
			continue
		}
		if slices.ContainsFunc(have, func(f onboard.FieldError) bool { return f.Question == q.ID }) {
			continue
		}
		texts := append([]string{a.Text}, a.List...)
		for _, t := range texts {
			if found := gitleaks.FindSecrets(q.ID, t); len(found) > 0 {
				out = append(out, onboard.FieldError{Question: q.ID, Rule: ruleSecretAnswer,
					Message: "looks like a secret (" + found[0].RuleID + " " + found[0].Preview + "); nothing was saved: edit the answer"})
				break
			}
		}
	}
	return out
}

// hasSecretAnswer reports whether fe holds a secretAnswerErrors refusal.
func hasSecretAnswer(fe []onboard.FieldError) bool {
	return slices.ContainsFunc(fe, func(f onboard.FieldError) bool { return f.Rule == ruleSecretAnswer })
}

// secretHint is secretAnswerHint with the setup and step filled in.
func secretHint(name string, step onboard.StepID) string {
	return strings.NewReplacer("<name>", name, "<step>", string(step)).Replace(secretAnswerHint)
}
