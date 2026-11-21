//go:build stories && !windows

package stories

// b_rostermeta_test.go — story B, step 10 (devco:D-862, bug hunt H-114,
// owner org-setup item 3): people and teams on the org's roster hold
// custom fields, as notes and resources do. Cora gives the web team an
// emoji and herself a desk with --meta on team add and actor edit; the
// shows print them, lore meta unset removes one, and Nia reads them on
// her own laptop after a sync. Every printed command runs as printed.

import (
	"encoding/json"
	"testing"
)

func TestStoryBRosterRowsHoldMeta(t *testing.T) {
	b := newBCompany(t)
	s := b.s
	const store = "acme_team"

	s.Step("D-862/team-and-person-take-custom-fields", func() {
		b.person(t, bCTO)
		b.lore(bCTO, "store", "init", "--name", store, "--url", b.store.SSHURL(), "--yes").OK(t)
		b.lore(bCTO, "org", "create", "--org", bOrg, "--store", store, "--own", "github.com/acme/*",
			"--key", "human:"+bCTO.Email, "--name", bCTO.Name, "--handle", "cora", "--yes").OK(t)
		r := b.lore(bCTO, "team", "add", "--handle", "@web", "--name", "Web", "--meta", "emoji=spider", "--meta", "room=web-chat", "--yes").OK(t).
			Says(t, "@web").Never(t, "E_META_TABLE", "unknown flag")
		s.NamedFlags(t, r)
		b.lore(bCTO, "actor", "edit", "--actor", "@cora", "--meta", "desk=4F", "--yes").OK(t).Says(t, "edited (meta)")
		b.lore(bCTO, "team", "show", "--handle", "@web").OK(t).Says(t, "meta:", "emoji: spider", "room: web-chat")
		b.lore(bCTO, "actor", "show", "--id", "@cora").OK(t).Says(t, "meta:", "desk: 4F")
		b.lore(bCTO, "actor", "list", "--json").OK(t).Says(t, `"desk": "4F"`)
		b.lore(bCTO, "team", "list", "--json").OK(t).Says(t, `"emoji": "spider"`)
	})

	s.Step("D-862/meta-unset-and-a-teammate-reads-them", func() {
		team := b.lore(bCTO, "team", "show", "--handle", "@web", "--json").OK(t)
		var out struct {
			Team struct {
				ID string `json:"id"`
			} `json:"team"`
		}
		if err := json.Unmarshal([]byte(team.Stdout), &out); err != nil || out.Team.ID == "" {
			t.Fatalf("team show --json: %v\n%s", err, team.Stdout)
		}
		id := out.Team.ID
		b.lore(bCTO, "meta", "unset", "--id", id, "--key", "room", "--yes").OK(t)
		b.lore(bCTO, "team", "show", "--handle", "@web").OK(t).Says(t, "emoji: spider").Never(t, "room: web-chat")
		b.lore(bCTO, "member", "add", "--email", bHire.Email, "--name", bHire.Name, "--handle", "nia", "--yes").OK(t)
		b.lore(bCTO, "sync").OK(t)
		s.AdvanceDays(1)
		b.person(t, bHire)
		b.lore(bHire, "org", "import", "--url", b.store.SSHURL(), "--key", "human:"+bHire.Email, "--name", bHire.Name, "--handle", "nia", "--yes").OK(t)
		b.lore(bHire, "team", "show", "--handle", "@web").OK(t).Says(t, "emoji: spider")
		b.lore(bHire, "actor", "show", "--id", "@cora").OK(t).Says(t, "desk: 4F")
	})
}
