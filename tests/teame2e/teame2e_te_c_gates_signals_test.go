//go:build teame2e

package teame2e

// te-c signals gates smoke with a remote (later 507, OV48, TE-C-1): a claim
// taken while the project was writable, then a teammate pushes a format
// gate. Every signals-ref action of the holder (heartbeat, release) and of
// a teammate (claim next, revoke, reassign) is refused with E_OLD_LORE
// before anything is pushed, the signals ref keeps its commit, and the
// claim still shows in a read with the read-only line on stderr.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestTeC_SignalsGateSmokeWithARemote(t *testing.T) {
	for _, g := range teCGates {
		t.Run(g.name, func(t *testing.T) {
			rt := newRepoTeam(t, th.Cmd("task", "claim"), th.Cmd("task", "heartbeat"), th.Cmd("task", "release"))
			id := rt.addTask(t, rt.c1, "claimed before the gate")
			other := rt.addTask(t, rt.c1, "free task")
			rt.c1.MustLore("task", "claim", id)
			sig := rt.app.Rev(refSignals)
			if sig == "" {
				t.Fatalf("the claim must reach the remote signals ref %s", refSignals)
			}

			rt.c2.Git("pull", "-q", "--ff-only", "origin", "main")
			teCEditMeta(t, rt.c2, teCRepoMeta, g)
			rt.c2.CommitAll("declare a future format")
			rt.c2.AdminPush("origin", "main")
			rt.c1.Git("pull", "-q", "--ff-only", "origin", "main")

			for _, w := range []struct {
				c    *th.Clone
				args []string
			}{
				{rt.c1, []string{"task", "heartbeat", id}},
				{rt.c1, []string{"task", "release", id}},
				{rt.c1, []string{"task", "claim", other}},
				{rt.c2, []string{"task", "claim", other}},
				{rt.c2, []string{"task", "revoke", id}},
			} {
				w.c.Lore(w.args...).Fails(t, th.ExitGuard, "E_OLD_LORE")
			}
			if now := rt.app.Rev(refSignals); now != sig {
				t.Errorf("the signals ref moved on a read-only project: %s -> %s", sig, now)
			}
			r := rt.c1.MustLore("task", "show", id)
			if !strings.Contains(r.Stderr, "read-only here") || !strings.Contains(r.Stderr, g.says) {
				t.Errorf("a read must print the read-only line naming %q on stderr: %s", g.says, r)
			}
		})
	}
}
