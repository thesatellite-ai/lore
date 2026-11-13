//go:build journeys && !windows

package journeys

// folders_many_test.go — an org with several stores (owner reports,
// 2026-11-11). Lee's org lists four stores in its lore-org.yaml: the team
// store, two brand-new EMPTY repos, and one left broken by an older lore
// (a folder's _meta.json and no lore-store.yaml). From the plain parent
// folder Lee makes client folders in each, lists every store, links a
// client repo that must never hold lore files to the second store by its
// remote, and writes a task from it. Then Lee removes folders: an empty
// one made in the wrong store (its name then works in another store), a
// non-empty one refused, and the same with --purge, whose rows come back
// with `lore trash restore`. A repo attached to a removed folder gets a
// doctor finding.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The org's other stores and the client repos (placeholders).
const (
	jManyTwoSlug    = "github.com/acme-co/notes-two"
	jManyThreeSlug  = "github.com/acme-co/notes-three"
	jManyBrokenSlug = "github.com/acme-co/notes-old"
	jManyHubSlug    = "github.com/client-co/hub"
	jManyPortalSlug = "github.com/client-co/portal"
	jManyMarker     = "lore-store.yaml"
	jManyOrgFile    = "org/lore-org.yaml"
	jManyTaskTitle  = "hub: wire the payment webhook"
	jManyOldSlug    = "github.com/acme-co/old-app"
	jManyOldProject = "prj_01a1cb5d817172998c6a1880a90a2001"
	jManyOldActor   = "act_541757c158dc7fb58c1c338b0a602001"
)

// jManyOldNotes writes a small v0.1.17 repo's .lore/data into c (its
// project, one actor, two memories and one rule) and commits it.
func jManyOldNotes(t *testing.T, c *teamharness.Clone) {
	t.Helper()
	data := filepath.Join(c.Dir, ".lore", "data")
	jWrite(t, filepath.Join(data, "_meta.json"), `{"_v": 1, "project_id": "`+jManyOldProject+`"}`+"\n")
	jWrite(t, filepath.Join(data, "projects", jManyOldProject+".json"), `{"_table": "projects", "_v": 1, "created_at": "2026-11-01T09:00:00.000000000Z", "id": "`+jManyOldProject+`", "name": "old-app", "updated_at": "2026-11-01T09:00:00.000000000Z"}`+"\n")
	jWrite(t, filepath.Join(data, "actors", jManyOldActor+".json"), `{"_table": "actors", "_v": 1, "display_name": "Lee Lead", "id": "`+jManyOldActor+`", "kind": "human", "stable_key": "human:`+tlLee.Email+`"}`+"\n")
	for i, body := range []string{"the old app deploys on fridays", "the old app's cache is cold at boot"} {
		id := fmt.Sprintf("mem_01a1cb5d827d726cabe90000000020%02d", i)
		jWrite(t, filepath.Join(data, "memories", id+".json"), `{"_table": "memories", "_v": 1, "body": "`+body+`", "created_at": "2026-11-02T10:00:00.000000000Z", "created_by_actor_id": "`+jManyOldActor+
			`", "id": "`+id+`", "kind": "retrieved", "project_id": "`+jManyOldProject+`", "source_kind": "manual", "trust_score": 0.5, "updated_at": "2026-11-02T10:00:00.000000000Z"}`+"\n")
	}
	c.CommitAll("v0.1.17 notes")
}

func TestJourneyFoldersManyStores(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	app := tt.repo[tlLee.Email]
	two, three, broken := w.Remote(jManyTwoSlug), w.Remote(jManyThreeSlug), w.Remote(jManyBrokenSlug)
	mainRef := "refs/heads/main"
	var hub *teamharness.Clone
	var hubBefore string

	w.Step("day1/start-team-with-four-stores", func() {
		tlStartTeam(t, lee, app.Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		// the broken store: an older lore pushed a folder without the marker
		b := lee.Clone(broken, "notes-old")
		jWrite(t, filepath.Join(b.Dir, "client_z", ".lore", "data", "_meta.json"), `{"_v":1,"project_id":"prj_0190f1a2b3c47d5e8f90a1b2c3d4e5f6"}`+"\n")
		b.CommitAll("client_z")
		b.Git("push", "-q", "origin", "HEAD:main")
		// the org's lore-org.yaml now lists every store; org refresh takes them
		s := lee.Clone(tt.store, "team-store")
		jWrite(t, filepath.Join(s.Dir, filepath.FromSlash(jManyOrgFile)), "name: "+tlOrg+"\nstores:\n"+
			"    - url: "+tt.store.SSHURL()+"\n      org_folder: org\n"+
			"    - url: "+two.SSHURL()+"\n    - url: "+three.SSHURL()+"\n    - url: "+broken.SSHURL()+"\n")
		s.CommitAll("list every store")
		s.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(parent, "org", "refresh", tlOrg).OK(t)
	})

	w.Step("day2/a-folder-in-each-store", func() {
		w.AdvanceDays(1)
		// the success line names the store written to by its short name
		// (its repo name), not the org and not its whole url
		lee.Lore(parent, "store", "folder", "add", two.SSHURL(), "client_a").OK(t).Says(t, "client_a in notes-two")
		lee.Lore(parent, "store", "folder", "add", "notes-three", "client_d").OK(t).Says(t, "client_d in notes-three")
		lee.Lore(parent, "store", "folder", "add", "team", "client_c").OK(t).Says(t, "client_c in "+tlOrg)
		for _, r := range []*teamharness.Remote{two, three} {
			// the first write made each empty repo a store: marker in the same commit
			if !slices.Contains(r.Files(mainRef), jManyMarker) {
				t.Errorf("%s has no %s after its first folder: %v", r.Slug, jManyMarker, r.Files(mainRef))
			}
			if n := r.CommitCount(mainRef); n != 1 {
				t.Errorf("%s: %d commits, want the marker and the folder in one", r.Slug, n)
			}
		}
		// a second command on the second store works (it refused before)
		lee.Lore(parent, "store", "folder", "add", two.SSHURL(), "client_b").OK(t)
		// the broken store is adopted: its next write adds the marker
		lee.Lore(parent, "store", "folder", "add", broken.SSHURL(), "client_y").OK(t)
		if !slices.Contains(broken.Files(mainRef), jManyMarker) {
			t.Errorf("the adopted store has no marker after a write: %v", broken.Files(mainRef))
		}

		// every store of the org entry is listed, each once, and the personal one
		var sl struct {
			Stores []struct {
				Name   string `json:"name"`
				Source string `json:"source"`
			} `json:"stores"`
		}
		tlJSON(t, lee.Lore(parent, "store", "list", "--json").OK(t), &sl)
		org := 0
		for _, s := range sl.Stores {
			if s.Source == "org" {
				org++
			}
		}
		for _, short := range []string{"notes-two", "notes-three", "notes-old"} { // NAME is the short name, the url its own column
			if !slices.ContainsFunc(sl.Stores, func(e struct {
				Name   string `json:"name"`
				Source string `json:"source"`
			}) bool {
				return e.Name == short
			}) {
				t.Errorf("store list does not name %s by its short name: %+v", short, sl.Stores)
			}
		}
		if org != 5 { // the team store's root and org/ folder, and the three other stores
			t.Errorf("store list shows %d org stores, want 5 (team root, org folder, three more): %+v", org, sl.Stores)
		}
		var fl struct {
			Folders []jFolderRow `json:"folders"`
			Stores  []string     `json:"stores"`
		}
		tlJSON(t, lee.Lore(parent, "store", "folder", "list", "--json").OK(t), &fl)
		if len(fl.Stores) != 5 { // the team store, three more, me
			t.Errorf("store folder list read %d stores, want 5: %v", len(fl.Stores), fl.Stores)
		}
		for _, f := range []string{"client_a", "client_b", "client_c", "client_d", "client_y", "client_z"} {
			if !slices.ContainsFunc(fl.Folders, func(r jFolderRow) bool { return r.Folder == f }) {
				t.Errorf("store folder list does not list %s: %+v", f, fl.Folders)
			}
		}

		// the store commands that take one store work on a second one
		lee.Lore(parent, "store", "show", three.SSHURL()).OK(t).Says(t, three.SSHURL())
		lee.Lore(parent, "store", "show", "notes-three").OK(t).Says(t, "notes-three")
		lee.Lore(parent, "store", "visibility", three.SSHURL(), "internal").OK(t)
		lee.Lore(parent, "store", "set", three.SSHURL(), "push=batch").OK(t).Says(t, "push batch")
		lee.Lore(parent, "store", "compact", three.SSHURL(), "--dry-run").OK(t)
		lee.Lore(parent, "store", "set", three.SSHURL(), "push=now").OK(t)
	})

	w.Step("day2/a-folder-of-the-third-store-by-the-org-name", func() {
		// client_d lives in the org's third store: <org>/client_d and a
		// bare client_d name it (folder names are unique across the org's
		// stores), never "<org> has no folder client_d" (owner report
		// 2026-11-12, store adopt)
		old := jPlainRepo(t, lee, w.Remote(jManyOldSlug), filepath.Join(parent, "old-app"))
		jManyOldNotes(t, old)
		// the folder (and where its files land) is what is checked
		// Lee's v0.1.17 actor id is mapped onto Lee's roster actor (one
		// identity, one id): its row is not adopted, the memories name the
		// roster's actor (owner report 2026-11-12)
		lee.Lore(parent, "store", "adopt", tlOrg+"/client_d", "--from", old.Dir, "--dry-run").OK(t).Says(t, "would adopt 2 rows", "/client_d", "actor "+jManyOldActor+" (human:"+tlLee.Email+") → act_")
		lee.Lore(parent, "store", "adopt", tlOrg+"/client_d", "--from", old.Dir, "--yes").OK(t).Says(t, "adopted 2 rows", "/client_d")
		if got := three.FilesMatching(mainRef, "client_d/actors/"+jManyOldActor); len(got) != 0 {
			t.Errorf("Lee's second actor id was adopted: %v", got)
		}
		for _, f := range three.FilesMatching(mainRef, "client_d/memories/") {
			if doc, _ := three.Read(mainRef, f); strings.Contains(doc, jManyOldActor) {
				t.Errorf("%s still names Lee's second actor id", f)
			}
		}
		lee.Lore(parent, "store", "adopt", "client_d", "--from", old.Dir, "--yes").OK(t).Says(t, "nothing to adopt", "/client_d")
		if len(three.FilesMatching(mainRef, "client_d/")) < 2 {
			t.Errorf("the adopted rows are not in the third store's client_d: %v", three.Files(mainRef))
		}
		if got := tt.store.FilesMatching(mainRef, "client_d/"); len(got) != 0 {
			t.Errorf("adopt made client_d in the org's first store: %v", got)
		}
		// an unknown folder names the stores searched and the folders found
		lee.Lore(parent, "store", "adopt", tlOrg+"/client_q", "--from", old.Dir, "--yes").Refused(t).
			Says(t, "has no folder client_q", "searched the org's stores", "client_a", "client_d")
		lee.Lore(parent, "store", "adopt", "client_q", "--from", old.Dir, "--yes").Refused(t).
			Says(t, "no store of the org has a folder client_q", "searched the org's stores", "client_d")

		// store folder remove <org> <folder> removes it from its own store
		lee.Lore(parent, "store", "folder", "add", three.SSHURL(), "client_e").OK(t)
		lee.Lore(parent, "store", "folder", "remove", tlOrg, "client_e", "--yes").OK(t).Says(t, "removed folder client_e")
		if got := three.FilesMatching(mainRef, "client_e/"); len(got) != 0 {
			t.Errorf("the third store still has client_e: %v", got)
		}
	})

	w.Step("day3/a-client-repo-with-no-lore-files", func() {
		w.AdvanceDays(1)
		hub = jPlainRepo(t, lee, w.Remote(jManyHubSlug), filepath.Join(parent, "hub"))
		hubBefore = jRepoSnapshot(t, hub.Dir)
		lee.LoreIn(hub, "org", "link", tlOrg, "--project", "client_a", "--store", two.SSHURL(), "--dry-run").OK(t).Says(t, "nothing written")
		lee.LoreIn(hub, "org", "link", tlOrg, "--project", "client_a", "--store", two.SSHURL(), "--yes").OK(t).Says(t, "nothing was written to this repo")
		lee.LoreIn(hub, "task", "add", jManyTaskTitle).OK(t).Says(t, "client_a")
		lee.LoreIn(hub, "task", "list").OK(t).Says(t, jManyTaskTitle)
		lee.LoreIn(hub, "doctor").OK(t).Says(t, "holds no lore data")
		if len(two.FilesMatching(mainRef, "client_a/tasks/")) != 1 {
			t.Errorf("the task did not land in the second store's client_a/: %v", two.Files(mainRef))
		}
		// from the client repo linked to the second store, --to <org>/<folder>
		// and --to <folder> write to the third store (nothing in the repo)
		lee.LoreIn(hub, "task", "add", "client d: by the org name", "--to", tlOrg+"/client_d").OK(t)
		lee.LoreIn(hub, "memory", "add", "client d: by the folder alone", "--to", "client_d").OK(t)
		if n := len(three.FilesMatching(mainRef, "client_d/tasks/")); n < 1 {
			t.Errorf("--to %s/client_d did not write into the third store: %v", tlOrg, three.Files(mainRef))
		}
		if got := tt.store.FilesMatching(mainRef, "client_d/"); len(got) != 0 {
			t.Errorf("a --to write made client_d in the org's first store: %v", got)
		}

		if _, err := os.Stat(filepath.Join(hub.Dir, ".lore")); err == nil {
			t.Error("lore made .lore/ in the client repo")
		}
		if after := jRepoSnapshot(t, hub.Dir); after != hubBefore {
			t.Errorf("link and task add changed the client repo (tree, ignores or hooks):\nbefore %s\nafter  %s", hubBefore, after)
		}
	})

	w.Step("day4/remove-a-folder-made-in-the-wrong-store", func() {
		w.AdvanceDays(1)
		portal := jPlainRepo(t, lee, w.Remote(jManyPortalSlug), filepath.Join(parent, "portal"))
		// client_c lives in the team store, so the third store cannot have it
		lee.LoreIn(portal, "org", "link", tlOrg, "--project", "client_c", "--store", three.SSHURL(), "--yes").Refused(t).Says(t, "client_c")
		lee.Lore(parent, "store", "folder", "remove", "team", "client_c").Refused(t).Says(t, "remove the empty folder client_c", "--yes")
		lee.Lore(parent, "store", "folder", "remove", "team", "client_c", "--yes").OK(t).Says(t, "removed folder client_c")
		if got := tt.store.FilesMatching(mainRef, "client_c/"); len(got) != 0 {
			t.Errorf("the team store still has client_c: %v", got)
		}
		lee.Lore(parent, "store", "folder", "list", "team").OK(t).Never(t, "client_c")
		// the name is free: made in the third store and linked there
		lee.Lore(parent, "store", "folder", "add", three.SSHURL(), "client_c").OK(t)
		lee.LoreIn(portal, "org", "link", tlOrg, "--project", "client_c", "--store", three.SSHURL(), "--yes").OK(t)
		// the org folder is never removed
		lee.Lore(parent, "store", "folder", "remove", "team", "org", "--yes").Refused(t).Says(t, "org folder")
	})

	w.Step("day5/remove-a-folder-with-rows", func() {
		w.AdvanceDays(1)
		lee.Lore(parent, "store", "folder", "remove", two.SSHURL(), "client_a", "--yes").Refused(t).
			Says(t, "3 rows", "1 repo link", "lore move", "lore org unlink", "--purge")
		if len(two.FilesMatching(mainRef, "client_a/_meta.json")) != 1 {
			t.Fatal("a refused remove changed the store")
		}
		// --purge never runs on --yes: it asks for the literal --confirm
		lee.Lore(parent, "store", "folder", "remove", two.SSHURL(), "client_a", "--purge", "--yes").Refused(t).Says(t, "--confirm", "3 rows")
		var out struct {
			Batch  string `json:"batch"`
			Purged bool   `json:"purged"`
			Rows   int    `json:"rows"`
		}
		tlJSON(t, lee.Lore(parent, "store", "folder", "remove", two.SSHURL(), "client_a", "--purge", "--confirm", "--json").OK(t), &out)
		if !out.Purged || out.Rows != 3 || !strings.HasPrefix(out.Batch, "bat_") {
			t.Fatalf("remove --purge = %+v, want purged, 3 rows and a bat_ id", out)
		}
		if len(two.FilesMatching(mainRef, "client_a/_meta.json")) != 0 {
			t.Error("the purged folder's _meta.json is still in the store")
		}
		lee.Lore(parent, "store", "folder", "list", two.SSHURL()).OK(t).Never(t, "client_a")
		// the link went with it: the client repo is no longer linked
		lee.LoreIn(hub, "task", "list").Never(t, jManyTaskTitle)

		// back: make the folder again (same id), link, restore the batch
		lee.Lore(parent, "store", "folder", "add", two.SSHURL(), "client_a").OK(t)
		lee.LoreIn(hub, "org", "link", tlOrg, "--project", "client_a", "--store", two.SSHURL(), "--yes").OK(t)
		lee.LoreIn(hub, "trash", "list").OK(t).Says(t, out.Batch)
		lee.LoreIn(hub, "trash", "restore", out.Batch, "--yes").OK(t)
		lee.LoreIn(hub, "task", "list").OK(t).Says(t, jManyTaskTitle)
	})

	w.Step("day6/a-repo-attached-to-a-removed-folder", func() {
		w.AdvanceDays(1)
		lee.LoreIn(app, "store", "add", "notes_two", two.SSHURL(), "--folder", "client_b", "--yes").OK(t)
		lee.LoreIn(app, "doctor").OK(t).Never(t, "removed_store_folder")
		lee.Lore(parent, "store", "folder", "remove", two.SSHURL(), "client_b", "--yes").OK(t)
		lee.LoreIn(app, "doctor").Says(t, "removed_store_folder", "client_b", "lore store remove --name notes_two")
	})
}

// jPlainRepo clones r (one commit with code, no lore files) into dest: a
// client's repo that must never hold lore files.
func jPlainRepo(t testing.TB, m *Machine, r *teamharness.Remote, dest string) *teamharness.Clone {
	t.Helper()
	c := m.Clone(r, filepath.Base(dest))
	jWrite(t, filepath.Join(c.Dir, "main.go"), "package main\n")
	c.CommitAll("init")
	c.Git("push", "-q", "origin", "HEAD:main")
	m.MoveClone(t, c, dest)
	return c
}

// jWrite writes body to p, making its folders.
func jWrite(t testing.TB, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
