//go:build setupe2e

package setupe2e

// matrix_test.go — every valid shape (AllShapes) through the real
// binary's HTTP API, in parallel (go test -parallel bounds it). A world
// that cannot be built or a server that does not start FAILS the shape;
// nothing is skipped.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAPIMatrix(t *testing.T) {
	shapes := AllShapes()
	only := os.Getenv(envOnly)
	ran := 0
	for _, s := range shapes {
		if only != "" && !strings.Contains(s.Name(), only) {
			continue
		}
		ran++
		t.Run(s.Name(), func(t *testing.T) {
			t.Parallel()
			runAPIShape(t, s)
		})
	}
	if only == "" || strings.Contains(teamPushCase, only) {
		ran++
		t.Run(teamPushCase, func(t *testing.T) {
			t.Parallel()
			runTeamPush(t)
		})
	}
	if ran == 0 {
		t.Fatalf("no shape matched %s=%q", envOnly, only)
	}
}

// teamPushCase is the admin walk of TestAPIMatrix: the shapes walk a solo
// setup, so the Start a team step's push question gets its own case.
const teamPushCase = "team-push-batch"

// runTeamPush starts a team with push batch on the team step and checks
// the team store's lore-store.yaml (on the fake GitHub) says push batch,
// in one settings commit made after the team was created.
func runTeamPush(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	s := Shape{GHAccounts: 1, GHActive: ActiveFirst, SSH: SSHSeveral, Keys: KeysWithPub, LoreHome: HomeEmpty, Start: StartPlain, Push: PushBatch}
	w, err := Build(ctx, t.TempDir(), s, loreBin)
	if err != nil {
		t.Fatalf("build world: %v", err)
	}
	if err := w.Start(ctx, 0, loreBin); err != nil {
		t.Fatalf("start lore setup web: %v", err)
	}
	defer w.Stop()
	if err := w.StartTeamPush(ctx, loreBin, w.Home, InviteHub, InviteOrg, inviteRepo, PushBatch); err != nil {
		t.Fatalf("start a team with push batch: %v\nserver output:\n%s", err, w.Output())
	}
	// C5: the settings commit waits in the admin's clone of the team store
	// until they send it; a git push there keeps the commit as it is (a
	// batch-mode lore sync would fold it into one batch commit)
	clones, _ := filepath.Glob(filepath.Join(w.Home, ".lore", "stores", "*", inviteRepo))
	if len(clones) != 1 {
		t.Fatalf("the admin's clone of the team store: %v", clones)
	}
	push := exec.CommandContext(ctx, "/usr/bin/git", "push", "-q", "origin", "main")
	push.Dir, push.Env = clones[0], w.runEnv
	if out, err := push.CombinedOutput(); err != nil {
		t.Fatalf("git push in the team store clone: %v\n%s", err, out)
	}
	bare := filepath.Join(w.Dir, "remote", w.Accounts[0].Login, inviteRepo+".git")
	show := exec.CommandContext(ctx, "/usr/bin/git", "--git-dir", bare, "show", "main:lore-store.yaml")
	marker, err := show.CombinedOutput()
	if err != nil || !strings.Contains(string(marker), "push: batch") {
		t.Fatalf("team store lore-store.yaml after the team step with push batch: %v\n%s", err, marker)
	}
	logc := exec.CommandContext(ctx, "/usr/bin/git", "--git-dir", bare, "log", "--format=%s", "main")
	subjects, err := logc.CombinedOutput()
	if err != nil || !strings.Contains(string(subjects), "lore: store set "+InviteOrg) {
		t.Fatalf("no settings commit on the team store: %v\n%s", err, subjects)
	}
	t.Logf("ok: team %s store says push batch; commits:\n%s", InviteOrg, subjects)
}

func runAPIShape(t *testing.T, s Shape) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	w, err := Build(ctx, t.TempDir(), s, loreBin)
	if err != nil {
		t.Fatalf("build world: %v", err)
	}
	if err := w.Start(ctx, 0, loreBin); err != nil {
		t.Fatalf("start lore setup web: %v", err)
	}
	defer w.Stop()
	o := Walk(ctx, w)
	if len(o.Failures) == 0 {
		t.Logf("ok: setup %s, steps %v, typed %v, fixes run %v, needs person %v, %d gh/ssh lines judged", o.Setup, o.Steps, o.MustSupply, o.Fixed, o.NeedsPerson, o.Judged)
		return
	}
	for _, f := range o.Failures {
		t.Errorf("FAIL %s: %s", s.Name(), f)
	}
	for _, l := range []string{w.Info().Logs.GH, w.Info().Logs.SSH} {
		if b, err := os.ReadFile(l); err == nil {
			t.Logf("%s:\n%s", l, b)
		}
	}
	t.Logf("shape %s\nsteps: %v\ntyped: %v\ntrace:\n  %s\nserver output:\n%s", s.Name(), o.Steps, o.MustSupply, strings.Join(o.Trace, "\n  "), w.Output())
}
