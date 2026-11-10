//go:build userscen

package userscen

// Knowledge campaign: --help of every command of the area. A person who
// types `lore <noun> <verb> --help` gets the usage line and at least one
// example line to copy (85 of the 121 commands had none before 2026-11-08),
// and each noun's help lists exactly the verbs the campaign drives, so a
// new verb cannot ship untested by this matrix.

import (
	"sort"
	"strings"
	"testing"
)

func TestCampaignKnowledge_HelpEveryCommand(t *testing.T) {
	_, _, c := knowProject(t)
	for _, cmd := range campKnowCommands() {
		t.Run(strings.ReplaceAll(cmd, " ", "_"), func(t *testing.T) {
			args := append(strings.Fields(cmd), "--help")
			r := c.Lore(args...).OK(t).Says(t, "Usage:", "lore "+cmd)
			block := cliBlock(r.Stdout, "Examples:")
			n := 0
			for _, l := range block {
				if strings.HasPrefix(strings.TrimSpace(l), "lore ") {
					n++
				}
			}
			if strings.Contains(r.Stdout, "(required) (required)") {
				t.Fatalf("lore %s --help says (required) twice:\n%s", cmd, r.Stdout)
			}
			if n == 0 {
				t.Fatalf("lore %s --help has no example line to copy:\n%s", cmd, r.Stdout)
			}
		})
	}
}

func TestCampaignKnowledge_HelpListsEveryVerb(t *testing.T) {
	_, _, c := knowProject(t)
	for noun, want := range campKnowVerbs {
		t.Run(noun, func(t *testing.T) {
			r := c.Lore(noun, "--help").OK(t)
			var got []string
			for _, l := range cliBlock(r.Stdout, "Available Commands:") {
				f := strings.Fields(l)
				if len(f) > 0 && f[0] != "help" && f[0] != "completion" {
					got = append(got, f[0])
				}
			}
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("lore %s --help lists %v, the campaign drives %v: add the new verb to campKnowVerbs and its scenarios", noun, got, want)
			}
		})
	}
}
