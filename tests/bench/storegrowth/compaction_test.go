//go:build bench

package storegrowth

// compaction_test.go — store growth SG-6 numbers (devco:D-1003): the same
// generated store before and after a compaction with an archive that keeps
// the newest 1% of its commits (1M → 10k is the spec's acceptance row).
//
//	before ─► first fetch bytes (git clone --bare) and a cold lore sync
//	    (time, .git size)
//	compact ─► compact_archive=on, LORE_COMPACT_V2=1, lore store compact
//	    --before <1% of the history> --yes (time; the archive ref pushed)
//	adopt ─► an existing clone (copied before the compaction) syncs: it
//	    re-does the rewrite and adopts it (time)
//	after ─► first fetch bytes and a cold lore sync of a fresh clone; the
//	    planted held changes stay held; lore store compact --verify (time)
//
// Run: LORE_BENCH_BIN=<lore> STOREGROWTH_COMMITS=100000 go test -tags bench
// -run '^TestBenchCompaction$' -timeout 180m ./tests/bench/storegrowth/
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

func TestBenchCompaction(t *testing.T) {
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
	fetchBytes := func(name string) {
		dest := filepath.Join(w.root, "probe-"+strings.ReplaceAll(name, " ", "-"))
		r := w.timeGit(w.root, "clone", "-q", "--bare", "--no-local", "file://"+w.bare, dest)
		s := Sample{Name: "First fetch (git clone --bare), " + name, Value: w.du(dest)}
		s.add(r)
		run.Samples = append(run.Samples, s)
		_ = os.RemoveAll(dest)
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
	run.Samples = append(run.Samples, Sample{Name: "Remote size, before", Value: w.du(w.bare)})
	fetchBytes("before")
	cold("before")
	run.Samples = append(run.Samples, w.held())

	// the flag, and an existing clone kept aside for the adoption
	w.must("store", "set", "--store", "agency", "--settings", "compact_archive=on")
	w.must("sync")
	existing := filepath.Join(w.root, "existing-clone")
	if out, err := exec.Command("cp", "-a", w.clone, existing).CombinedOutput(); err != nil {
		t.Fatalf("copy the clone: %v %s", err, out)
	}

	// the cutoff falls right after the commit keep first-parent steps
	// back: the generated dates are minutes apart, ending before now
	keep := n / 100
	at, err := strconv.ParseInt(w.git(w.bare, "log", "-1", "--format=%ct", "main~"+strconv.Itoa(keep)), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	age := time.Since(time.Unix(at, 0)) - time.Second
	w.env = append(w.env, "LORE_COMPACT_V2=1")
	if os.Getenv("STOREGROWTH_TRACE") == "1" {
		w.env = append(w.env, "LORE_TRACE=1")
	}
	c := Sample{Name: "lore store compact (keep the newest " + strconv.Itoa(keep) + " commits)"}
	r := w.lore("store", "compact", "--store", "agency", "--before", strconv.FormatInt(int64(age/time.Second), 10)+"s", "--yes")
	c.add(r)
	c.Note = firstLine(r.out)
	if os.Getenv("STOREGROWTH_TRACE") == "1" {
		t.Logf("compaction output:\n%s", r.out)
	}
	run.Samples = append(run.Samples, c)
	if r.err != nil || !strings.Contains(r.out, "archived as refs/lore-archive/epoch-0") {
		t.Fatalf("compaction: %v\n%s", r.err, r.out)
	}
	run.Samples = append(run.Samples, Sample{Name: "Commits on main after", Value: w.git(w.bare, "rev-list", "--count", "main")})
	run.Samples = append(run.Samples, w.refsOnOldHistory())

	// the existing clone adopts by re-doing the rewrite
	compacted := w.clone + ".compacted"
	if err := os.Rename(w.clone, compacted); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(existing, w.clone); err != nil {
		t.Fatal(err)
	}
	a := Sample{Name: "Existing clone adopts (lore sync: verify, re-do, move)"}
	r = w.lore("sync")
	a.add(r)
	run.Samples = append(run.Samples, a)
	if r.err != nil || strings.Contains(r.out, "cannot verify") {
		t.Fatalf("adoption: %v\n%s", r.err, r.out)
	}
	_ = os.RemoveAll(compacted)

	run.Samples = append(run.Samples, Sample{Name: "Remote size, after (archive ref kept)", Value: w.du(w.bare)})
	fetchBytes("after")
	cold("after")
	run.Samples = append(run.Samples, w.trustedAfter(plan))
	run.Samples = append(run.Samples, w.cloneOnOldHistory())
	v := Sample{Name: "lore store compact --verify (fetch the archive, replay, re-do)"}
	r = w.lore("store", "compact", "--store", "agency", "--verify")
	v.add(r)
	v.Note = firstLine(r.out)
	run.Samples = append(run.Samples, v)
	if !strings.Contains(r.out, "verified against its archive") {
		t.Errorf("--verify: %v\n%s", r.err, r.out)
	}

	out := os.Getenv("STOREGROWTH_OUT")
	if out == "" {
		out = filepath.Join("..", "..", "..", "tmp", "bench")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(out, "compaction-"+strconv.Itoa(n)+".md")
	mustWrite(t, path, run.Markdown())
	t.Logf("report: %s\n%s", path, run.Markdown())
}

// trustedAfter checks a fresh clone of the compacted store: the planted
// changes were held back when the snapshot was made, so the snapshot holds
// their accepted versions (the self-promotion is not there) and nothing is
// held any more; a planted change kept after the cut would still be held.
func (w *world) trustedAfter(p Plan) Sample {
	s := Sample{Name: "Planted changes after compaction", Note: "the snapshot holds the accepted versions (spec 9: compaction never launders a held change)"}
	promo := w.git(w.clone, "show", "HEAD:"+p.PromotionPath)
	admin := strings.Contains(promo, `"org_role": "admin"`)
	r := w.lore("doctor")
	held := strings.Count(r.out, "held back")
	s.Value = "self-promotion in the tip " + strconv.FormatBool(admin) + ", " + strconv.Itoa(held) + " held line(s)"
	if admin && held == 0 {
		w.t.Errorf("the compacted store trusts the planted self-promotion:\n%s", promo)
	}
	return s
}

// refsOnOldHistory names the remote refs (other than the archive) that
// still reach the old history's root: each one makes every clone download
// the old history again.
func (w *world) refsOnOldHistory() Sample {
	root := w.git(w.bare, "rev-list", "--max-parents=0", "refs/lore-archive/epoch-0")
	var still []string
	for _, ref := range strings.Fields(w.git(w.bare, "for-each-ref", "--format=%(refname)")) {
		if strings.HasPrefix(ref, "refs/lore-archive/") {
			continue
		}
		cmd := exec.Command("git", "merge-base", "--is-ancestor", root, ref)
		cmd.Dir, cmd.Env = w.bare, w.env
		if cmd.Run() == nil {
			still = append(still, ref)
		}
	}
	return Sample{Name: "Remote refs still on the old history", Value: strconv.Itoa(len(still)) + " " + strings.Join(still, " ")}
}

// cloneOnOldHistory says whether the fresh clone holds the archived tip
// and which of its refs reach the old history's root.
func (w *world) cloneOnOldHistory() Sample {
	tip := w.git(w.bare, "rev-parse", "refs/lore-archive/epoch-0")
	root := w.git(w.bare, "rev-list", "--max-parents=0", tip)
	has := exec.Command("git", "cat-file", "-e", tip+"^{commit}")
	has.Dir, has.Env = w.clone, w.env
	var still []string
	for _, ref := range strings.Fields(w.git(w.clone, "for-each-ref", "--format=%(refname)")) {
		cmd := exec.Command("git", "merge-base", "--is-ancestor", root, ref)
		cmd.Dir, cmd.Env = w.clone, w.env
		if cmd.Run() == nil {
			still = append(still, ref)
		}
	}
	return Sample{Name: "Fresh clone after: holds the archived tip, refs on the old history",
		Value: strconv.FormatBool(has.Run() == nil) + "; " + strconv.Itoa(len(still)) + " " + strings.Join(still, " ")}
}
