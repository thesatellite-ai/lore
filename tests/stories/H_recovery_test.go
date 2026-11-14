//go:build stories && !windows

package stories

// H_recovery_test.go — story H (tests/stories/H-recovery.md): things going
// wrong. Damaged row files, history rewritten by someone else, store remotes
// that vanish or move, and the way back from each. Every damage must be
// named with its file, its cause and the next step, nothing else may be
// lost, and no remote may move unless a person pushed or synced.
//
// The damage-by-damage detail of a fresh project is the journey
// TestJourneyDamagedLoreFiles; this story replays the track's own mix: the
// damages the journey does not walk (an unreadable file, conflict markers,
// a cut-short file, a duplicate id) on a team repo, and the deleted
// .lore / _meta.json / byte order mark on a project still on the lore
// 0.1.17 format, which must stay on it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Placeholders of story H: made-up people, org and repos.
const (
	hLeadName = "Hana Ostrowska"
	hLeadMail = "hana@recovery.example.test"
	hDevName  = "Dev Okafor"
	hDevMail  = "dev.okafor@recovery.example.test"
)

func TestStoryHCorruption(t *testing.T) {
	s := New(t, "H")
	lib := s.Seed(s.Remote("github.com/recovery-co/gql-lib"), func(dir string) { V017Repo(t, dir, "gql-lib", SmallShape) })
	tool := s.Seed(s.Remote("github.com/recovery-co/dev-tool"), func(dir string) { PlainRepo(t, dir) })
	brick := s.Seed(s.Remote("github.com/recovery-co/site"), func(dir string) { PlainRepo(t, dir) })
	dev := s.Person(hDevName, hDevMail)
	var cl, ct, cb *teamharness.Clone
	var mems []string
	var libRefs, brickRefs, brickTree string

	s.Step("day1/clones-and-a-team-project", func() {
		cl, ct, cb = dev.Clone(lib, "gql-lib"), dev.Clone(tool, "dev-tool"), dev.Clone(brick, "site")
		libRefs, brickRefs, brickTree = s.RefsOf(lib), s.RefsOf(brick), TreeOf(t, cb.Dir)
		dev.LoreIn(ct, "init").OK(t)
		for _, b := range []string{"the cache warms on boot", "retries back off twice", "the queue drains at night"} {
			dev.LoreIn(ct, "memory", "add", "--body", b).OK(t)
		}
		ct.CommitAll("lore: adopt")
		ct.Git("push", "-q", "origin", "HEAD:main")
		mems = hRows(t, ct.Dir, "memories")
		if len(mems) != 3 {
			t.Fatalf("want 3 memory files, got %v", mems)
		}
	})

	s.Step("day2/unreadable-row-names-permission-and-chmod", func() {
		s.AdvanceDays(1)
		f, id := mems[0], hID(mems[0])
		if err := os.Chmod(f, 0); err != nil {
			t.Fatal(err)
		}
		r := dev.LoreIn(ct, "memory", "show", "--id", id).Refused(t, id+".json", "permission denied", "chmod u+rw")
		r.Never(t, "internal error")
		dev.LoreIn(ct, "doctor").Never(t, "HEALTHY")
		if err := os.Chmod(f, 0o644); err != nil {
			t.Fatal(err)
		}
		dev.LoreIn(ct, "memory", "show", "--id", id).OK(t).Says(t, "the cache warms on boot")
	})

	s.Step("day3/conflict-markers-are-called-a-conflict", func() {
		s.AdvanceDays(1)
		f, id := mems[1], hID(mems[1])
		good := hRead(t, f)
		hWrite(t, f, "<<<<<<< HEAD\n"+good+"=======\n"+good+">>>>>>> theirs\n")
		dev.LoreIn(ct, "memory", "show", "--id", id).Refused(t, id+".json", "unresolved git merge conflict", "lore sync conflicts")
		hCommitRefused(t, ct, "conflict")
		hWrite(t, f, good)
	})

	s.Step("day4/cut-short-file-is-named-with-its-fix", func() {
		s.AdvanceDays(1)
		f, id := mems[2], hID(mems[2])
		good := hRead(t, f)
		hWrite(t, f, good[:len(good)/2])
		dev.LoreIn(ct, "memory", "show", "--id", id).Refused(t, id+".json", "not valid JSON", "checkout").Never(t, "internal error")
		// D-115: the other reads go on and never call the damage a refused hand edit
		dev.LoreIn(ct, "memory", "list").Says(t, "the cache warms on boot").Never(t, "hand edit refused")
		hCommitRefused(t, ct, "invalid JSON")
		hWrite(t, f, good)
	})

	s.Step("day5/duplicate-id-is-named-and-not-committed", func() {
		s.AdvanceDays(1)
		a, b := mems[0], mems[1]
		goodB := hRead(t, b)
		hWrite(t, b, hRead(t, a)) // b now holds a's row: a copy-paste of a file
		dev.LoreIn(ct, "memory", "show", "--id", hID(b)).Refused(t, hID(b)+".json", "a duplicate id")
		hCommitRefused(t, ct, "but its file name says "+hID(b))
		hWrite(t, b, goodB)
	})

	s.Step("day6/deleted-lore-folder-in-an-old-format-project", func() {
		s.AdvanceDays(1)
		if err := os.RemoveAll(filepath.Join(cl.Dir, ".lore")); err != nil {
			t.Fatal(err)
		}
		dev.LoreIn(cl, "task", "list").Refused(t, "is deleted from the work tree", "checkout HEAD -- .lore").Never(t, "sparse", "run `lore init`", "lore init sets")
		dev.LoreIn(cl, "doctor").Never(t, "HEALTHY")
		cl.Git("checkout", "HEAD", "--", ".lore")
	})

	s.Step("day7/deleted-meta-is-flagged-and-put-back", func() {
		meta := filepath.Join(cl.Dir, ".lore", "data", "_meta.json")
		if err := os.Remove(meta); err != nil {
			t.Fatal(err)
		}
		dev.LoreIn(cl, "doctor").Says(t, "_meta.json is deleted", "checkout HEAD -- .lore/data/_meta.json").Never(t, "HEALTHY")
		dev.LoreIn(cl, "doctor", "--fix", "--yes")
		if _, err := os.Stat(meta); err != nil {
			t.Fatalf("doctor --fix did not put _meta.json back: %v", err)
		}
	})

	s.Step("day8/notepad-bom-is-read-and-stripped", func() {
		f := hRows(t, cl.Dir, "memories")[0]
		good := hRead(t, f)
		hWrite(t, f, "\xef\xbb\xbf"+good)
		dev.LoreIn(cl, "memory", "show", "--id", hID(f)).OK(t)
		dev.LoreIn(cl, "doctor", "--fix", "--yes")
		if got := hRead(t, f); strings.HasPrefix(got, "\xef\xbb\xbf") {
			t.Errorf("doctor --fix left the byte order mark in %s", hRel(t, cl.Dir, f))
		}
		hWrite(t, f, good)
	})

	s.Step("after/nothing-lost-and-the-rules-hold", func() {
		list := dev.LoreIn(ct, "memory", "list").OK(t)
		list.Says(t, "the cache warms on boot", "retries back off twice", "the queue drains at night")
		if st := ct.Status(); st != "" {
			t.Errorf("the repaired project has changes left over:\n%s", st)
		}
		if bad := NewFormatFiles(t, cl.Dir); len(bad) != 0 {
			t.Errorf("repairs raised the 0.1.17 project's format: %v", bad)
		}
		s.Untouched(t, cb.Dir, brickTree, "every repair in the other repos")
		s.NoPushSince(t, lib, libRefs, "reads, doctor and doctor --fix")
		s.NoPushSince(t, brick, brickRefs, "reads, doctor and doctor --fix")
	})
}

func TestStoryHRewrites(t *testing.T) {
	s := New(t, "H")
	app := s.Seed(s.Remote("github.com/recovery-co/api"), func(dir string) { PlainRepo(t, dir) })
	store := s.Remote("github.com/recovery-co/team-notes")
	lead := s.Person(hLeadName, hLeadMail)
	dev := s.Person(hDevName, hDevMail)
	var la, da *teamharness.Clone
	var gone string

	s.Step("day1/a-repo-with-notes-on-two-laptops", func() {
		la = lead.Clone(app, "api")
		lead.LoreIn(la, "init").OK(t)
		lead.LoreIn(la, "memory", "add", "--body", "the api pages at 100 rows").OK(t)
		lead.LoreIn(la, "memory", "add", "--body", "tokens expire after an hour").OK(t)
		la.CommitAll("lore: adopt")
		la.Git("push", "-q", "origin", "HEAD:main")
		da = dev.Clone(app, "api")
		dev.LoreIn(da, "memory", "list").OK(t).Says(t, "the api pages at 100 rows", "tokens expire after an hour")
	})

	s.Step("day2/a-row-removed-by-git-rm-comes-back-from-history", func() {
		s.AdvanceDays(1)
		f := hRows(t, da.Dir, "memories")[0]
		gone = hID(f)
		rel := hRel(t, da.Dir, f)
		da.Git("rm", "-q", rel)
		da.Git("commit", "-q", "-m", "drop an old note")
		dev.LoreIn(da, "memory", "show", "--id", gone).Never(t, "internal error")
		// the documented way back: the file from the commit before the removal
		del := da.Git("log", "-n", "1", "--format=%H", "--diff-filter=D", "--", rel)
		da.Git("checkout", strings.TrimSpace(del)+"^", "--", rel)
		dev.LoreIn(da, "memory", "show", "--id", gone).OK(t)
		da.Git("commit", "-q", "-m", "bring the note back")
		da.Git("push", "-q", "origin", "HEAD:main")
	})

	hWaiting(t, "D-128", func(t *testing.T) {
		// a row removed by git rm + commit: trash --from-history names the
		// commit and prints the git checkout that brings it back, never
		// "purged by ?" or "it never comes back"
		f := hRows(t, da.Dir, "memories")[1]
		rel := hRel(t, da.Dir, f)
		da.Git("rm", "-q", rel)
		da.Git("commit", "-q", "-m", "drop another note")
		dev.LoreIn(da, "trash", "--from-history").OK(t).Says(t, hID(f), "git checkout").Never(t, "purged by ?", "never comes back")
		dev.LoreIn(da, "trash", "restore", "--id", hID(f)).Says(t, "git checkout")
	})

	s.Step("day3/trash-restore-round-trips-through-git", func() {
		s.AdvanceDays(1)
		la.Git("pull", "-q", "--rebase", "origin", "main")
		f := hRows(t, la.Dir, "memories")[0]
		id := hID(f)
		lead.LoreIn(la, "memory", "delete", "--id", id, "--yes").OK(t)
		la.CommitAll("lore: delete a note")
		la.Git("push", "-q", "origin", "HEAD:main")
		da.Git("pull", "-q", "--rebase", "origin", "main")
		dev.LoreIn(da, "trash", "list").OK(t).Says(t, id)
		dev.LoreIn(da, "trash", "restore", "--id", id).OK(t)
		da.CommitAll("lore: restore a note")
		da.Git("push", "-q", "origin", "HEAD:main")
		la.Git("pull", "-q", "--rebase", "origin", "main")
		lead.LoreIn(la, "memory", "show", "--id", id).OK(t)
	})

	s.Step("day4/an-emptied-store-is-refused-and-restored", func() {
		s.AdvanceDays(1)
		lead.LoreIn(la, "store", "init", "--name", "notes", "--url", store.SSHURL(), "--yes").OK(t)
		lead.LoreIn(la, "memory", "add", "--body", "the deploy window is friday", "--to", "notes").OK(t)
		lead.LoreIn(la, "sync").OK(t)
		if !hRemoteHas(store, "the deploy window is friday") {
			t.Fatal("the store note never reached the store remote")
		}
		// someone empties the store repo (deleted and made again)
		if err := os.RemoveAll(store.Dir); err != nil {
			t.Fatal(err)
		}
		s.Git(s.Root, "init", "-q", "--bare", "-b", "main", store.Dir)
		before := s.RefsOf(store)
		r := lead.LoreIn(la, "sync")
		if r.Code == 0 {
			t.Errorf("a sync into an emptied store must be refused naming it: %s", r)
		}
		r.Says(t, "was reset", "lore store push --restore")
		s.NoPushSince(t, store, before, "the refused sync")
		s.NamedFlags(t, r)
		lead.LoreIn(la, "store", "push", "--store", "notes", "--restore", "--yes").OK(t)
		if !hRemoteHas(store, "the deploy window is friday") {
			t.Error("store push --restore did not bring the note back")
		}
	})

	hWaiting(t, "D-124", func(t *testing.T) {
		// a force-push on the code remote drops a commit carrying a note:
		// the author's next pull/sync names the dropped note and the way
		// back (the backup ref or the reflog), pre-push is not silent
		lead.LoreIn(la, "memory", "add", "--body", "only in the dropped commit").OK(t)
		la.CommitAll("lore: a note")
		la.Git("push", "-q", "origin", "HEAD:main")
		da.Git("fetch", "-q", "origin")
		da.Git("push", "-q", "--force", "origin", "HEAD~0:main")
		r := lead.LoreIn(la, "sync")
		r.Says(t, "only in the dropped commit", "git push")
	})

	hWaiting(t, "D-125", func(t *testing.T) {
		// rebase + force-push: commit links follow the rewritten shas
		lead.LoreIn(la, "doctor").Never(t, "HEALTHY")
	})

	hWaiting(t, "D-126", func(t *testing.T) {
		// the store remote rewound by a force-push: the next member sync
		// names the rewind instead of silently re-pushing the dropped commits
		head := store.Rev("main")
		store.SetRef("refs/heads/main", head+"~1")
		r := dev.LoreIn(da, "sync")
		r.Says(t, "rewound")
		if store.Rev("main") == head {
			t.Error("the sync re-pushed the commits the rewind dropped")
		}
	})

	hWaiting(t, "D-127", func(t *testing.T) {
		// trash restore of an org-store row from a format-1 repo works
		// like the delete of the same row there did
		dev.LoreIn(da, "trash", "restore", "--id", "mem_0").Never(t, "E_FORMAT_OLD")
	})

	hWaiting(t, "D-140", func(t *testing.T) {
		// git commit -a with a new row commits it once, never "nothing added"
		dev.LoreIn(da, "memory", "add", "--body", "a commit -a note").OK(t)
		r := da.TryGit("commit", "-a", "-q", "-m", "work")
		if r.Code != 0 || strings.Contains(r.Out(), "nothing added") {
			t.Errorf("git commit -a with a new row failed: %s", r)
		}
	})

	s.Step("after/no-surprise-pushes", func() {
		before := s.RefsOf(app)
		dev.LoreIn(da, "memory", "list").OK(t)
		dev.LoreIn(da, "doctor")
		s.NoPushSince(t, app, before, "reads and doctor")
	})
}

func TestStoryHLostRemotes(t *testing.T) {
	s := New(t, "H")
	app := s.Seed(s.Remote("github.com/recovery-co/worker"), func(dir string) { PlainRepo(t, dir) })
	mine := s.Remote("github.com/dev-okafor/lore-me")
	devP := s.Persona(hDevName, hDevMail)
	dev := devP.Laptop()
	var c *teamharness.Clone

	s.Step("day1/a-repo-and-a-personal-store", func() {
		c = dev.Clone(app, "worker")
		dev.LoreIn(c, "init").OK(t)
		c.CommitAll("lore: adopt")
		c.Git("push", "-q", "origin", "HEAD:main")
		dev.LoreIn(c, "store", "init", "--name", "me", "--url", mine.SSHURL(), "--yes").OK(t)
		dev.LoreIn(c, "memory", "add", "--body", "synced before the loss", "--to", "me").OK(t)
		dev.LoreIn(c, "sync").OK(t)
	})

	s.Step("day2/personal-remote-gone-with-two-unsynced-notes", func() {
		s.AdvanceDays(1)
		dev.LoreIn(c, "memory", "add", "--body", "unsynced one", "--to", "me").OK(t)
		dev.LoreIn(c, "memory", "add", "--body", "unsynced two", "--to", "me").OK(t)
		if err := os.RemoveAll(mine.Dir); err != nil {
			t.Fatal(err)
		}
		// forget must refuse: this clone holds the only copy of two notes
		if r := dev.LoreIn(c, "store", "forget", "--url", mine.SSHURL(), "--yes"); r.Code == 0 {
			t.Errorf("store forget dropped a clone holding unpushed work: %s", r)
		}
		dev.LoreIn(c, "memory", "list").OK(t).Says(t, "synced before the loss", "unsynced one", "unsynced two")
	})

	s.Step("day2/remade-remote-gets-everything-back", func() {
		s.Git(s.Root, "init", "-q", "--bare", "-b", "main", mine.Dir)
		dev.LoreIn(c, "store", "push", "--store", mine.SSHURL(), "--restore", "--yes").OK(t)
		for _, b := range []string{"synced before the loss", "unsynced one", "unsynced two"} {
			if !hRemoteHas(mine, b) {
				t.Errorf("the restored store lacks %q", b)
			}
		}
	})

	s.Step("day3/hooks-removed-by-hand-come-back-without-clobbering", func() {
		s.AdvanceDays(1)
		hook := filepath.Join(c.Dir, ".git", "hooks", "pre-commit")
		mineLine := "echo team-check-ran >/dev/null\n"
		hWrite(t, hook, "#!/bin/sh\n"+mineLine)
		if err := os.Chmod(hook, 0o755); err != nil {
			t.Fatal(err)
		}
		dev.LoreIn(c, "fix").Says(t, "hook")
		got := hRead(t, hook)
		if !strings.Contains(got, mineLine) {
			t.Errorf("lore fix clobbered the person's own pre-commit line:\n%s", got)
		}
		if !strings.Contains(got, "lore") {
			t.Errorf("lore fix did not put lore's block back:\n%s", got)
		}
		key := FakeSecret("H-hooks")
		c.WriteFile("config/prod.env", "AWS_KEY="+key+"\n")
		c.Git("add", "-A")
		r := c.TryGit("commit", "-q", "-m", "prod config")
		if r.Code == 0 {
			t.Error("a commit with a fake key passed after lore fix put the hooks back")
		}
		s.NotPrinted(t, key, r)
		c.Git("reset", "-q")
		if err := os.Remove(filepath.Join(c.Dir, "config", "prod.env")); err != nil {
			t.Fatal(err)
		}
	})

	hWaiting(t, "D-141", func(t *testing.T) {
		// hooks taken over by husky: doctor says the leak guard is off
		c.Git("config", "core.hooksPath", ".husky")
		dev.LoreIn(c, "doctor").Says(t, "leak guard").Never(t, "HEALTHY")
	})

	hWaiting(t, "D-142", func(t *testing.T) {
		// with husky, a lore write never adds files to the tracked .husky
		c.WriteFile(".husky/pre-commit", "#!/bin/sh\nnpm test\n")
		c.CommitAll("husky")
		c.Git("config", "core.hooksPath", ".husky")
		dev.LoreIn(c, "memory", "add", "--body", "x").OK(t)
		if st := c.Git("status", "--porcelain", "--", ".husky"); st != "" {
			t.Errorf("lore added hook files to the tracked .husky folder:\n%s", st)
		}
	})

	hWaiting(t, "D-129", func(t *testing.T) {
		// lore sync status works outside a repo, like lore sync
		dev.Lore(dev.Home, "sync", "status").OK(t)
	})

	hWaiting(t, "D-130", func(t *testing.T) {
		// a deleted store remote is named as gone with set-url / --restore,
		// not "offline, check the network"
		if err := os.RemoveAll(mine.Dir); err != nil {
			t.Fatal(err)
		}
		dev.LoreIn(c, "memory", "add", "--body", "after the delete", "--to", "me").OK(t)
		dev.LoreIn(c, "sync").Says(t, "lore store set-url", "--restore").Never(t, "offline", "check the network")
	})

	hWaiting(t, "D-131", func(t *testing.T) {
		// store set-url outside a repo is refused (or works) with the right
		// reason; a plain member cannot rewrite the org's store url
		dev.Lore(dev.Home, "store", "set-url", "--name", "me", "--url", mine.SSHURL()).Never(t, "E_NOT_A_STORE")
	})

	hWaiting(t, "D-143", func(t *testing.T) {
		// store doctor: unsent commits are a warning counted once, and under
		// the manual push mode it names lore sync, not "the next command pushes"
		dev.LoreIn(c, "store", "doctor").Never(t, "E_INVALID_INPUT", "the next lore command pushes")
	})

	hWaiting(t, "D-144", func(t *testing.T) {
		// a new laptop after the old one died: lore says what it could not
		// find (the local-only personal store), the join line says local is machine-only
		desk := devP.NewLaptop("desk")
		dc := desk.Clone(app, "worker")
		desk.LoreIn(dc, "doctor").Says(t, "machine-only")
	})
}
