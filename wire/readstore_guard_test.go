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
	// devco D-506: reads of this machine or this checkout, never a store's rows
	"batch.list":       "this machine's batch log (<home>/state/batches), no store",
	"batch.show":       "this machine's batch log (<home>/state/batches), no store",
	"pii_pattern.list": "this machine's own PII patterns (the machine source), no store",
	"sync.conflicts":   "reports the conflicts of every store this checkout syncs, as sync.status",
	"sync.peek":        "reads another git ref of this checkout, never a store",
	"scope.list":       "this repo's folder scopes, keyed by this checkout's repo id",
	"scope.show":       "the scope chain of a path in this repo, keyed by this checkout's repo id",
	"branch.list":      "the branch layers of this checkout's code repo (its repo key), not a store's rows",
	"branch.show":      "one branch layer of this checkout's code repo (default the branch checked out)",
	"inbox.show":       "claims, gates and handoffs live on this repo's refs/lore/signals; a store clone refuses coordination",
	"mission.status":   "a mission's claims live on this repo's refs/lore/signals; a store clone refuses coordination",
}

// readsLeftForStore are reads that should take --store and do not yet
// (devco D-499 widened; the coordinator splits them across workers).
// Remove a name when its read takes the flag; the guard then holds it.
var readsLeftForStore = []string{}

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
	for _, name := range []string{"resource.list", "resource.show", "resource.kinds", "team.list", "team.show", "member.list", "member.show", "trash.list", "trash.show", "meta.show", "meta.get", "meta.keys", "rule.show", "task.show", "task.triage", "task.template.show", "task.view.list", "memory.show", "decision.show", "row.show",
		// D-506
		"commit.show", "link.list", "policy.show", "project.show", "repo.list", "row.markdown", "row.path", "secrets.allow.list", "secrets.list", "secrets.rules", "secrets.show"} {
		if !seen[name] {
			t.Errorf("%s is not a read in the registry", name)
		}
	}
}

// TestStoreRouterNamesTheReadScope pins devco D-507: `task show --store
// <org>` (an alias of --from, the field is "from") from a plain folder
// failed E_NOT_A_STORE because the outside-repo and store-clone resolvers
// read only a field named "store". An input naming its store through
// op.StoreRouter is scoped as --store is, under any field name.
func TestStoreRouterNamesTheReadScope(t *testing.T) {
	t.Parallel()
	a, err := New(Options{RepoDir: t.TempDir(), Home: t.TempDir(), FlagActor: testEmail, Wall: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	for _, tc := range []struct{ op, body, want string }{
		{"task.show", `{"id":"tsk_01a1e7ddb6977a5bab2a89e9ef46743f","from":"acmeteam/org"}`, "acmeteam/org"},
		{"task.triage", `{"store":"acmeteam"}`, "acmeteam"},
		{"task.recur.show", `{"id":"tsk_01a1e7ddb6977a5bab2a89e9ef46743f","store":"acmeteam"}`, "acmeteam"},
	} {
		e, ok := a.Registry().Lookup(tc.op)
		if !ok {
			t.Fatalf("%s is not in the registry", tc.op)
		}
		d := op.JSONDecoder{Body: []byte(tc.body)}
		in, derr := e.Decode(d)
		if derr != nil {
			t.Fatalf("%s: %v", tc.op, derr)
		}
		if got := scopeOf(in).store; got != tc.want {
			t.Errorf("%s: scopeOf store = %q, want %q", tc.op, got, tc.want)
		}
		if !routedStoreRead(e, d) {
			t.Errorf("%s: a read naming %s is not routed to that store", tc.op, tc.want)
		}
	}
}
