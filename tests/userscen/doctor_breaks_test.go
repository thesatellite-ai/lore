//go:build userscen

package userscen

// Break one thing doctor claims to detect, read the exact finding and its
// fix text, run the fix, and see doctor clean again (docs/operations/
// doctor.md, backup-restore-repair.md):
//
//   - cache damaged past its first pages: BROKEN (exit 2), "cache <path> is
//     damaged; delete it …"; delete it as told → HEALTHY, rows intact
//   - cache header damaged: the next command moves it to .lore/backups/,
//     says so once, rebuilds; doctor HEALTHY; a second run says nothing
//   - cache deleted: the next command rebuilds it, nothing lost
//   - a row file that is not JSON: BROKEN, the [sync] file error
//     (invalid-file) and the "fix or remove it" error; reads leave it out
//   - conflict markers in a row file: BROKEN, kind conflict-markers,
//     open_conflicts counted
//   - the lore git hook blocks removed: DEGRADED with the "run any lore
//     command" fix; `lore sync` reinstalls them → HEALTHY
//   - the merge driver unset (with LORE_SYNC_GIT=0): a note only; the next
//     command reinstalls it
//   - LORE_SYNC_ALLOW_SECRETS set: DEGRADED with the unset fix
//   - two different lore binaries on PATH: DEGRADED naming both; one
//     binary reached twice (a symlinked PATH folder, merged /usr) is not
//     two binaries
//   - tasks.triage_owner naming nobody: DEGRADED with the policy fix; the
//     fix → HEALTHY

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// damageMiddle overwrites pages in the middle of a SQLite file, past the
// header a plain open reads, so only quick_check sees the damage.
func damageMiddle(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Clean(path), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	const page = 4096
	if st.Size() < 40*page {
		t.Fatalf("cache %s is only %d bytes; the scenario needs a cache with many pages", path, st.Size())
	}
	junk := []byte(strings.Repeat("\xff", 5*page))
	if _, err := f.WriteAt(junk, (st.Size()/page/2)*page); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorBreak_CacheDamagedInsideIsBrokenUntilDeleted(t *testing.T) {
	p := newDoctorPerson(t)
	cache := p.file(".lore/lore-cache.db")
	damageMiddle(t, cache)

	rep, res := p.doctor(t)
	if rep.Status != "broken" || !hasLine(rep.Errors, "cache "+cache+" is damaged; delete it (and its -wal/-shm files): the next lore command rebuilds it from .lore/data, nothing is lost") {
		t.Fatalf("a cache damaged past its header is an error naming the file and the delete fix: %s", res)
	}
	text := p.c.Lore("doctor")
	if text.Code != 2 || !strings.Contains(text.Stdout, "Status: BROKEN") || !strings.Contains(text.Stdout, "Errors:\n  - cache ") {
		t.Fatalf("text report of a damaged cache: %s", text)
	}

	// The fix, exactly as printed.
	for _, s := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(cache + s); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	list := p.c.MustLore("rule", "list")
	if !strings.Contains(list.Stdout, "Run the tests before you push") {
		t.Fatalf("the rebuilt cache has the rule back: %s", list)
	}
	rep, res = p.doctor(t)
	if rep.Status != "healthy" || len(rep.Errors) != 0 {
		t.Fatalf("after the fix doctor is healthy: %s", res)
	}
}

func TestDoctorBreak_CacheHeaderDamagedIsMovedAsideAndRebuilt(t *testing.T) {
	p := newDoctorPerson(t)
	cache := p.file(".lore/lore-cache.db")
	if err := os.WriteFile(cache, []byte(strings.Repeat("garbage!", 64)), 0o600); err != nil {
		t.Fatal(err)
	}
	res := p.c.MustLore("rule", "list")
	if !strings.Contains(res.Stderr, "the cache "+cache+" was damaged; moved it to "+p.file(".lore/backups")+"/lore-cache.db.corrupt-") ||
		!strings.Contains(res.Stderr, "rebuilt it from the row files (nothing lost)") || !strings.Contains(res.Stdout, "Run the tests before you push") {
		t.Fatalf("a damaged cache is moved aside, rebuilt, and said once: %s", res)
	}
	// One moved-aside copy (with its -wal/-shm journal files, if any).
	backups, err := filepath.Glob(p.file(".lore/backups/lore-cache.db.corrupt-*"))
	dbs := 0
	for _, b := range backups {
		if !strings.HasSuffix(b, "-wal") && !strings.HasSuffix(b, "-shm") {
			dbs++
		}
	}
	if err != nil || dbs != 1 {
		t.Fatalf("one moved-aside copy in .lore/backups: %v %v", backups, err)
	}
	rep, out := p.doctor(t)
	if rep.Status != "healthy" {
		t.Fatalf("doctor after the rebuild: %s", out)
	}
	again := p.c.MustLore("rule", "list")
	if strings.Contains(again.Stderr, "damaged") {
		t.Fatalf("the notice is said once, not on every command: %s", again)
	}
	// .lore/backups is ignored by git: the damaged copy never reaches a commit.
	if st := p.c.Status(); strings.Contains(st, "backups") {
		t.Fatalf(".lore/backups shows in git status: %q", st)
	}
}

func TestDoctorBreak_CacheDeletedIsRebuiltTwice(t *testing.T) {
	p := newDoctorPerson(t)
	cache := p.file(".lore/lore-cache.db")
	for round := 1; round <= 2; round++ {
		for _, s := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(cache + s); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		list := p.c.MustLore("rule", "list")
		if !strings.Contains(list.Stdout, "Run the tests before you push") {
			t.Fatalf("round %d: the rule comes back from .lore/data: %s", round, list)
		}
		if _, err := os.Stat(cache); err != nil {
			t.Fatalf("round %d: the cache was rebuilt: %v", round, err)
		}
		if rep, res := p.doctor(t); rep.Status != "healthy" {
			t.Fatalf("round %d: %s", round, res)
		}
	}
}

// ruleFile is the one rule's row file (repo-relative).
func (p *doctorPerson) ruleFile(t *testing.T) string {
	t.Helper()
	m, err := filepath.Glob(p.file(".lore/data/rules/rul_*.json"))
	if err != nil || len(m) != 1 {
		t.Fatalf("one rule file: %v %v", m, err)
	}
	rel, err := filepath.Rel(p.c.Dir, m[0])
	if err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(rel)
}

func TestDoctorBreak_BrokenRowFiles(t *testing.T) {
	scenParallel(t) // its sub-tests each build their own World and run in parallel
	for _, tc := range []struct {
		name, body, kind string
		conflicts        int
	}{
		{"not json", "{broken", "invalid-file", 0},
		{"empty file", "", "invalid-file", 0},
		{"json array", "[1,2]", "invalid-file", 0},
		{"conflict markers", "<<<<<<< HEAD\n{\"a\":1}\n=======\n{\"a\":2}\n>>>>>>> theirs\n", "conflict-markers", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newDoctorPerson(t)
			rel := p.ruleFile(t)
			good, ok := p.c.ReadFile(rel)
			if !ok {
				t.Fatal("rule file unreadable")
			}
			p.c.WriteFile(rel, tc.body)

			rep, res := p.doctor(t)
			s := rep.Sync
			if rep.Status != "broken" || s == nil || len(s.FileErrors) != 1 || s.FileErrors[0].Path != rel || s.FileErrors[0].Kind != tc.kind ||
				s.OpenConflicts != tc.conflicts || !hasLine(rep.Errors, "store repo: "+rel+" is not a usable row file", "fix or remove it (every read leaves it out)") {
				t.Fatalf("broken row file %q: %s", tc.name, res)
			}
			// Reads leave it out, and say so on stderr pointing at doctor.
			list := p.c.MustLore("rule", "list")
			if strings.Contains(list.Stdout, "Run the tests before you push") || !strings.Contains(list.Stderr, rel) || !strings.Contains(list.Stderr, "(lore doctor)") {
				t.Fatalf("a read leaves the broken file out and names it: %s", list)
			}

			// The fix: restore the file (git checkout of the committed copy).
			p.c.WriteFile(rel, good)
			if rep, res = p.doctor(t); rep.Status != "healthy" || len(rep.Sync.FileErrors) != 0 {
				t.Fatalf("after restoring the file: %s", res)
			}
		})
	}
}

func TestDoctorBreak_GitHooksRemovedThenReinstalled(t *testing.T) {
	p := newDoctorPerson(t)
	hooks := p.c.Git("rev-parse", "--path-format=absolute", "--git-path", "hooks")
	for _, h := range []string{"post-commit", "pre-push"} {
		if err := os.Remove(filepath.Join(hooks, h)); err != nil {
			t.Fatal(err)
		}
	}
	// LORE_SYNC_GIT=0: doctor reports what is there without reinstalling.
	off := p.c.With("LORE_SYNC_GIT=0")
	res := off.Lore("doctor", "--json")
	rep := doctorDecode(t, res)
	if rep.Status != "degraded" || !hasLine(rep.Warnings, "live: git hook block missing in post-commit, pre-push", "run any lore command (such as `lore sync`) to install it") {
		t.Fatalf("missing hook blocks: %s", res)
	}
	p.c.MustLore("sync")
	for _, h := range []string{"post-commit", "pre-push"} {
		b, err := os.ReadFile(filepath.Join(hooks, h))
		if err != nil || !strings.Contains(string(b), "lore-sync:live") {
			t.Fatalf("hook %s reinstalled by lore sync: %v %q", h, err, b)
		}
	}
	if rep, res = p.doctor(t); rep.Status != "healthy" {
		t.Fatalf("after the fix: %s", res)
	}
}

func TestDoctorBreak_MergeDriverUnsetIsANote(t *testing.T) {
	p := newDoctorPerson(t)
	p.c.Git("config", "--unset", "merge.lore.driver")
	res := p.c.With("LORE_SYNC_GIT=0").Lore("doctor", "--json")
	rep := doctorDecode(t, res)
	if rep.Status != "healthy" || rep.Sync == nil || rep.Sync.MergeDriverOK ||
		!hasLine(rep.Notes, "lore merge driver not configured in this clone (the next lore command installs it, unless LORE_SYNC_GIT=0)") {
		t.Fatalf("an unset merge driver is a note: %s", res)
	}
	p.c.MustLore("rule", "list")
	if got := p.c.Git("config", "--get", "merge.lore.driver"); !strings.Contains(got, "merge-driver") {
		t.Fatalf("the next command reinstalls the driver: %q", got)
	}
	if rep, res = p.doctor(t); rep.Status != "healthy" || !rep.Sync.MergeDriverOK {
		t.Fatalf("after the fix: %s", res)
	}
}

func TestDoctorBreak_RemovedAllowSecretsEnv(t *testing.T) {
	p := newDoctorPerson(t)
	for _, v := range []string{"1", "true", "0"} {
		res := p.c.With("LORE_SYNC_ALLOW_SECRETS="+v).Lore("doctor", "--json")
		rep := doctorDecode(t, res)
		if rep.Status != "degraded" || !hasLine(rep.Warnings, "LORE_SYNC_ALLOW_SECRETS is set but does nothing since 0.1.18 (the hooks always scan); unset it") {
			t.Fatalf("LORE_SYNC_ALLOW_SECRETS=%s: %s", v, res)
		}
	}
	if rep, res := p.doctor(t); rep.Status != "healthy" {
		t.Fatalf("unset again: %s", res)
	}
}

func TestDoctorBreak_LoreBinariesOnPath(t *testing.T) {
	p := newDoctorPerson(t)
	shim := filepath.Join(p.w.Root, "shim")
	// A second, different lore program in another PATH folder.
	other := filepath.Join(p.w.Root, "old-install")
	if err := os.MkdirAll(other, 0o750); err != nil {
		t.Fatal(err)
	}
	th.WriteExecutable(t, filepath.Join(other, "lore"), "#!/bin/sh\necho old lore\n")
	res := p.c.With("PATH="+other+string(os.PathListSeparator)+p.path).Lore("doctor", "--json")
	rep := doctorDecode(t, res)
	if rep.Status != "degraded" || len(rep.PathConflicts) != 2 ||
		!hasLine(rep.Warnings, "multiple lore binaries on PATH: "+filepath.Join(other, "lore")+", "+filepath.Join(shim, "lore")) {
		t.Fatalf("two lore binaries on PATH: %s", res)
	}

	// The same binary reached through a symlinked folder (merged /usr:
	// /bin → /usr/bin) is one binary, not two.
	alias := filepath.Join(p.w.Root, "shim-alias")
	if err := os.Symlink(shim, alias); err != nil {
		t.Fatal(err)
	}
	res = p.c.With("PATH="+alias+string(os.PathListSeparator)+p.path).Lore("doctor", "--json")
	rep = doctorDecode(t, res)
	if rep.Status != "healthy" || len(rep.PathConflicts) != 0 {
		t.Fatalf("one binary reached through two PATH folders is not a conflict: %s", res)
	}
}

func TestDoctorBreak_TriageOwnerNamesNobody(t *testing.T) {
	p := newDoctorPerson(t)
	p.c.MustLore("policy", "set", "tasks.triage_owner", "@nobody-here", "--yes")
	rep, res := p.doctor(t)
	if rep.Status != "degraded" || !hasLine(rep.Warnings,
		"triage_owner: tasks.triage_owner is @nobody-here, which matches no actor; no inbox lists proposed tasks; fix: lore policy set tasks.triage_owner <id or @handle> --to team") {
		t.Fatalf("an unknown triage owner: %s", res)
	}
	// The fix names an actor by id (whoami's actor_id), as the hint says.
	var who struct {
		Data struct {
			Identity struct {
				ActorID string `json:"actor_id"`
			} `json:"identity"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(p.c.MustLore("whoami", "--json").Stdout), &who); err != nil || who.Data.Identity.ActorID == "" {
		t.Fatalf("whoami --json actor_id: %v", err)
	}
	p.c.MustLore("policy", "set", "tasks.triage_owner", who.Data.Identity.ActorID, "--yes")
	if rep, res = p.doctor(t); hasLine(rep.Warnings, "triage_owner") {
		t.Fatalf("a triage owner naming this person is silent: %s", res)
	}
}
