//go:build stories && !windows

package stories

// WU_claimpush_test.go — owner use pass 2 wrong 2 (devco D-580..D-582) on
// WW's world: in a linked repo, `task claim` pushed the store's signals ref
// to the store's remote without --push, authored by "<person> via <tool>",
// and `task release` said only "released" and left the remote lease.
//
//	D-580: claim without --push is refused and the store remote is unchanged
//	D-581: the pushed signals commit carries the org identity, never an agent
//	D-582: release takes the claim off the store remote and says so

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// TestStoryWUClaimPushOnlyWhenAsked walks claim and release in the linked repo.
func TestStoryWUClaimPushOnlyWhenAsked(t *testing.T) {
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
	m.SetEnv(teamharness.EnvPushClaims, "0") // no standing go-ahead: only --push asks
	id := wwJSONID(m.Lore(app, "task", "add", "--title", "claim push check", "--json").OK(t).Stdout)
	before := store.Rev(ref)

	r := m.Lore(app, "task", "claim", "--id", id)
	t.Logf("claim without --push:\n%s", r)
	if r.Code == 0 || !strings.Contains(r.Out(), "--push") {
		t.Errorf("devco:D-580: a claim in the linked repo without --push was not refused:\n%s", r)
	}
	if got := store.Rev(ref); got != before {
		t.Errorf("devco:D-580: the store remote's %s moved without --push (%q -> %q)", ref, before, got)
	}

	r = m.Lore(app, "task", "claim", "--id", id, "--push").OK(t)
	t.Logf("claim --push:\n%s", r)
	claimed := store.Rev(ref)
	if claimed == "" || claimed == before {
		t.Fatalf("devco:D-580: claim --push did not reach the store remote's %s", ref)
	}
	author, err := exec.Command("git", "-C", store.Dir, "log", "-1", "--format=%an <%ae>", ref).Output()
	if err != nil {
		t.Fatal(err)
	}
	if a := strings.TrimSpace(string(author)); a != wwAcme.Name+" <"+wwAcme.Email+">" {
		t.Errorf("devco:D-581: the store's signals commit is authored %q, want the org identity %s <%s>", a, wwAcme.Name, wwAcme.Email)
	}
	if !hasClaimFile(store, ref, id) {
		t.Fatalf("the store remote's signals ref holds no claim of %s: %v", id, store.Files(ref))
	}

	r = m.Lore(app, "task", "release", "--id", id).OK(t)
	t.Logf("release:\n%s", r)
	if !strings.Contains(r.Out(), "pushed to the store's remote") {
		t.Errorf("devco:D-582: release does not say where the release went:\n%s", r)
	}
	if hasClaimFile(store, ref, id) {
		t.Errorf("devco:D-582: after release the store remote still holds the claim of %s: %v", id, store.Files(ref))
	}
}

// hasClaimFile reports a claim file of task id on ref of r.
func hasClaimFile(r *teamharness.Remote, ref, id string) bool {
	for _, f := range r.Files(ref) {
		if strings.Contains(f, id) && strings.Contains(f, "claims/") {
			return true
		}
	}
	return false
}
