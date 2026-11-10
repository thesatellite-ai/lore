package wire_test

// setupplan_regress_test.go — setup.plan (the first call the setup page
// makes, before it can draw anything) answers at once, whatever the
// network does.

import (
	"strings"
	"testing"
	"time"
)

// rwPlanBudget is how long setup.plan may take with every network call
// hanging for rwNetDelay: well under one slow call, so a plan that waits
// on even one probe fails.
const (
	rwPlanBudget = 1500 * time.Millisecond
	rwNetDelay   = "3"
)

// Finding d08387a2 / owner "blank page": the setup page showed nothing
// while setup.plan ran ssh -T per alias, gh api and version probes (8 s on
// a real machine, longer on a slow network). The plan must make no ssh or
// gh network call and answer within rwPlanBudget for a new setup and for a
// saved one, on a network where every call takes seconds.
func TestRegress_PlanLoadsWithoutBlankPage(t *testing.T) {
	w := newRegressWorld(t, rwShape{GHAccounts: 2, SSH: "several", IncludeIf: 1})
	a := rwAllAccounts[0]
	w.WriteConfig(rwPersonalConfig(a.Login, a.Email) + "    personal_store: git@github.com:" + a.Login + "/lore-me.git\n")
	t.Setenv("RW_NET_DELAY", rwNetDelay)
	for _, setup := range []string{"personal", "work"} {
		w.ResetLogs()
		start := time.Now()
		w.Plan(setup)
		took := time.Since(start)
		var net []string
		net = append(net, w.Lines("ssh", "")...)
		for _, l := range w.Lines("gh", "") {
			if !strings.Contains(l, "args=auth ") {
				net = append(net, l)
			}
		}
		if len(net) > 0 {
			t.Errorf("setup.plan %s made network calls:\n%s", setup, strings.Join(net, "\n"))
		}
		if took > rwPlanBudget {
			t.Errorf("setup.plan %s took %s with a slow network (budget %s)", setup, took, rwPlanBudget)
		}
	}
}
