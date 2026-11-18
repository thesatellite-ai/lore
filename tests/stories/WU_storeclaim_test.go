//go:build stories && !windows

package stories

// WU_storeclaim_test.go — owner use pass 3 weak 16 (devco:D-635) on WW's
// world: a store task's claim lives on the store's signals ref, yet
// `task claim --id <id>` from the home folder said coordination runs in a
// code repo, and inside the store clone's folder it said the folder is a
// store clone, not a code repo. Now claim and release of a store task
// run from home and from the store clone alike, on the store's signals
// ref, with the same --push rule (D-580) and release (D-582).

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// TestStoryWUStoreTaskClaimAnywhere claims a store task from home, sees the
// claim from the linked repo, and releases it from the store clone.
// (task next --setup from home still refuses as a code-repo read: left.)
func TestStoryWUStoreTaskClaimAnywhere(t *testing.T) {
	t.Parallel()
	w := wwBuild(t)
	m := w.m
	app := w.places["own-linked"].Dir
	folder := w.places["store-folder"].Dir
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
	m.SetEnv(teamharness.EnvPushClaims, "0") // no standing go-ahead: only --push asks
	id := wwJSONID(m.Lore(app, "task", "add", "--title", "claim from home", "--json").OK(t).Stdout)
	m.Lore(app, "sync").OK(t)
	before := store.Rev(ref)

	// printed: each output whose next steps must work where printed
	type printedAt struct{ dir, out string }
	var printed []printedAt
	r := m.Lore(m.Home, "task", "claim", "--id", id)
	t.Logf("home: claim without --push:\n%s", r)
	printed = append(printed, printedAt{m.Home, r.Out()})
	if r.Code == 0 || !strings.Contains(r.Out(), "--push") || strings.Contains(r.Out(), "runs in a code repo") {
		t.Errorf("devco:D-635: a store task's claim from home without --push: want the D-580 refusal naming --push:\n%s", r)
	}
	if got := store.Rev(ref); got != before {
		t.Errorf("devco:D-635: the store remote's %s moved without --push (%q -> %q)", ref, before, got)
	}

	r = m.Lore(m.Home, "task", "claim", "--id", id, "--push")
	t.Logf("home: claim --push:\n%s", r)
	if r.Code != 0 {
		t.Fatalf("devco:D-635: a store task's claim from home with --push refused:\n%s", r)
	}
	if !hasClaimFile(store, ref, id) {
		t.Fatalf("devco:D-635: the store remote's signals ref holds no claim of %s after claim --push from home: %v", id, store.Files(ref))
	}
	// the linked repo (a teammate's view of the same store) sees the claim
	r = m.Lore(app, "task", "show", "--id", id).OK(t)
	if !strings.Contains(r.Out(), "claim:    active") {
		t.Errorf("devco:D-635: the linked repo does not see the claim taken from home:\n%s", r)
	}

	r = m.Lore(folder, "task", "release", "--id", id)
	t.Logf("store clone: release:\n%s", r)
	printed = append(printed, printedAt{folder, r.Out()})
	if r.Code != 0 {
		t.Fatalf("devco:D-635: a store task's release inside the store clone refused:\n%s", r)
	}
	if hasClaimFile(store, ref, id) {
		t.Errorf("devco:D-635: after release the store remote still holds the claim of %s: %v", id, store.Files(ref))
	}

	// GUARD: every command these outputs print works exactly where printed
	// (a template's <id> and <org> filled in with this task and org)
	fill := strings.NewReplacer("<id>", id, "<org>", wOrg)
	for _, p := range printed {
		for _, c := range wuBackticked(fill.Replace(p.out)) {
			r = m.Lore(p.dir, c...)
			t.Logf("printed command lore %s:\n%s", strings.Join(c, " "), r)
			if r.Code != 0 {
				t.Errorf("devco:D-635: the printed command `lore %s` fails where printed (%s):\n%s", strings.Join(c, " "), p.dir, r)
			}
		}
	}
}

// wuBackticked is every `lore …` command in backticks in out, as
// arguments (without "lore"); one still holding a placeholder is skipped.
func wuBackticked(out string) [][]string {
	var cmds [][]string
	parts := strings.Split(out, "`")
	for i := 1; i < len(parts); i += 2 {
		line, ok := strings.CutPrefix(strings.TrimSpace(parts[i]), "lore ")
		if !ok || strings.ContainsAny(line, "<>…") {
			continue
		}
		cmds = append(cmds, wuSplit(line))
	}
	return cmds
}
