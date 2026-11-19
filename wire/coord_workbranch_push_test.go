package wire

import (
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/gitremote"
)

// TestWorkBranchPushOnlyWhenAskedD598: `task note` and the heartbeat
// daemon pushed the claim's code work branch whenever live.autopush was on
// by default, though neither takes --push (devco D-598). The branch is
// pushed only when the person turned pushes on: live.autopush: true in
// config.yaml or LORE_PUSH_CLAIMS=1.
func TestWorkBranchPushOnlyWhenAskedD598(t *testing.T) {
	repo := gitRepo(t)
	bare := t.TempDir()
	gitIn(t, bare, nil, "init", "-q", "--bare")
	gitIn(t, repo, nil, "remote", "add", "origin", bare)
	gitIn(t, repo, nil, "checkout", "-q", "-b", "agent/a-1/work")
	gitIn(t, repo, nil, "commit", "-q", "--allow-empty", "-m", "work")
	r, err := gitref.Open(background(), repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	fr := gitremote.NewFaultRemote(&gitremote.Git{Repo: r, Name: "origin"})
	w := &coordWorld{repo: r, remote: fr, remoteName: "origin", autopush: true}
	t.Setenv(envPushClaims, "")
	if _, ok := w.pushWorkBranch(background(), "agent/a-1/work"); ok || !w.autopush || w.pushesAsked() {
		t.Fatalf("autopush on by default only: the work branch was pushed (ok=%v)", ok)
	}
	t.Setenv(envPushClaims, envPushClaimsOn)
	if !w.pushesAsked() {
		t.Fatal("LORE_PUSH_CLAIMS=1 asks for pushes")
	}
	t.Setenv(envPushClaims, "")
	w.autopushChosen = true
	if _, ok := w.pushWorkBranch(background(), "agent/a-1/work"); !ok {
		t.Fatal("live.autopush: true asks for pushes, but the work branch was not pushed")
	}
}

// TestNoteSeesBranchPushedByHandD598: with pushes not turned on, a person
// pushes the work branch with git themselves; lore must see that head on
// the remote-tracking ref and not tell them it was "not pushed".
func TestNoteSeesBranchPushedByHandD598(t *testing.T) {
	repo := gitRepo(t)
	bare := t.TempDir()
	gitIn(t, bare, nil, "init", "-q", "--bare")
	gitIn(t, repo, nil, "remote", "add", "origin", bare)
	gitIn(t, repo, nil, "checkout", "-q", "-b", "agent/a-1/work")
	gitIn(t, repo, nil, "commit", "-q", "--allow-empty", "-m", "work")
	r, err := gitref.Open(background(), repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := &coordWorld{repo: r, remoteName: "origin"}
	if _, ok := w.branchOnRemote(background(), "agent/a-1/work"); ok {
		t.Fatal("never pushed, but branchOnRemote says it is on the remote")
	}
	gitIn(t, repo, nil, "push", "-q", "origin", "agent/a-1/work")
	if _, ok := w.branchOnRemote(background(), "agent/a-1/work"); !ok {
		t.Fatal("pushed by hand, but branchOnRemote does not see it")
	}
	gitIn(t, repo, nil, "commit", "-q", "--allow-empty", "-m", "more")
	if _, ok := w.branchOnRemote(background(), "agent/a-1/work"); ok {
		t.Fatal("a commit after the push is not on the remote, but branchOnRemote says it is")
	}
}
