//go:build teame2e

package teame2e

// later.md 288 (renameOrgClashes e2e, the r4 repro) on cmd/lore with two
// clones: a repo whose committed .lore/stores.yaml already uses the org's
// bare name for the store root (written before the repo was linked, as
// v0.1.17's `lore store init <org>` did) still sends --to team into its
// linked folder: the folder entry is attached as "<org>/<folder>"; --to
// <org> keeps v0.1.17's place (the linked folder, wire linkedFolderName)
// and `--to <org>/.` is the explicit store root, on every teammate's clone.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestTeD_OrgNameClashWithCommittedStoresYAML(t *testing.T) {
	a := newAgency(t, th.Flag("to", "memory", "add"))
	tc := a.alice.Clone(a.tools, "tools")
	tc.MustLore("init", "--non-interactive", "--name=tools")
	tc.WriteFile(".lore/stores.yaml", "stores:\n  - name: "+orgNorthwind+"\n    url: https://"+slugStore+"\n")
	tc.CommitAll("lore + stores.yaml naming the store root")
	tc.AdminPush("origin", "main")
	tc.MustLore("org", "link", orgNorthwind, "--project", "tools", "--yes")

	clash := orgNorthwind + "/tools"
	tc.MustLore("store", "list").Says(t, clash)
	memIn := func(c *th.Clone, body, to string) string {
		t.Helper()
		return mustID(t, c.MustLore("memory", "add", "--body", body, "--to", to), "mem")
	}
	where := map[string]string{
		memIn(tc, "alice to team", "team"):                "tools/memories/",
		memIn(tc, "alice to the folder", clash):           "tools/memories/",
		memIn(tc, "alice to the bare org", orgNorthwind):  "tools/memories/",
		memIn(tc, "alice to the root", orgNorthwind+"/."): "memories/",
	}

	// dev1 clones the repo with the committed stores.yaml: same names, same
	// places.
	dc := a.dev1.Clone(a.tools, "tools")
	dc.MustLore("store", "list").Says(t, clash)
	where[memIn(dc, "dev1 to team", "team")] = "tools/memories/"
	where[memIn(dc, "dev1 to the bare org", orgNorthwind)] = "tools/memories/"
	where[memIn(dc, "dev1 to the root", orgNorthwind+"/.")] = "memories/"
	tc.MustLore("sync")
	dc.MustLore("sync")

	files := a.store.Files(refMain)
	for id, dir := range where {
		found := ""
		for _, f := range files {
			if strings.HasSuffix(f, "/"+id+".json") || f == "memories/"+id+".json" {
				found = f
			}
		}
		if found != dir+id+".json" {
			t.Errorf("%s: want %s%s.json, found %q", id, dir, id, found)
		}
	}
	if st := dc.Status(); st != "" {
		t.Fatalf("store writes leave the repo alone: %q", st)
	}
}
