//go:build userscen

package userscen

// Knowledge campaign: what a person reads in the plain-text list of every
// kind. Each line names the row's id AND something recognisable — its
// title or name, or its body for a kind without one — on one line, cut
// when long. Nine kinds printed only "<badge>  <id>" (the list half of
// INTEG-KNOWLEDGE-3), so `lore pattern list` was a column of ids.

import (
	"strings"
	"testing"
)

func TestCampaignKnowledge_ListTextNamesEveryRow(t *testing.T) {
	scenParallel(t)
	for _, k := range campKnowKinds() {
		t.Run(k.noun, func(t *testing.T) {
			_, _, c := knowProject(t)
			long := strings.Repeat("ab ", 2000)
			rows := map[string]string{ // id → the text its line must show
				campMustAdd(t, c, k, "Retry the gateway", "Retry the gateway\ntwice on 502"): "Retry the gateway",
				campMustAdd(t, c, k, "שלום 🚀", "שלום 🚀"):                                     "שלום 🚀",
				campMustAdd(t, c, k, long, long):                                             "ab ab ab",
			}
			out := c.Lore(k.noun, "list").OK(t).Stdout
			lines := strings.Split(strings.TrimSpace(out), "\n")
			for id, want := range rows {
				found := false
				for _, l := range lines {
					if strings.Contains(l, id) {
						found = true
						if !strings.Contains(l, want) {
							t.Fatalf("%s list line %q does not show %q", k.noun, l, want)
						}
						if len(l) > 200 {
							t.Fatalf("%s list line is %d bytes: a long body is not cut", k.noun, len(l))
						}
					}
				}
				if !found {
					t.Fatalf("%s list has no line for %s:\n%s", k.noun, id, out)
				}
			}
		})
	}
}
