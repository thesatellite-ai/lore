package setup

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// TestJoinKeepsAdminKind pins I-17: when the join step made the setup an
// admin setup (the org's admin on a second machine, who answered the kind
// step "member" as the join lines say), the saved kind step says admin
// too, so a later step's state (loadState: the saved kind step wins) keeps
// it an admin setup. A member join, and any other step, change nothing.
func TestJoinKeepsAdminKind(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name string
		step onboard.StepID
		kind onboard.Kind
		want onboard.Kind
	}{
		{"admin join", onboard.StepJoin, onboard.KindAdmin, onboard.KindAdmin},
		{"member join", onboard.StepJoin, onboard.KindMember, onboard.KindMember},
		{"another step", onboard.StepFolders, onboard.KindAdmin, onboard.KindMember},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := newFakeEnv()
			e := newTestEngine(env)
			kindRow := onboard.Progress{Setup: "acme", Step: onboard.StepKind, Status: onboard.StepDone,
				Answers: onboard.Answers{kindAnswer: {Text: string(onboard.KindMember)}, "name": {Text: "acme"}}}
			if err := env.store.PutProgress(ctx, kindRow); err != nil {
				t.Fatal(err)
			}
			s := stepState{name: "acme", entry: onboard.SetupEntry{Name: "acme", Kind: c.kind, Org: "acme"},
				progress: map[onboard.StepID]onboard.Progress{onboard.StepKind: kindRow}}
			if err := e.keepJoinedKind(ctx, env.store, s, onboard.Step{ID: c.step}); err != nil {
				t.Fatal(err)
			}
			got := env.store.progress["acme"][onboard.StepKind]
			if k := answerKind(got.Answers); k != c.want {
				t.Errorf("saved kind step = %q, want %q", k, c.want)
			}
			if got.Answers["name"].Text != "acme" {
				t.Errorf("the kind step's other answers were lost: %+v", got.Answers)
			}
			if answerKind(kindRow.Answers) != onboard.KindMember {
				t.Error("the caller's progress row was changed in place")
			}
		})
	}
}
