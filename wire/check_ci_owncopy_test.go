package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCIAcceptsOwnRepoActorCopies (devco R-13, regression of devco D-14):
// in an org's own repo lore writes full actor copies (stable_key, handle,
// stamped with the roster store or the org entry as origin; Q-R2-2), and
// `lore check leaks --ci` must accept them: it refused every own repo where
// a teammate had written a row and hinted `lore migrate run`, which keeps
// them. An unstamped copy with an email and a 0.1.17 copy still block.
// Break-verified: with checkCI on the strict client rule the first case
// fails with "an actor copy holds more than". devco S-16: the same in a
// repo with no org, where the repo is the roster and its rows (full,
// unstamped, with lore's write stamp) must pass too; break-verified with
// RepoRosterActorRow returning false.
func TestCIAcceptsOwnRepoActorCopies(t *testing.T) {
	t.Parallel()
	const pid = "prj_01a1dc09d63d72738410e34732dcd15f"
	const meta = `{"_v":2,"format":2,"project_id":"` + pid + `","repo_id":"rep_b2519885dcb17d479bd9049302a12896"}`
	head := `{"_hlc":"2026-11-14T21:49:57.155Z/0000/act_85d85545ff7478548da99a698c7f1700","_table":"actors","created_at":"2026-11-14T21:49:57.155000000Z","display_name":"Rosa","kind":"human",`
	roster := head + `"_v":2,"id":"act_85d85545ff7478548da99a698c7f1700","handle":"rosa","stable_key":"human:rosa@example.test","origin_store":"sto_01a1dc09ca527e8ab39ce4fb44ed13a7","origin_version":"2026-11-14T21:49:57.155Z/0000/act_85d85545ff7478548da99a698c7f1700"}`
	orgEntry := head + `"_v":2,"id":"act_fec494f08acd7b63b6bdc78d19886835","stable_key":"human:ivo@example.test","origin_store":"org:initrode","origin_version":"3760a8de4c94"}`
	// devco S-16: a repo outside any org is its own roster (actor register,
	// agent start, a sync landing them): its rows are full and unstamped,
	// and carry lore's write stamp. R-13's cases above all had an org.
	written := `"_w":{"v":"0.1.18"},`
	noOrgPerson := head + written + `"_v":2,"id":"act_85d85545ff7478548da99a698c7f1700","handle":"rosa","stable_key":"human:rosa@example.test"}`
	noOrgAgent := `{"_hlc":"2026-11-14T21:50:37.171Z/0000/act_85d85545ff7478548da99a698c7f1700","_table":"actors",` + written + `"_v":2,"active":true,"created_at":"2026-11-14T21:50:37.171000000Z","display_name":"rosa-codex-1","id":"act_84ea8ce9268673f888936a21b8a22e61","kind":"agent","label":"rosa-codex-1","owner_actor_id":"act_85d85545ff7478548da99a698c7f1700","stable_key":"agent:rosa-codex-1"}`
	cases := []struct {
		name   string
		actors map[string]string
		want   string // "" = clean
	}{
		{"own repo copies", map[string]string{"act_85d85545ff7478548da99a698c7f1700": roster, "act_fec494f08acd7b63b6bdc78d19886835": orgEntry}, ""},
		{"no-org repo roster rows", map[string]string{"act_85d85545ff7478548da99a698c7f1700": noOrgPerson, "act_84ea8ce9268673f888936a21b8a22e61": noOrgAgent}, ""},
		{"no-org roster row with an unknown key", map[string]string{"act_85d85545ff7478548da99a698c7f1700": head + written + `"_v":2,"id":"act_85d85545ff7478548da99a698c7f1700","stable_key":"human:rosa@example.test","email":"rosa@example.test"}`}, "email"},
		{"lore 0.1.17 roster row", map[string]string{"act_85d85545ff7478548da99a698c7f1700": head + written + `"_v":1,"id":"act_85d85545ff7478548da99a698c7f1700","handle":"rosa","stable_key":"human:rosa@example.test"}`}, "stable_key"},
		{"unstamped copy with an email", map[string]string{"act_85d85545ff7478548da99a698c7f1700": head + `"_v":2,"id":"act_85d85545ff7478548da99a698c7f1700","stable_key":"human:rosa@example.test"}`}, "stable_key"},
		{"stamp that names no org", map[string]string{"act_85d85545ff7478548da99a698c7f1700": head + `"_v":2,"id":"act_85d85545ff7478548da99a698c7f1700","stable_key":"human:rosa@example.test","origin_store":"setup:solo","origin_version":"3760a8de4c94"}`}, "stable_key"},
		{"lore 0.1.17 copy", map[string]string{"act_85d85545ff7478548da99a698c7f1700": head + `"_v":1,"id":"act_85d85545ff7478548da99a698c7f1700","stable_key":"human:rosa@example.test","origin_store":"sto_01a1dc09ca527e8ab39ce4fb44ed13a7","origin_version":"x"}`}, "lore migrate run"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			data := filepath.Join(root, ".lore", "data")
			if err := os.MkdirAll(filepath.Join(data, "actors"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(data, "_meta.json"), []byte(meta), 0o644); err != nil {
				t.Fatal(err)
			}
			for id, doc := range c.actors {
				if err := os.WriteFile(filepath.Join(data, "actors", id+".json"), []byte(doc), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			err := checkCI(context.Background(), root)
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("--ci refused lore's own copies: %v", err)
			case c.want != "" && err == nil:
				t.Fatal("--ci accepted the copy")
			case c.want != "" && !strings.Contains(err.Error()+hintOf(err), c.want):
				t.Fatalf("--ci error lacks %q: %v / %s", c.want, err, hintOf(err))
			}
		})
	}
}
