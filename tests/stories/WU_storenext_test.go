//go:build stories && !windows

package stories

// WU_storenext_test.go — devco:D-666 on WW's world, the rest of owner use
// pass 3 weak 16: after D-635 a store task was claimed and released by id
// from the home folder, but `task next --setup <setup>` (and --store
// <org>) from home still refused as a code-repo read, and the verbs that
// name no task (task heartbeat with no --id) found no claim taken from
// home: its held record lives in the store clone's state dir. Now a member
// takes the next store task from home with --push, heartbeats it with no
// --id from home, inside the store clone and in a linked code repo, and
// releases it; the linked repo (a teammate's view of the same store) sees
// each step. devco:D-671: `task heartbeat --daemon` from home and inside
// the store clone keeps that claim alive instead of refusing.

import (
	"cmp"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// wuDaemonFor is how long the story lets `task heartbeat --daemon` run:
// its first round renews at once, so the stop finds it waiting for the
// next one.
var wuDaemonFor = 8 * time.Second

// TestStoryWUStoreTaskNextFromHome is the member's day from the home
// folder: task next over the setup's org store, the claim with --push,
// heartbeat without --id anywhere, release.
func TestStoryWUStoreTaskNextFromHome(t *testing.T) {
	t.Parallel()
	w := wwBuild(t)
	m := w.m
	app := w.places["own-linked"].Dir
	root := w.places["store-root"].Dir
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
	m.Lore(app, "task", "add", "--title", "next from home", "--json").OK(t)
	m.Lore(app, "sync").OK(t)
	before := store.Rev(ref)
	setup := []string{"--setup", wSetup}

	type printedAt struct{ dir, out string }
	var printed []printedAt
	look := func(dir string, args ...string) teamharness.Result {
		r := m.Lore(dir, args...)
		t.Logf("%s: lore %s:\n%s", dir, strings.Join(args, " "), r)
		printed = append(printed, printedAt{dir, r.Out()})
		return r
	}

	// the read: the queue over the setup's org store, ranked across folders
	r := look(m.Home, append([]string{"task", "next"}, setup...)...)
	if r.Code != 0 || !strings.Contains(r.Out(), "next ") {
		t.Errorf("devco:D-666: task next --setup from home: want the next store task, got:\n%s", r)
	}
	for _, s := range []string{wOrg, wOrg + "/" + wFolder} {
		if r = look(m.Home, "task", "next", "--store", s); r.Code != 0 || !strings.Contains(r.Out(), "next ") {
			t.Errorf("devco:D-666: task next --store %s from home: want the next store task, got:\n%s", s, r)
		}
	}

	// the claim: refused without --push (D-580), nothing written
	r = look(m.Home, append([]string{"task", "next", "--claim"}, setup...)...)
	if r.Code == 0 || !strings.Contains(r.Out(), "--push") {
		t.Errorf("devco:D-666: task next --claim from home without --push: want the D-580 refusal naming --push:\n%s", r)
	}
	if got := store.Rev(ref); got != before {
		t.Errorf("devco:D-666: the store remote's %s moved without --push (%q -> %q)", ref, before, got)
	}
	r = m.Lore(m.Home, append([]string{"task", "next", "--claim", "--push", "--json"}, setup...)...)
	t.Logf("home: task next --claim --push:\n%s", r)
	var out struct {
		Claimed string `json:"claimed"`
	}
	if r.Code != 0 || json.Unmarshal([]byte(r.Stdout), &out) != nil || out.Claimed == "" {
		t.Fatalf("devco:D-666: task next --claim --push from home took no store task:\n%s", r)
	}
	id := out.Claimed
	if !hasClaimFile(store, ref, id) {
		t.Fatalf("devco:D-666: the store remote holds no claim of %s after task next --claim --push from home: %v", id, store.Files(ref))
	}
	m.Lore(app, "task", "show", "--id", id).OK(t).Says(t, "claim:    active")

	// heartbeat with no --id: from home, inside the store clone, in a
	// linked code repo (the held record is the store clone's)
	// (within lease.renew_every of the claim it says the claim is held; the
	// story clock stands still, so --id below is the renewal)
	for _, dir := range []string{m.Home, root, app} {
		r = look(dir, "task", "heartbeat")
		if r.Code != 0 || !strings.Contains(r.Out(), id) {
			t.Errorf("devco:D-666: task heartbeat with no --id in %s: want %s renewed or held, got:\n%s", dir, id, r)
		}
	}
	if r = look(m.Home, "task", "heartbeat", "--id", id); r.Code != 0 || !strings.Contains(r.Out(), id+" renewed") {
		t.Errorf("devco:D-666: task heartbeat --id %s from home: want it renewed, got:\n%s", id, r)
	}

	// devco:D-671: the daemon from home and inside the store clone keeps
	// the store claim alive: it is still running when stopped, and its
	// first round renewed the claim on the store's remote (the ref moved);
	// before, it refused at once with E_NOT_A_STORE. --id names one claim.
	for _, c := range []struct {
		dir  string
		args []string
	}{{m.Home, nil}, {root, nil}, {m.Home, []string{"--id", id}}} {
		pre := store.Rev(ref)
		dr, stopped := m.LoreFor(c.dir, wuDaemonFor, append([]string{"task", "heartbeat", "--daemon"}, c.args...)...)
		t.Logf("%s: lore task heartbeat --daemon %v (stopped %v):\n%s", c.dir, c.args, stopped, dr)
		if !stopped {
			t.Errorf("devco:D-671: task heartbeat --daemon %v in %s exited instead of keeping the claim alive:\n%s", c.args, c.dir, dr)
		}
		if got := store.Rev(ref); got == pre {
			t.Errorf("devco:D-671: task heartbeat --daemon %v in %s renewed nothing on the store's %s", c.args, c.dir, ref)
		}
	}
	if r = look(m.Home, "task", "heartbeat", "--daemon", "--id", "tsk_0000000000000000000000000000000f"); r.Code == 0 {
		t.Errorf("devco:D-671: the daemon for a claim nobody holds runs on:\n%s", r)
	}

	r = look(m.Home, "task", "release", "--id", id)
	if r.Code != 0 || hasClaimFile(store, ref, id) {
		t.Fatalf("devco:D-666: release from home left the claim of %s (%v):\n%s", id, store.Files(ref), r)
	}
	m.Lore(app, "task", "show", "--id", id).OK(t).Never(t, "claim:    active")
	r = look(m.Home, "task", "heartbeat")
	if r.Code != 0 || strings.Contains(r.Out(), id) {
		t.Errorf("devco:D-666: heartbeat after release still names %s:\n%s", id, r)
	}

	// the verbs that stay repo-only refuse from home with a step that works
	for _, args := range [][]string{{"inbox"}, {"mission", "status"}, {"task", "next"}} {
		look(m.Home, args...)
	}

	// GUARD: each read's printed way to take the task works from home as
	// printed (it carries the read's --setup / --store), then is released
	for _, p := range printed {
		_, take, ok := strings.Cut(p.out, "take it: ")
		if !ok {
			continue
		}
		take, _, _ = strings.Cut(take, "\n")
		next, alt, _ := strings.Cut(take, " (or ")
		for _, c := range []string{next, strings.TrimSuffix(alt, ")")} {
			args, isLore := strings.CutPrefix(strings.TrimSpace(c), "lore ")
			if !isLore {
				continue
			}
			r = m.Lore(p.dir, append(wuSplit(args), "--json")...)
			t.Logf("printed take-it command lore %s:\n%s", args, r)
			var got struct {
				Claimed string `json:"claimed"`
			}
			_ = json.Unmarshal([]byte(r.Stdout), &got)
			if r.Code != 0 {
				t.Errorf("devco:D-666: the printed command `lore %s` fails where printed (%s):\n%s", args, p.dir, r)
				continue
			}
			taken := cmp.Or(got.Claimed, id)
			m.Lore(p.dir, "task", "release", "--id", taken).OK(t)
		}
	}

	// GUARD: every command these outputs print works exactly where printed
	fill := strings.NewReplacer("<id>", id, "<org>", wOrg, "<setup>", wSetup)
	for _, p := range printed {
		for _, c := range wuBackticked(fill.Replace(p.out)) {
			if c[0] == "task" && len(c) > 1 && (c[1] == "release" || c[1] == "claim") {
				continue // the claim is already released above
			}
			r = m.Lore(p.dir, c...)
			t.Logf("printed command lore %s:\n%s", strings.Join(c, " "), r)
			if r.Code != 0 {
				t.Errorf("devco:D-666: the printed command `lore %s` fails where printed (%s):\n%s", strings.Join(c, " "), p.dir, r)
			}
		}
	}
}
