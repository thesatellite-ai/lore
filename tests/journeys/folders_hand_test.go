//go:build journeys && !windows

package journeys

// folders_hand_test.go — Lee edits the team store by hand (plain files
// round, brief 2.1–2.4): the store clone is a plain git checkout, so he
// opens row files with a text editor (here: plain file writes), deletes
// one, moves one to another folder, and uses git by hand. Every next lore
// command picks the change up: validated, re-stamped as Lee, committed as
// "hand edit: …" and pushed; a broken file is refused with file:line and
// stays out of the store while the other edits go. Ann, a teammate, edited
// the same row earlier while offline: Lee's re-stamped hand edit is newer
// and wins the merge (without the re-stamp her older change would win).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The hand-edit journey's names (placeholders).
const (
	jHandSlug    = "github.com/acme-co/hand-web"
	jHandA       = "client_hand"
	jHandB       = "client_other"
	jHandTitle1  = "Task one"
	jHandEdited1 = "Task one, edited by hand"
	jHandTitle4  = "Task four"
	jHandEdited4 = "Task four, edited by hand"
	jHandGitText = "Task four, committed with git by hand"
)

func TestJourneyFoldersHandEdits(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	ann := tt.person(tlAnn)
	parent := filepath.Join(lee.Home, "code")
	mainRef := "refs/heads/main"
	var web, storeDir, t1, t2, t3, t4 string
	file := func(folder, id string) string { return filepath.Join(storeDir, folder, "tasks", id+".json") }
	write := func(p, old, repl string) {
		t.Helper()
		body := jRead(t, p)
		if !strings.Contains(body, old) {
			t.Fatalf("%s lacks %q:\n%s", p, old, body)
		}
		if err := os.WriteFile(p, []byte(strings.Replace(body, old, repl, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	remoteRow := func(folder, id string) string {
		t.Helper()
		doc, ok := tt.store.Read(mainRef, folder+"/tasks/"+id+".json")
		if !ok {
			t.Fatalf("the store's main branch has no %s/tasks/%s.json (files: %v)", folder, id, tt.store.FilesMatching(mainRef, id))
		}
		return doc
	}

	w.Step("day1/team-folders-repo-and-tasks", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", jHandA).OK(t)
		lee.Lore(parent, "store", "folder", "add", "team", jHandB).OK(t)
		c := lee.Clone(w.Remote(jHandSlug), "hand-web")
		lee.MoveClone(t, c, filepath.Join(parent, "hand-web"))
		lee.LoreIn(c, "init", "--non-interactive", "--name=hand-web").OK(t)
		lee.LoreIn(c, "org", "link", tlOrg, "--project", jHandA, "--yes").OK(t)
		web = c.Dir
		t1 = jID(t, lee.Lore(web, "task", "add", jHandTitle1, "--to", tlOrg, "--json").OK(t))
		t2 = jID(t, lee.Lore(web, "task", "add", "Task two", "--to", tlOrg, "--json").OK(t))
		t3 = jID(t, lee.Lore(web, "task", "add", "Task three", "--to", tlOrg, "--json").OK(t))
		t4 = jID(t, lee.Lore(web, "task", "add", jHandTitle4, "--to", tlOrg, "--json").OK(t))
		p := jPath(t, lee.Lore(web, "path", t1, "--json").OK(t))
		storeDir = strings.TrimSuffix(p.Path, string(filepath.Separator)+filepath.Join(jHandA, "tasks", t1+".json"))
		if storeDir == p.Path {
			t.Fatalf("lore path %s = %s, want it under the %s folder of the store clone", t1, p.Path, jHandA)
		}
		// Ann joins and works in her own clone of the store's folder
		_, line := tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlAnn.Email)
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{tlFolders})
	})

	w.Step("day2/plain-git-is-set-up-in-the-clone", func() {
		w.AdvanceDays(1)
		cfg := jRead(t, filepath.Join(storeDir, ".git", "config"))
		for _, want := range []string{`[merge "lore"]`, "merge-driver %O %A %B %P"} {
			if !strings.Contains(cfg, want) {
				t.Errorf("the store clone's .git/config lacks %q:\n%s", want, cfg)
			}
		}
		jFileHas(t, filepath.Join(storeDir, ".git", "info", "attributes"), "*.json merge=lore")
		jFileHas(t, filepath.Join(storeDir, ".git", "hooks", "pre-commit"), "lore store check", "sync hook pre-commit", "check leaks --staged")
		if strings.Contains(cfg, "hooksPath") {
			t.Errorf("lore set core.hooksPath in the clone although nothing global bypasses .git/hooks:\n%s", cfg)
		}
	})

	annFolder := func() string {
		t.Helper()
		rel, err := filepath.Rel(lee.LorePath("stores"), storeDir)
		if err != nil {
			t.Fatal(err)
		}
		return filepath.Join(ann.LorePath("stores"), rel, jHandA)
	}

	w.Step("day3/ann-edits-task-one-offline", func() {
		w.AdvanceDays(1)
		ann.Lore(tt.repo[tlAnn.Email].Dir, "sync").OK(t)
		ann.Lore(annFolder(), "task", "list").OK(t).Says(t, jHandTitle1)
		ann.Offline()
		ann.Lore(annFolder(), "task", "edit", t1, "--priority", "high").OK(t)
	})

	w.Step("day4/a-hand-edit-is-picked-up-by-the-next-command", func() {
		w.AdvanceDays(1)
		before := remoteRow(jHandA, t1)
		write(file(jHandA, t1), jHandTitle1, jHandEdited1)
		write(file(jHandA, t1), `"priority": "medium"`, `"priority": "low"`)
		r := lee.Lore(web, "task", "list").OK(t)
		r.Says(t, "picked up 1 row edited by hand in store "+tlOrg, "hand edit: tasks "+t1, "edited tasks "+t1+" (priority, title)", file(jHandA, t1))
		if log := tt.store.Log(mainRef); len(log) == 0 || log[0] != "hand edit: tasks "+t1 {
			t.Fatalf("the store's last commit is %q, want \"hand edit: tasks %s\"", log, t1)
		}
		if a := tt.store.Authors(mainRef); len(a) == 0 || !strings.Contains(a[0], tlLee.Email) {
			t.Errorf("the hand edit is committed as %q, want Lee's git identity", a)
		}
		after := remoteRow(jHandA, t1)
		if !strings.Contains(after, jHandEdited1) || jHLC(t, after) == jHLC(t, before) {
			t.Errorf("the pushed row is not the re-stamped hand edit:\nbefore %s\nafter  %s", before, after)
		}
		if st := lee.OpenClone(tt.store, storeDir).Status(); st != "" {
			t.Errorf("the store clone is not clean after the pick-up:\n%s", st)
		}
		lee.Lore(web, "show", t1).OK(t).Says(t, jHandEdited1)
	})

	w.Step("day5/the-older-offline-edit-loses-to-the-hand-edit", func() {
		w.AdvanceDays(1)
		ann.Online()
		// her next write syncs the store: fetch, merge field by field (a
		// priority both changed: the newer edit wins), push
		ann.Lore(annFolder(), "task", "add", "Ann is back online").OK(t).Never(t, "refused")
		if doc := remoteRow(jHandA, t1); !strings.Contains(doc, `"priority": "low"`) || !strings.Contains(doc, jHandEdited1) {
			t.Errorf("Ann's older priority overwrote Lee's newer hand edit:\n%s", doc)
		}
		if files := tt.store.FilesMatching(mainRef, "tasks/"); len(files) < 5 {
			t.Errorf("Ann's new task did not reach the store: %v", files)
		}
	})

	w.Step("day6/a-hand-delete-goes-to-the-trash", func() {
		w.AdvanceDays(1)
		if err := os.Remove(file(jHandA, t2)); err != nil {
			t.Fatal(err)
		}
		lee.Lore(web, "task", "list").OK(t).Says(t, "deleted tasks "+t2, "lore trash restore "+t2).Never(t, "Task two")
		lee.Lore(web, "trash", "list").OK(t).Says(t, t2)
		lee.Lore(web, "trash", "restore", t2).OK(t)
		lee.Lore(web, "show", t2).OK(t).Says(t, "Task two")
	})

	w.Step("day7/a-hand-move-to-another-folder-is-a-move", func() {
		w.AdvanceDays(1)
		to := file(jHandB, t3)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(file(jHandA, t3), to); err != nil {
			t.Fatal(err)
		}
		lee.Lore(web, "task", "add", "A probe in the other folder", "--to", tlOrg+"/"+jHandB).OK(t).
			Says(t, "picked up a move made by hand", "hand move: tasks "+t3)
		if doc := remoteRow(jHandB, t3); !strings.Contains(doc, "Task three") {
			t.Errorf("the moved row is not in %s on the store: %s", jHandB, doc)
		}
		if st := lee.OpenClone(tt.store, storeDir).Status(); st != "" {
			t.Errorf("the store clone is not clean after the move:\n%s", st)
		}
	})

	w.Step("day8/a-broken-hand-edit-is-refused-and-the-rest-goes", func() {
		w.AdvanceDays(1)
		write(file(jHandA, t1), `"status": "`, `"status": "someday-maybe-`)
		write(file(jHandA, t4), jHandTitle4, jHandEdited4)
		r := lee.Lore(web, "task", "list").OK(t)
		r.Says(t, "hand edit refused: "+file(jHandA, t1)+":", "status", "not committed or pushed until the file is fixed",
			"picked up 1 row edited by hand", "edited tasks "+t4)
		if doc := remoteRow(jHandA, t1); strings.Contains(doc, "someday-maybe") {
			t.Errorf("the refused edit reached the store: %s", doc)
		}
		if doc := remoteRow(jHandA, t4); !strings.Contains(doc, jHandEdited4) {
			t.Errorf("the valid edit beside the refused one did not reach the store: %s", doc)
		}
		jFileHas(t, file(jHandA, t1), "someday-maybe")
		lee.Lore(web, "doctor").Says(t, file(jHandA, t1), "by hand")
		// fixed by hand: the next command picks it up
		write(file(jHandA, t1), `"status": "someday-maybe-`, `"status": "`)
		write(file(jHandA, t1), jHandEdited1, jHandEdited1+" again")
		lee.Lore(web, "task", "list").OK(t).Says(t, "edited tasks "+t1)
		if doc := remoteRow(jHandA, t1); !strings.Contains(doc, jHandEdited1+" again") {
			t.Errorf("the fixed edit did not reach the store: %s", doc)
		}
	})

	w.Step("day9/git-by-hand-in-the-store-clone", func() {
		w.AdvanceDays(1)
		c := lee.OpenClone(tt.store, storeDir)
		// a stray note is named by doctor, never deleted
		note := filepath.Join(storeDir, "notes.txt")
		if err := os.WriteFile(note, []byte("my notes\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// a commit carrying a secret is refused by lore's hook
		secret := "AKIA" + strings.Repeat("Q", 4) + "7XKD2M4N5P6R"
		write(file(jHandA, t4), jHandEdited4, "key "+secret)
		c.Git("add", "--", filepath.Join(jHandA, "tasks", t4+".json"))
		if r := c.TryGit("commit", "-m", "a hand commit with a key"); r.Code == 0 || !strings.Contains(r.Out(), "secret") {
			t.Fatalf("the hand commit with a secret was not refused by lore's hook: %s", r)
		}
		c.Git("reset", "-q")
		c.Git("checkout", "HEAD", "--", filepath.Join(jHandA, "tasks", t4+".json"))
		// a valid hand commit and a hand push go through
		write(file(jHandA, t4), jHandEdited4, jHandGitText)
		c.Git("commit", "-q", "-am", "retitle task four by hand")
		c.Git("push", "-q", "origin", "HEAD:main")
		if doc := remoteRow(jHandA, t4); !strings.Contains(doc, jHandGitText) {
			t.Errorf("the hand push did not land: %s", doc)
		}
		// Ann's change arrives with a plain git pull (lore's merge driver)
		r := ann.Lore(annFolder(), "task", "edit", t2, "--title", "Task two, from Ann").OK(t)
		if doc := remoteRow(jHandA, t2); !strings.Contains(doc, "Task two, from Ann") {
			ac := ann.OpenClone(tt.store, filepath.Dir(annFolder()))
			t.Fatalf("Ann's edit did not reach the store: %s\n%s\nstatus: %s\nlog: %s\nsync status: %s\nrepo sync status: %s", doc, r, ac.Status(), ac.Git("log", "--oneline", "-8", "--all"), ann.Lore(annFolder(), "sync", "status"), ann.Lore(tt.repo[tlAnn.Email].Dir, "sync", "status"))
		}
		c.Git("pull", "-q", "--no-rebase", "origin", "main")
		jFileHas(t, file(jHandA, t2), "Task two, from Ann")
		lee.Lore(web, "show", t2).OK(t).Says(t, "Task two, from Ann")
		lee.Lore(web, "doctor").Says(t, "notes.txt", "never deletes")
		if _, err := os.Stat(note); err != nil {
			t.Errorf("the stray note is gone: %v", err)
		}
	})

	w.Step("day10/a-hand-edit-in-the-code-repo-is-re-stamped-for-the-commit", func() {
		w.AdvanceDays(1)
		id := jID(t, lee.Lore(web, "task", "add", "A repo task", "--to", "repo", "--no-live", "--json").OK(t))
		repo := lee.OpenClone(w.Remote(jHandSlug), web)
		repo.Git("add", "-A", "--", ".lore/data")
		repo.Git("commit", "-q", "-m", "a repo task with the code")
		p := filepath.Join(web, ".lore", "data", "tasks", id+".json")
		before := jRead(t, p)
		write(p, "A repo task", "A repo task, edited by hand")
		lee.Lore(web, "task", "list").OK(t).Says(t, "picked up a hand edit of tasks "+id+" (title)", "commit it with your code")
		after := jRead(t, p)
		if !strings.Contains(after, "A repo task, edited by hand") || jHLC(t, after) == jHLC(t, before) {
			t.Errorf("the repo row was not re-stamped:\nbefore %s\nafter  %s", before, after)
		}
		if head := repo.Git("log", "-1", "--format=%s"); head != "a repo task with the code" {
			t.Errorf("lore committed in the code repo: %s", head)
		}
	})
}

// jHLC is the _hlc value of a row document.
func jHLC(t testing.TB, doc string) string {
	t.Helper()
	i := strings.Index(doc, `"_hlc": "`)
	if i < 0 {
		t.Fatalf("no _hlc in %s", doc)
	}
	rest := doc[i+len(`"_hlc": "`):]
	return rest[:strings.Index(rest, `"`)]
}
