package wire

// trash_actor_test.go — later 246 (OV9): trash restore and every purge
// path write the caller's actor row into each store they touch before the
// write, so the updated_by of a restored row or a tombstone resolves in
// that store even when it never held the caller's row.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
	"github.com/thesatellite-ai/lore/schema"
)

// TestTrashWritesActorRowFirst: a memory written, then trashed or archived,
// in the attached store kb; the caller's actor row is removed from kb (a
// store that never held it), and a fresh App runs the trash write: the
// actor row is back in kb afterwards, for restore, purge <id>, purge
// --older-than and purge --archived-before. A dry run writes no actor row.
func TestTrashWritesActorRowFirst(t *testing.T) {
	who, err := identity.ActorID(testEmail)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		archive bool // archive the memory instead of deleting it
		op      string
		input   func(id string) string
		dry     bool
	}{
		{name: "restore", op: opstrash.OpRestore, input: func(id string) string { return `{"target":"` + id + `"}` }},
		{name: "purge id", op: opstrash.OpPurge, input: func(id string) string { return `{"target":"` + id + `","confirm":true}` }},
		{name: "purge older-than", op: opstrash.OpPurge, input: func(string) string { return `{"older_than":"0d","confirm":true}` }},
		{name: "purge archived-before", archive: true, op: opstrash.OpPurge,
			input: func(string) string { return `{"archived_before":"2999-01-01","confirm":true}` }},
		{name: "purge dry run", op: opstrash.OpPurge, dry: true,
			input: func(id string) string { return `{"target":"` + id + `","dry_run":true}` }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := gitRepo(t)
			kb := folderStoreAt(t, "sto_0e45506e64357c94a87e70ef1c6b0001", "internal")
			writeStoresYAML(t, repo, "stores:\n  - name: kb\n    url: "+kb+"\n")
			internal := Options{RepoIsClient: func(context.Context) (bool, error) { return false, nil }}
			a := openWith(t, repo, internal)
			withProject(t, a)
			out, err := run(t, a, opsmemory.OpAdd, `{"body":"note in kb","to":"kb"}`, true)
			if err != nil {
				t.Fatal(err)
			}
			var added struct {
				ID string `json:"id"`
			}
			if err = json.Unmarshal([]byte(out), &added); err != nil || added.ID == "" {
				t.Fatalf("add: %s %v", out, err)
			}
			if c.archive {
				_, err = run(t, a, opsmemory.OpArchive, `{"id":"`+added.ID+`"}`, true)
			} else {
				_, err = run(t, a, opsmemory.OpDelete, `{"id":"`+added.ID+`"}`, true)
			}
			if err != nil {
				t.Fatal(err)
			}
			actorFile := filepath.Join(filepath.FromSlash(strings.TrimPrefix(kb, "file://")), ".lore", "data", schema.TableActors, who.String()+".json")
			mustRead(t, actorFile)
			if err = os.Remove(actorFile); err != nil {
				t.Fatal(err)
			}
			b := openWith(t, repo, internal)
			if out, err = run(t, b, c.op, c.input(added.ID), true); err != nil {
				t.Fatalf("%s: %v", c.op, err)
			}
			if !strings.Contains(out, added.ID) {
				t.Fatalf("%s did not touch %s: %s", c.op, added.ID, out)
			}
			_, serr := os.Stat(actorFile)
			if c.dry != os.IsNotExist(serr) {
				t.Fatalf("actor row in kb after %s: stat err %v (dry run %v)", c.name, serr, c.dry)
			}
		})
	}
}
