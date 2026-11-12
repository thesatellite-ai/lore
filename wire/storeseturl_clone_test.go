package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/engine/attach"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestSetStoreURLCarriesUnpushedClone (B3-15): a note written while the
// store's old address was gone is a commit in the old clone only. set-url
// carries that clone to the new address (no second clone), so the next
// sync pushes the note there. A stranded clone an older lore left (the
// places moved by hand, a new clone made) is a doctor warning whose fix,
// set-url run again, carries it the same way and drops the new clone.
func TestSetStoreURLCarriesUnpushedClone(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	oldURL, oldDir := bareStore(t, hosts, "acme/kb")
	home := t.TempDir()
	repo := gitRepo(t)
	writeStoresYAML(t, repo, "stores: []\n")
	a := openWith(t, repo, Options{Home: home})
	withProject(t, a)
	if _, err := run(t, a, opsstores.OpInit, `{"name":"kb","url":"`+oldURL+`"}`, true); err != nil {
		t.Fatalf("init: %v", err)
	}
	_ = a.Close()
	a = openWith(t, repo, Options{Home: home})
	histAddMemory(t, a, "kb", "pushed before the move")
	_ = a.Close()

	newURL := "https://" + histHost + "/acme/kb-moved"
	newDir := filepath.Join(hosts, "acme", "kb-moved")
	if err := os.Rename(oldDir, newDir); err != nil {
		t.Fatal(err)
	}
	a = openWith(t, repo, Options{Home: home})
	waiting := histAddMemory(t, a, "kb", "written while the old address was gone")
	res, err := run(t, a, opsstores.OpSetURL, `{"name":"kb","url":"`+newURL+`"}`, true)
	if err != nil {
		t.Fatalf("set-url: %v", err)
	}
	var out opsstores.SetURLOut
	if err = json.Unmarshal([]byte(res), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Clone, "unpushed commit(s)") {
		t.Fatalf("set-url did not carry the old clone: %+v", out)
	}
	if _, err = a.SyncStores(background(), "kb"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if tree := gitIn(t, newDir, nil, "ls-tree", "-r", "--name-only", "main"); !strings.Contains(tree, schema.TableMemories+"/"+waiting+".json") {
		t.Fatalf("the waiting note never reached the new address:\n%s", tree)
	}
	found, err := gitbackend.ScanClones(home)
	if err != nil || len(found) != 1 {
		t.Fatalf("want one clone of kb, got %+v (%v)", found, err)
	}
	if f, _ := a.DoctorStoreClones(background()); len(f) != 0 {
		t.Fatalf("doctor after the carry: %+v", f)
	}
	_ = a.Close()

	// What an older lore left: the store moved again by hand (stores.yaml
	// and the address record), a new clone made at the new address, the
	// waiting note stranded in the old clone.
	nextURL := "https://" + histHost + "/acme/kb-next"
	nextDir := filepath.Join(hosts, "acme", "kb-next")
	if err = os.Rename(newDir, nextDir); err != nil {
		t.Fatal(err)
	}
	a = openWith(t, repo, Options{Home: home})
	stranded := histAddMemory(t, a, "kb", "stranded by an older lore")
	_ = a.Close()
	writeStoresYAML(t, repo, "stores:\n  - name: kb\n    url: "+nextURL+"\n")
	a = openWith(t, repo, Options{Home: home})
	if _, err = a.storeIDAt(background(), attach.Store{Name: "kb", URL: nextURL}); err != nil { // makes the new clone
		t.Fatalf("open the new address: %v", err)
	}
	if err = recordStoreAddress(home, out.ID, nextURL, a.opts.Wall()); err != nil {
		t.Fatal(err)
	}
	f, err := a.DoctorStoreClones(background())
	if err != nil || len(f) != 1 || !strings.Contains(f[0].Message, "unpushed commit(s)") || f[0].Fix != "lore store set-url "+out.ID+" "+nextURL {
		t.Fatalf("doctor on a stranded clone: %+v (%v)", f, err)
	}
	if _, err = run(t, a, opsstores.OpSetURL, `{"name":"`+out.ID+`","url":"`+nextURL+`"}`, true); err != nil {
		t.Fatalf("set-url (the doctor's fix): %v", err)
	}
	_ = a.Close() // the next command, as on the command line
	a = openWith(t, repo, Options{Home: home})
	if _, err = a.SyncStores(background(), "kb"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if tree := gitIn(t, nextDir, nil, "ls-tree", "-r", "--name-only", "main"); !strings.Contains(tree, schema.TableMemories+"/"+stranded+".json") {
		t.Fatalf("the stranded note never reached the new address:\n%s", tree)
	}
	if found, _ = gitbackend.ScanClones(home); len(found) != 1 {
		t.Fatalf("want one clone after the fix, got %+v", found)
	}
	if rec := gitbackend.ReadCloneRecord(home).Clones; len(rec) != 1 {
		t.Fatalf("the clone record keeps the old address: %+v", rec)
	}
	if f, _ = a.DoctorStoreClones(background()); len(f) != 0 {
		t.Fatalf("doctor after the fix: %+v", f)
	}
}
