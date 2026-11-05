package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/op"
)

// TestAroundRunsOutsideIn: Around hooks nest in install order, a hook
// without Around is skipped, and one that does not call next stops the run.
func TestAroundRunsOutsideIn(t *testing.T) {
	var got []string
	hooks := []Hook{
		{Name: "a", Around: func(ctx context.Context, _ *Run, next Next) (op.Result, error) {
			got = append(got, "a-in")
			res, err := next(ctx)
			got = append(got, "a-out")
			return res, err
		}},
		{Name: "plain"},
		{Name: "b", Around: func(context.Context, *Run, Next) (op.Result, error) {
			got = append(got, "b")
			return op.Result{}, nil // stops: the app is never reached
		}},
	}
	r := &Runner{hooks: hooks}
	if _, err := r.around(context.Background(), &Run{}, 0); err != nil {
		t.Fatal(err)
	}
	if want := "a-in b a-out"; join(got) != want {
		t.Fatalf("order = %q, want %q", join(got), want)
	}
}

func join(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += " "
		}
		out += x
	}
	return out
}

// TestLoadLiveSettings: defaults without a file, then each key of the
// live:, lease: and default_branch blocks overrides only itself; a broken
// file keeps the defaults.
func TestLoadLiveSettings(t *testing.T) {
	home := t.TempDir()
	d := loadLiveSettings(home)
	if d.FetchTTL != defaultLiveFetchTTL || !d.Autopush || d.Remote != defaultLiveRemote || d.PushRetries != defaultPushRetries || !d.Defaults["tasks"] || d.Defaults["rules"] {
		t.Fatalf("defaults = %+v", d)
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("default_branch: trunk\nlive:\n  fetch_ttl: 5m\n  autopush: false\n  remote: up\n  defaults:\n    rules: true\nlease:\n  push_retries: 2\n")
	s := loadLiveSettings(home)
	if s.FetchTTL != 5*time.Minute || s.Autopush || s.Remote != "up" || s.PushRetries != 2 || !s.Defaults["rules"] || !s.Defaults["tasks"] || s.DefaultBranch != "trunk" || s.CompactAfter != defaultLiveCompactAfter {
		t.Fatalf("overrides = %+v", s)
	}
	write("live: [not a map")
	if b := loadLiveSettings(home); b.FetchTTL != defaultLiveFetchTTL || !b.Autopush {
		t.Fatalf("broken file = %+v", b)
	}
}

// TestLooksLikeRowID: only <prefix>_<hex> ids get the live not-found answer.
func TestLooksLikeRowID(t *testing.T) {
	for in, want := range map[string]bool{
		"tsk_01a1ac": true, "mem_0f": true, "tsk_ab cd": false,
		"tsk_": false, "_ab": false, "nope": false, "Tsk_ab": false, "tsk_xyz": false, "a/b_cd": false,
	} {
		if got := looksLikeRowID(in); got != want {
			t.Errorf("looksLikeRowID(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestSkipLive: machine-only and pre-project operations never open a live
// session (migrate holds M, ranked before L).
func TestSkipLive(t *testing.T) {
	for name, want := range map[string]bool{"migrate.run": true, "init": true, "config.get": true, "task.add": false, "live.compact": false} {
		if got := skipLive(name); got != want {
			t.Errorf("skipLive(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestAddPurged: ids are unioned and sorted, other keys kept, a non-list
// "ids" is an error (never an empty set), non-string entries skipped.
func TestAddPurged(t *testing.T) {
	check := func(b []byte, err error, want string, note bool) {
		t.Helper()
		doc, got, perr := purgedIDs(b)
		if err != nil || perr != nil || join(got) != want || doc.Str("note") != map[bool]string{true: "x"}[note] {
			t.Fatalf("file = %s (%v, %v), ids %v", b, err, perr, got)
		}
	}
	b, err := addPurged(nil, []string{"tsk_b", "tsk_a"})
	check(b, err, "tsk_a tsk_b", false)
	b, err = addPurged([]byte(`{"_v":1,"ids":["tsk_c",7],"note":"x"}`), []string{"tsk_a", "tsk_c"})
	check(b, err, "tsk_a tsk_c", true)
	if _, err := addPurged([]byte(`{"ids":"tsk_a"}`), nil); err == nil {
		t.Fatal("a non-list ids must fail")
	}
}

// TestLiveFinished: kinds with a lifecycle finish on their end states only;
// a kind without one is finished on every write.
func TestLiveFinished(t *testing.T) {
	for _, c := range []struct {
		table, status string
		want          bool
	}{
		{"tasks", "done", true}, {"tasks", "cancelled", true}, {"tasks", "open", false},
		{"missions", "active", false}, {"handoffs", "accepted", true}, {"rules", "", true},
	} {
		body := canonjson.Object{"status": canonjson.String(c.status)}
		if got := liveFinished(c.table, body); got != c.want {
			t.Errorf("liveFinished(%s, %s) = %v, want %v", c.table, c.status, got, c.want)
		}
	}
}
