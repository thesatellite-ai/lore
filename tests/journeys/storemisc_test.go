//go:build journeys && !windows

package journeys

// storemisc_test.go — a lead looks up people, teams and a store folder
// from inside the folder (devco D-489, D-491, D-492). Before: `search
// <handle>` found no one and `--tables team` was refused ("none of
// --tables matched a known entity: team"); `decision list --repo <folder>`
// said only `no repo "<folder>" in this project` while --project worked;
// `project show` printed the name and the date, no store, rows or members.
//
//	store folder ─▶ search finds @lee and @clientx ─▶ --repo <folder> names --project ─▶ project show counts

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Names of the store-misc journey (placeholders).
const (
	jsmFolder = "app1"
	jsmTeam   = "clientx"
	jsmTask   = "renew the app1 domain"
)

func TestJourneyStoreFolderFindsPeopleAndCounts(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	app := tt.repo[tlLee.Email].Dir
	var folder string

	w.Step("day1/a-store-folder-with-a-task-and-a-team", func() {
		tlStartTeam(t, lee, app, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", jsmFolder).OK(t)
		out := lee.Lore(parent, "task", "add", jsmTask, "--to", tlOrg+"/"+jsmFolder, "--json").OK(t).Out()
		m := regexp.MustCompile(`"id": "(tsk_[0-9a-f]{32})"`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("no task id in %s", out)
		}
		lee.Lore(parent, "team", "add", "@"+jsmTeam, "--members", "@lee", "--yes").OK(t)
		lee.Lore(parent, "project", "member", "add", "--project", jsmFolder, "--actor", "@lee", "--role", "dev", "--yes").OK(t)
		p := jPath(t, lee.Lore(parent, "path", m[1], "--json").OK(t))
		folder = filepath.Dir(filepath.Dir(p.Path))
		lee.Lore(parent, "sync").OK(t)
	})

	w.Step("day1/search-finds-people-and-teams", func() {
		// D-489: a handle, a name and a team are found by the ranked search
		lee.Lore(folder, "search", "lee").OK(t).Says(t, "member").Never(t, "(no matches)")
		lee.Lore(folder, "search", jsmTeam, "--tables", "team").OK(t).Says(t, "@"+jsmTeam).Never(t, "none of --tables matched")
		lee.Lore(folder, "search", "lee", "--tables", "member", "--json").OK(t).Says(t, `"entity": "member"`)
		lee.Lore(folder, "search", "status").OK(t).Never(t, "actors", "teams")
	})

	w.Step("day1/repo-flag-names-the-folder", func() {
		// D-491: --repo <folder> in a store folder points at --project
		lee.Lore(folder, "decision", "list", "--repo", jsmFolder).Refused(t, "is a project (a store folder), not a repo", "--project "+jsmFolder)
		lee.Lore(folder, "rule", "list", "--repo", jsmFolder).Refused(t, "--project "+jsmFolder)
		lee.Lore(folder, "decision", "list", "--project", jsmFolder).OK(t)
	})

	w.Step("day1/project-show-has-the-counts", func() {
		// D-492: store, rows, repos and members, as project list counts them
		r := lee.Lore(folder, "project", "show").OK(t).Says(t, "name:    "+jsmFolder, "store:   [", "rows:    ", "repos:   0", "members: 1", "@lee  dev")
		list := lee.Lore(folder, "project", "list").OK(t).Out()
		rows := regexp.MustCompile(`rows:\s+(\d+)`).FindStringSubmatch(r.Out())
		if rows == nil || !strings.Contains(list, jsmFolder) || !regexp.MustCompile(jsmFolder+`.*  `+rows[1]+` rows?\b`).MatchString(list) {
			t.Errorf("project show's rows differ from project list's:\nshow: %s\nlist: %s", r.Out(), list)
		}
		lee.Lore(folder, "project", "show", "--json").OK(t).Says(t, `"rows": `, `"store": "[`)
	})
}
