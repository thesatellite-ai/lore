package wire_test

// setupsave_regress_test.go — Save & next right after a fresh check reuses
// that check's results instead of running every network check again.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// Finding d01a1fab→891b57d9 (realmac): each Save & next re-ran every check
// the step's Check had just run (ssh -T per alias, gh api, git ls-remote),
// so a step the user had just seen green took several more seconds and
// could flip on a flaky network. Save after a fresh check must make no new
// ssh / gh / git call for checks that passed.
//
// The calls are attributed by order, not by time: the test waits for the
// probes the check left running (Settle) before it clears the logs, and
// for the save's own before it reads them. A check returns once one alias
// logs in as the account while the other aliases still answer; on a busy
// machine such a probe ended inside the save's window and was counted as a
// re-run. "slow alias" makes that probe slow on purpose, so the check
// always returns before it ends.
func TestRegress_SaveDoesNotRerunFreshChecks(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	t.Run("every alias answers at once", func(t *testing.T) { saveDoesNotRerunFreshChecks(t) })
	t.Run("slow alias", func(t *testing.T) {
		t.Setenv("RW_SLOW_ALIAS", "work-gh:"+rwSlowAliasDelay)
		saveDoesNotRerunFreshChecks(t)
	})
}

// rwSlowAliasDelay is how long the slow alias's ssh -T takes (seconds): far
// longer than a check that already has its answer takes to return.
const rwSlowAliasDelay = "1"

func saveDoesNotRerunFreshChecks(t *testing.T) {
	w := newRegressWorld(t, rwShape{GHAccounts: 2, SSH: "several"})
	a := w.Accounts[0]
	w.AddRemote(a.Login, "lore-me")
	setup := "personal"
	steps := []struct {
		id      string
		answers rwAnswers
	}{
		{"kind", rwAnswers{"kind": str("solo"), "name": str(setup)}},
		{"identity", rwAnswers{"name": str("Pat Example"), "email": str(a.Email)}},
		{"github", rwAnswers{"github_account": str(a.Login)}},
		{"personal_store", rwAnswers{"personal_store": str("existing"), "store_url": str("git@github.com:" + a.Login + "/lore-me.git")}},
	}
	for _, s := range steps {
		out := w.Check(setup, s.id, s.answers)
		for _, c := range out.Checks {
			if c.Status == "fail" && c.Level == "error" {
				t.Fatalf("step %s: check %s failed in a healthy world: %s\n%s", s.id, c.Check, c.Detail, w.Calls())
			}
		}
		warned := 0
		for _, c := range out.Checks {
			if c.Status == "warn" {
				warned++
			}
		}
		w.Settle() // the check's own probes end before the save's window opens
		w.ResetLogs()
		if err := w.Apply(setup, s.id, s.answers); err != nil {
			t.Fatalf("step %s: Save after a green check refused: %v", s.id, err)
		}
		w.Settle() // and the save's own have ended before its calls are read
		if s.id != "github" && s.id != "personal_store" {
			continue
		}
		var calls []string
		for _, tool := range []string{"ssh", "gh"} {
			calls = append(calls, w.Lines(tool, "")...)
		}
		// git: only the network ones (ls-remote / fetch / push), not the
		// local config writes the save itself makes.
		for _, l := range w.Lines("git", "") {
			if strings.Contains(l, "ls-remote") || strings.Contains(l, "fetch") || strings.Contains(l, "clone") {
				calls = append(calls, l)
			}
		}
		if warned == 0 && len(calls) > 0 {
			t.Errorf("step %s: Save re-ran the checks it had just run (%d network calls):\n%s", s.id, len(calls), strings.Join(calls, "\n"))
		}
	}
}
