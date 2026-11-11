//go:build journeys && !windows

package journeys

// folders_team_test.go — an admin runs the team from an org with several
// stores (owner reports, 2026-11-11). Lee's org lists two stores: the team
// store and a client store. A client repo that holds no lore files is
// linked to a folder of the client store whose project row does not exist
// yet. From that repo Lee sends the first invite: it used to hang forever
// (the session lookup made the folder's project row, whose audit record
// asked for the session again). Folder names resolve across every store
// of the org, in a linked repo and outside any repo, and `project list`
// shows every folder with its store. Lee makes a team, changes its
// members and removes it, outside any repo, and a task assigned to the
// team keeps working.

import (
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The client store, the client repo and the people invited (placeholders).
const (
	jTeamTwoSlug   = "github.com/acme-co/notes-clients"
	jTeamHubSlug   = "github.com/client-co/billing"
	jTeamDevEmail  = "devi@example.test"
	jTeamDevGitHub = "devi-gh"
	jTeamQAEmail   = "quinn@example.test"
	jTeamHandle    = "web"
	jTeamTaskTitle = "billing: retry failed charges"
	// jTeamSessionEnv names this process's session (identity.EnvSession).
	jTeamSessionEnv = "LORE_SESSION"
)

// jProject is one row of `project list --json`.
type jProject struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Source struct {
		Store  string `json:"store"`
		Folder string `json:"folder"`
	} `json:"_source"`
}

func TestJourneyFoldersTeamFromLinkedRepo(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	two := w.Remote(jTeamTwoSlug)
	var hub *teamharness.Clone

	w.Step("day1/an-org-with-a-client-store", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		s := lee.Clone(tt.store, "team-store")
		jWrite(t, filepath.Join(s.Dir, filepath.FromSlash(jManyOrgFile)), "name: "+tlOrg+"\nstores:\n"+
			"    - url: "+tt.store.SSHURL()+"\n      org_folder: org\n"+
			"    - url: "+two.SSHURL()+"\n")
		s.CommitAll("list the client store")
		s.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(parent, "org", "refresh", tlOrg).OK(t)
		lee.Lore(parent, "store", "folder", "add", two.SSHURL(), "client_a").OK(t)
		lee.Lore(parent, "store", "folder", "add", two.SSHURL(), "client_b").OK(t)
		lee.Lore(parent, "store", "folder", "add", "team", "client_c").OK(t)
		// a client repo with no lore files, linked to client_a, which has no
		// project row yet
		hub = jPlainRepo(t, lee, w.Remote(jTeamHubSlug), filepath.Join(parent, "billing"))
		lee.LoreIn(hub, "org", "link", tlOrg, "--project", "client_a", "--store", two.SSHURL(), "--yes").OK(t)
	})

	w.Step("day1/first-invite-from-the-linked-repo", func() {
		// the hang: every lore run has a deadline (teamharness CmdTimeout),
		// past which it is stopped with a goroutine dump and fails here
		// (by prj_ id, as the owner did: folder names did not resolve then;
		// from an agent's session, where every write links its session row)
		lee.SetEnv(jTeamSessionEnv, "billing-session-1")
		defer lee.SetEnv(jTeamSessionEnv, "")
		var fl struct {
			Folders []jFolderRow `json:"folders"`
		}
		tlJSON(t, lee.Lore(parent, "store", "folder", "list", two.SSHURL(), "--json").OK(t), &fl)
		folderID := map[string]string{}
		for _, f := range fl.Folders {
			folderID[f.Folder] = f.ID
		}
		lee.LoreIn(hub, "team", "invite", jTeamDevEmail, "--github", jTeamDevGitHub, "--role", "dev",
			"--project", folderID["client_a"], "--project", folderID["client_b"], "--yes").OK(t).Says(t, "lore setup join lore-invite:v1:")
		inv := jInvites(t, lee, hub.Dir)
		if len(inv) != 1 || len(inv[0].Projects) != 2 {
			t.Fatalf("the invite names two projects: %+v", inv)
		}
	})

	w.Step("day2/folder-names-across-the-org-stores", func() {
		w.AdvanceDays(1)
		// outside any repo, a folder of either store resolves
		lee.Lore(parent, "team", "invite", jTeamQAEmail, "--role", "qa", "--project", "client_b", "--project", "client_c", "--yes").OK(t)
		lee.Lore(parent, "team", "invite", "nobody@example.test", "--role", "dev", "--project", "client_zz", "--yes").
			Fails(t, teamharness.ExitNotFound, "E_NOT_FOUND", "client_zz", "lore project list")
		// project list shows every folder of the org, each with its store
		// (named, not the url: a run with no terminal redacts urls)
		var pl struct {
			Data []jProject `json:"data"`
		}
		tlJSON(t, lee.LoreIn(hub, "project", "list", "--json").OK(t), &pl)
		stores := map[string]string{}
		for _, p := range pl.Data {
			stores[p.Name] = p.Source.Store
		}
		for _, f := range []string{"client_a", "client_b", "client_c"} {
			if stores[f] == "" {
				t.Errorf("project list does not show %s with its store: %+v", f, pl.Data)
			}
		}
		if stores["client_b"] == stores["client_c"] {
			t.Errorf("client_b and client_c live in different stores, project list says %q", stores["client_b"])
		}
		lee.LoreIn(hub, "project", "list").OK(t).Says(t, "client_a", "client_b", "client_c", "(folder, no rows yet)")
	})

	w.Step("day3/teams-outside-any-repo", func() {
		w.AdvanceDays(1)
		lee.Lore(parent, "team", "add", "@"+jTeamHandle, "--name", "Web", "--members", "@lee").OK(t).Says(t, "@web (1 member(s)) added")
		var tl struct {
			Teams []struct {
				Handle  string `json:"handle"`
				Active  bool   `json:"active"`
				Members []struct {
					Handle string `json:"handle"`
				} `json:"members"`
			} `json:"teams"`
		}
		tlJSON(t, lee.Lore(parent, "team", "list", "--json").OK(t), &tl)
		if len(tl.Teams) != 1 || tl.Teams[0].Handle != jTeamHandle || len(tl.Teams[0].Members) != 1 || tl.Teams[0].Members[0].Handle != "lee" {
			t.Fatalf("team list = %+v", tl)
		}
		lee.LoreIn(hub, "team", "show", "@web").OK(t).Says(t, "@web", "@lee")
		// the team works as an @mention on a task (written to the org store)
		lee.LoreIn(hub, "task", "add", jTeamTaskTitle, "--to", tlOrg, "--assign", "@web").OK(t)
		lee.LoreIn(hub, "task", "list", "--to", tlOrg).OK(t).Says(t, jTeamTaskTitle)
		lee.Lore(parent, "team", "member", "remove", "@web", "@lee").OK(t).Says(t, "0 member(s) now", "@lee taken off")
		lee.Lore(parent, "team", "remove", "@web").OK(t).Says(t, "removed")
		lee.Lore(parent, "team", "list").OK(t).Never(t, "@web")
		lee.Lore(parent, "team", "add", "@lee").Refused(t).Says(t, "a team and a person cannot share one")
	})
}

// jInvite is one row of `team invite list --json`.
type jInvite struct {
	ID       string   `json:"id"`
	Email    string   `json:"email"`
	Projects []string `json:"projects"`
}

// jInvites is the org's invites as m reads them in dir.
func jInvites(t testing.TB, m *Machine, dir string) []jInvite {
	t.Helper()
	var out struct {
		Invites []jInvite `json:"invites"`
	}
	tlJSON(t, m.Lore(dir, "team", "invite", "list", "--json").OK(t), &out)
	return out.Invites
}
