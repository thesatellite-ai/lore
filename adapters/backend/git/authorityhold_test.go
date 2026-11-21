package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
)

// TestHeldCommitIsNeverPushed (devco D-694): a lead's hand-made commit
// that puts himself on a team is held back by every reader, so his own
// sync keeps it on his machine instead of pushing it: the report names
// the hold, the reason, the commit and the way out, and the remote's team
// row stays the admin's. Once he undoes it with the printed git revert,
// the next sync sends both commits and the team is the admin's version.
func TestHeldCommitIsNeverPushed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	o := newOrgTeam(t)
	leadPM, team := mustID[ids.ProjectMember](t), teamID(t)
	teamRow := func(members ...ids.AnyID) string {
		var ms []string
		for _, m := range members {
			ms = append(ms, `"`+m.String()+`"`)
		}
		return rowJSON("teams", team, map[string]string{"project_id": `"` + o.project.String() + `"`, "handle": `"api"`,
			"member_actor_ids": "[" + strings.Join(ms, ",") + "]"})
	}
	o.h.commit(o.boss.email, map[string]string{
		orgPath("project_members", leadPM): memberRow(leadPM, o.max.actor, o.project, `["lead"]`),
		orgPath("teams", team):             teamRow(o.boss.actor),
	})
	b := orgFolderClone(t, o.h.remote)
	p := orgPath("teams", team)
	if err := os.WriteFile(filepath.Join(b.Dir(), filepath.FromSlash(p)), []byte(teamRow(o.boss.actor, o.max.actor)), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, b.Dir(), "commit", "--quiet", "--no-verify", "--author", "max <"+o.max.email+">", "-am", "put myself on api")
	mine := strings.TrimSpace(gitT(t, b.Dir(), "rev-parse", "--short=7", "HEAD"))

	rep, err := b.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"teams " + team.String(), "commit " + mine, o.max.email, "only an admin changes the teams", "kept on this machine, not pushed", "git -C " + b.Dir() + " revert --no-edit " + mine} {
		if !strings.Contains(rep.Held, want) {
			t.Errorf("sync's hold = %q; want %q in it", rep.Held, want)
		}
	}
	if rep.Pushed {
		t.Errorf("sync pushed the held commit")
	}
	if remote := gitT(t, o.h.remote, "show", testBranch+":"+p); strings.Contains(remote, o.max.actor.String()) {
		t.Fatalf("the remote's team row carries the held change: %s", remote)
	}

	// the way out the line names: undo the commit, sync again
	gitT(t, b.Dir(), "revert", "--no-edit", mine)
	rep, err = b.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Held != "" || !rep.Pushed {
		t.Errorf("sync after the revert: held %q, pushed %v; want sent", rep.Held, rep.Pushed)
	}
	if remote := gitT(t, o.h.remote, "show", testBranch+":"+p); strings.Contains(remote, o.max.actor.String()) {
		t.Errorf("the remote's team row after the revert: %s", remote)
	}
	if doc := rowDoc(t, b, team); strings.Contains(doc, o.max.actor.String()) {
		t.Errorf("team api as read = %s; want the admin's version", doc)
	}
}

// TestMemberTeamContactCommitIsHeld (devco D-867): a team's contacts are
// an admin's change like its members, so a member's hand-made commit that
// points @api's chat channel elsewhere is held back by sync and never
// pushed, with the reason naming the team's contacts.
func TestMemberTeamContactCommitIsHeld(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	o := newOrgTeam(t)
	team := teamID(t)
	_, rest, _ := strings.Cut(teamID(t).String(), "_")
	contact, err := ids.ParseAny(string(ids.PrefixTeamContact) + "_" + rest)
	if err != nil {
		t.Fatal(err)
	}
	o.h.commit(o.boss.email, map[string]string{
		orgPath("teams", team): rowJSON("teams", team, map[string]string{"project_id": `"` + o.project.String() + `"`, "handle": `"api"`,
			"member_actor_ids": `["` + o.boss.actor.String() + `"]`}),
	})
	b := orgFolderClone(t, o.h.remote)
	p := orgPath("team_contacts", contact)
	body := rowJSON("team_contacts", contact, map[string]string{"team_id": `"` + team.String() + `"`, "type": `"slack"`,
		"value": `"#not-the-team"`, "value_norm": `"#not-the-team"`, "primary": "true"})
	if err := os.MkdirAll(filepath.Dir(filepath.Join(b.Dir(), filepath.FromSlash(p))), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.Dir(), filepath.FromSlash(p)), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, b.Dir(), "add", "-A")
	gitT(t, b.Dir(), "commit", "--quiet", "--no-verify", "--author", "max <"+o.max.email+">", "-m", "our channel")
	rep, err := b.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"team_contacts " + contact.String(), o.max.email, "changes a team's contacts", "kept on this machine, not pushed"} {
		if !strings.Contains(rep.Held, want) {
			t.Errorf("sync's hold = %q; want %q in it", rep.Held, want)
		}
	}
	if rep.Pushed {
		t.Errorf("sync pushed the held commit")
	}
}
