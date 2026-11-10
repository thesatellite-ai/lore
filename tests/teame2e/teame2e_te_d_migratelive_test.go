//go:build teame2e

package teame2e

// later.md 288 (MigrateLive e2e) on cmd/lore with two clones: memory rows
// a v0.1.17 teammate kept on the repo's live ref (refs/lore/live, `_v: 1`
// with a `_live` stamp) come into the repo's row files as `_v: 2` on one
// person's `lore migrate run`; the other clone reads them after a pull,
// and a second run changes nothing.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// teDLiveMem is a memory row that exists only on the v0.1.17 live ref.
const teDLiveMem = "mem_01a1a59b891c70b789144cd3e711fd01"

// teDPlantLive points refs/lore/live at a parentless commit holding
// files (path -> content) on c's remote, the way v0.1.17 kept the ref:
// never in the work tree or HEAD's history.
func teDPlantLive(t *testing.T, c *th.Clone, files map[string]string) {
	t.Helper()
	idx := c.With("GIT_INDEX_FILE=" + filepath.Join(t.TempDir(), "index"))
	for path, content := range files {
		tmp := filepath.Join(t.TempDir(), "blob")
		if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		oid := idx.Git("hash-object", "-w", tmp)
		idx.Git("update-index", "--add", "--cacheinfo", "100644,"+oid+","+path)
	}
	commit := idx.Git("commit-tree", idx.Git("write-tree"), "-m", "lore live (v0.1.17)")
	// Through a scratch branch, then set on the remote as is: a v0.1.17
	// clone's push replaced whatever the ref held.
	const scratch = "refs/heads/te-d-v0117-live"
	c.AdminPush("--no-verify", "origin", commit+":"+scratch)
	c.Remote.SetRef(refLive, commit)
	c.Remote.DeleteRef(scratch)
}

func TestTeD_MigrateLiveRowsReachTeammates(t *testing.T) {
	bc, carol := teDSoloPair(t, th.Cmd("migrate", "run"))
	bc.MustLore("init", "--non-interactive", "--name=tool")
	bc.CommitAll("lore")
	bc.AdminPush("origin", "main")
	prj := bc.MustLore("whoami").Out()
	i := strings.Index(prj, "prj_")
	if i < 0 {
		t.Fatalf("whoami names the project: %s", prj)
	}
	project := prj[i : i+len("prj_")+32]
	doc := `{"_table":"memories","_v":1,"body":"the staging db is reset nightly","created_at":"2026-11-04T08:09:56.252002659Z",` +
		`"id":"` + teDLiveMem + `","kind":"retrieved","project_id":"` + project + `","source_kind":"manual",` +
		`"updated_at":"2026-11-04T08:09:56.252002894Z","_live":{"hlc":"2026-11-04T08:09:56.252Z/0000/act_0e45506e64357c94a87e70ef1c6bbba9"}}`
	teDPlantLive(t, bc, map[string]string{liveRowPath(rootFolder, "memories", teDLiveMem): doc})

	cc := carol.Clone(bc.Remote, "tool")
	cc.Git("fetch", "-q", "origin", refLive+":"+refLive)
	rel := dataDir + "/memories/" + teDLiveMem + ".json"
	if _, ok := cc.ReadFile(rel); ok {
		t.Fatal("control: before migrate the row exists only on the live ref")
	}
	cc.MustLore("migrate", "run").Says(t, teDLiveMem)
	body, ok := cc.ReadFile(rel)
	if !ok {
		t.Fatalf("the live row is a row file after migrate: %s", cc.Status())
	}
	compact := strings.Join(strings.Fields(body), "")
	for _, want := range []string{`"_v":2`, `"_hlc":"2026-11-04T08:09:56.252Z/0000/`, "stagingdbisresetnightly"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("the migrated row lacks %s: %s", want, body)
		}
	}
	if strings.Contains(compact, `"_live"`) {
		t.Fatalf("the _live block stays on the live ref: %s", body)
	}
	cc.MustLore("migrate", "run").Never(t, teDLiveMem)
	cc.CommitAll("lore: migrate")
	cc.AdminPush("origin", "main")

	bc.Git("pull", "-q")
	bc.MustLore("memory", "show", teDLiveMem).Says(t, "the staging db is reset nightly")
}
