package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// stampedRow is a memory row file of id stating writer as updated_by,
// stamped wall (the stamp's actor part is the writer, as lore writes it).
func stampedRow(id ids.AnyID, writer ids.AnyID, wall, body string) string {
	return `{"_table":"memories","_v":2,"_hlc":"` + wall + `/0000/` + writer.String() + `","id":"` + id.String() +
		`","body":"` + body + `","updated_by":"` + writer.String() + `"}` + "\n"
}

// contractorPeriod is a contractor's open stint.
func (p person) contractorPeriod() string {
	return rowJSON("actor_periods", p.period, map[string]string{"actor_id": `"` + p.actor.String() + `"`, "type": `"contractor"`, "started_on": `"2026-01-01"`})
}

// commitLocal writes files and commits them as author without pushing.
func (h *handStore) commitLocal(author string, files map[string]string) {
	h.t.Helper()
	for p, body := range files {
		full := filepath.Join(h.dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			h.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			h.t.Fatal(err)
		}
		gitT(h.t, h.dir, "add", p)
	}
	gitT(h.t, h.dir, "-c", "user.name=x", "-c", "user.email="+author, "commit", "--quiet", "--no-verify", "--author", "x <"+author+">", "-m", "edit")
}

// heldReason is the reason the clone holds id back, "" when it does not.
func heldReason(t *testing.T, b *Backend, id ids.AnyID) string {
	t.Helper()
	held, err := b.Held(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range held {
		if h.ID == id.String() {
			return h.Reason + " (commit " + h.Short() + " by " + h.Author + ")"
		}
	}
	return ""
}

// orgTeam is the made-up org of these tests: an admin, a member and a
// contractor (a member of one project), on the roster of a hand store.
type orgTeam struct {
	h             *handStore
	boss, max, cy person
	project       ids.AnyID
}

// newOrgTeam commits the roster as the admin.
func newOrgTeam(t *testing.T) orgTeam {
	t.Helper()
	o := orgTeam{h: newHandStore(t), boss: newPerson(t, "boss@example.test"), max: newPerson(t, "max@example.test"), cy: newPerson(t, "cy@partner.test"),
		project: mustID[ids.Project](t)}
	pm := mustID[ids.ProjectMember](t)
	o.h.commit(o.boss.email, map[string]string{
		orgPath("actors", o.boss.actor): o.boss.actorRow(), orgPath("actor_periods", o.boss.period): o.boss.periodRow("admin", ""),
		orgPath("actors", o.max.actor): o.max.actorRow(), orgPath("actor_periods", o.max.period): o.max.periodRow("member", ""),
		orgPath("actors", o.cy.actor): o.cy.actorRow(), orgPath("actor_periods", o.cy.period): o.cy.contractorPeriod(),
		orgPath("project_members", pm): rowJSON("project_members", pm, map[string]string{"actor_id": `"` + o.cy.actor.String() + `"`, "project_id": `"` + o.project.String() + `"`}),
	})
	return o
}

// TestAuthorityHoldsForeignRows (devco R-4, R-15, C35): a roster or
// project row whose stated writer is not the commit's author, and a row a
// contractor wrote outside their projects, are held back on every reader
// and named; an ordinary row whose stated writer is someone else (a
// teammate's plain-git hand edit, or a borrowed name) is read with the
// commit's author named as its writer, never the borrowed name; a
// member's own row, and a version carrying a stamp its stated writer
// committed before (a conflict settled with --take other), are read as
// they are.
func TestAuthorityHoldsForeignRows(t *testing.T) {
	t.Parallel()
	o := newOrgTeam(t)
	own, spoof, shared, cyRow := mustID[ids.Memory](t), mustID[ids.Memory](t), mustID[ids.Memory](t), mustID[ids.Memory](t)
	proj := mustID[ids.Project](t)
	o.h.commit(o.max.email, map[string]string{orgPath("memories", own): stampedRow(own, o.max.actor, "2026-01-02T00:00:00.000Z", "mine")})
	o.h.commit(o.max.email, map[string]string{orgPath("memories", spoof): stampedRow(spoof, o.boss.actor, "2026-01-02T00:00:01.000Z", "says the admin")})
	o.h.commit(o.max.email, map[string]string{orgPath("projects", proj): rowJSON("projects", proj, map[string]string{"name": `"planted"`, "updated_by": `"` + o.boss.actor.String() + `"`})})
	o.h.commit(o.boss.email, map[string]string{orgPath("memories", shared): stampedRow(shared, o.boss.actor, "2026-01-02T00:00:02.000Z", "v1")})
	o.h.commit(o.max.email, map[string]string{orgPath("memories", shared): stampedRow(shared, o.max.actor, "2026-01-02T00:00:03.000Z", "v2")})
	// max settles a conflict with --take other: the admin's v1, stamp kept
	o.h.commit(o.max.email, map[string]string{orgPath("memories", shared): stampedRow(shared, o.boss.actor, "2026-01-02T00:00:02.000Z", "v1")})
	o.h.commit(o.cy.email, map[string]string{orgPath("memories", cyRow): stampedRow(cyRow, o.cy.actor, "2026-01-02T00:00:04.000Z", "a company rule")})

	b := orgFolderClone(t, o.h.remote)
	if _, read := readField(t, b, own, "body"); !read {
		t.Error("a member's own row was not read")
	}
	if body, read := readField(t, b, shared, "body"); !read || body != "v1" {
		t.Errorf("the settled conflict reads %q (read %v), want v1", body, read)
	}
	if w, _ := readField(t, b, shared, "updated_by"); w != o.boss.actor.String() {
		t.Errorf("the settled conflict names %q as its writer, want the admin", w)
	}
	if body, read := readField(t, b, spoof, "body"); !read || body != "says the admin" {
		t.Errorf("an ordinary row naming another writer reads %q (read %v, held %q); C35 reads it", body, read, heldReason(t, b, spoof))
	}
	if w, _ := readField(t, b, spoof, "updated_by"); w != o.max.actor.String() {
		t.Errorf("an ordinary row naming the admin is shown as written by %q, want the committer %s", w, o.max.actor)
	}
	got, err := b.Get(context.Background(), memories, spoof)
	if err != nil {
		t.Fatalf("Get of the credited row: %v", err)
	}
	if w := jsonField(string(got.Doc), "updated_by"); w != o.max.actor.String() {
		t.Errorf("Get names %q as the writer, want the committer %s", w, o.max.actor)
	}
	if _, read := readField(t, b, proj, "name"); read {
		t.Error("a project row that claims the admin wrote it was read")
	}
	if why := heldReason(t, b, proj); !strings.Contains(why, "claims to be written by") || !strings.Contains(why, o.max.email) {
		t.Errorf("hold of the spoofed project row = %q; want the claimed writer and the author named", why)
	}
	if _, read := readField(t, b, cyRow, "body"); read {
		t.Error("a contractor's org-folder row was read")
	}
	if why := heldReason(t, b, cyRow); !strings.Contains(why, "contractor") {
		t.Errorf("hold of the contractor's row = %q; want the contractor wall named", why)
	}

	// a cache filled before the check (its cursor already at HEAD) gets
	// the held rows hidden once, on its next incremental read
	c := orgFolderClone(t, o.h.remote)
	h, err := c.head(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rows, _, err := c.Read(context.Background(), store.Cursor(h))
	if err != nil {
		t.Fatal(err)
	}
	hidden := map[string]bool{}
	for _, r := range rows {
		if r.Deleted {
			hidden[r.ID.String()] = true
		}
	}
	if !hidden[proj.String()] || !hidden[cyRow.String()] {
		t.Errorf("incremental read at HEAD hid %v; want the spoofed project and the contractor's rows hidden from an old cache", hidden)
	}
	credited := false
	for _, r := range rows {
		if r.ID == spoof && jsonField(string(r.Doc), "updated_by") == o.max.actor.String() {
			credited = true
		}
	}
	if !credited {
		t.Error("an old cache that took the borrowed writer was not served the row with the committer credited")
	}
	if again, _, _ := c.Read(context.Background(), store.Cursor(h)); len(again) != 0 {
		t.Errorf("a second read re-served %d rows; the hold set did not change", len(again))
	}
}

// TestEvilMergeIsChecked: a merge commit whose tree adds a row neither
// parent had is judged like any commit (an ordinary row is credited to
// the merger, C35), while the rows each side wrote
// pass through the merge.
func TestEvilMergeIsChecked(t *testing.T) {
	t.Parallel()
	o := newOrgTeam(t)
	side, planted := mustID[ids.Memory](t), mustID[ids.Memory](t)
	gitT(t, o.h.dir, "switch", "--quiet", "-c", "side")
	o.h.commitLocal(o.max.email, map[string]string{orgPath("memories", side): stampedRow(side, o.max.actor, "2026-01-03T00:00:00.000Z", "side")})
	gitT(t, o.h.dir, "switch", "--quiet", testBranch)
	mainRow := mustID[ids.Memory](t)
	o.h.commitLocal(o.boss.email, map[string]string{orgPath("memories", mainRow): stampedRow(mainRow, o.boss.actor, "2026-01-03T00:00:00.500Z", "main")})
	gitT(t, o.h.dir, "-c", "user.name=x", "-c", "user.email="+o.max.email, "merge", "--quiet", "--no-ff", "--no-commit", "side")
	o.h.commit(o.max.email, map[string]string{orgPath("memories", planted): stampedRow(planted, o.boss.actor, "2026-01-03T00:00:01.000Z", "planted in a merge")})

	b := orgFolderClone(t, o.h.remote)
	if _, read := readField(t, b, side, "body"); !read {
		t.Error("the side branch's own row was not read")
	}
	if w, _ := readField(t, b, planted, "updated_by"); w != o.max.actor.String() {
		t.Errorf("a row planted in a merge commit names %q as its writer, want the merger %s (C35)", w, o.max.actor)
	}
}

// TestClientStoreContractorWall (devco R-15): a store with no org folder
// (a client store) is checked against its org's roster: a contractor's
// row in their project's folder is read, one in another client's folder
// is held back.
func TestClientStoreContractorWall(t *testing.T) {
	t.Parallel()
	o := newOrgTeam(t)
	org := orgFolderClone(t, o.h.remote)
	org.marker.OrgFolder = rowfs.FolderOrg // the hand store's marker names none

	client := newHandStore(t)
	other := mustID[ids.Project](t)
	mine, theirs := mustID[ids.Memory](t), mustID[ids.Memory](t)
	client.commit(o.boss.email, map[string]string{
		"anaweb/projects/" + o.project.String() + ".json": rowJSON("projects", o.project, map[string]string{"name": `"anaweb"`}),
		"zeta/projects/" + other.String() + ".json":       rowJSON("projects", other, map[string]string{"name": `"zeta"`}),
	})
	client.commit(o.cy.email, map[string]string{"anaweb/memories/" + mine.String() + ".json": stampedRow(mine, o.cy.actor, "2026-01-04T00:00:00.000Z", "my project")})
	client.commit(o.cy.email, map[string]string{"zeta/memories/" + theirs.String() + ".json": stampedRow(theirs, o.cy.actor, "2026-01-04T00:00:01.000Z", "another client")})

	open := func(folder string) *Backend {
		b, err := OpenClone(context.Background(), client.remote, filepath.Join(t.TempDir(), "clones", "host", "client"), folder, store.OpenOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = b.Close() })
		b.SetRosterSource(org)
		return b
	}
	if _, read := readField(t, open("anaweb"), mine, "body"); !read {
		t.Error("the contractor's row in their project's folder was not read")
	}
	z := open("zeta")
	if _, read := readField(t, z, theirs, "body"); read {
		t.Error("the contractor's row in another client's folder was read")
	}
	if why := heldReason(t, z, theirs); !strings.Contains(why, "contractor") {
		t.Errorf("hold = %q; want the contractor wall named", why)
	}
}
