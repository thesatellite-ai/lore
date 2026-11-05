//go:build teame2e

package teame2e

// team_e2e_syncport_test.go — B11 ports of the sync_e2e_test.go behaviours
// that stay in 0.1.18, run against LORE_TEAM_BIN (cmd/lore): row files are
// the truth (STORE_SERVER_SPEC 3), so what git does to .lore/data (clone,
// checkout, merge, revert, stash, a second worktree) is what the next
// command sees, with no lore.db to export or import. The parts of the old
// tests that checked lore.db, the v0.1.17 sync engine or the repo git
// wiring are not here (fates in /space/tmp/lore-core/p3/b11-fates.md).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// syncSolo is one developer's clone of acme/app with lore initialised,
// committed and pushed.
type syncSolo struct {
	w     *th.World
	app   *th.Remote
	alice *th.Actor
	c     *th.Clone
}

func newSyncSolo(t *testing.T) *syncSolo {
	t.Helper()
	w := newTeamWorld(t)
	s := &syncSolo{w: w, app: w.Remote(slugApp)}
	s.alice = w.Human("alice", "alice@acme.example")
	s.c = s.alice.Clone(s.app, "app")
	s.c.MustLore("init", "--non-interactive", "--name=app")
	// A second command before the commit, as the old tests did: on
	// cmd/lore init itself does not write .gitignore yet (NOTE to
	// p3-modeb, test kept in fixall/b11-sync/init-gitignore.diff).
	s.c.MustLore("rule", "list")
	s.c.CommitAll("lore")
	s.c.AdminPush("origin", "main")
	return s
}

// addRule adds a rule and returns its id.
func addSyncRule(t *testing.T, c *th.Clone, body string) string {
	t.Helper()
	return mustID(t, c.MustLore("rule", "add", "--body", body), "rul")
}

// ruleFile is a rule's row file path relative to the clone.
func ruleFile(id string) string { return dataDir + "/rules/" + id + ".json" }

// TestB11Sync_FreshCloneSeesCommittedRows (TestE2E_InitBootstrapFreshCloneAdopt,
// TestE2E_PostMergeHookImports): a fresh clone ships no cache, lists the
// committed rule at once, and reading it leaves the shared files clean; a
// row pulled later shows on the next command.
func TestB11Sync_FreshCloneSeesCommittedRows(t *testing.T) {
	s := newSyncSolo(t)
	if _, ok := s.c.ReadFile(dataDir + "/" + metaFile); !ok {
		t.Fatal("init must write .lore/data/_meta.json")
	}
	id := addSyncRule(t, s.c, "use JWT for auth")
	for _, f := range []string{".lore/lore.db", ".lore/lore-cache.db", ".lore/state/"} {
		if st := s.c.Git("status", "--porcelain", "--", f); st != "" {
			t.Fatalf("%s must stay out of git: %q", f, st)
		}
	}
	s.c.CommitAll("rule")
	s.c.AdminPush("origin", "main")
	bob := s.w.Human("bob", "bob@acme.example").Clone(s.app, "app")
	for _, f := range []string{".lore/lore.db", ".lore/lore-cache.db"} {
		if _, err := os.Stat(filepath.Join(bob.Dir, filepath.FromSlash(f))); err == nil {
			t.Fatalf("a clone must not ship %s", f)
		}
	}
	bob.MustLore("rule", "list").Says(t, "use JWT for auth")
	before, _ := s.c.ReadFile(ruleFile(id))
	if after, _ := bob.ReadFile(ruleFile(id)); after != before {
		t.Fatal("reading a fresh clone changed the row file")
	}
	if st := bob.Git("status", "--porcelain", "--", dataDir); st != "" {
		t.Fatalf("a fresh clone must not dirty shared files: %s", st)
	}
	addSyncRule(t, s.c, "pulled later")
	s.c.CommitAll("second")
	s.c.AdminPush("origin", "main")
	bob.Git("pull", "-q")
	bob.MustLore("rule", "list").Says(t, "pulled later")
}

// TestB11Sync_BranchesFollowAndMerge (TestE2E_BranchesFollowAndMerge,
// TestE2E_PeekOtherBranch first half): rows follow the checkout; a
// feature branch's rule is not seen on main until it is merged, then both
// sides' rules are.
func TestB11Sync_BranchesFollowAndMerge(t *testing.T) {
	s := newSyncSolo(t)
	c := s.c
	c.Git("checkout", "-q", "-b", "feature/b")
	addSyncRule(t, c, "feature knowledge")
	c.CommitAll("feature")
	c.Git("checkout", "-q", "main")
	c.MustLore("rule", "list").Never(t, "feature knowledge")
	addSyncRule(t, c, "main knowledge")
	c.CommitAll("main")
	c.Git("merge", "-q", "--no-edit", "feature/b")
	c.MustLore("rule", "list").Says(t, "feature knowledge", "main knowledge")
}

// TestB11Sync_InitOnCloneAdopts (TestE2E_InitOnCloneAdopts): init in a
// clone that already holds a project keeps that project; the new name is
// not created.
func TestB11Sync_InitOnCloneAdopts(t *testing.T) {
	s := newSyncSolo(t)
	bob := s.w.Human("bob", "bob@acme.example").Clone(s.app, "app")
	bob.Lore("init", "--non-interactive", "--name=other")
	bob.MustLore("project", "list").Says(t, "app").Never(t, "other")
}

// TestB11Sync_RevertRemovesRow (TestE2E_RevertTrashRestore, core part):
// git revert of the commit that added a rule removes it from the next
// listing; checking the file out again brings it back (the git way the old
// test's refusal named).
func TestB11Sync_RevertRemovesRow(t *testing.T) {
	s := newSyncSolo(t)
	c := s.c
	id := addSyncRule(t, c, "temporary rule")
	c.CommitAll("add rule")
	c.Git("revert", "--no-edit", "HEAD")
	c.MustLore("rule", "list").Never(t, "temporary rule", id)
	c.Git("checkout", "HEAD~1", "--", ruleFile(id))
	c.MustLore("rule", "list").Says(t, "temporary rule")
}

// TestB11Sync_ReadOnlySeesHandEdits (TestE2E_ReadOnly): a hand edit of a
// row file shows under LORE_READ_ONLY=1, and a write is refused there.
func TestB11Sync_ReadOnlySeesHandEdits(t *testing.T) {
	s := newSyncSolo(t)
	id := addSyncRule(t, s.c, "x")
	body, _ := s.c.ReadFile(ruleFile(id))
	s.c.WriteFile(ruleFile(id), strings.Replace(body, `"body": "x"`, `"body": "edited in file"`, 1))
	if b, _ := s.c.ReadFile(ruleFile(id)); !strings.Contains(b, "edited in file") {
		t.Fatalf("setup: hand edit not applied: %s", b)
	}
	ro := s.c.With("LORE_READ_ONLY=1")
	ro.MustLore("rule", "list").Says(t, "edited in file")
	if r := ro.Lore("rule", "add", "--body", "nope"); r.Code == 0 {
		t.Fatalf("a write must be refused under LORE_READ_ONLY=1: %s", r)
	}
	s.c.MustLore("rule", "list").Never(t, "nope")
}

// TestB11Sync_WorktreeAndStash (TestE2E_Worktree, TestE2E_StashRoundTrip):
// a second worktree sees the committed rows and keeps its own new ones;
// stashing an uncommitted row hides it and popping brings it back.
func TestB11Sync_WorktreeAndStash(t *testing.T) {
	s := newSyncSolo(t)
	c := s.c
	addSyncRule(t, c, "in main worktree")
	c.CommitAll("base")
	wt := filepath.Join(filepath.Dir(c.Dir), "wt")
	c.Git("worktree", "add", "-q", "-b", "wt-branch", wt)
	other := s.alice.OpenClone(s.app, wt)
	other.MustLore("rule", "list").Says(t, "in main worktree")
	addSyncRule(t, other, "only in worktree")
	c.MustLore("rule", "list").Never(t, "only in worktree")

	addSyncRule(t, c, "work in progress")
	c.Git("stash", "-u", "-q")
	c.MustLore("rule", "list").Never(t, "work in progress")
	c.Git("stash", "pop", "-q")
	c.MustLore("rule", "list").Says(t, "work in progress")
}

// TestB11Sync_DeletedCacheRebuilt (TestE2E_DeletedDBRebuilt, core part):
// deleting the repo cache loses nothing: the next command rebuilds it from
// the row files.
func TestB11Sync_DeletedCacheRebuilt(t *testing.T) {
	s := newSyncSolo(t)
	addSyncRule(t, s.c, "survives")
	for _, f := range []string{"lore.db", "lore.db-wal", "lore.db-shm", "lore-cache.db", "lore-cache.db-wal", "lore-cache.db-shm"} {
		_ = os.Remove(filepath.Join(s.c.Dir, ".lore", f)) // any of them may not exist
	}
	s.c.MustLore("rule", "list").Says(t, "survives")
}

// TestB11Sync_ConcurrentWriters (TestE2E_ConcurrentWriters): two writers
// adding rules at the same instant in one checkout all succeed, one file
// per rule.
func TestB11Sync_ConcurrentWriters(t *testing.T) {
	s := newSyncSolo(t)
	const perWriter = 6
	var ps []*th.Pending
	for _, p := range []string{"A", "B"} {
		for i := range perWriter {
			ps = append(ps, s.c.Prepare("rule", "add", "--body", fmt.Sprintf("%s-%d", p, i)))
		}
	}
	for _, r := range s.w.Simultaneously(ps...) {
		if r.Code != 0 {
			t.Errorf("concurrent write failed: %s", r)
		}
	}
	entries, err := os.ReadDir(filepath.Join(s.c.Dir, ".lore", "data", "rules"))
	if err != nil || len(entries) != 2*perWriter {
		t.Fatalf("want %d rule files, got %d (%v)", 2*perWriter, len(entries), err)
	}
}

// TestB11Sync_SecretNotWritten (TestE2E_SecretNotExported): a key-shaped
// body is refused and no row file is written; v0.1.17's --allow-secrets is
// unknown; --not-a-secret with a reason writes the row and its allow row
// (FB-SEC-1: a bare --yes is no choice).
func TestB11Sync_SecretNotWritten(t *testing.T) {
	s := newSyncSolo(t)
	key := "AKIA" + strings.Repeat("Q", 16) // built at run time: no key-shaped literal in source
	body := "aws key " + key
	r := s.c.Lore("rule", "add", "--body", body)
	if r.Code == 0 || (!strings.Contains(r.Out(), "E_SECRET_DETECTED") && !strings.Contains(r.Out(), "E_NEEDS_CONFIRM")) {
		t.Fatalf("a secret must be refused: %s", r)
	}
	if m, _ := filepath.Glob(filepath.Join(s.c.Dir, ".lore", "data", "rules", "*.json")); len(m) != 0 {
		t.Fatalf("a refused secret left a row file: %v", m)
	}
	if r := s.c.Lore("rule", "add", "--allow-secrets", "--body", body); r.Code == 0 || !strings.Contains(r.Out(), "unknown flag") {
		t.Fatalf("--allow-secrets is gone: %s", r)
	}
	id := mustID(t, s.c.MustLore("rule", "add", "--not-a-secret", "--reason", "test fixture key", "--yes", "--body", body), "rul")
	if b, ok := s.c.ReadFile(ruleFile(id)); !ok || !strings.Contains(b, key) {
		t.Fatal("the confirmed row was not written")
	}
	if m, _ := filepath.Glob(filepath.Join(s.c.Dir, ".lore", "data", "secret_allows", "*.json")); len(m) == 0 {
		t.Fatal("the confirmation left no secret_allows row")
	}
}

// TestB11Sync_InitGuards (TestE2E_InitRefusesOnlyRealProject,
// TestE2E_InitRespectsBroaderIgnore): a .lore/state folder alone is no
// project (init runs), a second init is refused, and a .gitignore that
// already ignores .lore/ is left as it is.
func TestB11Sync_InitGuards(t *testing.T) {
	w := newTeamWorld(t)
	app := w.Remote(slugApp)
	alice := w.Human("alice", "alice@acme.example")
	c := alice.Clone(app, "app")
	if err := os.MkdirAll(filepath.Join(c.Dir, ".lore", "state"), 0o750); err != nil {
		t.Fatal(err)
	}
	c.MustLore("init", "--non-interactive", "--name=app")
	if r := c.Lore("init", "--non-interactive", "--name=again"); r.Code == 0 || !strings.Contains(r.Out(), "E_ALREADY_INITIALIZED") {
		t.Fatalf("a second init must be refused: %s", r)
	}

	d := alice.Clone(app, "app2")
	d.WriteFile(".gitignore", ".lore/\n")
	d.MustLore("init", "--non-interactive", "--name=app")
	if got, _ := d.ReadFile(".gitignore"); got != ".lore/\n" {
		t.Fatalf("a broader ignore must be left alone: %q", got)
	}
}

// TestB11Sync_ShowWithOptionalTimesUnset (TestE2E_ShowCommandsWithOptionalTimesUnset):
// memory show prints a memory with no valid_until and no such line.
func TestB11Sync_ShowWithOptionalTimesUnset(t *testing.T) {
	s := newSyncSolo(t)
	id := mustID(t, s.c.MustLore("memory", "add", "--body", "no valid-until on this one"), "mem")
	s.c.MustLore("memory", "show", id).Says(t, "no valid-until on this one").Never(t, "valid_until", "0001-01-01")
}

// TestB11Sync_MissionJSONForScripts (TestE2E_MissionJSONForScripts): the
// --json shapes scripts read: mission add / tasklist add give the id,
// mission show gives title, status, target date and its tasks with due
// dates as dates; task list prints full ids; a bad date is refused.
func TestB11Sync_MissionJSONForScripts(t *testing.T) {
	s := newSyncSolo(t)
	c := s.c
	type idOut struct {
		Data struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"data"`
	}
	m := th.DecodeJSON[idOut](t, c.MustLore("mission", "add", "Ship v0.2", "--target=2026-06-15", "--json"))
	if !strings.HasPrefix(m.Data.ID, "msn_") || m.Data.Title != "Ship v0.2" {
		t.Fatalf("mission add --json: %+v", m)
	}
	tl := th.DecodeJSON[idOut](t, c.MustLore("tasklist", "add", "--title=Sprint", "--body=current sprint", "--json"))
	if !strings.HasPrefix(tl.Data.ID, "tlt_") {
		t.Fatalf("tasklist add --json: %+v", tl)
	}
	c.MustLore("task", "add", "first", "--tasklist="+tl.Data.ID, "--mission="+m.Data.ID, "--due=2026-06-01")
	c.MustLore("task", "add", "second", "--tasklist="+tl.Data.ID, "--mission="+m.Data.ID)
	type shown struct {
		Data struct {
			Title      string `json:"title"`
			Status     string `json:"status"`
			TargetDate string `json:"target_date"`
			Tasks      []struct {
				Status string `json:"status"`
				DueAt  string `json:"due_at"`
			} `json:"tasks"`
		} `json:"data"`
	}
	sh := th.DecodeJSON[shown](t, c.MustLore("mission", "show", m.Data.ID, "--json"))
	if sh.Data.Title != "Ship v0.2" || sh.Data.Status == "" || !strings.HasPrefix(sh.Data.TargetDate, "2026-06-15") || len(sh.Data.Tasks) != 2 {
		t.Fatalf("mission show --json: %+v", sh.Data)
	}
	due := ""
	for _, task := range sh.Data.Tasks {
		if task.DueAt != "" {
			due = task.DueAt
		}
	}
	if due != "2026-06-01" {
		t.Fatalf("a task's due date is a date: %+v", sh.Data.Tasks)
	}
	c.MustLore("task", "list").Says(t, "tsk_").Never(t, "T-tsk_")
	if r := c.Lore("mission", "add", "bad date", "--target=15/06/2026"); r.Code == 0 {
		t.Fatalf("a bad date must be refused: %s", r)
	}
}

// TestB11Sync_InitIgnoresCacheAtOnce: `lore init && git add -A && git
// commit` must commit no cache, lock or state file (v0.1.17 init wrote
// .gitignore itself; STORE_SERVER_SPEC 7.4).
func TestB11Sync_InitIgnoresCacheAtOnce(t *testing.T) {
	w := newTeamWorld(t)
	c := w.Human("alice", "alice@acme.example").Clone(w.Remote(slugApp), "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	c.Git("add", "-A")
	staged := c.Git("diff", "--cached", "--name-only")
	for _, bad := range []string{".lore/lore-cache.db", ".lore/lock", ".lore/state/", ".lore/lore.db"} {
		if strings.Contains(staged, bad) {
			t.Fatalf("init left %s for git to commit:\n%s", bad, staged)
		}
	}
}
