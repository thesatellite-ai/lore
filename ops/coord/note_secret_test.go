package coord

import "testing"

// TestTaskNoteTakesSecretAnswers (devco:S-7): task note's text is scanned
// for secrets and the refusal hints --secret-ref, --secret-unstored and
// --not-a-secret --reason --yes; the op must ask (Confirm.MayAsk), which is
// what gives the CLI and REST those answer flags. Without it the hinted
// re-run failed with "unknown flag: --not-a-secret".
func TestTaskNoteTakesSecretAnswers(t *testing.T) {
	for _, e := range Ops(nil) {
		s := e.Spec()
		if s.Name != OpTaskNote {
			continue
		}
		if !s.Confirm.MayAsk {
			t.Fatalf("%s does not take the secret answer flags its refusal names", s.Name)
		}
		return
	}
	t.Fatalf("%s is not registered", OpTaskNote)
}
