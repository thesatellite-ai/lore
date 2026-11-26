//go:build bench

package storegrowth

// doctor_test.go — lore doctor on a long history (devco:D-1012, bug hunt
// H-274): on a generated 1M-commit store doctor once ran 21+ minutes at
// 3.3 GB RSS, because some checks walked or loaded the whole history.
// Doctor must stay cheap whatever the history length (spec §8: no
// full-history walks): at 1M commits under 30 s, and under 350 MB RSS
// at any size (bug hunt H-278: 100k commits under 8 s).
//
// Per size it seeds and generates the store, clones it with lore sync,
// then times lore doctor (and its peak RSS) and checks the two planted
// changes are still held back, so a speed-up that drops a check fails.
//
// Run: LORE_BENCH_BIN=<lore> STOREGROWTH_COMMITS=1000,100000 go test
// -tags bench -run '^TestBenchDoctor$' -timeout 300m ./tests/bench/storegrowth/
// (under testlock + machine-limits on a shared machine). A run with
// STOREGROWTH_DOCTOR_MAX=30s fails when doctor is slower.

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

// doctorMaxRSS is the most memory doctor may hold at any size (H-274;
// H-278, devco:D-1016: 350 MB, from ~500 MB at 100k commits).
const doctorMaxRSS = 350 << 20

func TestBenchDoctor(t *testing.T) {
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
	if s := os.Getenv("STOREGROWTH_DOCTOR_MAX"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			t.Fatalf("STOREGROWTH_DOCTOR_MAX: %v", err)
		}
		limit = d
	}
	var b strings.Builder
	b.WriteString("| Commits | Generate | First clone (lore sync) | lore doctor | Peak RSS | Held back |\n|---|---|---|---|---|---|\n")
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
		clone := w.lore("sync")
		if clone.err != nil || clone.over {
			t.Fatalf("first clone: %v\n%s", clone.err, clone.out)
		}
		_ = w.lore("doctor") // the first run after a clone fills the read caches, as a person's first doctor would
		r, rss := w.doctorRSS()
		held := w.held()
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %.0f MB | %s |\n", n, FormatDuration(gen), FormatDuration(clone.wall), FormatDuration(r.wall), float64(rss)/(1<<20), held.Value)
		if r.over || (limit > 0 && r.wall > limit) {
			t.Errorf("%d commits: lore doctor took %s (limit %s)", n, FormatDuration(r.wall), limit)
		}
		if rss > doctorMaxRSS {
			t.Errorf("%d commits: lore doctor peaked at %.0f MB RSS (limit %d MB)", n, float64(rss)/(1<<20), doctorMaxRSS>>20)
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
	path := filepath.Join(out, "doctor.md")
	mustWrite(t, path, b.String())
	t.Logf("report: %s\n%s", path, b.String())
}

// doctorRSS runs lore doctor once and returns it with its peak RSS.
func (w *world) doctorRSS() (result, int64) {
	r, rss := w.timedRSS(w.app, w.loreBin(), "doctor")
	return r, rss
}

// writeEnv writes env.sh into a kept world (STOREGROWTH_KEEP): it sets
// the world's environment and enters the app repo, so lore doctor can be
// profiled again on the same store without generating it again.
func (w *world) writeEnv() {
	var env strings.Builder
	for _, kv := range w.env {
		k, v, _ := strings.Cut(kv, "=")
		env.WriteString("export " + k + "=" + strconv.Quote(v) + "\n")
	}
	env.WriteString("cd " + strconv.Quote(w.app) + "\n")
	mustWrite(w.t, filepath.Join(w.root, "env.sh"), env.String())
}
