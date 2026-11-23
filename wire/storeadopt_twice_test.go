package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// TestStoreAdoptSaysWhatItLeavesHeldTwice (devco:D-939, bug hunt H-200):
// a plain adopt into a folder of a store the repo attaches whole leaves
// the repo's copies live beside the folder's, so every command by id on
// one answers E_AMBIGUOUS_ID and doctor is DEGRADED (copied_id); adopt
// said nothing. Its dry run and its run now count those rows and name the
// one command that settles them (the same adopt with --remove-source);
// that rerun counts none and keeps the actor row the rows left in the
// repo were saved by (removed, doctor's actor_roster warned).
func TestStoreAdoptSaysWhatItLeavesHeldTwice(t *testing.T) {
	t.Parallel()
	repo, _, _, _ := folderStoreRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	// the writer's actor row, as a real repo holds it (the test identity
	// writes none); the project row left in the repo was saved by it
	actorFile := filepath.Join(repo, ".lore", "data", "actors", testActor+".json")
	if err := os.MkdirAll(filepath.Dir(actorFile), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(actorFile, []byte(`{"_hlc": "2026-11-01T09:00:00.000Z/0000/`+testActor+`", "_table": "actors", "_v": 2, "created_at": "2026-11-01T09:00:00.000000000Z", "created_by": "`+testActor+`", "display_name": "dana", "id": "`+testActor+`", "kind": "human", "updated_at": "2026-11-01T09:00:00.000000000Z", "updated_by": "`+testActor+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if prj, _ := filepath.Glob(filepath.Join(repo, ".lore", "data", "projects", "*.json")); len(prj) != 1 || !strings.Contains(string(mustRead(t, prj[0])), testActor) {
		t.Fatalf("fixture: the project row is not saved by the test actor: %v", prj)
	}
	if _, err := run(t, a, opsstores.OpFolderAdd, `{"store":"northwind","folder":"client_x"}`, true); err != nil {
		t.Fatal(err)
	}
	raw, err := run(t, a, opsmemory.OpAdd, `{"body":"the invoice queue drains hourly","to":"repo"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	mem := idOf(t, raw)
	if _, serr := os.Stat(filepath.Join(repo, ".lore", "data", "memories", mem+".json")); serr != nil {
		t.Fatalf("fixture: the memory is not a repo row file: %v", serr)
	}
	const settle = "lore store adopt --target northwind/client_x --remove-source --yes"

	dry, err := adoptRun(t, a, `{"target":"northwind/client_x","dry_run":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if dry.HeldTwice != 1 || dry.Settle != settle {
		t.Fatalf("dry run: held_twice %d settle %q, want 1 and %q", dry.HeldTwice, dry.Settle, settle)
	}
	if txt := dry.Text(); !strings.Contains(txt, "1 row would stay live in") || !strings.Contains(txt, settle) {
		t.Fatalf("dry run text:\n%s", txt)
	}
	out, err := adoptRun(t, a, `{"target":"northwind/client_x"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if out.HeldTwice != 1 || out.Settle != settle || !strings.Contains(out.Text(), "1 row now held twice") {
		t.Fatalf("adopt: held_twice %d settle %q\n%s", out.HeldTwice, out.Settle, out.Text())
	}

	rm, err := adoptRun(t, a, `{"target":"northwind/client_x","remove_source":true}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if rm.HeldTwice != 0 || rm.Settle != "" || strings.Contains(rm.Text(), "held twice") {
		t.Fatalf("--remove-source still says held twice: %+v", rm)
	}
	if _, serr := os.Stat(filepath.Join(repo, ".lore", "data", "memories", mem+".json")); !os.IsNotExist(serr) {
		t.Fatalf("the adopted memory is still in the repo: %v", serr)
	}
	if _, serr := os.Stat(actorFile); serr != nil {
		t.Fatal("--remove-source removed the actor row the repo's project row was saved by")
	}
	again, err := adoptRun(t, a, `{"target":"northwind/client_x","dry_run":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if again.HeldTwice != 0 || again.Settle != "" {
		t.Fatalf("after settling: %+v", again)
	}
}
