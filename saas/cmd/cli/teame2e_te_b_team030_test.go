//go:build teame2e

package main

// TEAM-030 (LORE_TEAM_SPEC 7.2) as a matrix on the real binary:
// add and edit × target (default, repo, org store) × repo class (the
// linked client repo, Northwind's own internal repo). A team is refused
// only where the task lands in the client repo's own store (--to repo
// there); the default target of a linked client repo is the linked folder
// of the org store (F14: never "repo" by accident), which is internal, the
// same as --to <org>. A person is allowed everywhere, and a refused write
// leaves nothing in the client repo.

import (
	"encoding/json"
	"testing"

	th "saas/cmd/cli/internal/teamharness"
)

func TestTeB_Team030Matrix(t *testing.T) {
	cr := newCrew(t, th.Flag("assign", "task", "add"), th.Flag("to", "task", "add"), th.Flag("assign", "task", "edit"))
	cr.plantRosterTeam(t, "backend")
	cr.aliceApp.MustLore("sync")
	cr.toolsC.MustLore("sync")

	repos := []struct {
		name   string
		c      *th.Clone
		tl     string
		client bool
	}{
		{"client", cr.aliceApp, cr.agency.tl, true},
		{"internal", cr.toolsC, cr.tl, false},
	}
	targets := []struct {
		name, to string
		// inStore: where the row lands in the client repo (the org
		// store's linked folder, else the client repo's own store).
		inStore bool
	}{
		{"default", "", true},
		{"repo", "repo", false},
		{"org", orgNorthwind, true},
	}
	for _, rp := range repos {
		for _, tg := range targets {
			name := rp.name + "/" + tg.name
			refused := rp.client && !tg.inStore
			addArgs := func(title string) []string {
				args := []string{"task", "add", title}
				if tg.to != "" {
					args = append(args, "--to", tg.to)
				}
				if tg.to != orgNorthwind {
					args = append(args, "--tasklist", rp.tl)
				}
				return args
			}
			// add with a team.
			teamTitle := "teb team task " + name
			r := rp.c.Lore(append(addArgs(teamTitle), "--assign", "@backend")...)
			if refused {
				r.Refused(t, "E_INVALID_INPUT", "TEAM-030")
			} else {
				r.OK(t)
			}
			// edit a plain task to the team.
			plain := mustID(t, rp.c.MustLore(addArgs("teb plain task "+name)...), "tsk")
			if rp.client {
				if got := teBStoreOf(t, rp.c, plain); (got == orgNorthwind) != tg.inStore {
					t.Fatalf("%s: premise: the task lands in store %q", name, got)
				}
			}
			r = rp.c.Lore("task", "edit", plain, "--assign", "@backend")
			if refused {
				r.Refused(t, "E_INVALID_INPUT", "TEAM-030")
			} else {
				r.OK(t)
			}
			// A person is always allowed.
			rp.c.MustLore("task", "edit", plain, "--assign", "@alice")
			if refused {
				rp.c.Lore("sync")
				if f := cr.clientSees(t, teamTitle, "backend"); len(f) != 0 {
					t.Fatalf("%s: a refused team assignment writes nothing into the client repo: %v", name, f)
				}
			}
		}
	}
}

// teBStoreOf is the store `lore task show --json` names for a task.
func teBStoreOf(t *testing.T, c *th.Clone, id string) string {
	t.Helper()
	r := c.MustLore("task", "show", id, "--json")
	var out struct {
		Data struct {
			Source struct {
				Store string `json:"store"`
			} `json:"_source"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		t.Fatalf("decode task show --json: %v\n%s", err, r)
	}
	return out.Data.Source.Store
}
