//go:build userscen

package userscen

// sync_status_test.go — what `lore sync status --json` says about the data
// folder, and what `lore store doctor` says in a repo whose stores are
// attached in .lore/stores.yaml (not org stores).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// syncStatusFacts is the data-folder part of `lore sync status --json`.
type syncStatusFacts struct {
	Data struct {
		DataDirPresent bool `json:"data_dir_present"`
		TrackedFiles   int  `json:"tracked_files"`
	} `json:"data"`
}

// A script reading sync status is told the truth about .lore/data: present
// with 0 tracked files before the first commit, and git's tracked count
// after it (the same numbers doctor prints; both were always false/0).
func TestSyncStatus_DataFolderFacts(t *testing.T) {
	scenParallel(t)
	w := newWorld(t)
	p := w.Human("alice", "alice@example.com")
	p.WriteConfig(th.Config{Live: th.FreshFetch()})
	c := p.Clone(w.Remote(syncSlugApp), "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	c.MustLore("memory", "add", "--body", "a note")
	before := th.DecodeJSON[syncStatusFacts](t, c.MustLore("sync", "status", "--json"))
	if !before.Data.DataDirPresent || before.Data.TrackedFiles != 0 {
		t.Fatalf("before the first commit: want data_dir_present true, tracked_files 0; got %+v", before.Data)
	}
	c.CommitAll("lore")
	tracked := len(splitLines(c.Git("ls-files", "--", ".lore/data")))
	after := th.DecodeJSON[syncStatusFacts](t, c.MustLore("sync", "status", "--json"))
	if !after.Data.DataDirPresent || after.Data.TrackedFiles != tracked || tracked == 0 {
		t.Fatalf("after the commit: want data_dir_present true, tracked_files %d; got %+v", tracked, after.Data)
	}
}

// store doctor checks every store the repo and machine use, not only the
// v0.1.17 org stores: a store attached in .lore/stores.yaml is listed
// with where it stands, and sync status prints one line per store (items
// 6 and 7).
func TestSyncStatus_StoreDoctorChecksAttachedStores(t *testing.T) {
	scenParallel(t)
	w := newWorld(t)
	p := w.Human("alice", "alice@example.com")
	c := p.Clone(w.Remote(syncSlugApp), "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	c.MustLore("store", "init", "notes", w.Remote(syncSlugStore).URL(), "--yes")
	c.MustLore("memory", "list", "--store", "notes")
	doc := c.MustLore("store", "doctor")
	doc.Says(t, "stores this repo and machine use:", "✓ notes (stores.yaml, git)", "ahead 0, behind 0 of origin/", "clones of the org stores in ~/.lore/config.yaml")
	if strings.Contains(doc.Stdout, "direct pushes") || strings.Contains(doc.Stdout, "no org stores configured") {
		t.Fatalf("store doctor still prints the old lines:\n%s", doc.Stdout)
	}
	c.MustLore("sync", "status").Says(t, "stores:", "  notes: ahead 0, behind 0 of origin/")
}

// splitLines is the non-empty lines of s.
func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// syncRefusals is the store_refusals part of `lore sync status --json`.
type syncRefusals struct {
	Data struct {
		StoreRefusals []struct {
			Store  string   `json:"store"`
			Code   string   `json:"code"`
			Reason string   `json:"reason"`
			Rows   []string `json:"rows"`
			Next   string   `json:"next"`
		} `json:"store_refusals"`
	} `json:"data"`
}

// A store whose remote refuses the push (a server-side rule): the write
// stays committed in the local clone, sync status names the store, the
// reason and the waiting row; once the rule is lifted, lore sync sends it
// and the refusal is gone.
func TestSyncStatus_StoreRefusedPush(t *testing.T) {
	st := newSyncTeam(t, 1)
	a := st.clones[0]
	r := st.w.Remote(syncSlugStore)
	a.MustLore("store", "init", "notes", r.URL(), "--yes")
	hook := filepath.Join(r.Dir, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'error: pushes to this store need a review' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mem := th.DecodeJSON[syncAddOut](t, a.Lore("memory", "add", "kept while refused", "--to", "notes", "--json")).ID
	if mem == "" {
		t.Fatal("the refused write must still answer its id (committed locally)")
	}
	a.Lore("sync") // C5: the push (refused by the hook) happens on lore sync
	for _, f := range r.Files(syncStoreRef) {
		if strings.Contains(f, mem) {
			t.Fatal("a refused push cannot be on the store remote")
		}
	}
	s := th.DecodeJSON[syncRefusals](t, a.MustLore("sync", "status", "--json"))
	if len(s.Data.StoreRefusals) != 1 || s.Data.StoreRefusals[0].Store != "notes" || s.Data.StoreRefusals[0].Next == "" {
		t.Fatalf("sync status must name the refused store: %+v", s.Data.StoreRefusals)
	}
	a.MustLore("sync", "status").Says(t, "refused pushes:   1", "store notes")
	if src, ok := syncMemoryStore(t, a, mem); !ok || src != "notes" {
		t.Fatal("the author still reads the refused row from the local clone")
	}

	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	rep := th.DecodeJSON[syncReport](t, a.MustLore("sync", "--json"))
	if len(rep.Data.Stores) != 1 || rep.Data.Stores[0].Sent == 0 {
		t.Fatalf("with the rule lifted, sync sends the waiting commit: %+v", rep.Data.Stores)
	}
	found := false
	for _, f := range r.Files(syncStoreRef) {
		found = found || strings.Contains(f, mem)
	}
	if !found {
		t.Fatal("the row must be on the store remote after the sync")
	}
	s = th.DecodeJSON[syncRefusals](t, a.MustLore("sync", "status", "--json"))
	if len(s.Data.StoreRefusals) != 0 {
		t.Fatalf("a push that landed clears the refusal: %+v", s.Data.StoreRefusals)
	}
}
