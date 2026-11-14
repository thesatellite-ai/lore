package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
)

// TestRowFormatV1FromAPreMigrationBranch (devco K-3): v1 row files that
// were last written before the repo's migration (a branch made before it,
// rebased, merged or cherry-picked onto it) are named as such, with lore
// fix --migrate as the fix, never "a lore older than 0.1.18 still writes
// here". One v1 file written after the migration brings that warning back.
func TestRowFormatV1FromAPreMigrationBranch(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	if _, err := run(t, a, opsmigrate.OpRun, `{}`, false); err != nil {
		t.Fatal(err)
	}
	// the branch's file: written on 2026-11-04 (v1Row), before the migration
	v1Row(t, repo, schema.TableMemories, commitLineMemB, `"project_id":"`+projectID(t, a)+`","body":"two"`)
	a.wrote(nil)
	meta := filepath.Join(repo, filepath.FromSlash(rowfs.RepoMetaPath))
	if err := os.WriteFile(meta, []byte(`{"_v": 1, "features": ["hlc"], "format": 2, "migrated_at": "2026-11-10T09:00:00Z", "min_lore_version": "0.1.18", "project_id": "`+projectID(t, a)+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := a.DoctorRowFormat(background())
	if err != nil || len(got) != 1 || got[0].Fix != rowFormatBranchFix || !strings.Contains(got[0].Message, "branch made before it") || strings.Contains(got[0].Message, "older than 0.1.18 still writes") {
		t.Fatalf("v1 rows from a pre-migration branch: %+v %v", got, err)
	}
	// a v1 file stamped after the migration: an old lore still writes here
	dir := filepath.Join(repo, ".lore", "data", string(schema.TableMemories))
	late := `{"_table":"memories","_v":1,"created_at":"2026-11-12T08:00:00Z","id":"` + commitLineMemA + `","project_id":"` + projectID(t, a) + `","body":"late","updated_at":"2026-11-12T08:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, commitLineMemA+".json"), []byte(late), 0o600); err != nil {
		t.Fatal(err)
	}
	a.wrote(nil)
	got, err = a.DoctorRowFormat(background())
	if err != nil || len(got) != 1 || got[0].Fix != rowFormatAfterFix {
		t.Fatalf("a v1 file written after the migration: %+v %v", got, err)
	}
}
