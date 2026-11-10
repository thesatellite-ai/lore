package setup

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// TestPerToolChecksFanOut: a tools.* check runs once per picked tool
// (Target = the tool), only for the picked tools (the answer over the
// saved setup), each its own result; info-level Vars lower the level.
func TestPerToolChecksFanOut(t *testing.T) {
	cases := []struct {
		name    string
		saved   []string
		answers onboard.Answers
		want    []string
	}{
		{"answer picks the tools", []string{"claude_code", "codex", "cursor"}, onboard.Answers{onboard.QIDTools: {IsList: true, List: []string{"codex", "cursor", "codex"}}}, []string{"codex", "cursor"}},
		{"saved tools without an answer", []string{"claude_code"}, onboard.Answers{}, []string{"claude_code"}},
		{"nothing picked: one setup-wide row", nil, onboard.Answers{onboard.QIDTools: {IsList: true}}, []string{""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newFakeEnv(onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Tools: c.saved})
			e := newTestEngine(env)
			out, err := e.Check(bg, CheckIn{Setup: "personal", Step: string(onboard.StepTools), Answers: c.answers})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, r := range out.Checks {
				if r.Check == onboard.CheckToolsPointerFile {
					got = append(got, r.Target)
				}
			}
			if !slices.Equal(got, c.want) {
				t.Fatalf("targets = %q, want %q (%+v)", got, c.want, out.Checks)
			}
		})
	}
	env := newFakeEnv(onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Tools: []string{"codex"}})
	env.prober.results[onboard.CheckToolsPointerFile] = ProbeResult{Status: onboard.StatusWarn, Vars: map[string]string{onboard.VarLevel: string(onboard.LevelInfo)}}
	out, err := newTestEngine(env).Check(bg, CheckIn{Setup: "personal", Step: string(onboard.StepTools)})
	if err != nil || len(out.Checks) != 1 || out.Checks[0].Level != onboard.LevelInfo {
		t.Fatalf("info row = %+v, %v", out.Checks, err)
	}
}
