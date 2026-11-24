//go:build stories && !windows

package stories

// O_ask_identifiers_test.go — devco D-971 (bug hunt H-230, Sync benchmark
// set 5 S5-06 and S5-11) on the owner-shaped machine (story O): an own
// repo of the org linked to its folder keeps many short notes and a
// decision sharing the question's common words (role, key, system; sync
// workflow run), two memories naming the asked errors as code
// identifiers (ErrGroupNameTaken, ErrBuiltinGroupLocked), an off-topic
// hotfix and the hotfix naming JobTimeLimit. lore ask read
// ErrGroupNameTaken as one word holding none of "reserved role key", so
// the rows sharing only the common words filled the first places. Now
// the memories naming the errors are in the top three and the on-topic
// hotfix leads.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOAskIdentifiersRank(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	home := oOrg + "/rolesapp"

	s.Step("D-971/short-rows-naming-identifiers", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "rolesapp").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "rolesapp", "--own", "--yes").OK(t)
		var b strings.Builder
		line := func(op string, in map[string]any) {
			raw, err := json.Marshal(map[string]any{"op": op, "input": in})
			if err != nil {
				t.Fatal(err)
			}
			b.Write(raw)
			b.WriteByte('\n')
		}
		for i := range 20 {
			line("memory add", map[string]any{"body": fmt.Sprintf("Note %d: the workspace role screen lists each role, its key and the system flag; the sync workflow run page shows the client error for each run.", i)})
		}
		line("decision add", map[string]any{"title": "Workspace roles: system roles plus custom roles", "body": "Each workspace gets the system roles owner, admin and member when it is created; a custom role has its own key, unique in the workspace, and system roles are never deleted."})
		line("memory add", map[string]any{"body": "role.Create returns ErrGroupNameTaken (409) when the new key collides with a seeded key (owner, admin, member)."})
		line("memory add", map[string]any{"body": "role.Update and role.Delete return ErrBuiltinGroupLocked for a seeded role; only custom roles change."})
		line("hotfix add", map[string]any{"title": "Workflow client error map", "body": "The sync workflow client maps each run error of the engine (cancelled, failed, timed out) to a typed sync error; a run that timed out was shown as an internal error until the map listed it."})
		line("hotfix add", map[string]any{"title": "JobTimeLimit raised to 1 h", "body": "Long catalog imports hit the 30 min JobTimeLimit and stopped mid-batch; JobTimeLimit is now 1 h in the workflow options."})
		batch := filepath.Join(t.TempDir(), "rows.jsonl")
		if err := os.WriteFile(batch, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		ada.Lore(pc.Dir, "batch", "apply", "--file", batch, "--yes").OK(t)
		ada.Lore(pc.Dir, "whoami").OK(t).Says(t, home)
	})

	s.Step("D-971/the-rows-naming-the-asked-thing-lead", func() {
		top := func(q string, n int) string {
			r := ada.Lore(pc.Dir, "ask", "--query", q, "--json").OK(t)
			var out struct {
				Rows []struct {
					Title string `json:"title"`
					Body  string `json:"body"`
				} `json:"rows"`
			}
			if err := json.Unmarshal([]byte(r.Out()), &out); err != nil {
				t.Fatalf("[O D-971] ask --json: %v\n%s", err, r)
			}
			var sb strings.Builder
			for _, row := range out.Rows[:min(n, len(out.Rows))] {
				sb.WriteString(row.Title + " " + row.Body + "\n")
			}
			return sb.String()
		}
		q := "what happens when a custom role uses a reserved role key, and can someone edit a system role"
		if got := top(q, 3); !strings.Contains(got, "ErrGroupNameTaken") || !strings.Contains(got, "ErrBuiltinGroupLocked") {
			t.Errorf("[O D-971] the memories naming the asked errors are not in the top three of %q:\n%s\n%s", q, got, ada.Lore(pc.Dir, "ask", "--query", q).OK(t))
		}
		q = "what is the max run duration of a sync workflow run and why was it raised"
		if got := top(q, 1); !strings.Contains(got, "JobTimeLimit raised") {
			t.Errorf("[O D-971] the on-topic hotfix does not lead %q:\n%s\n%s", q, got, ada.Lore(pc.Dir, "ask", "--query", q).OK(t))
		}
	})
}
