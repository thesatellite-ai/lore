package wire

// migrate_noop_journal_test.go — later 60 (FA-FMT-3): a no-op second
// `lore migrate run` keeps the first run's journal and backup ref
// (Begin/dropBackup only drop what the empty run itself made), so
// `lore migrate rollback` afterwards still restores the v1 bytes.

import (
	"path/filepath"
	"strings"
	"testing"

	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
)

func TestMigrateNoopSecondRunKeepsJournal(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	v1Row(t, repo, schema.TableMemories, journalMemID, `"project_id":"`+projectID(t, a)+`","body":"hello"`)
	path := filepath.Join(repo, ".lore", "data", schema.TableMemories, journalMemID+".json")
	before := string(mustRead(t, path))
	raw, err := run(t, a, opsmigrate.OpRun, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	first := migrateOut(t, raw)
	journal := filepath.Join(repo, filepath.FromSlash(MigrateJournalFile))
	kept := string(mustRead(t, journal))
	migrated := string(mustRead(t, path))

	for i := range 2 { // the second and third run have nothing to do
		raw, err = run(t, a, opsmigrate.OpRun, `{}`, false)
		if err != nil {
			t.Fatalf("no-op run %d: %v", i+2, err)
		}
		if out := migrateOut(t, raw); out.BackupRef != "" {
			t.Fatalf("no-op run %d made a backup: %+v", i+2, out)
		}
		if got := string(mustRead(t, journal)); got != kept {
			t.Fatalf("no-op run %d changed the journal:\n%s\nwant\n%s", i+2, got, kept)
		}
		if snapshots(t).Resolve(background(), repo, first.BackupRef) == "" {
			t.Fatalf("no-op run %d dropped the first run's backup ref %s", i+2, first.BackupRef)
		}
		if got := string(mustRead(t, path)); got != migrated {
			t.Fatalf("no-op run %d rewrote the row", i+2)
		}
	}
	if st, serr := a.Status(background()); serr != nil || !strings.Contains(st.Journal, first.BackupRef) {
		t.Fatalf("status after no-op runs: %+v %v", st, serr)
	}
	if _, err = run(t, a, opsmigrate.OpRollback, `{}`, false); err != nil {
		t.Fatalf("rollback after no-op runs: %v", err)
	}
	if got := string(mustRead(t, path)); got != before {
		t.Fatalf("rollback did not restore the v1 bytes:\n%s\nwant\n%s", got, before)
	}
}
