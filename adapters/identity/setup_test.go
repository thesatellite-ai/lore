package identity

import (
	"context"
	"testing"
)

// TestSetupIdentityPlace pins ONBOARDING_DESIGN 3.4: a solo setup's identity
// answers after the org entry and before the top-level identity; --actor /
// LORE_ACTOR still win.
func TestSetupIdentityPlace(t *testing.T) {
	setup := &SetupIdentity{Setup: "personal", Entry: Entry{Key: "human:mira@personal.example", Name: "Mira", Handle: "mira"}}
	top := Config{Identity: Entry{Key: "human:mira@top.example"}}
	org := &Org{Name: "agency", Identity: Entry{Key: "human:mira@agency.example"}}
	noenv := func(string) string { return "" }
	noGit := func(context.Context) string { return "" }
	cases := []struct {
		name string
		in   Inputs
		key  string
		step Step
	}{
		{"setup beats top-level", Inputs{Config: top, Setup: setup}, "human:mira@personal.example", StepSetup},
		{"org entry beats setup", Inputs{Config: top, Setup: setup, Org: org}, "human:mira@agency.example", StepOrg},
		{"--actor beats setup", Inputs{Config: top, Setup: setup, FlagActor: "human:tomas@x.example"}, "human:tomas@x.example", StepFlag},
		{"no setup: top-level as before", Inputs{Config: top}, "human:mira@top.example", StepTop},
		{"empty setup key is skipped", Inputs{Config: top, Setup: &SetupIdentity{Setup: "personal"}}, "human:mira@top.example", StepTop},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.Home, tc.in.Getenv, tc.in.GitEmail = t.TempDir(), noenv, noGit
			r, err := resolveChain(context.Background(), tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if r.StableKey != tc.key || r.Step != tc.step {
				t.Fatalf("got %s via %s, want %s via %s", r.StableKey, r.Step, tc.key, tc.step)
			}
		})
	}
}
