//go:build bench

package storegrowth

// firstsync_test.go — the first open of a long history (bug hunt H-279,
// devco:D-1017): the first `lore sync` after a clone replays the store's
// whole roster history, and the first `lore doctor` after it reads what
// the sync kept. On a generated 1M-commit store the first sync once took
// ~38 minutes at up to 4 GB: the replay read every row version of the
// history at once, through an in-process inflate with no delta cache.
//
// Per size it seeds and generates the store, removes the clone, times the
// first lore sync with lore's own peak memory (git's processes are not
// lore's: with a file:// remote the server side of the clone runs on this
// machine too), then the first lore doctor, and checks the planted
// changes are still held back.
//
// Run: LORE_BENCH_BIN=<lore> STOREGROWTH_COMMITS=10000,100000,300000 go
// test -tags bench -run '^TestBenchFirstSync$' -timeout 300m
// ./tests/bench/storegrowth/ (under testlock + machine-limits on a shared
// machine). STOREGROWTH_SYNC_MAX=<duration> fails a slower first sync.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// firstSyncMaxRSS is the most memory lore's own process may hold during
// the first sync of a store, at any history length (H-279).
const firstSyncMaxRSS = 1 << 30

func TestBenchFirstSync(t *testing.T) {
	bin := os.Getenv("LORE_BENCH_BIN")
	if bin == "" {
		t.Skip("LORE_BENCH_BIN is not set")
	}
	sizes := []int{1000}
	if s := os.Getenv("STOREGROWTH_COMMITS"); s != "" {
		sizes = nil
		for _, f := range strings.Split(s, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(f))
			if err != nil || n < 100 {
				t.Fatalf("STOREGROWTH_COMMITS: %q is not a size of at least 100", f)
			}
			sizes = append(sizes, n)
		}
	}
	var limit time.Duration
	if s := os.Getenv("STOREGROWTH_SYNC_MAX"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			t.Fatalf("STOREGROWTH_SYNC_MAX: %v", err)
		}
		limit = d
	}
	var b strings.Builder
	b.WriteString("| Commits | Generate | First sync | Its peak RSS (lore) | First doctor | Its peak RSS (lore) | Held back |\n|---|---|---|---|---|---|---|\n")
	for _, n := range sizes {
		w := newWorld(t, bin)
		plan := w.seed(n)
		t0 := time.Now()
		if _, err := Import(context.Background(), w.bare, plan); err != nil {
			t.Fatal(err)
		}
		gen := time.Since(t0)
		if err := os.RemoveAll(w.clone); err != nil {
			t.Fatal(err)
		}
		sync, syncRSS := w.timedOwnRSS(w.app, w.loreBin(), "sync")
		if sync.err != nil || sync.over {
			t.Fatalf("first sync: %v\n%s", sync.err, sync.out)
		}
		doctor, doctorRSS := w.timedOwnRSS(w.app, w.loreBin(), "doctor")
		held := w.held()
		fmt.Fprintf(&b, "| %d | %s | %s | %.0f MB | %s | %.0f MB | %s |\n", n, FormatDuration(gen), FormatDuration(sync.wall), float64(syncRSS)/(1<<20),
			FormatDuration(doctor.wall), float64(doctorRSS)/(1<<20), held.Value)
		if limit > 0 && sync.wall > limit {
			t.Errorf("%d commits: the first lore sync took %s (limit %s)", n, FormatDuration(sync.wall), limit)
		}
		if syncRSS > firstSyncMaxRSS {
			t.Errorf("%d commits: the first lore sync peaked at %.0f MB RSS (limit %d MB)", n, float64(syncRSS)/(1<<20), firstSyncMaxRSS>>20)
		}
		if os.Getenv("STOREGROWTH_KEEP") != "" {
			w.writeEnv()
		}
	}
	out := os.Getenv("STOREGROWTH_OUT")
	if out == "" {
		out = filepath.Join("..", "..", "..", "tmp", "bench")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(out, "first-sync.md")
	mustWrite(t, path, b.String())
	t.Logf("report: %s\n%s", path, b.String())
}
