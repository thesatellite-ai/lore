package onboard

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/logic/storepush"
)

// The personal store step (4.4) and the Start a team step (4.6) each ask
// one optional push mode question whose options are storepush's modes.
func TestPushQuestionInStoreSteps(t *testing.T) {
	for _, id := range []StepID{StepPersonalStore, StepTeam} {
		s, ok := StepByID(id)
		if !ok {
			t.Fatalf("no step %s", id)
		}
		i := slices.IndexFunc(s.Questions, func(q Question) bool { return q.ID == QIDPush })
		if i < 0 {
			t.Fatalf("step %s asks no %s question", id, QIDPush)
		}
		q := s.Questions[i]
		if q.Type != QChoice || q.Required || q.Help == "" {
			t.Errorf("step %s push question = %+v, want an optional choice with help", id, q)
		}
		var got []storepush.Mode
		for _, c := range q.Choices.Static {
			got = append(got, storepush.Mode(c.Value))
			if c.Label == "" || c.Detail == "" {
				t.Errorf("step %s push choice %s has no label or detail", id, c.Value)
			}
		}
		if !slices.Equal(got, storepush.Modes()) {
			t.Errorf("step %s push choices %v, want %v", id, got, storepush.Modes())
		}
	}
	for _, id := range []StepID{StepKind, StepIdentity, StepGitHub, StepJoin, StepTools, StepSecrets, StepFolders} {
		s, _ := StepByID(id)
		if slices.ContainsFunc(s.Questions, func(q Question) bool { return q.ID == QIDPush }) {
			t.Errorf("step %s asks push; only the store steps do", id)
		}
	}
}

func TestPushSettings(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []storepush.Assignment
	}{
		{"", nil},
		{"now", nil},
		{" NOW ", nil},
		{"batch", []storepush.Assignment{{Key: storepush.KeyPush, Value: "batch"}}},
		{"Manual", []storepush.Assignment{{Key: storepush.KeyPush, Value: "manual"}}},
	} {
		got, err := PushSettings(c.in)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("PushSettings(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	if _, err := PushSettings("hourly"); err == nil {
		t.Error(`PushSettings("hourly") accepted`)
	}
}
