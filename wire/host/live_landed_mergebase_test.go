package host

// live_landed_mergebase_test.go — T-3: a list read through origin/<default>
// every branch row whose file differs between HEAD and origin (two reads
// and two decodes per row). After `lore migrate run` is committed and not
// pushed every row file differs, and reads of a 5,500-row repo took
// 15-25 s. Only a file origin changed since the merge base can be a copy
// that landed after this checkout's last pull; a file only HEAD changed is
// this checkout's own, and is neither read from origin nor replaced.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// mbTaskID is the id of the i-th generated task (made-up, valid ids).
func mbTaskID(i int) string { return fmt.Sprintf("tsk_01a1d4e2c0de7b3a9f5e7d10%08x", i) }

// mbCommitRows writes the i-th task of n at sec (title prefixed) to the
// work tree, sec(i) choosing each row's time, and commits them.
func mbCommitRows(t testing.TB, dir string, n int, sec func(int) int, title, msg string) []store.Row {
	t.Helper()
	rows := make([]store.Row, n)
	for i := range n {
		r := ltRow(t, schema.TableTasks, mbTaskID(i), sec(i), fmt.Sprintf("%s %d", title, i), false)
		rows[i] = r
		full := filepath.Join(dir, filepath.FromSlash(mainDataPath(live.RootFolder, schema.TableTasks+"/"+mbTaskID(i)+rowFileExt)))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, r.Doc, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", msg)
	return rows
}

// mbFixture: n tasks landed at t=5 (the merge base); origin then changed
// task 0 (t=9); this checkout rewrote every task at t=1 (a local commit,
// not pushed: the shape of a committed migration). The branch store holds
// HEAD's rows, as the row folder would.
func mbFixture(t testing.TB, n int) ltFixture {
	t.Helper()
	f := newLiveFixture(t)
	dir := f.b.top
	mbCommitRows(t, dir, n, func(int) int { return 5 }, "landed", "base")
	mbCommitRows(t, dir, n, func(i int) int {
		if i == 0 {
			return 9
		}
		return 5
	}, "landed", "origin changed task 0")
	gitT(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	gitT(t, dir, "reset", "-q", "--hard", "HEAD~1")
	local := mbCommitRows(t, dir, n, func(int) int { return 1 }, "local", "local rewrite, not pushed")
	changes := make([]store.Change, n)
	for i, r := range local {
		changes[i] = store.Change{Row: r}
	}
	if err := f.inner.Write(f.ctx, changes); err != nil {
		t.Fatal(err)
	}
	return f
}

// TestLiveBackend_ListKeepsLocalOnlyChanges: a list shows HEAD's copy of
// every row origin has not touched since the merge base (even though
// origin's copy carries a later hlc), and still reads through the one row
// origin changed after it (task 0). Break-verification: without the
// merge-base rule every row showed origin's t=5 copy.
func TestLiveBackend_ListKeepsLocalOnlyChanges(t *testing.T) {
	const n = 40
	f := mbFixture(t, n)
	rows, _, err := f.b.Read(f.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, r := range rows {
		if r.Table != schema.TableTasks {
			continue
		}
		seen++
		want := ltRow(t, schema.TableTasks, r.ID.String(), 1, "", false).HLC
		if r.ID.String() == mbTaskID(0) {
			want = ltRow(t, schema.TableTasks, r.ID.String(), 9, "", false).HLC
		}
		if r.HLC.Compare(want) != 0 {
			t.Errorf("%s: hlc %s, want %s", r.ID, r.HLC, want)
		}
	}
	if seen != n {
		t.Fatalf("listed %d tasks, want %d", seen, n)
	}
	// Get agrees with the list (landedCopy's own merge-base check).
	for _, i := range []int{0, 1, n - 1} {
		r, err := f.b.Get(f.ctx, schema.TableTasks, mustAnyID(t, mbTaskID(i)))
		if err != nil {
			t.Fatal(err)
		}
		sec := 1
		if i == 0 {
			sec = 9
		}
		if want := ltRow(t, schema.TableTasks, mbTaskID(i), sec, "", false).HLC; r.HLC.Compare(want) != 0 {
			t.Errorf("Get %s: hlc %s, want %s", mbTaskID(i), r.HLC, want)
		}
	}
}

// TestLiveBackend_ListOfLocallyRewrittenRepoIsQuick is the timing guard of
// T-3 with a generous bound: one full read of 3,000 rows that HEAD
// rewrote and origin did not. Before the fix it read and decoded 6,000
// files from git; now it is two ls-trees (well under a second; the bound
// is 15 s so a loaded machine never fails it).
func TestLiveBackend_ListOfLocallyRewrittenRepoIsQuick(t *testing.T) {
	if testing.Short() {
		t.Skip("builds 3,000 row files")
	}
	f := mbFixture(t, 3000)
	start := time.Now()
	rows, _, err := f.b.Read(f.ctx, "")
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 3000 {
		t.Fatalf("listed %d rows", len(rows))
	}
	t.Logf("full read of 3,000 locally rewritten rows: %s", took.Round(time.Millisecond))
	if took > 15*time.Second {
		t.Fatalf("a full read of 3,000 locally rewritten rows took %s (T-3: each row was read and decoded from git twice)", took)
	}
}

// BenchmarkLiveReadLocallyRewritten is a full read of 3,000 rows that HEAD
// rewrote and origin did not (a committed, unpushed migration).
func BenchmarkLiveReadLocallyRewritten(b *testing.B) {
	f := mbFixture(b, 3000)
	b.ResetTimer()
	for range b.N {
		if _, _, err := f.b.Read(f.ctx, ""); err != nil {
			b.Fatal(err)
		}
	}
}
