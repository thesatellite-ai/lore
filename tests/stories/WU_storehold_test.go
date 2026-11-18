//go:build stories && !windows

package stories

// WU_storehold_test.go — devco:D-672 and D-673 on WW's world (owner use
// pass 3 leftovers). D-672: `hold claim` / `hold release` from the home
// folder refused with the git hint, though a resource an org shares
// belongs on its store's signals ref; now --store <org>/<folder> holds it
// there (--push as for a claim), the linked code repo of that folder (where a
// teammate holds it) reaches the same hold, and the release from home takes it off the store's
// remote. D-673: a code repo's `task heartbeat --daemon` renewed only that
// repo's claims; a store claim taken from home was left to expire. Now a
// repo with no claims of its own runs the store daemon, and --id of a
// store claim renews it from the code repo.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// hasHoldFile reports a hold of name on remote r's signals ref.
func hasHoldFile(r *teamharness.Remote, ref, name string) bool { return holdFiles(r, ref, name) > 0 }

// holdFiles counts the holds of name on remote r's signals ref (one per
// folder holding it).
func holdFiles(r *teamharness.Remote, ref, name string) int {
	n := 0
	for _, f := range r.Files(ref) {
		if strings.HasSuffix(f, "resources/"+name+".json") {
			n++
		}
	}
	return n
}

// TestStoryWUStoreHoldFromHome is the member holding the org's staging
// slot from the home folder, the teammate seeing it, and the release.
func TestStoryWUStoreHoldFromHome(t *testing.T) {
	t.Parallel()
	w := wwBuild(t)
	m := w.m
	app := w.places["own-linked"].Dir
	var store *teamharness.Remote
	for _, r := range w.remotes {
		if r.Slug == wStoreSlug {
			store = r
		}
	}
	if store == nil {
		t.Fatal("no team store remote in the WW world")
	}
	const ref = "refs/lore/signals/main"
	const slot = "staging"
	m.SetEnv(teamharness.EnvPushClaims, "0") // no standing go-ahead: only --push asks
	m.Lore(app, "task", "add", "--title", "hold and daemon", "--json").OK(t)
	m.Lore(app, "sync").OK(t)
	at := wOrg + "/" + wFolder

	type printedAt struct{ dir, out string }
	var printed []printedAt
	look := func(dir string, args ...string) teamharness.Result {
		r := m.Lore(dir, args...)
		t.Logf("%s: lore %s:\n%s", dir, strings.Join(args, " "), r)
		printed = append(printed, printedAt{dir, r.Out()})
		return r
	}

	// from home with no place named: the refusal names --store
	if r := look(m.Home, "hold", "claim", "--name", slot); r.Code == 0 || !strings.Contains(r.Out(), "--store") {
		t.Errorf("devco:D-672: hold claim from home with no --store: want a refusal naming --store, got:\n%s", r)
	}
	// D-580: without --push nothing is written
	before := store.Rev(ref)
	if r := look(m.Home, "hold", "claim", "--name", slot, "--store", at); r.Code == 0 || !strings.Contains(r.Out(), "--push") {
		t.Errorf("devco:D-672: hold claim --store without --push: want the D-580 refusal, got:\n%s", r)
	}
	if got := store.Rev(ref); got != before {
		t.Errorf("devco:D-672: the store remote's %s moved without --push", ref)
	}
	r := look(m.Home, "hold", "claim", "--name", slot, "--store", at, "--push")
	if r.Code != 0 || !hasHoldFile(store, ref, slot) {
		t.Fatalf("devco:D-672: hold claim --store %s --push from home put no hold on the store remote (%v):\n%s", at, store.Files(ref), r)
	}
	// the linked code repo of that folder (where a teammate holds it)
	// reaches the same hold: claiming again there renews that one file
	if r = look(app, "hold", "claim", "--name", slot, "--push"); r.Code != 0 || holdFiles(store, ref, slot) != 1 {
		t.Errorf("devco:D-672: the linked repo of %s does not reach the hold taken from home (%v):\n%s", at, store.Files(ref), r)
	}
	// release from home clears what the pushed hold published
	if r = look(m.Home, "hold", "release", "--name", slot, "--store", at); r.Code != 0 || hasHoldFile(store, ref, slot) {
		t.Errorf("devco:D-672: hold release --store from home left the hold (%v):\n%s", store.Files(ref), r)
	}
	if r = look(app, "hold", "claim", "--name", slot, "--push"); r.Code != 0 {
		t.Errorf("devco:D-672: the teammate cannot take %s after the release:\n%s", slot, r)
	} else {
		m.Lore(app, "hold", "release", "--name", slot).OK(t)
	}

	// devco:D-673: a store claim taken from home, kept alive by the code
	// repo's daemon (the repo holds no claim of its own) and by --id there
	r = m.Lore(m.Home, "task", "next", "--claim", "--push", "--setup", wSetup, "--json")
	var got struct {
		Claimed string `json:"claimed"`
	}
	_ = json.Unmarshal([]byte(r.Stdout), &got)
	id := got.Claimed
	if r.Code != 0 || id == "" {
		t.Fatalf("devco:D-673: no store task claimed from home:\n%s", r)
	}
	for _, args := range [][]string{nil, {"--id", id}} {
		pre := store.Rev(ref)
		dr, stopped := m.LoreFor(app, wuDaemonFor, append([]string{"task", "heartbeat", "--daemon"}, args...)...)
		t.Logf("%s: lore task heartbeat --daemon %v (stopped %v):\n%s", app, args, stopped, dr)
		if !stopped || store.Rev(ref) == pre {
			t.Errorf("devco:D-673: the code repo's task heartbeat --daemon %v left the store claim %s to expire (running %v):\n%s", args, id, stopped, dr)
		}
	}
	m.Lore(m.Home, "task", "release", "--id", id).OK(t)

	// GUARD: every command these outputs print works exactly where printed
	fill := strings.NewReplacer("<org>/<folder>", at, "<org>", wOrg)
	for _, p := range printed {
		for _, c := range wuBackticked(fill.Replace(p.out)) {
			r = m.Lore(p.dir, c...)
			t.Logf("printed command lore %s:\n%s", strings.Join(c, " "), r)
			if r.Code != 0 {
				t.Errorf("devco:D-672: the printed command `lore %s` fails where printed (%s):\n%s", strings.Join(c, " "), p.dir, r)
			}
			if len(c) > 1 && c[0] == "hold" && c[1] == "claim" {
				rel := []string{"hold", "release", "--name", slot}
				if i := indexOf(c, "--store"); i > 0 && i+1 < len(c) {
					rel = append(rel, "--store", c[i+1])
				}
				m.Lore(p.dir, rel...)
			}
		}
	}
}

// indexOf is the index of v in xs, -1 when absent.
func indexOf(xs []string, v string) int {
	for i, x := range xs {
		if x == v {
			return i
		}
	}
	return -1
}
