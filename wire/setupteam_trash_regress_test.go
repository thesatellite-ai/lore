package wire_test

// setupteam_trash_regress_test.go — re-adding an admin setup whose
// namesake is in the trash (owner decision 2026-11-08): the team step's
// org.name_free said only "an orgs[] entry named hub exists", with no way
// out but a hand edit. It now names the trashed setup and the two ways out
// (restore it, or purge it to reuse the name); the setup's own team, its
// team step opened for edit, passes.

import (
	"strings"
	"testing"
)

const rwTrashedTeamConfig = `orgs:
  - name: hub
    stores:
      - url: git@github.com:octo-personal/lore-team.git
        org_folder: org
trash:
  setups:
    - name: hubadmin
      kind: admin
      org: hub
      removed_at: "2026-11-08T09:00:00Z"
`

// orgNameFree is the org.name_free result of out (zero when absent).
func orgNameFree(out rwCheckOut) (status, detail string) {
	for _, c := range out.Checks {
		if c.Check == "org.name_free" {
			return c.Status, c.Detail
		}
	}
	return "", ""
}

func TestTeamNameOfATrashedSetup(t *testing.T) {
	w := newRegressWorld(t, rwShape{GHAccounts: 1, SSH: "default_only"})
	w.WriteConfig(rwTrashedTeamConfig)
	if err := w.Apply("hubadmin", "kind", rwAnswers{"kind": str("admin"), "name": str("hubadmin")}); err != nil {
		t.Fatalf("kind step of a new setup named like a trashed one: %v", err)
	}
	st, detail := orgNameFree(w.Check("hubadmin", "team", rwAnswers{"org": str("hub"), "store_url": str("git@github.com:octo-personal/lore-team.git")}))
	if st != "fail" || !strings.Contains(detail, "in the trash") || !strings.Contains(detail, "restore") || !strings.Contains(detail, "purge") {
		t.Fatalf("org.name_free for a trashed setup's team = %s %q, want a fail naming the trash, restore and purge", st, detail)
	}
}
