package setup

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// TestProbesKnowThePendingIdentity (devco D-448): an admin or member setup
// whose entry is not written yet (team or join step ahead) keeps the email
// only in its saved identity step. A later step's checks must get it:
// github.identity_email said "no identity email yet" right after the
// identity step. A written entry is probed as it is.
func TestProbesKnowThePendingIdentity(t *testing.T) {
	idRow := onboard.Progress{Setup: "acme", Step: onboard.StepIdentity, Status: onboard.StepDone,
		Answers: onboard.Answers{onboard.QIDEmail: text("ada@acme.example"), onboard.QIDName: text("Ada Byron")}}
	step := onboard.Step{ID: onboard.StepGitHub, Checks: []onboard.CheckID{onboard.CheckIdentityGitEmail}}
	for _, c := range []struct {
		name   string
		exists bool
		want   string
	}{
		{"pending admin setup", false, "human:ada@acme.example"},
		{"written entry", true, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := newFakeEnv()
			e := newTestEngine(env)
			s := stepState{name: "acme", kind: onboard.KindAdmin, exists: c.exists,
				entry:    onboard.SetupEntry{Name: "acme", Kind: onboard.KindAdmin},
				progress: map[onboard.StepID]onboard.Progress{onboard.StepIdentity: idRow}}
			if _, err := e.probe(bg, s, step, onboard.Answers{}); err != nil {
				t.Fatal(err)
			}
			if len(env.prober.seen) != 1 {
				t.Fatalf("probe requests: %+v", env.prober.seen)
			}
			if got := env.prober.seen[0].Setup.Identity.Key; got != c.want {
				t.Errorf("probed identity key = %q, want %q", got, c.want)
			}
			if s.entry.Identity.Key != "" {
				t.Error("the setup's own entry was given an identity (a member/admin entry never holds one)")
			}
		})
	}
}
