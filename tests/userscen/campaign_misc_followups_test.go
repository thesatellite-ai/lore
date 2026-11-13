//go:build userscen

package userscen

// Misc follow-ups (batch 2026-11-08, fz2), each driven through the built
// binary:
//   - `lore <kind> delete <id>` checks the id BEFORE it asks to confirm:
//     a malformed id answers E_INVALID_ID and a well-formed id of no row
//     E_NOT_FOUND, never E_NEEDS_CONFIRM about a delete that cannot
//     happen; a real row still asks;
//   - human `plan show` / `tasklist show` print the title, status, live
//     task count and body under the v0.1.17 "<badge>  <id>" line;
//   - a coordination verb on an unknown task or mission is E_NOT_FOUND
//     with a hint naming the next step;
//   - `lore config set` run by many processes at once loses no edit: each
//     either lands or is refused E_STALE (re-run it), never overwritten;
//   - a refused write leaves nothing in .lore, not even the writer's
//     actor row made for that write.

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// mfDeleteKinds is every kind whose delete asks to confirm (Confirm
// Always): the ones that asked before checking the id, and the ones that
// already checked it (rule, memory, workflow), so none regresses.
var mfDeleteKinds = []string{
	"architecturenote", "behaviour", "comment", "cookbookrecipe", "decision",
	"handoff", "hotfix", "incident", "memory", "mission", "pattern", "playbook", "prompt",
	"rule", "snapshot", "suggestion", "tastepref", "techdoc", "workflow", "workspace",
}

// mfPrefix finds the id prefix a refusal names: `… is not a rule id (rul_…)`.
var mfPrefix = regexp.MustCompile(`\(([a-z]+)_…\)`)

// mfMissingHex is 32 hex characters that parse as an id no test row has.
const mfMissingHex = "01a1b809623a78b8b1212e4489503297"

// TestMiscFollowups_DeleteChecksIDFirst: kinds × {malformed id, id of no
// row}: the refusal is the id's, not a confirm question, and nothing in
// the tree changes.
func TestMiscFollowups_DeleteChecksIDFirst(t *testing.T) {
	c := resRepo(t)
	before := cdTreeHash(t, c)
	for _, k := range mfDeleteKinds {
		bad := c.Lore(k, "delete", "nope")
		m := mfPrefix.FindStringSubmatch(bad.Stderr)
		if bad.Code != cdDocExit(t, "E_INVALID_ID") || !strings.Contains(bad.Stderr, "[E_INVALID_ID]") || m == nil {
			t.Errorf("lore %s delete nope: want E_INVALID_ID naming the id prefix, got exit %d: %s", k, bad.Code, bad.Out())
			continue
		}
		id := m[1] + "_" + mfMissingHex
		miss := c.Lore(k, "delete", id)
		if miss.Code != cdDocExit(t, "E_NOT_FOUND") || !strings.Contains(miss.Stderr, "[E_NOT_FOUND]") {
			t.Errorf("lore %s delete %s: want E_NOT_FOUND before any question, got exit %d: %s", k, id, miss.Code, miss.Out())
		}
	}
	if after := cdTreeHash(t, c); after != before {
		t.Error("a refused delete changed the tree")
	}
	// A real row still asks first (the check did not replace the question).
	add := c.MustLore("decision", "add", "--title=Use SQLite", "--body=Local first.")
	id := regexp.MustCompile(`dec_[0-9a-f]{32}`).FindString(add.Stdout)
	if r := c.Lore("decision", "delete", id); r.Code != cdDocExit(t, "E_NEEDS_CONFIRM") {
		t.Errorf("lore decision delete --id %s: want E_NEEDS_CONFIRM for a real row, got exit %d: %s", id, r.Code, r.Out())
	}
}

// TestMiscFollowups_PlanTasklistShow: plan and tasklist show the fields a
// person needs, and the task count follows the tasks: one task in both,
// one only in the list, one in the list but deleted (not counted).
func TestMiscFollowups_PlanTasklistShow(t *testing.T) {
	c := resRepo(t)
	plan := regexp.MustCompile(`pln_[0-9a-f]{32}`).FindString(c.MustLore("plan", "add", "--title", "Invite flow", "--body", "1. backend 2. landing page").Stdout)
	list := regexp.MustCompile(`tlt_[0-9a-f]{32}`).FindString(c.MustLore("tasklist", "add", "--title", "Sprint 4", "--body", "two weeks").Stdout)
	tsk := regexp.MustCompile(`tsk_[0-9a-f]{32}`)
	c.MustLore("task", "add", "both", "--tasklist", list, "--plan", plan, "--commitment", "accepted")
	c.MustLore("task", "add", "list only", "--tasklist", list, "--commitment", "accepted")
	gone := tsk.FindString(c.MustLore("task", "add", "gone", "--tasklist", list, "--plan", plan, "--commitment", "accepted").Stdout)
	c.MustLore("task", "delete", gone, "--yes")
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"plan", "show", plan}, "PL  " + plan + "\n  title: Invite flow\n  status: draft\n  tasks: 1\n\n1. backend 2. landing page\n"},
		{[]string{"tasklist", "show", list}, "TA  " + list + "\n  title: Sprint 4\n  status: active\n  tasks: 2\n\ntwo weeks\n"},
	}
	for _, k := range cases {
		out := c.MustLore(k.args...).Stdout
		if !strings.HasPrefix(out, k.want) {
			t.Errorf("lore %s:\n%s\nwant it to start with:\n%s", strings.Join(k.args, " "), out, k.want)
		}
	}
}

// TestMiscFollowups_CoordUnknownHasHint: `lore task claim` of a task id
// and of a mission id (--coordinate) that name no row: E_NOT_FOUND and a
// hint (the host's `lore sync peek` for a task, the mission list for a
// mission), never a bare refusal.
func TestMiscFollowups_CoordUnknownHasHint(t *testing.T) {
	c := resRepo(t)
	cases := []struct {
		args []string
		hint string
	}{
		{[]string{"task", "claim", "tsk_" + mfMissingHex}, "lore sync peek"},
		{[]string{"task", "claim", "msn_" + mfMissingHex, "--coordinate"}, "lore mission list"},
	}
	for _, k := range cases {
		r := c.Lore(k.args...)
		if r.Code != cdDocExit(t, "E_NOT_FOUND") || !strings.Contains(r.Stderr, "[E_NOT_FOUND]") || !strings.Contains(r.Stderr, "hint: ") || !strings.Contains(r.Stderr, k.hint) {
			t.Errorf("lore %s: want E_NOT_FOUND with a hint naming %q, got exit %d: %s", strings.Join(k.args, " "), k.hint, r.Code, r.Out())
		}
	}
}

// mfConfigWriters is how many `lore config set` processes race.
const mfConfigWriters = 8

// TestMiscFollowups_ConfigSetConcurrentNoLostEdit: N processes each set
// their own key of one config.yaml at once. Each run either lands or is
// refused E_STALE / E_STORE_LOCKED with nothing written; re-running the
// refused ones, every key ends up in the file: no edit was silently
// overwritten by another process's copy of the file.
func TestMiscFollowups_ConfigSetConcurrentNoLostEdit(t *testing.T) {
	w := newWorld(t)
	a := w.Human("alice", "alice@example.com")
	dir := w.Root
	pending := map[int]bool{}
	for i := range mfConfigWriters {
		pending[i] = true
	}
	stale, locked := cdDocExit(t, "E_STALE"), cdDocExit(t, "E_STORE_LOCKED")
	for round := 0; len(pending) > 0; round++ {
		if round == 5*mfConfigWriters {
			t.Fatalf("config set never landed for %v", pending)
		}
		var mu sync.Mutex
		var wg sync.WaitGroup
		for i := range pending {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r := a.Lore(dir, "config", "set", fmt.Sprintf("repo_setups.r%d", i), fmt.Sprintf("s%d", i))
				switch r.Code {
				case 0:
					mu.Lock()
					delete(pending, i)
					mu.Unlock()
				case stale, locked:
					if !strings.Contains(r.Stderr, "hint: ") {
						t.Errorf("config set r%d refused without a hint: %s", i, r.Out())
					}
				default:
					t.Errorf("config set r%d: exit %d: %s", i, r.Code, r.Out())
				}
			}()
		}
		wg.Wait()
		if t.Failed() {
			return
		}
	}
	for i := range mfConfigWriters {
		key, want := fmt.Sprintf("repo_setups.r%d", i), fmt.Sprintf("s%d", i)
		if got := strings.TrimSpace(a.Lore(dir, "config", "get", key).Stdout); got != want {
			t.Errorf("config get %s = %q, want %q: an edit that reported success was lost", key, got, want)
		}
	}
}

// TestMiscFollowups_RefusedWriteLeavesNoActorRow: bob's first write in a
// clone is refused: an edit with --no-live of a row that landed on main
// after his last pull, refused at the store write after his actor row was
// made for it (memory and rule). `git status --porcelain .lore` stays as it was: no
// actor row file of his is left behind for a write that never happened.
func TestMiscFollowups_RefusedWriteLeavesNoActorRow(t *testing.T) {
	cases := []struct {
		name string
		args func(id string) []string
	}{
		{"memory edit --no-live", func(id string) []string {
			return []string{"memory", "edit", id, "--body", "bob edits before pulling", "--no-live"}
		}},
		{"rule edit --no-live", func(id string) []string {
			return []string{"rule", "edit", id, "--body", "bob edits before pulling", "--no-live"}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newSyncTeam(t, 2)
			a, b := st.clones[0], st.clones[1]
			noun := c.args("")[0]
			id := th.DecodeJSON[knowAdded](t, a.Lore(noun, "add", "--body", "live at once", "--live", "--json")).ID
			a.Lore("sync").OK(t)
			before := b.Git("status", "--porcelain", "--untracked-files=all", "--", ".lore")
			r := b.Lore(c.args(id)...)
			// the refusal comes after the actor step (the backend write)
			if r.Code != cdDocExit(t, "E_REFUSED") || !strings.Contains(r.Stderr, "git pull first") {
				t.Fatalf("lore %s: want the not-pulled E_REFUSED, got exit %d: %s", strings.Join(c.args(id), " "), r.Code, r.Out())
			}
			if after := b.Git("status", "--porcelain", "--untracked-files=all", "--", ".lore"); after != before {
				t.Fatalf("a refused write changed .lore:\nbefore %q\nafter  %q", before, after)
			}
		})
	}
}
