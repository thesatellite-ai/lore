package wire

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/model/errs"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
)

// journalMemID is a fixture memory id.
const journalMemID = "mem_01a1a59b891c70b789144cd3e711fd01"

// TestMigrateBackupRollbackLocal (FQ5, 7.6 before push): a run writes the
// backup ref and journal; rollback restores the v1 file byte for byte; a
// second rollback has nothing to undo; a run that changes nothing keeps no
// backup.
func TestMigrateBackupRollbackLocal(t *testing.T) {
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
	out := migrateOut(t, raw)
	if !strings.HasPrefix(out.BackupRef, "refs/lore-backup/migrate/") || len(out.Journal) == 0 {
		t.Fatalf("run: %+v", out)
	}
	if snapshots(t).Resolve(background(), repo, out.BackupRef) == "" {
		t.Fatalf("backup ref %s missing", out.BackupRef)
	}
	if !strings.Contains(string(mustRead(t, path)), `"_v": 2`) {
		t.Fatal("the run did not rewrite the row")
	}
	st, err := a.Status(background())
	if err != nil || !strings.Contains(st.Journal, out.BackupRef) || st.Lock != "free" {
		t.Fatalf("status: %+v %v", st, err)
	}

	if _, err = run(t, a, opsmigrate.OpRollback, `{}`, false); err != nil {
		t.Fatal(err)
	}
	if got := string(mustRead(t, path)); got != before {
		t.Fatalf("rollback did not restore the bytes:\n%s\nwant\n%s", got, before)
	}
	if _, err = run(t, a, opsmigrate.OpRollback, `{}`, false); !errs.Has(err, errs.NotFound) {
		t.Fatalf("second rollback: want E_NOT_FOUND, got %v", err)
	}
}

// TestMigrateRollbackLoses: a migrated row edited after the run blocks the
// rollback (E_MIGRATE_ROLLBACK_LOSES naming the file), and nothing changes.
func TestMigrateRollbackLoses(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	v1Row(t, repo, schema.TableMemories, journalMemID, `"project_id":"`+projectID(t, a)+`","body":"hello"`)
	if _, err := run(t, a, opsmigrate.OpRun, `{}`, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, ".lore", "data", schema.TableMemories, journalMemID+".json")
	edited := strings.Replace(string(mustRead(t, path)), "hello", "hello again", 1)
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, a, opsmigrate.OpRollback, `{}`, false)
	if !errs.Has(err, errs.MigrateRollbackLoses) || !strings.Contains(err.Error(), journalMemID) {
		t.Fatalf("want E_MIGRATE_ROLLBACK_LOSES naming the row, got %v", err)
	}
	if string(mustRead(t, path)) != edited {
		t.Fatal("a refused rollback changed the file")
	}
}

// TestMigrateNothingKeepsNoBackup: a run with nothing to migrate leaves no
// backup ref and no journal (so a second run is a no-op); its output keeps
// only the team-lock line (OV38: the run says no team lock guarded it, a
// no-op run included; without --team-lock that is the machine-lock-only
// line, A-7).
func TestMigrateNothingKeepsNoBackup(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	raw, err := run(t, a, opsmigrate.OpRun, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if out := migrateOut(t, raw); out.BackupRef != "" || len(out.Journal) != 1 || !strings.HasPrefix(out.Journal[0], "team lock: none (this machine's migration lock only") {
		t.Fatalf("empty run kept a backup: %+v", out)
	}
	if ref := git.BackupRefPrefix + a.opts.Wall().Local().Format(backupDateLayout); snapshots(t).Resolve(background(), repo, ref) != "" {
		t.Fatalf("backup ref %s left", ref)
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(MigrateJournalFile))); !os.IsNotExist(err) {
		t.Fatalf("journal left: %v", err)
	}
}

// TestMigrateUnlock (7.2): a lock whose process is gone is released; a lock
// held by a running process (this test) is E_MIGRATE_LOCKED.
func TestMigrateUnlock(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	lock := filepath.Join(repo, filepath.FromSlash(MigrateLockFile))
	if err := os.MkdirAll(filepath.Dir(lock), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Unlock(background()); !errs.Has(err, errs.MigrateLocked) {
		t.Fatalf("live holder: want E_MIGRATE_LOCKED, got %v", err)
	}
	if err := os.WriteFile(lock, []byte("2147483646"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := a.Unlock(background())
	if err != nil || !out.Released {
		t.Fatalf("stale lock: %+v %v", out, err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatal("the stale lock is still there")
	}
}

// snapshots is the git backup helper.
func snapshots(t *testing.T) git.Snapshots {
	t.Helper()
	s, err := git.NewSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	return s
}
