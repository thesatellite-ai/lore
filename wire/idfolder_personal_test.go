package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	oprow "github.com/thesatellite-ai/lore/ops/row"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestWriteByIDFindsPersonalFolderRow (devco:D-755): a row kept in a folder
// of the personal store (`--to me/<folder>`) is written by id from every
// folder, as an org folder's row is: from a repo lore is set up in that
// attaches only `me` (the store root), from a git repo lore is not set up
// in, and from a folder that is no repo. Before the fix every such write
// answered E_NOT_FOUND ("looked in the stores attached here (me, repo)")
// or refused the folder as not set up.
func TestWriteByIDFindsPersonalFolderRow(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	storeDir := t.TempDir()
	writer := openWith(t, gitRepo(t), Options{Home: home})
	if _, err := run(t, writer, opsstores.OpInit, `{"name":"me","url":"`+fileURL(storeDir)+`"}`, true); err != nil {
		t.Fatalf("store init me: %v", err)
	}
	add := func(opName, input string) string {
		t.Helper()
		out, err := run(t, writer, opName, input, true)
		if err != nil {
			t.Fatalf("%s: %v", opName, err)
		}
		if opName == opstask.OpAdd {
			var w opstask.AddOut
			if err := json.Unmarshal([]byte(out), &w); err != nil || w.Data.ID == "" {
				t.Fatalf("task add output %s: %v", out, err)
			}
			return w.Data.ID
		}
		return idOf(t, out)
	}
	setUp := gitRepo(t)
	places := []struct {
		name string
		dir  string
		prep func(*App)
	}{
		{"repo set up, attaching only me", setUp, func(a *App) { withProject(t, a) }},
		{"git repo lore is not set up in", gitRepo(t), func(*App) {}},
		{"no repo (home folder)", t.TempDir(), func(*App) {}},
	}
	for _, p := range places {
		mem := add(opsmemory.OpAdd, `{"body":"deploys go out on tuesdays","to":"me/alpha"}`)
		moved := add(opsmemory.OpAdd, `{"body":"the staging db resets nightly","to":"me/alpha"}`)
		task := add(opstask.OpAdd, `{"title":"rotate the build key","to":"me/alpha"}`)
		a := openWith(t, p.dir, Options{Home: home})
		p.prep(a)
		steps := []struct{ op, in string }{
			{opsmemory.OpEdit, `{"id":"` + mem + `","body":"deploys go out on wednesdays"}`},
			{opstask.OpDone, `{"id":"` + task + `"}`},
			{oprow.OpMove, `{"id":"` + moved + `","to":"me/beta"}`},
			{opsmemory.OpDelete, `{"id":"` + mem + `"}`},
		}
		for _, s := range steps {
			if _, err := run(t, a, s.op, s.in, true); err != nil {
				t.Errorf("%s: %s by id on a me/alpha row: %v", p.name, s.op, err)
			}
		}
		if p.name == places[0].name {
			// the repo the write ran in keeps no row of the personal store
			if _, err := os.Stat(memPath(p.dir, mem)); err == nil {
				t.Errorf("%s: the personal row was written into the repo", p.name)
			}
		}
	}
	// the edits landed in the personal store's folder, nowhere else
	hits, _ := filepath.Glob(filepath.Join(storeDir, "beta", "*", "mem_*.json"))
	if len(hits) != len(places) {
		t.Errorf("me/beta holds %d moved notes, want %d", len(hits), len(places))
	}
	// an id no folder holds still answers not found, naming the places looked
	a := openWith(t, setUp, Options{Home: home})
	_, err := run(t, a, opsmemory.OpEdit, `{"id":"mem_01a1f9777db970bcae91072062779cf4","body":"x"}`, true)
	if !errs.Has(err, errs.NotFound) || !strings.Contains(err.Error(), "me/alpha") {
		t.Errorf("an unknown id: %v; want E_NOT_FOUND naming me/alpha among the folders looked in", err)
	}
}
