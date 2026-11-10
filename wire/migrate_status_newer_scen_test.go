package wire

// User scenario (migrate area): a teammate on a newer lore wrote a row in a
// format this lore cannot read (`_v` above row.FormatVersion). `lore migrate
// status` must count it in the place's "newer" column (and its total), next
// to the v1 and v2 rows, and `lore migrate run` must leave its file alone.

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
)

// newerMemID is the fixture id of the row written by a newer lore.
const newerMemID = "mem_01a1a59b891c70b789144cd3e711fe77"

// v1MemID is the fixture id of a v0.1.17 row beside it.
const v1MemID = "mem_01a1a59b891c70b789144cd3e711fe01"

// TestMigrateScenStatusCountsNewerRows: one v1 row and one row at
// FormatVersion+1: status says v1 1, newer 1; a run rewrites the v1 row
// only and the newer file stays byte-identical; status then says v1 0,
// newer still 1.
func TestMigrateScenStatusCountsNewerRows(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	pid := projectID(t, a)
	v1Row(t, repo, schema.TableMemories, v1MemID, `"project_id":"`+pid+`","body":"old"`)
	newer := `{"_hlc":"2026-11-04T08:09:56.252Z/0000/act_0e45506e64357c94a87e70ef1c6bbba9","_table":"memories","_v":` +
		strconv.Itoa(row.FormatVersion+1) + `,"body":"future","created_at":"2026-11-04T08:09:56.252002659Z","id":"` + newerMemID +
		`","project_id":"` + pid + `","updated_at":"2026-11-04T08:09:56.252002894Z"}`
	newerPath := filepath.Join(repo, ".lore", "data", schema.TableMemories, newerMemID+".json")
	if err := os.WriteFile(newerPath, []byte(newer), 0o600); err != nil {
		t.Fatal(err)
	}
	repoPlace := func(st opsmigrate.StatusOut) opsmigrate.PlaceStatus {
		t.Helper()
		for _, p := range st.Places {
			if p.Place == "repo" {
				return p
			}
		}
		t.Fatalf("status has no repo place: %+v", st)
		return opsmigrate.PlaceStatus{}
	}
	st, err := a.Status(background())
	if err != nil {
		t.Fatal(err)
	}
	p := repoPlace(st)
	if p.V1 != 1 || p.Newer != 1 || p.Total != p.V1+p.V2+p.Newer {
		t.Fatalf("before the run: %+v, want v1 1, newer 1, total = v1+v2+newer", p)
	}
	if _, err := run(t, a, opsmigrate.OpRun, `{"no_push":true}`, false); err != nil {
		t.Fatal(err)
	}
	if got := string(mustRead(t, newerPath)); got != newer {
		t.Fatalf("migrate run changed a row written by a newer lore:\n%s", got)
	}
	st, err = a.Status(background())
	if err != nil {
		t.Fatal(err)
	}
	if p = repoPlace(st); p.V1 != 0 || p.Newer != 1 || p.Total != p.V2+p.Newer {
		t.Fatalf("after the run: %+v, want v1 0, newer 1", p)
	}
}
