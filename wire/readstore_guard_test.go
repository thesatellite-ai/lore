package wire

import (
	"slices"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/op"
)

// readsWithoutStore are the reads that truly take no --store, each with
// why. A new read lands with --store (op.StoreFlag, or a filter of its
// own) or with a line here.
var readsWithoutStore = map[string]string{
	"audit.log":       "reads this machine's audit log, no store",
	"audit.verify":    "checks this machine's audit chain, no store",
	"live.diff":       "this repo's refs/lore/live, never a store",
	"live.log":        "this repo's refs/lore/live, never a store",
	"live.show":       "this repo's refs/lore/live, never a store",
	"migrate.status":  "this repo's migration state",
	"store.list":      "lists the stores themselves",
	"sync.run":        "syncs every store; `lore sync <store>` names one by argument",
	"sync.status":     "reports every store's sync state",
	"search.rebuild":  "rebuilds this machine's cache of every attached store",
	"search.status":   "reports this machine's cache",
	"machine.show":    "this machine's own row",
	"branch.mode":     "this repo's branch mode",
	"lint.run":        "lints this repo's files",
	"explain.run":     "explains a command, reads no rows",
	"workspace.show":  "this checkout's workspace",
	"sync.trash.list": "this machine's local sync trash",
}

// readsLeftForStore are reads that should take --store and do not yet
// (devco D-499 widened; the coordinator splits them across workers).
// Remove a name when its read takes the flag; the guard then holds it.
var readsLeftForStore = []string{
	"batch.list", "batch.show", "branch.list", "branch.show", "commit.show", "inbox.show", "link.list",
	"mission.status", "pii_pattern.list", "policy.show", "project.show", "repo.list", "row.markdown",
	"row.path", "scope.list", "scope.show", "secrets.allow.list", "secrets.list", "secrets.rules",
	"secrets.show", "sync.conflicts", "sync.peek", "task.checklist.list", "task.deferred", "task.export",
	"task.history", "task.recur.show", "task.someday", "task.template.list", "task.template.show",
	"task.time.show", "task.triage", "task.view.list", "task.view.show",
}

// TestEveryReadTakesStore walks the operation registry (devco D-499,
// widened by the owner: `resource list --kind code_repo --store <org>/org`
// said "unknown flag"): every read takes --store <org>[/<folder>] unless
// readsWithoutStore says why it cannot, or it is still in
// readsLeftForStore. A left read that gained the flag must leave the list.
func TestEveryReadTakesStore(t *testing.T) {
	t.Parallel()
	a, err := New(Options{RepoDir: t.TempDir(), Home: t.TempDir(), FlagActor: testEmail, Wall: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	seen := map[string]bool{}
	for _, e := range a.Registry().Entries() {
		s := e.Spec()
		if !s.OnlyReads() || s.Local {
			continue
		}
		seen[s.Name] = true
		has := slices.ContainsFunc(e.InputFields(), func(f op.FieldInfo) bool {
			return f.Name == readStoreField || f.Name == "to" || f.Flag == readStoreField || slices.Contains(f.Aliases, readStoreField)
		})
		_, exempt := readsWithoutStore[s.Name]
		left := slices.Contains(readsLeftForStore, s.Name)
		switch {
		case has && (exempt || left):
			t.Errorf("%s takes --store now: take it out of readsWithoutStore / readsLeftForStore", s.Name)
		case !has && !exempt && !left:
			t.Errorf("%s is a read with no --store: embed op.StoreFlag (or give it a store filter), or say in readsWithoutStore why it cannot take one", s.Name)
		}
	}
	for name := range readsWithoutStore {
		if !seen[name] {
			t.Errorf("readsWithoutStore names %s, which is no read any more", name)
		}
	}
	for _, name := range readsLeftForStore {
		if !seen[name] {
			t.Errorf("readsLeftForStore names %s, which is no read any more", name)
		}
	}
	// the owner's families are done and stay done
	for _, name := range []string{"resource.list", "resource.show", "resource.kinds", "team.list", "team.show", "member.list", "member.show", "trash.list", "trash.show", "meta.show", "meta.get", "meta.keys", "rule.show", "task.show", "memory.show", "decision.show", "row.show"} {
		if !seen[name] {
			t.Errorf("%s is not a read in the registry", name)
		}
	}
}
