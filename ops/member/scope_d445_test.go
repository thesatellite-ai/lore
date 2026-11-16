package member

// scope_d445_test.go — `project member list` in a project's folder and
// with --project names that project's people and warns only about the
// teams it uses (devco D-445: inside the sync folder it listed every
// project, and --project sync printed a line for every person of every
// client team of the org).

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/profilestore/profilestoretest"
	"github.com/thesatellite-ai/lore/schema"
)

// hereEnv is the fixture's Env run inside a project's store folder.
type hereEnv struct {
	*profilestoretest.Fake
	here string
}

func (h hereEnv) HereProject(context.Context) (string, bool) { return h.here, h.here != "" }

func TestMemberListInAFolderIsThatProject(t *testing.T) {
	x := newFixture(t)
	for _, in := range []MemberAddIn{
		{Project: "acme-hub", Actor: "@mira", Roles: []string{"dev"}},
		{Project: "org", Actor: "@carol", Roles: []string{"lead"}},
	} {
		if _, err := x.add(in); err != nil {
			t.Fatal(err)
		}
	}
	out, err := memberList(x.ctx, hereEnv{Fake: x.f, here: x.hub.String()}, MemberListIn{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Project != "acme-hub" || len(out.Members) != 1 || !strings.Contains(out.Members[0].Actor, "mira") {
		t.Fatalf("in the acme-hub folder: project %q, members %+v", out.Project, out.Members)
	}
	every, err := memberList(x.ctx, x.f, MemberListIn{})
	if err != nil || len(every.Members) != 2 {
		t.Fatalf("outside a folder every project: %+v %v", every.Members, err)
	}
}

func TestMemberTeamWarningOnlyTeamsTheProjectUses(t *testing.T) {
	x := newFixture(t)
	team := func(handle string, project ids.ID[ids.Project], members ...ids.ID[ids.Actor]) {
		x.f.Put(row.Row{Table: schema.TableTeams, ID: anyID(t, ids.PrefixTeam), Fields: map[string]value.Value{
			"project_id": mustRef(t, project.Any()), "handle": txt(t, handle), "active": value.BoolOf(true), schema.FieldTeamMembers: refList(t, members...)}})
	}
	team("frontend", x.org, x.mira, x.carol) // org-wide, used by acme-hub (mira is a member)
	team("clientx", x.org, x.carol)          // org-wide, nobody of it in acme-hub
	team("acme-hub", x.org, x.carol)         // org-wide, named after the project
	if _, err := x.add(MemberAddIn{Project: "acme-hub", Actor: "@mira", Roles: []string{"dev"}}); err != nil {
		t.Fatal(err)
	}
	got := x.list(MemberListIn{Project: "acme-hub"}).Warnings
	for _, w := range got {
		if strings.Contains(w, "@clientx") {
			t.Errorf("a team acme-hub does not use warns: %s", w)
		}
	}
	for _, want := range []string{"@frontend: @carol", "@acme-hub: @carol"} {
		if !slices.ContainsFunc(got, func(w string) bool { return strings.Contains(w, want) }) {
			t.Errorf("no warning %q in:\n%s", want, strings.Join(got, "\n"))
		}
	}
}
