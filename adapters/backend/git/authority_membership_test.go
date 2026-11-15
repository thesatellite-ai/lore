package git

import (
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

// TestStampCarryNeedsSameContent (devco D-329): a version that carries
// a stamp its stated writer committed is read only when its content is
// what they committed; the same stamp on changed content is held back.
func TestStampCarryNeedsSameContent(t *testing.T) {
	t.Parallel()
	o := newOrgTeam(t)
	shared := mustID[ids.Memory](t)
	o.h.commit(o.boss.email, map[string]string{orgPath("memories", shared): stampedRow(shared, o.boss.actor, "2026-01-02T00:00:02.000Z", "v1")})
	o.h.commit(o.max.email, map[string]string{orgPath("memories", shared): stampedRow(shared, o.max.actor, "2026-01-02T00:00:03.000Z", "v2")})
	// the admin's stamp and name, on a body the admin never wrote
	o.h.commit(o.max.email, map[string]string{orgPath("memories", shared): stampedRow(shared, o.boss.actor, "2026-01-02T00:00:02.000Z", "changed")})

	b := orgFolderClone(t, o.h.remote)
	if body, _ := readField(t, b, shared, "body"); body == "changed" {
		t.Error("changed content under the admin's old stamp was read")
	}
	if why := heldReason(t, b, shared); !strings.Contains(why, "claims to be written by") || !strings.Contains(why, o.max.email) {
		t.Errorf("hold = %q; want the claimed writer and the author named", why)
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
}
