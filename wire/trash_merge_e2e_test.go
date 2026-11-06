package wire

// trash_merge_e2e_test.go — later 288.3 / 525 (OV63, DELETION 3.7): the
// delete merge rules end to end across a git store synced by two
// checkouts, through the real ops and `lore sync` (SyncStores).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
)

// trashListIDs is the ids `lore trash list` (input) prints in app.
func trashListIDs(t *testing.T, app *App, input string) map[string]opstrash.Item {
	t.Helper()
	out, err := run(t, app, opstrash.OpList, input, false)
	if err != nil {
		t.Fatal(err)
	}
	var l opstrash.ListOut
	if err = json.Unmarshal([]byte(out), &l); err != nil {
		t.Fatal(err)
	}
	got := map[string]opstrash.Item{}
	for _, it := range l.Data {
		got[it.ID] = it
	}
	return got
}

// TestTrashMergeAcrossStoreSync (DELETION 3.7): checkouts A and B share
// the git store kb. A deletes three memories and syncs; before pulling, B
// edits the first, deletes the third and leaves the second; both sync.
// Then, in both checkouts: the edited row is alive with B's body and is
// listed by trash --overruled (edit wins over a delete, the delete is
// noted, never silently lost); the untouched row is in the trash; the row
// deleted on both sides is in the trash once; nothing else is.
func TestTrashMergeAcrossStoreSync(t *testing.T) {
	hosts := histGitWorld(t)
	url, _ := bareStore(t, hosts, "acme/kb")
	// each App attaches the stores of stores.yaml once: reopen after init/add
	repoA, repoB := gitRepo(t), gitRepo(t)
	if _, err := run(t, openWith(t, repoA, Options{}), opsstores.OpInit, `{"name":"kb","url":"`+url+`"}`, true); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := run(t, openWith(t, repoB, Options{}), opsstores.OpAdd, `{"name":"kb","url":"`+url+`"}`, true); err != nil {
		t.Fatalf("add: %v", err)
	}
	a, b := openWith(t, repoA, Options{}), openWith(t, repoB, Options{})
	withProject(t, a)
	withProject(t, b)
	sync := func(app *App, who string) {
		t.Helper()
		if _, err := app.SyncStores(background(), "kb"); err != nil {
			t.Fatalf("sync %s: %v", who, err)
		}
	}
	add := func(body string) string {
		t.Helper()
		out, err := run(t, a, opsmemory.OpAdd, `{"body":"`+body+`","to":"kb"}`, true)
		if err != nil {
			t.Fatal(err)
		}
		return idOf(t, out)
	}
	edited, untouched, both := add("edited on B"), add("deleted on A only"), add("deleted on both")
	sync(a, "A")
	sync(b, "B")
	for _, id := range []string{edited, untouched, both} {
		if _, err := run(t, b, opsmemory.OpShow, `{"id":"`+id+`"}`, false); err != nil {
			t.Fatalf("B does not see %s after sync: %v", id, err)
		}
		if _, err := run(t, a, opsmemory.OpDelete, `{"id":"`+id+`"}`, true); err != nil {
			t.Fatalf("A delete %s: %v", id, err)
		}
	}
	sync(a, "A")
	if _, err := run(t, b, opsmemory.OpEdit, `{"id":"`+edited+`","body":"B kept this"}`, true); err != nil {
		t.Fatalf("B edit: %v", err)
	}
	if _, err := run(t, b, opsmemory.OpDelete, `{"id":"`+both+`"}`, true); err != nil {
		t.Fatalf("B delete: %v", err)
	}
	sync(b, "B")
	sync(a, "A")

	for _, c := range []struct {
		who string
		app *App
	}{{"A", a}, {"B", b}} {
		out, err := run(t, c.app, opsmemory.OpShow, `{"id":"`+edited+`"}`, false)
		if err != nil || !strings.Contains(out, "B kept this") {
			t.Fatalf("%s: the edited row is not alive with B's body: %s %v", c.who, out, err)
		}
		trashed := trashListIDs(t, c.app, `{"to":"kb"}`)
		if _, ok := trashed[edited]; ok || len(trashed) != 2 {
			t.Fatalf("%s: trash = %v, want exactly %s and %s", c.who, trashed, untouched, both)
		}
		for _, id := range []string{untouched, both} {
			if _, ok := trashed[id]; !ok {
				t.Fatalf("%s: %s is not in the trash: %v", c.who, id, trashed)
			}
			if _, err = run(t, c.app, opsmemory.OpShow, `{"id":"`+id+`"}`, false); !errs.Has(err, errs.InTrash) {
				t.Fatalf("%s: show %s = %v, want E_IN_TRASH", c.who, id, err)
			}
		}
		over := trashListIDs(t, c.app, `{"overruled":true}`)
		if it, ok := over[edited]; !ok || len(over) != 1 || it.Overruled == "" {
			t.Fatalf("%s: trash --overruled = %+v, want only %s with its note", c.who, over, edited)
		}
	}
}
