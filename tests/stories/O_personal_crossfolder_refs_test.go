//go:build stories && !windows

package stories

// O_personal_crossfolder_refs_test.go — story O, devco D-793 (owner P-12,
// bug hunt H-50): in a personal knowledge base the journal folder's notes
// name people kept in the work folder (${res:person/<name>}). `lore memory
// add --to me/journal` opens me/journal alone, so the reference resolved
// nothing and the note got no row_refs line: `resource graph --up` never
// listed it. And a cache an older lore had filled kept its old (missing)
// lines until a manual `lore search rebuild`. Here an owner-shaped laptop
// keeps a personal store on a local bare repo with a work and a journal
// folder; journal notes name a person added before them and one added
// after them, graph --up from the whole store lists every note, and a
// cache whose reference lines are gone (as an older lore left it) gets
// them back on its next open.

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite" // the old-cache step edits the journal cache file
)

// TestStoryOPersonalCrossFolderRefs: a journal note naming a person kept in
// the work folder reaches row_refs and graph --up, written before or after
// the person, and an older cache catches up by itself (devco D-793).
func TestStoryOPersonalCrossFolderRefs(t *testing.T) {
	s := New(t, "O")
	me := s.Remote("github.com/" + oAdminPersonal.Login + "/lore-me")
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	notes := []string{"met ${res:person/alice_ng} about orbit", "lunch with ${res:person/bob_li} next week"}
	s.Step("personal-store/journal-names-work-people", func() {
		ada.Lore(ada.Home, "store", "init", "--name", "me", "--url", me.Dir, "--yes").OK(t)
		ada.Lore(ada.Home, "store", "folder", "add", "me", "work").OK(t)
		ada.Lore(ada.Home, "store", "folder", "add", "me", "journal").OK(t)
		ada.Lore(ada.Home, "resource", "kind", "add", "--kind", "person", "--to", "me/work", "--yes").OK(t)
		ada.Lore(ada.Home, "resource", "add", "--kind", "person", "--name", "alice_ng", "--to", "me/work", "--yes").OK(t)
		for _, n := range notes {
			ada.Lore(ada.Home, "memory", "add", "--body", n, "--to", "me/journal", "--yes").OK(t)
		}
		ada.Lore(ada.Home, "resource", "add", "--kind", "person", "--name", "bob_li", "--to", "me/work", "--yes").OK(t)
	})
	listed := func(when string) {
		t.Helper()
		for i, person := range []string{"alice_ng", "bob_li"} {
			r := ada.Lore(ada.Home, "resource", "graph", "--id", person, "--up", "--store", "me")
			r.OK(t)
			if !strings.Contains(r.Out(), notes[i][:12]) {
				t.Errorf("[O D-793] %s: `lore resource graph --id %s --up --store me` misses the journal note %q:\n%s", when, person, notes[i], r)
			}
		}
	}
	s.Step("personal-store/graph-up-lists-journal-notes", func() { listed("after the writes") })
	s.Step("personal-store/older-cache-catches-up", func() {
		var files []string
		if err := filepath.WalkDir(ada.Home, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && d.Name() == "journal.db" {
				files = append(files, p)
			}
			return err
		}); err != nil || len(files) != 1 {
			t.Fatalf("the me/journal cache file: %v %v", files, err)
		}
		db, err := sql.Open("sqlite", files[0])
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{`DELETE FROM row_refs`, `DELETE FROM row_expanded`, `DELETE FROM "_refs_stamp"`} {
			if _, err := db.ExecContext(context.Background(), q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		listed("on the first read of a cache an older lore filled")
	})
}
