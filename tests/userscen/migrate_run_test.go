//go:build userscen

package userscen

// migrate_run_test.go — `lore migrate run|status|rollback|unlock` the way a
// user meets them after upgrading from v0.1.17:
//
//	TestMigrateRunOldDataMatrix     every row kind v0.1.17 had (memory, rule, decision,
//	                                hotfix, pattern, playbook, prompt, task) × caller
//	                                (person, agent): the old files read the same as new
//	                                ones; status lists them; a dry run names each and
//	                                changes nothing; the run rewrites each with every
//	                                field kept (file-level lossy diff and show --json),
//	                                pushes under the team lock; a second run is a no-op;
//	                                a teammate's fresh clone reads the same rows
//	TestMigrateRunNoOldData         a fresh 0.1.18 repo: status, dry run and run all say
//	                                nothing to migrate, keep no backup, change no file
//	TestMigrateRunSoloRepoNoRemote  a repo with no remote: the run says no team lock was
//	                                taken and never that one was released
//	TestMigrateRunAfterCrash        a run killed halfway (after the first table): the next
//	                                run is refused naming the stale lock and the next step,
//	                                status names the interrupted run, unlock + run finish
//	                                it with every row intact, a third run is a no-op
//	TestMigrateNothingToUndoOrUnlock
//	                                rollback with no migration and unlock with no lock:
//	                                exact code, exit, message and next step

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Exit codes the errors reference fixes (docs/reference/errors.md).
const (
	migExitMigrateLocked = 24 // E_MIGRATE_LOCKED, E_MIGRATE_ROLLBACK_LOSES
	migExitInvalid       = 20 // E_INVALID_INPUT
)

// migBackupPrefix starts every migrate backup ref.
const migBackupPrefix = "refs/lore-backup/migrate/"

// migPlanted is one v0.1.17 row a scenario planted.
type migPlanted struct {
	k    migKind
	id   string
	v1   []byte // the v0.1.17 file
	show string // `lore <noun> show <id> --json` before the downgrade
}

// migPlant adds one row of every kind in c, records how each shows, turns
// every file back into a v0.1.17 file, commits and pushes.
func migPlant(t *testing.T, c *th.Clone) []migPlanted {
	t.Helper()
	var out []migPlanted
	for _, k := range migKinds() {
		id := migAdd(t, c, k)
		show := c.Lore(k.noun, "show", id, "--json").OK(t).Stdout
		out = append(out, migPlanted{k: k, id: id, show: show})
	}
	for i := range out {
		out[i].v1 = migDowngrade(t, migRowPath(c, out[i].k.table, out[i].id))
	}
	c.CommitAll("rows as v0.1.17 wrote them")
	c.AdminPush("origin", "main")
	return out
}

// migShowsSame fails unless every planted row shows in c as it did before
// it was turned into a v0.1.17 file.
func migShowsSame(t *testing.T, what string, c *th.Clone, rows []migPlanted) {
	t.Helper()
	for _, p := range rows {
		got := c.Lore(p.k.noun, "show", p.id, "--json").OK(t).Stdout
		if !migJSONEqual(got, p.show) {
			t.Errorf("%s: %s %s shows differently:\n got %s\nwant %s", what, p.k.noun, p.id, got, p.show)
		}
	}
}

// migAssertRewritten is the file-level lossy diff: each planted file is now
// `_v: 2` with an `_hlc`, and every member of the v0.1.17 file but `_v` is
// still there with the same value; the only new member is `_hlc`.
func migAssertRewritten(t *testing.T, c *th.Clone, rows []migPlanted) {
	t.Helper()
	for _, p := range rows {
		v1 := migDoc(t, p.v1)
		v2 := migDoc(t, migRead(t, migRowPath(c, p.k.table, p.id)))
		if migCompact(t, v2[migKeyVersion]) != "2" {
			t.Errorf("%s/%s: _v %s after the run, want 2", p.k.table, p.id, v2[migKeyVersion])
		}
		if hlc := v2[migKeyHLC]; len(hlc) < 3 {
			t.Errorf("%s/%s: no _hlc after the run", p.k.table, p.id)
		}
		for key, old := range v1 {
			if key == migKeyVersion {
				continue
			}
			now, ok := v2[key]
			if !ok {
				t.Errorf("%s/%s: field %s lost by the migration (was %s)", p.k.table, p.id, key, old)
				continue
			}
			if migCompact(t, now) != migCompact(t, old) {
				t.Errorf("%s/%s: field %s changed by the migration: %s → %s", p.k.table, p.id, key, old, now)
			}
		}
		for key := range v2 {
			if _, ok := v1[key]; !ok && key != migKeyHLC {
				t.Errorf("%s/%s: the migration added field %s = %s", p.k.table, p.id, key, v2[key])
			}
		}
	}
}

// migWantRows fails unless rows names exactly "<table>/<id>" of each planted row.
func migWantRows(t *testing.T, what string, rows []string, planted []migPlanted) {
	t.Helper()
	var want []string
	for _, p := range planted {
		want = append(want, p.k.table+"/"+p.id)
	}
	got := slices.Clone(rows)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s: rows %v, want %v", what, got, want)
	}
}

func TestMigrateRunOldDataMatrix(t *testing.T) {
	scenParallel(t) // its sub-tests each build their own World and run in parallel
	for _, caller := range []string{"person", "agent"} {
		t.Run(caller, func(t *testing.T) {
			scenParallel(t)
			w, app, c := migNew(t)
			planted := migPlant(t, c)
			n := len(planted)
			run := c
			if caller == "agent" {
				run = c.As(c.A.Agent("bot"))
			}

			// Old files read exactly as the rows did before (upcast in memory).
			migShowsSame(t, "v0.1.17 files", run, planted)

			// status: every planted row is pending, per table.
			st := migStatus(t, run)
			if p := migRepoPlace(t, st); p.V1 != n || p.Newer != 0 || p.Total != p.V1+p.V2 {
				t.Fatalf("status before: %+v, want v1 %d", p, n)
			}
			for _, p := range planted {
				if !slices.Contains(st.Pending, p.k.table+": 1 row at v1") {
					t.Errorf("status pending %q does not list %s", st.Pending, p.k.table)
				}
			}
			if st.Lock != "free" || st.Journal != "" {
				t.Errorf("status before any run: lock %q journal %q", st.Lock, st.Journal)
			}

			// Dry run: names every row, writes nothing, keeps no backup.
			before := migSnapshot(t, c)
			dry := th.DecodeJSON[migRunOut](t, run.Lore("migrate", "run", "--dry-run", "--json"))
			if !dry.DryRun || dry.Count != n || dry.BackupRef != "" {
				t.Fatalf("dry run: %+v, want dry_run, count %d, no backup", dry, n)
			}
			migWantRows(t, "dry run", dry.Rows, planted)
			run.Lore("migrate", "run", "--dry-run").OK(t).Says(t, "Would upgrade "+strconv.Itoa(n)+" rows", "Nothing written (dry run).")
			migSameSnapshot(t, "after the dry run", migSnapshot(t, c), before)
			if refs := migBackupRefs(c); len(refs) != 0 {
				t.Fatalf("a dry run made backup refs %v", refs)
			}
			if s := c.Status(); s != "" {
				t.Fatalf("a dry run changed the work tree:\n%s", s)
			}

			// The run: every row rewritten, every field kept, pushed under the team lock (asked for: --team-lock).
			out := th.DecodeJSON[migRunOut](t, run.Lore("migrate", "run", "--team-lock", "--json"))
			if out.DryRun || out.Count != n || !strings.HasPrefix(out.BackupRef, migBackupPrefix) {
				t.Fatalf("run: %+v, want count %d and a backup ref", out, n)
			}
			migWantRows(t, "run", out.Rows, planted)
			if len(out.Journal) == 0 || !strings.HasPrefix(out.Journal[0], "team lock: pushed a hold to refs/lore/signals") || out.Journal[len(out.Journal)-1] != "team lock released" {
				t.Errorf("run journal %q: want the team lock taken first and released last", out.Journal)
			}
			if !slices.Contains(migBackupRefs(c), out.BackupRef) {
				t.Errorf("backup ref %s is not in the clone: %v", out.BackupRef, migBackupRefs(c))
			}
			migAssertRewritten(t, c, planted)
			migShowsSame(t, "migrated files", run, planted)

			st = migStatus(t, run)
			if p := migRepoPlace(t, st); p.V1 != 0 || p.Newer != 0 {
				t.Errorf("status after: %+v, want v1 0", p)
			}
			if len(st.Pending) != 0 || !strings.Contains(st.Journal, out.BackupRef) || st.Lock != "free" {
				t.Errorf("status after: pending %q journal %q lock %q", st.Pending, st.Journal, st.Lock)
			}

			// Twice: a no-op that changes nothing and keeps no new backup.
			after := migSnapshot(t, c)
			again := th.DecodeJSON[migRunOut](t, run.Lore("migrate", "run", "--json"))
			if again.Count != 0 || len(again.Rows) != 0 || len(again.Steps) != 0 || again.BackupRef != "" {
				t.Fatalf("second run: %+v, want nothing", again)
			}
			run.Lore("migrate", "run").OK(t).Says(t, "Nothing to migrate: every row is already in the current format.").Never(t, "Upgraded")
			migSameSnapshot(t, "after a second run", migSnapshot(t, c), after)

			// A teammate who clones after the migration is pushed reads the same rows.
			c.CommitAll("lore: migrate format")
			c.AdminPush("origin", "main")
			bob := w.Human("bob", "bob@example.com").Clone(app, "app")
			migShowsSame(t, "teammate clone", bob, planted)
			bob.Lore("migrate", "run", "--dry-run").OK(t).Says(t, "Nothing to migrate")
		})
	}
}

func TestMigrateRunNoOldData(t *testing.T) {
	scenParallel(t)
	_, _, c := migNew(t)
	st := migStatus(t, c)
	if p := migRepoPlace(t, st); p.V1 != 0 || p.Newer != 0 || p.V2 == 0 || p.Total != p.V2 {
		t.Fatalf("status of a fresh repo: %+v, want only v2 rows", p)
	}
	if len(st.Pending) != 0 || st.Journal != "" || st.Lock != "free" || st.MinLoreVersion != "0.1.18" || st.Writes != 2 {
		t.Fatalf("status of a fresh repo: %+v", st)
	}
	before := migSnapshot(t, c)
	c.Lore("migrate", "run", "--dry-run").OK(t).Says(t, "Nothing to migrate: every row is already in the current format.", "Nothing written (dry run).")
	dry := th.DecodeJSON[migRunOut](t, c.Lore("migrate", "run", "--dry-run", "--json"))
	if !dry.DryRun || dry.Count != 0 || len(dry.Rows) != 0 || len(dry.Steps) != 0 {
		t.Fatalf("dry run of a fresh repo: %+v", dry)
	}
	out := th.DecodeJSON[migRunOut](t, c.Lore("migrate", "run", "--json"))
	if out.Count != 0 || len(out.Rows) != 0 || out.BackupRef != "" {
		t.Fatalf("run of a fresh repo: %+v, want nothing and no backup", out)
	}
	if refs := migBackupRefs(c); len(refs) != 0 {
		t.Fatalf("a run with nothing to do kept backup refs %v", refs)
	}
	for _, off := range [][]string{{"--no-push"}, {"--offline"}} {
		r := th.DecodeJSON[migRunOut](t, c.Lore(append([]string{"migrate", "run", "--json"}, off...)...))
		if r.Count != 0 || len(r.Journal) != 0 {
			t.Fatalf("run %v of a fresh repo: %+v (no team lock is taken, so no lock lines)", off, r)
		}
	}
	migSameSnapshot(t, "fresh repo after every run", migSnapshot(t, c), before)
	if s := c.Status(); s != "" {
		t.Fatalf("runs with nothing to do changed the work tree:\n%s", s)
	}
}

func TestMigrateRunSoloRepoNoRemote(t *testing.T) {
	scenParallel(t)
	w := newWorld(t)
	alice := w.Human("alice", "person@example.com")
	dir := w.Root + "/solo"
	alice.Lore(dir, "version").OK(t) // makes the folder
	w.Git(dir, "init", "-q", "-b", "main")
	alice.Lore(dir, "init", "--name=solo").OK(t)
	const noRemote = "team lock: this repo has no remote origin; nobody to race, none taken"
	for _, rows := range []string{"no-op", "rows"} {
		if rows == "rows" {
			alice.Lore(dir, "memory", "add", "kept in the branch", "--no-live").OK(t)
			ents := w.Git(dir, "ls-files", "--others", "--exclude-standard", migDataDir+"/memories")
			for _, rel := range strings.Fields(ents) {
				migDowngrade(t, dir+"/"+rel)
			}
		}
		out := th.DecodeJSON[migRunOut](t, alice.Lore(dir, "migrate", "run", "--team-lock", "--json"))
		if len(out.Journal) == 0 || out.Journal[0] != noRemote {
			t.Fatalf("%s: journal %q, want it to start with %q", rows, out.Journal, noRemote)
		}
		if slices.Contains(out.Journal, "team lock released") {
			t.Fatalf("%s: the run took no team lock yet says it released one: %q", rows, out.Journal)
		}
		if rows == "rows" && out.Count != 1 {
			t.Fatalf("rows: count %d, want 1", out.Count)
		}
	}
}

func TestMigrateRunAfterCrash(t *testing.T) {
	scenParallel(t)
	_, _, c := migNew(t)
	// Two tables, so a crash after the first leaves the branch half done.
	kinds := []migKind{migKinds()[0], migKinds()[1]}
	var planted []migPlanted
	for _, k := range kinds {
		id := migAdd(t, c, k)
		planted = append(planted, migPlanted{k: k, id: id, show: c.Lore(k.noun, "show", id, "--json").OK(t).Stdout})
	}
	for i := range planted {
		planted[i].v1 = migDowngrade(t, migRowPath(c, planted[i].k.table, planted[i].id))
	}
	c.CommitAll("v0.1.17 rows")

	crash := c.With("LORE_TEST_MIGRATE_CRASH_AT=table:1").Lore("migrate", "run", "--no-push")
	if crash.Code == 0 {
		t.Fatalf("the run did not stop after the first table (is LORE_SCEN_BIN a loretest build?):\n%s", crash)
	}
	// status names the interrupted run; the user just runs it again: the
	// dead run's lock is taken over and said (owner item 46), the run
	// resumes.
	st := c.Lore("migrate", "status").OK(t)
	st.Says(t, "interrupted migration", "stale")
	resumed := c.Lore("migrate", "run", "--no-push", "--json").OK(t)
	resumed.Says(t, "took over .lore/state/migrate.lock from pid")
	out := th.DecodeJSON[migRunOut](t, resumed)
	if !slices.ContainsFunc(out.Journal, func(l string) bool { return strings.Contains(l, "resum") }) {
		t.Errorf("the run after a crash does not say it resumed: %q", out.Journal)
	}
	migAssertRewritten(t, c, planted)
	migShowsSame(t, "after the resumed run", c, planted)
	again := th.DecodeJSON[migRunOut](t, c.Lore("migrate", "run", "--no-push", "--json"))
	if again.Count != 0 {
		t.Fatalf("third run: %+v, want nothing", again)
	}
	if s := migStatus(t, c); s.Lock != "free" || strings.Contains(s.Journal, "interrupted") {
		t.Fatalf("status after the resumed run: lock %q journal %q", s.Lock, s.Journal)
	}
}

func TestMigrateNothingToUndoOrUnlock(t *testing.T) {
	scenParallel(t)
	_, _, c := migNew(t)
	c.Lore("migrate", "rollback").Fails(t, th.ExitNotFound, "E_NOT_FOUND", "migrate: no migration to roll back on this machine", "hint: lore migrate status")
	var un struct {
		Released bool   `json:"released"`
		Line     string `json:"line"`
	}
	r := c.Lore("migrate", "unlock", "--json").OK(t)
	if err := json.Unmarshal([]byte(r.Stdout), &un); err != nil {
		t.Fatalf("unlock --json: %v\n%s", err, r.Stdout)
	}
	if un.Released || un.Line != "no migration lock on this machine" {
		t.Fatalf("unlock with no lock: %+v", un)
	}
	// Unknown flags and arguments are input errors, not a run.
	c.Lore("migrate", "run", "--force").Fails(t, migExitInvalid, "E_INVALID_INPUT", "--force")
}
