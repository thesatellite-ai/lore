//go:build teame2e

package teame2e

// Fixed rule 6 (fail closed) on the task write path of a linked client
// repo: when lore cannot reach the store that coordinates a client task,
// `task done` (with a lesson), `task cancel` and a `memory add` routed to the
// store stop with an error and write nothing into the client repo — no done
// row, no lesson.

import (
	"encoding/json"
	"os"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// breakStoreAccess removes alice's store clones and cache and cuts her
// network: lore can no longer read the store or its link index.
func (a *agency) breakStoreAccess(t *testing.T) {
	t.Helper()
	for _, dir := range []string{"clones", "cache"} {
		if err := os.RemoveAll(a.alice.LorePath(dir)); err != nil {
			t.Fatal(err)
		}
	}
	a.alice.Offline()
}

// TestF16_TaskDoneFailsClosedWithoutTheStore: a client task finished while
// the store is unreadable is refused; once the network is back and alice
// syncs, the client repo still holds neither a done row nor the lesson.
func TestF16_TaskDoneFailsClosedWithoutTheStore(t *testing.T) {
	a := newAgency(t, th.Flag("lesson", "task", "done"), th.Flag("to", "memory", "add"))
	id := mustID(t, a.acmeApp.MustLore("task", "add", "fix rounding", "--tasklist", a.tl), "tsk")
	a.acmeApp.MustLore("sync")
	a.aliceApp.MustLore("sync")

	a.breakStoreAccess(t)
	a.aliceApp.Lore("task", "done", id, "--lesson", internalNote).Refused(t).Never(t, "✓ "+id+" done")
	// A fact shared with the team has no task to fail first: its routing
	// must refuse too (was `finding add`, dropped in 0.1.18: the design
	// shares a fact as a memory in the store, p3/dropped.md).
	a.aliceApp.Lore("memory", "add", "--to", "team", "--body", "finding: "+internalNote).Refused(t)
	a.aliceApp.Lore("task", "cancel", id).Refused(t)

	a.alice.Online()
	a.aliceApp.Lore("sync")
	if row := a.clientTaskRow(t, id); row.Status == statusDone || row.Status == "cancelled" {
		t.Fatalf("a refused done/cancel must not reach the client row: status %q", row.Status)
	}
	if f := a.clientSees(t, "send the estimate before the call"); len(f) != 0 {
		t.Fatalf("the lesson of a refused done reached the client repo: %v", f)
	}
}

// TEAM-030: a team cannot be assigned on a task written into a linked
// client repo (team names are internal). The team lives on the org roster,
// not in the client's lore.db, so the refusal must still recognise the
// handle as a team — not answer "no person or team" — and write nothing.
// 0.1.18 has no command that makes a team (`lore team add` is dropped,
// p3/dropped.md), but teams a v0.1.17 lore made still sit on org rosters, so
// the team row is planted in the store as that version left it.
func TestF14_TeamAssignInClientRepoRefused(t *testing.T) {
	a := newAgency(t, th.Flag("assign", "task", "add"), th.Flag("to", "task", "add"))
	a.plantRosterTeam(t, "backend")
	a.aliceApp.MustLore("sync")
	const title = "rounding fix for the team"
	a.aliceApp.Lore("task", "add", title, "--to", "repo", "--tasklist", a.tl, "--assign", "@backend").
		Refused(t, "E_INVALID_INPUT", "TEAM-030")
	a.aliceApp.Lore("sync")
	if f := a.clientSees(t, title, "backend"); len(f) != 0 {
		t.Fatalf("a refused team assignment must write nothing into the client repo: %v", f)
	}
	// A person handle still works in the client repo.
	a.aliceApp.MustLore("task", "add", "rounding fix for alice", "--to", "repo", "--tasklist", a.tl, "--assign", "@alice")
}

// plantRosterTeam writes a team row with handle (and no members) into the
// org folder's roster on the store's main branch, the way a v0.1.17
// `lore team add` left it: no 0.1.18 command creates a team.
func (a *agency) plantRosterTeam(t *testing.T, handle string) {
	t.Helper()
	meta, ok := a.store.Read(refMain, folderOrg+"/"+dataDir+"/"+metaFile)
	if !ok {
		t.Fatal("test premise: the org folder has no _meta.json (nobody registered)")
	}
	var m struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal([]byte(meta), &m); err != nil || m.ProjectID == "" {
		t.Fatalf("org _meta.json names no project: %v %q", err, meta)
	}
	const id, by, at = "tem_0190bbbb0000700080000000000000cd", "act_00000000000070008000000000000000", "2026-11-01T09:00:00.000000000Z"
	type teamRow struct {
		HLC       string   `json:"_hlc"`
		Table     string   `json:"_table"`
		V         int      `json:"_v"`
		Active    bool     `json:"active"`
		CreatedAt string   `json:"created_at"`
		CreatedBy string   `json:"created_by"`
		Handle    string   `json:"handle"`
		ID        string   `json:"id"`
		Members   []string `json:"member_actor_ids"`
		ProjectID string   `json:"project_id"`
		UpdatedAt string   `json:"updated_at"`
		UpdatedBy string   `json:"updated_by"`
	}
	doc, err := json.MarshalIndent(teamRow{HLC: "2026-11-01T09:00:00.000Z/0000/" + by, Table: "teams", V: 2,
		Active: true, CreatedAt: at, CreatedBy: by, Handle: handle, ID: id,
		Members: []string{}, ProjectID: m.ProjectID, UpdatedAt: at, UpdatedBy: by}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	admin := a.w.Human("admin", "admin@northwind.example").Clone(a.store, "store-admin")
	admin.WriteFile(folderOrg+"/"+dataDir+"/teams/"+id+".json", string(doc)+"\n")
	admin.CommitAll("v0.1.17 lore: team @" + handle)
	admin.AdminPush("origin", "HEAD:main")
}
