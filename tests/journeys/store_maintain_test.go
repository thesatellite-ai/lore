//go:build journeys && !windows

package journeys

// store_maintain_test.go — devco:D-996 (store growth SG-2): a busy store
// clone collected loose objects forever (git's auto gc never reached its
// thresholds and lore ran none), so a few MB of rows took tens of MB on
// disk and path-limited logs ran without a commit-graph. Now a new clone
// has git's own auto gc off, the first command after a clone's pass is a
// day old starts a detached pass (the command does not wait), doctor names
// a clone that needs one, and `lore store maintain` runs it now.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// jSmLoose is the clone's loose object count (git count-objects).
func jSmLoose(t *testing.T, clone string) int {
	t.Helper()
	out, err := exec.Command("git", "-C", clone, "count-objects", "-v").CombinedOutput()
	if err != nil {
		t.Fatalf("git count-objects: %v\n%s", err, out)
	}
	for _, l := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(l, "count: "); ok {
			var n int
			_, _ = fmt.Sscan(v, &n)
			return n
		}
	}
	return -1
}

func TestJourneyStoreClonePackedOnceADay(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	nia := w.Persona("Nia Okoro", "nia@acme-co.example")
	lap := nia.Laptop()
	me := w.Remote("github.com/nia-okoro/lore-me")
	code := lap.Clone(w.Remote("github.com/acme-co/orbit"), "orbit")
	clone := filepath.Join(lap.Home, ".lore", "stores", "nia-okoro", "lore-me")
	stamp := filepath.Join(clone, ".git", "lore", "maint-stamp")

	w.Step("day1/a-new-clone-has-git-auto-gc-off", func() {
		lap.Lore(lap.Home, "store", "init", "--name", "me", "--url", me.SSHURL(), "--yes").OK(t)
		lap.LoreIn(code, "init").OK(t)
		out, err := exec.Command("git", "-C", clone, "config", "--local", "--get", "gc.auto").Output()
		if err != nil || strings.TrimSpace(string(out)) != "0" {
			t.Fatalf("gc.auto in the new clone = %q (%v), want 0", out, err)
		}
		if _, err := os.Stat(stamp); err != nil {
			t.Fatalf("a new clone has no maintenance stamp: %v", err)
		}
	})

	w.Step("day1/600-rows-leave-loose-objects", func() {
		var b strings.Builder
		for i := range 600 {
			fmt.Fprintf(&b, `{"op":"memory add","input":{"body":"widget note %d: the export cron runs at noon","to":"me"}}`+"\n", i)
		}
		file := filepath.Join(lap.Home, "notes.jsonl")
		if err := os.WriteFile(file, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		lap.LoreIn(code, "batch", "apply", "--file", file, "--yes").OK(t)
		if n := jSmLoose(t, clone); n <= 500 {
			t.Fatalf("fixture: %d loose objects, want over 500", n)
		}
		// the same day: doctor names the loose objects, and that lore packs
		// them by itself once the day-old pass is due; nothing starts yet
		lap.Lore(lap.Home, "doctor").Says(t, "store_maintenance", "older than store.maintenance_every (24h)")
		if n := jSmLoose(t, clone); n <= 500 {
			t.Fatalf("a pass ran the same day as the clone was made: %d loose objects", n)
		}
	})

	w.AdvanceDays(2)
	before, _ := os.ReadFile(stamp)

	w.Step("day3/doctor-names-the-pass", func() {
		lap.Lore(lap.Home, "doctor").Says(t, "store_maintenance", "store clone nia-okoro/lore-me", "loose object(s)", "lore store maintain --store nia-okoro/lore-me")
	})

	w.Step("day3/any-command-starts-it-in-the-background", func() {
		lap.Lore(lap.Home, "whoami").OK(t)
		deadline := time.Now().Add(60 * time.Second)
		for {
			now, _ := os.ReadFile(stamp)
			if string(now) != string(before) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("no maintenance pass within 60 s of the first command of the day (stamp %q)", now)
			}
			time.Sleep(200 * time.Millisecond)
		}
		if n := jSmLoose(t, clone); n != 0 {
			t.Fatalf("%d loose objects after the pass, want 0", n)
		}
		if _, err := os.Stat(filepath.Join(clone, ".git", "objects", "info", "commit-graphs", "commit-graph-chain")); err != nil {
			t.Fatalf("no commit-graph after the pass: %v", err)
		}
		lap.Lore(lap.Home, "doctor").Never(t, "store_maintenance")
		lap.Lore(lap.Home, "memory", "list", "--store", "me").OK(t).Says(t, "widget note 599")
	})

	w.Step("day3/store-maintain-runs-it-now", func() {
		lap.Lore(lap.Home, "store", "maintain", "--store", "nia-okoro/lore-me").OK(t).Says(t, "✓ nia-okoro/lore-me: .git", "commit-graph written")
		lap.Lore(lap.Home, "store", "maintain", "--all", "--due").OK(t).Says(t, "no store clone on this machine needed a pass")
		lap.Lore(lap.Home, "store", "maintain").Refused(t, "E_INVALID_INPUT", "lore store maintain --all")
	})
}
