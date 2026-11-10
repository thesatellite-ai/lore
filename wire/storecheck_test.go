package wire

// storecheck_test.go — item 6 and 7: store doctor checks every store this
// repo and machine use, and sync status prints where each stands.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// checkOf is the check of the store named name.
func checkOf(t *testing.T, list []opsstores.StoreCheck, name string) opsstores.StoreCheck {
	t.Helper()
	i := slices.IndexFunc(list, func(c opsstores.StoreCheck) bool { return c.Store == name })
	if i < 0 {
		t.Fatalf("no check of %s in %+v", name, list)
	}
	return list[i]
}

// hasProblem reports whether c has a problem containing part.
func hasProblem(c opsstores.StoreCheck, part string) bool {
	return slices.ContainsFunc(c.Problems, func(p string) bool { return strings.Contains(p, part) })
}

// TestCheckStores: the personal store and a stores.yaml git store are
// checked like any other; unpushed commits (count and age) and an
// unreachable remote are problems; a push clears them and records the
// push time; a git store never cloned here is a note; a folder store whose
// folder lost its marker is a problem.
func TestCheckStores(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	meURL, meDir := bareStore(t, hosts, "acme/me")
	farURL, _ := bareStore(t, hosts, "acme/far")
	repo, home := gitRepo(t), t.TempDir()
	a := openWith(t, repo, Options{Home: home})
	withProject(t, a)
	if _, err := run(t, a, opsstores.OpInit, `{"name":"me","url":"`+meURL+`"}`, true); err != nil {
		t.Fatalf("init me: %v", err)
	}
	a = openWith(t, repo, Options{Home: home}) // a new command sees the new store
	// the core App has no push-after-write: the write waits in the clone
	if _, err := run(t, a, opsmemory.OpAdd, `{"body":"placeholder waiting note","to":"me"}`, true); err != nil {
		t.Fatalf("add: %v", err)
	}
	gone := t.TempDir()
	writeStoresYAML(t, repo, "stores:\n  - name: far\n    url: "+farURL+"\n  - name: lost\n    url: file://"+filepath.ToSlash(gone)+"\n")

	list := a.CheckStores(background())
	me := checkOf(t, list, "me")
	if me.Source != opsstores.SourcePersonal || me.Kind != opsstores.StoreKindGit || !me.Reachable || me.Clone != "whole" ||
		me.Unpushed < 1 || me.OldestUnpushed == "" || !hasProblem(me, "not pushed, the oldest") {
		t.Fatalf("me with an unpushed write: %+v", me)
	}
	if far := checkOf(t, list, "far"); far.Clone != cloneNotCloned || len(far.Problems) != 0 || len(far.Notes) != 1 {
		t.Fatalf("a store never cloned here is a note: %+v", far)
	}
	if lost := checkOf(t, list, "lost"); lost.Kind != opsstores.StoreKindFolder || lost.Reachable || !hasProblem(lost, "no lore store marker") {
		t.Fatalf("a folder store without its marker: %+v", lost)
	}

	if _, err := a.SyncStores(background(), "me"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	me = checkOf(t, a.CheckStores(background()), "me")
	if len(me.Problems) != 0 || me.Unpushed != 0 || me.LastPush == "" {
		t.Fatalf("me after the push: %+v", me)
	}

	if err := os.Rename(meDir, meDir+".away"); err != nil {
		t.Fatal(err)
	}
	me = checkOf(t, a.CheckStores(background()), "me")
	if me.Reachable || !hasProblem(me, "unreachable: ") || hasProblem(me, "[E_") {
		t.Fatalf("me with its remote gone: %+v", me)
	}
}

// TestSyncStoreStates (item 7): sync status has one line and one data
// entry per store: ahead/behind of its remote and the last push.
func TestSyncStoreStates(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	kbURL, _ := bareStore(t, hosts, "acme/kb")
	repo, home := gitRepo(t), t.TempDir()
	a := openWith(t, repo, Options{Home: home})
	withProject(t, a)
	if _, err := run(t, a, opsstores.OpInit, `{"name":"kb","url":"`+kbURL+`"}`, true); err != nil {
		t.Fatalf("init: %v", err)
	}
	a = openWith(t, repo, Options{Home: home})
	if _, err := a.SyncStores(background(), "kb"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := run(t, a, opsmemory.OpAdd, `{"body":"placeholder note","to":"kb"}`, true); err != nil {
		t.Fatalf("add: %v", err)
	}
	var out opssync.StatusOut
	a.syncStoreStates(background(), &out)
	if len(out.Data.Stores) != 1 {
		t.Fatalf("stores: %+v", out.Data.Stores)
	}
	st := out.Data.Stores[0]
	if st.Store != "kb" || st.Kind != opssync.StoreKindGit || !st.Cloned || st.Branch == "" || st.Ahead < 1 || st.Behind != 0 || st.OldestUnpushed == "" {
		t.Fatalf("kb with an unpushed write: %+v", st)
	}
	text := strings.Join(out.Lines, "\n")
	if !strings.Contains(text, "stores:\n  kb: ahead ") || !strings.Contains(text, "behind 0 of origin/") {
		t.Fatalf("status lines:\n%s", text)
	}
}
