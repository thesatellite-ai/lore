//go:build bench

package storegrowth

// blobless_test.go — store growth SG-3b numbers (devco:D-1001): the same
// generated store cloned full and blobless (store.clone_filter).
//
//	first fetch, git level ─► bytes of `git clone --bare` full vs
//	    --filter=blob:none from the same remote (what lore's first fetch
//	    receives)
//	lore sync cold, full ─► time and .git size
//	lore sync cold, blobless ─► time and .git size (the cold roster replay
//	    reads every row version: one batched fetch of the old ones)
//	lore store reclone --filter blob:none ─► time and .git size; then a
//	    sync: the saved replay carried over, no old version fetched
//	held ─► the planted non-admin org file change and self-promotion stay
//	    held on the blobless clone
//
// Run: LORE_BENCH_BIN=<lore> STOREGROWTH_COMMITS=100000 go test -tags bench
// -run '^TestBenchBlobless$' -timeout 120m ./tests/bench/storegrowth/
// (under testlock + machine-limits on a shared machine).

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBenchBlobless(t *testing.T) {
	bin := os.Getenv("LORE_BENCH_BIN")
	if bin == "" {
		t.Skip("LORE_BENCH_BIN is not set")
	}
	n := 1000
	if s := os.Getenv("STOREGROWTH_COMMITS"); s != "" {
		v, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || v < 100 {
			t.Fatalf("STOREGROWTH_COMMITS: %q is not one size of at least 100", s)
		}
		n = v
	}
	w := newWorld(t, bin)
	plan := w.seed(n)
	st, err := Import(context.Background(), w.bare, plan)
	if err != nil {
		t.Fatal(err)
	}
	run := Run{Commits: n, When: time.Now(), Generated: st, Sha: "dev"}
	if b, err := exec.Command("git", "rev-parse", "--short=9", "HEAD").Output(); err == nil {
		run.Sha = strings.TrimSpace(string(b))
	}
	run.Samples = append(run.Samples, Sample{Name: "Remote size", Value: w.du(w.bare)})

	// what the first fetch receives, git level, same remote
	for _, c := range []struct{ name, filter string }{{"full", ""}, {"blobless", "--filter=blob:none"}} {
		dest := filepath.Join(w.root, "probe-"+c.name)
		args := []string{"clone", "-q", "--bare", "--no-local"}
		if c.filter != "" {
			args = append(args, c.filter)
		}
		r := w.timeGit(w.root, append(args, "file://"+w.bare, dest)...)
		s := Sample{Name: "First fetch, " + c.name + " (git clone --bare)", Value: w.du(dest)}
		s.add(r)
		run.Samples = append(run.Samples, s)
		_ = os.RemoveAll(dest)
	}

	cfg := filepath.Join(w.home, ".lore", "config.yaml")
	base, err := os.ReadFile(cfg) // #nosec G304 -- the bench's own temp home
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	setFilter := func(v string) {
		body := strings.TrimRight(string(base), "\n") + "\n"
		if v != "" {
			body += "store:\n  clone_filter: " + v + "\n"
		}
		mustWrite(t, cfg, body)
	}
	cold := func(name string) {
		if err := os.RemoveAll(w.clone); err != nil {
			t.Fatal(err)
		}
		s := Sample{Name: "First clone (lore sync, cold), " + name}
		s.add(w.lore("sync"))
		run.Samples = append(run.Samples, s)
		run.Samples = append(run.Samples, w.disk("After first clone, "+name)...)
	}
	cold("full")
	setFilter("blob:none")
	cold("blobless")
	run.Samples = append(run.Samples, w.held())

	setFilter("")
	rec := Sample{Name: "lore store reclone --filter blob:none (replay carried)"}
	rec.add(w.lore("store", "reclone", "--store", storeURL, "--filter", "blob:none"))
	run.Samples = append(run.Samples, rec)
	run.Samples = append(run.Samples, w.disk("After reclone")...)
	sync := Sample{Name: "lore sync after reclone"}
	sync.add(w.lore("sync"))
	run.Samples = append(run.Samples, sync)
	run.Samples = append(run.Samples, w.disk("After reclone + sync")...)
	run.Samples = append(run.Samples, w.held())
	missing := w.git(w.clone, "rev-list", "--objects", "--missing=print", "--all")
	run.Samples = append(run.Samples, Sample{Name: "Old objects still not in the reclone after sync", Value: strconv.Itoa(strings.Count(missing+"\n", "\n?"))})

	out := os.Getenv("STOREGROWTH_OUT")
	if out == "" {
		out = filepath.Join("..", "..", "..", "tmp", "bench")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(out, "blobless-"+strconv.Itoa(n)+".md")
	mustWrite(t, path, run.Markdown())
	t.Logf("report: %s\n%s", path, run.Markdown())
}
