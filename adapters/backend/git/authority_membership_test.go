package git

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
)

// teamID is a new tem_ id (teams have no id tag of their own).
func teamID(t *testing.T) ids.AnyID {
	t.Helper()
	_, rest, _ := strings.Cut(mustID[ids.ProjectMember](t).String(), "_")
	id, err := ids.ParseAny(string(ids.PrefixTeam) + "_" + rest)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// memberRow is a project_members row of actor on project with roles.
func memberRow(id, actor, project ids.AnyID, roles string) string {
	return rowJSON("project_members", id, map[string]string{"actor_id": `"` + actor.String() + `"`, "project_id": `"` + project.String() + `"`,
		"roles": roles, "started_on": `"2026-01-02"`})
}

// TestMembershipIsAdminOrLeadOnly (devco D-328): who is on a project or
// a team is changed only by an admin or a lead of that project. A
// contractor's own hand-made membership of another project, and a plain
// member's, are held back and named; a lead's change on their project,
// and a newcomer taking up exactly what their pending invite names, are
// read.
func TestMembershipIsAdminOrLeadOnly(t *testing.T) {
	t.Parallel()
	o := newOrgTeam(t)
	other, leadPM := mustID[ids.Project](t), mustID[ids.ProjectMember](t)
	nia := newPerson(t, "nia@example.test")
	invite, team := mustID[ids.Invite](t), teamID(t)
	// the admin makes max the lead of the contractor's project, invites
	// nia to the other project as dev and to team web
	o.h.commit(o.boss.email, map[string]string{
		orgPath("project_members", leadPM): memberRow(leadPM, o.max.actor, o.project, `["lead"]`),
		orgPath("invites", invite): rowJSON("invites", invite, map[string]string{"email": `"` + nia.email + `"`, "status": `"pending"`,
			"projects": `["` + other.String() + `"]`, "roles": `["dev"]`, "teams": `["web"]`, "period_type": `"employee"`}),
		orgPath("teams", team): rowJSON("teams", team, map[string]string{"project_id": `"` + other.String() + `"`, "handle": `"web"`,
			"member_actor_ids": `["` + o.boss.actor.String() + `"]`}),
	})

	cySelf, maxSelf, maxAdds := mustID[ids.ProjectMember](t), mustID[ids.ProjectMember](t), mustID[ids.ProjectMember](t)
	// the contractor adds themself to the other project (their own row)
	o.h.commit(o.cy.email, map[string]string{orgPath("project_members", cySelf): memberRow(cySelf, o.cy.actor, other, `["dev"]`)})
	// a plain member adds themself to the other project
	o.h.commit(o.max.email, map[string]string{orgPath("project_members", maxSelf): memberRow(maxSelf, o.max.actor, other, `["dev"]`)})
	// the lead adds the admin to their own project
	o.h.commit(o.max.email, map[string]string{orgPath("project_members", maxAdds): memberRow(maxAdds, o.boss.actor, o.project, `["reviewer"]`)})
	// nia joins: own actor and stint, the invited membership, team web
	niaPM, niaExtra := mustID[ids.ProjectMember](t), mustID[ids.ProjectMember](t)
	o.h.commit(nia.email, map[string]string{
		orgPath("actors", nia.actor): nia.actorRow(), orgPath("actor_periods", nia.period): nia.periodRow("member", ""),
		orgPath("project_members", niaPM): memberRow(niaPM, nia.actor, other, `["dev"]`),
		orgPath("teams", team): rowJSON("teams", team, map[string]string{"project_id": `"` + other.String() + `"`, "handle": `"web"`,
			"member_actor_ids": `["` + o.boss.actor.String() + `","` + nia.actor.String() + `"]`}),
	})
	// and then, beyond the invite, makes herself lead of the contractor's project
	o.h.commit(nia.email, map[string]string{orgPath("project_members", niaExtra): memberRow(niaExtra, nia.actor, o.project, `["lead"]`)})

	b := orgFolderClone(t, o.h.remote)
	for _, held := range []struct {
		name string
		id   ids.AnyID
		who  string
	}{{"the contractor's own membership of another project", cySelf, o.cy.email}, {"a member's own membership", maxSelf, o.max.email},
		{"a membership beyond the invite", niaExtra, nia.email}} {
		if _, read := readField(t, b, held.id, "actor_id"); read {
			t.Errorf("%s was read", held.name)
		}
		if why := heldReason(t, b, held.id); !strings.Contains(why, "who is on a project") || !strings.Contains(why, held.who) {
			t.Errorf("hold of %s = %q; want the membership rule and %s named", held.name, why, held.who)
		}
	}
	for _, ok := range []struct {
		name string
		id   ids.AnyID
	}{{"the lead's change on their project", maxAdds}, {"the invited membership", niaPM}, {"the invited team join", team}} {
		if _, read := readField(t, b, ok.id, "id"); !read {
			t.Errorf("%s was not read (held: %q)", ok.name, heldReason(t, b, ok.id))
		}
	}
}

// TestStampCarryNeedsSameContent (devco D-329, C35): a version that
// carries a stamp its stated writer committed keeps that writer only when
// its content is what they committed; the same stamp on changed content
// is read with the commit's author named as the writer.
func TestStampCarryNeedsSameContent(t *testing.T) {
	t.Parallel()
	o := newOrgTeam(t)
	shared := mustID[ids.Memory](t)
	o.h.commit(o.boss.email, map[string]string{orgPath("memories", shared): stampedRow(shared, o.boss.actor, "2026-01-02T00:00:02.000Z", "v1")})
	o.h.commit(o.max.email, map[string]string{orgPath("memories", shared): stampedRow(shared, o.max.actor, "2026-01-02T00:00:03.000Z", "v2")})
	// the admin's stamp and name, on a body the admin never wrote
	o.h.commit(o.max.email, map[string]string{orgPath("memories", shared): stampedRow(shared, o.boss.actor, "2026-01-02T00:00:02.000Z", "changed")})

	b := orgFolderClone(t, o.h.remote)
	if writer, _ := readField(t, b, shared, "updated_by"); writer != o.max.actor.String() {
		t.Errorf("changed content under the admin's old stamp names %q as its writer, want the committer %s", writer, o.max.actor)
	}
	if why := heldReason(t, b, shared); why != "" {
		t.Errorf("an ordinary row was held: %q; C35 reads it with the committer credited", why)
	}

	// the same content re-encoded (keys in another order, spaces between them)
	// still counts as what the admin wrote
	again := mustID[ids.Memory](t)
	o.h.commit(o.boss.email, map[string]string{orgPath("memories", again): stampedRow(again, o.boss.actor, "2026-01-02T00:00:05.000Z", "w1")})
	o.h.commit(o.max.email, map[string]string{orgPath("memories", again): stampedRow(again, o.max.actor, "2026-01-02T00:00:06.000Z", "w2")})
	o.h.commit(o.max.email, map[string]string{orgPath("memories", again): `{ "updated_by":"` + o.boss.actor.String() + `", "body":"w1", "id":"` + again.String() +
		`", "_hlc":"2026-01-02T00:00:05.000Z/0000/` + o.boss.actor.String() + `", "_v": 2, "_table":"memories" }` + "\n"})
	c := orgFolderClone(t, o.h.remote)
	if body, read := readField(t, c, again, "body"); !read || body != "w1" {
		t.Errorf("re-encoded settled conflict reads %q (read %v, held %q), want w1", body, read, heldReason(t, c, again))
	}
	if writer, _ := readField(t, c, again, "updated_by"); writer != o.boss.actor.String() {
		t.Errorf("the settled conflict names %q as its writer, want the admin who wrote it", writer)
	}
}

// TestTeamChangeIsAdminOnlyAtSync (devco D-694): who is on a team is an
// admin's change at sync as it is on the writer's machine (the local
// write guard): a lead of the team's project who commits a team change
// by hand (adding themselves, adding someone else, a rename, a new team
// of their project) is held back and named, while the admin's team
// change on the same project is read.
func TestTeamChangeIsAdminOnlyAtSync(t *testing.T) {
	t.Parallel()
	o := newOrgTeam(t)
	leadPM, team, adminTeam := mustID[ids.ProjectMember](t), teamID(t), teamID(t)
	teamRow := func(id ids.AnyID, handle string, members ...ids.AnyID) string {
		var ms []string
		for _, m := range members {
			ms = append(ms, `"`+m.String()+`"`)
		}
		return rowJSON("teams", id, map[string]string{"project_id": `"` + o.project.String() + `"`, "handle": `"` + handle + `"`,
			"member_actor_ids": "[" + strings.Join(ms, ",") + "]"})
	}
	// the admin makes max the lead of the project and gives it team api
	o.h.commit(o.boss.email, map[string]string{
		orgPath("project_members", leadPM): memberRow(leadPM, o.max.actor, o.project, `["lead"]`),
		orgPath("teams", team):             teamRow(team, "api", o.boss.actor),
	})
	// the lead puts himself and the contractor on api, by hand
	o.h.commit(o.max.email, map[string]string{orgPath("teams", team): teamRow(team, "api", o.boss.actor, o.max.actor, o.cy.actor)})
	// and makes a new team of his project with himself on it
	maxTeam := teamID(t)
	o.h.commit(o.max.email, map[string]string{orgPath("teams", maxTeam): teamRow(maxTeam, "ops", o.max.actor)})
	// the admin makes a team of the same project
	o.h.commit(o.boss.email, map[string]string{orgPath("teams", adminTeam): teamRow(adminTeam, "web", o.max.actor)})

	b := orgFolderClone(t, o.h.remote)
	if doc := rowDoc(t, b, team); doc == "" || strings.Contains(doc, o.max.actor.String()) {
		t.Errorf("team api as read = %s; want the admin's version (the lead not on it)", doc)
	}
	for _, held := range []struct {
		name string
		id   ids.AnyID
	}{{"the lead's change of team api", team}, {"the lead's new team ops", maxTeam}} {
		if why := heldReason(t, b, held.id); !strings.Contains(why, "who is on a team") || !strings.Contains(why, "only an admin changes the teams") || !strings.Contains(why, o.max.email) {
			t.Errorf("hold of %s = %q; want the admin-only team rule and %s named", held.name, why, o.max.email)
		}
	}
	if _, read := readField(t, b, maxTeam, "id"); read {
		t.Errorf("the lead's new team ops was read")
	}
	if _, read := readField(t, b, adminTeam, "id"); !read {
		t.Errorf("the admin's team web was not read (held: %q)", heldReason(t, b, adminTeam))
	}
}

// rowDoc is the row file of id as a full read has it ("" when left out).
func rowDoc(t *testing.T, b *Backend, id ids.AnyID) string {
	t.Helper()
	rows, _, err := b.Read(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ID == id {
			return string(r.Doc)
		}
	}
	return ""
}
