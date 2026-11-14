package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
)

// TestV017ModeBMigrateStatus: in a repo still on the v0.1.17 shared
// database (Mode B), `lore migrate status` refused E_UNSUPPORTED like
// every other command, so the one command that says where the repo stands
// could not answer. It now reports, with a note naming the retired mode;
// other commands still refuse.
func TestV017ModeBMigrateStatus(t *testing.T) {
	t.Parallel()
	repo, _, _ := v017ModeBRepo(t, "lore.yaml", "db_path: SHARED/lore.db\nproject_id: "+v17ProjA+"\n")
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	app, err := New(Options{RepoDir: repo, Home: t.TempDir(), Actor: actor, ModeB: V017ModeB{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() }) // test teardown
	out, err := run(t, app, opsmigrate.OpStatus, `{}`, false)
	if err != nil {
		t.Fatalf("migrate status in a Mode B repo: %v", err)
	}
	if !strings.Contains(out, "shared-database mode") {
		t.Errorf("migrate status does not name the retired mode:\n%s", out)
	}
	if _, err := run(t, app, opsmemory.OpList, `{}`, false); errs.CodeOf(err) != errs.Unsupported {
		t.Errorf("memory list in a Mode B repo: %v, want E_UNSUPPORTED", err)
	}
}
