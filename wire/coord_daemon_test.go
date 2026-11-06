package wire

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// daemonWorld is an own repo of an org (claims on its own signals ref)
// with a person on the roster, a claimed task, and a wall clock the test
// moves.
type daemonWorld struct {
	w    *orgWorld
	dir  string
	app  *App
	task string
	now  *atomic.Int64
}

func newDaemonWorld(t *testing.T) daemonWorld {
	t.Helper()
	w := newOrgWorld(t)
	ctx := context.Background()
	mira := w.home(t, "mira")
	h := orgHostAt(t, w.repo, mira)
	if _, err := h.Create(ctx, opsorgcmd.CreateIn{Org: "agency", Store: "agency", OrgFolder: "org", Own: []string{"github.com/agency/*"},
		Key: "human:mira@agency.example", Name: "Mira", Handle: "mira"}, true); err != nil {
		t.Fatalf("create: %v", err)
	}
	dir := w.ownRepo(t, mira)
	now := &atomic.Int64{}
	now.Store(time.Now().UnixNano())
	opts := StandaloneOptions(dir, mira, dir, "test")
	opts.Wall = func() time.Time { return time.Unix(0, now.Load()) }
	app, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	withProject(t, app)
	out, err := run(t, app, opstask.OpAdd, `{"title":"ship it","to":"repo"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(out, "tsk_")
	if i < 0 || len(out) < i+36 {
		t.Fatalf("task add output has no id: %s", out)
	}
	id := out[i : i+36]
	if _, err = run(t, app, opscoord.OpTaskClaim, `{"id":"`+id+`"}`, true); err != nil {
		t.Fatalf("claim: %v", err)
	}
	return daemonWorld{w: w, dir: dir, app: app, task: id, now: now}
}

// daemon runs `task heartbeat --daemon` (for id, "" = every held claim)
// until it returns or ctx ends.
func (d daemonWorld) daemon(ctx context.Context, id string) (opscoord.Report, error) {
	return standaloneCoord{a: d.app}.TaskHeartbeat(ctx, opscoord.HeartbeatIn{ID: id, Daemon: true})
}

// TestHeartbeatDaemon covers `task heartbeat --daemon` (F7, v0.1.17
// heartbeatDaemon): it renews at once, does not renew again before
// renew_every passes on the wall clock, and stops quietly when its context
// ends; a claim that is no longer held stops it with that claim's error
// at once; renewals that keep failing offline until the held expiry minus
// the grace stop it with E_LOST_CLAIM ("stop work").
func TestHeartbeatDaemon(t *testing.T) {
	t.Run("renews, then stops with the context", func(t *testing.T) {
		d := newDaemonWorld(t)
		before := orgGit(t, d.dir, "ls-remote", "origin", "refs/lore/signals")
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		rep, err := d.daemon(ctx, "")
		if err != nil {
			t.Fatalf("daemon: %v", err)
		}
		// the wall clock did not move: one renewal only (renew_every not reached)
		if len(rep.Lines) != 1 || rep.Lines[0] != "heartbeat daemon stopped after 1 renewal(s)" {
			t.Fatalf("report %q", rep.Lines)
		}
		if after := orgGit(t, d.dir, "ls-remote", "origin", "refs/lore/signals"); after == before {
			t.Fatal("the renewal never reached the remote signals ref")
		}
	})
	t.Run("a claim no longer held stops it", func(t *testing.T) {
		d := newDaemonWorld(t)
		if _, err := run(t, d.app, opscoord.OpTaskRelease, `{"id":"`+d.task+`"}`, true); err != nil {
			t.Fatalf("release: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		start := time.Now()
		_, err := d.daemon(ctx, d.task)
		if !errs.Has(err, errs.LostClaim) {
			t.Fatalf("daemon on a released claim: %v, want E_LOST_CLAIM", err)
		}
		if time.Since(start) > 15*time.Second {
			t.Fatal("the daemon must stop at the first failed renewal, not wait for the context")
		}
	})
	t.Run("offline until expiry minus grace", func(t *testing.T) {
		d := newDaemonWorld(t)
		orgGit(t, d.dir, "remote", "set-url", "origin", "https://github.com/agency/no-such-repo")
		d.now.Add(int64(19 * time.Minute)) // lease ttl 20m, grace 2m: past expires - grace
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := d.daemon(ctx, d.task)
		if !errs.Has(err, errs.LostClaim) || !strings.Contains(err.Error(), "renewals kept failing") {
			t.Fatalf("offline past expiry - grace: %v, want E_LOST_CLAIM renewals kept failing", err)
		}
	})
	t.Run("offline before expiry keeps running", func(t *testing.T) {
		d := newDaemonWorld(t)
		orgGit(t, d.dir, "remote", "set-url", "origin", "https://github.com/agency/no-such-repo")
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		rep, err := d.daemon(ctx, d.task)
		if err != nil {
			t.Fatalf("an offline renewal well before expiry must not stop the daemon: %v", err)
		}
		if len(rep.Lines) != 1 || rep.Lines[0] != "heartbeat daemon stopped after 0 renewal(s)" {
			t.Fatalf("report %q", rep.Lines)
		}
	})
}
