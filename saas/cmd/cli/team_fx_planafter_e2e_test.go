//go:build teame2e

package main

import (
	"strings"
	"testing"

	th "saas/cmd/cli/internal/teamharness"
)

// §7.4 idempotent plans + F9: a plan's after: entry may carry :merged, so
// a same-repo task waits until its dependency is merged, not only done; a
// re-run of the plan adds nothing and keeps the edge. Ported to `lore
// mission apply` (TSQ9: lore plan apply removed; same file format).
func TestNext_PlanAfterMergedSuffix(t *testing.T) {
	cr := newCrew(t, th.Cmd("mission", "apply"), th.Cmd("task", "next"))
	cr.toolsC.WriteFile("invite.yaml", `mission: {key: invite, title: "Invite landing page"}
tasks:
  - {key: backend,  title: "invite mutation + email"}
  - {key: frontend, title: "invite landing route", after: ["backend:merged"]}
`)
	m := mustID(t, cr.toolsC.MustLore("mission", "apply", "invite.yaml"), "msn")
	if again := cr.toolsC.MustLore("mission", "apply", "invite.yaml"); mustID(t, again, "msn") != m {
		t.Fatal("a re-run resolves to the same mission")
	}
	l := th.DecodeJSON[taskListed](t, cr.toolsC.Lore("task", "list", "--json", "--mission", m, "--all"))
	ids := map[string]string{}
	for _, d := range l.Data {
		ids[d.Title] = d.ID
	}
	if len(l.Data) != 2 {
		t.Fatalf("a re-run must not duplicate tasks: %+v", l.Data)
	}
	fe := cr.liveOrMainTask(t, ids["invite landing route"])
	if b := string(fe.Raw["blocked_by"]); !strings.Contains(b, ids["invite mutation + email"]) || !strings.Contains(b, `"merged"`) {
		t.Fatalf("the edge waits for merged: blocked_by=%s", b)
	}

	_, ac := cr.agent(t, "backend-1")
	ac.MustLore("task", "next").Says(t, ids["invite mutation + email"])
	ac.MustLore("task", "done", ids["invite mutation + email"], "--no-lesson")
	ac.Lore("task", "next").Fails(t, th.ExitNoWork, "merged")
}
