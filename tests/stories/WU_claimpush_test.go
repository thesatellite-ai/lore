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
//	D-639: release is the named exemption to --push (its help says so):
//	       it pushes only to clear what a pushed claim published, so a
//	       release with no pushed claim leaves the remote as it was
//	D-657: a heartbeat and a hold release with nothing pushed leave the
//	       remote as it was; after a pushed claim and a pushed hold they
//	       move it (the renewal and the release reach the remote)
//	D-669: the refusal prints the exact command that pushes (the real id,
//	       --push), run here as printed; a hold's refusal prints the hold
//	       again with --push and no task step

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

	// D-639: the claim was refused, so nothing of it is on the remote: a
	// release then pushes nothing either
	r = m.Lore(app, "task", "release", "--id", id)
	t.Logf("release with no pushed claim:\n%s", r)
	if got := store.Rev(ref); got != before {
		t.Errorf("devco:D-639: a release with no pushed claim moved the store remote's %s (%q -> %q):\n%s", ref, before, got, r)
	}
	help := m.Lore(app, "task", "release", "--help").OK(t).Out()
	if !strings.Contains(help, "no --push") || !strings.Contains(help, "pushed claim") {
		t.Errorf("devco:D-639: task release --help does not say it pushes with no --push, and why:\n%s", help)
	}

	// D-657: with no pushed claim and no pushed hold, a heartbeat and a
	// hold release have nothing on the remote to renew or clear: the
	// store remote's signals ref stays where it was
	r = m.Lore(app, "task", "heartbeat", "--id", id)
	t.Logf("heartbeat with no pushed claim:\n%s", r)
	if got := store.Rev(ref); got != before {
		t.Errorf("devco:D-657: a heartbeat with no pushed claim moved the store remote's %s (%q -> %q):\n%s", ref, before, got, r)
	}
	r = m.Lore(app, "hold", "claim", "--name", wucHold)
	t.Logf("hold claim without --push:\n%s", r)
	if r.Code == 0 || !strings.Contains(r.Out(), "--push") {
		t.Errorf("devco:D-657: a hold in the linked repo without --push was not refused:\n%s", r)
	}
	r = m.Lore(app, "hold", "release", "--name", wucHold)
	t.Logf("hold release with no pushed hold:\n%s", r)
	if got := store.Rev(ref); got != before {
		t.Errorf("devco:D-657: a hold release with no pushed hold moved the store remote's %s (%q -> %q):\n%s", ref, before, got, r)
	}

	// D-669: a hold refused for want of --push names the hold again, with
	// --push and the name given, never a task step or a placeholder
	r = m.Lore(app, "hold", "claim", "--name", "ci-suite")
	t.Logf("hold claim without --push:\n%s", r)
	if r.Code == 0 || !strings.Contains(r.Out(), "`lore hold claim --name ci-suite --push`") || strings.Contains(r.Out(), "task start") {
		t.Errorf("devco:D-669: the hold refusal does not print `lore hold claim --name ci-suite --push` alone:\n%s", r)
	}
	wuNoPlaceholder(t, "devco:D-669", r.Out())
	if got := store.Rev(ref); got != before {
		t.Errorf("devco:D-669: the refused hold moved the store remote's %s", ref)
	}

	// D-669: the claim's refusal printed the push command; it is run here
	// exactly as printed (it is the claim --push of this step)
	r = m.Lore(app, "task", "claim", "--id", id)
	wuNoPlaceholder(t, "devco:D-669", r.Out())
	printed := wuPrinted(r.Out(), "run `")
	if len(printed) != 1 || strings.Join(printed[0], " ") != "task claim --id "+id+" --push" {
		t.Fatalf("devco:D-669: the claim refusal does not print `lore task claim --id %s --push`: %q\n%s", id, printed, r)
	}
	if !strings.Contains(r.Out(), "`lore task start --id "+id+"`") {
		t.Errorf("devco:D-669: the claim refusal does not name task start with the task's id:\n%s", r)
	}
	r = m.Lore(app, printed[0]...).OK(t)
	t.Logf("claim --push (as printed):\n%s", r)
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

	// D-657: after a pushed claim, a heartbeat renews it on the remote
	// (the ref moves), and a pushed hold's release clears it there
	m.Lore(app, "task", "heartbeat", "--id", id).OK(t).Says(t, "renewed")
	renewed := store.Rev(ref)
	if renewed == claimed {
		t.Errorf("devco:D-657: a heartbeat after a pushed claim left the store remote's %s at %q", ref, claimed)
	}
	m.Lore(app, "hold", "claim", "--name", wucHold, "--push").OK(t).Says(t, "holding")
	held := store.Rev(ref)
	if held == renewed || !hasFileNamed(store, ref, wucHold) {
		t.Errorf("devco:D-657: hold claim --push did not reach the store remote's %s: %v", ref, store.Files(ref))
	}
	m.Lore(app, "hold", "release", "--name", wucHold).OK(t).Says(t, "released")
	if got := store.Rev(ref); got == held || hasFileNamed(store, ref, wucHold) {
		t.Errorf("devco:D-657: after hold release the store remote still holds %s (%q -> %q): %v", wucHold, held, got, store.Files(ref))
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

// wucHold is the resource held in the claim-push walk (made up).
const wucHold = "full-suite"

// hasFileNamed reports a file on ref of r whose path holds name.
func hasFileNamed(r *teamharness.Remote, ref, name string) bool {
	for _, f := range r.Files(ref) {
		if strings.Contains(f, name) {
			return true
		}
	}
	return false
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
