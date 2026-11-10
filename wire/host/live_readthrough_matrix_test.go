package host

// f-stale class sweep of t-old2 (the read-through E_STALE, fixed in
// f02213ad): every write shape a command can send for a row Get read
// through from origin/<default> (live_landed.go landedCopy), on both write
// paths (Write and the journal's WriteAudited), for both read-through
// cases (the work tree has no copy after a committed delete, or holds an
// older copy equal to HEAD). Each write expects the landed copy Get served;
// it must land in the branch data, not answer E_STALE, and an Expect that
// names any other version must still be refused.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// rtWriter is one of the liveBackend's two branch write entry points.
type rtWriter struct {
	name  string
	write func(ctx context.Context, b *liveBackend, chs []store.Change) error
}

var rtWriters = []rtWriter{
	{"Write", func(ctx context.Context, b *liveBackend, chs []store.Change) error { return b.Write(ctx, chs) }},
	{"WriteAudited", func(ctx context.Context, b *liveBackend, chs []store.Change) error {
		return b.WriteAudited(ctx, chs, nil)
	}},
}

// rtCommitFile writes r's file at its main data path in dir.
func rtCommitFile(t *testing.T, dir string, r store.Row) string {
	t.Helper()
	rel := mainDataPath(live.RootFolder, string(r.Table)+"/"+r.ID.String()+rowFileExt)
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, r.Doc, 0o600); err != nil {
		t.Fatal(err)
	}
	return rel
}

// TestLiveBackend_ReadThroughWriteMatrix: {no branch copy, older branch
// copy} x {edit, delete, restore of a landed tombstone} x {Write,
// WriteAudited}.
func TestLiveBackend_ReadThroughWriteMatrix(t *testing.T) {
	type tc struct {
		name        string
		landedGone  bool // the landed copy on origin/main is a tombstone
		deleteWrite bool // the write is a soft delete
		olderInTree bool // the work tree holds an older copy (== HEAD)
	}
	var cases []tc
	for _, older := range []bool{false, true} {
		cases = append(cases,
			tc{name: "edit", olderInTree: older},
			tc{name: "delete", deleteWrite: true, olderInTree: older},
			tc{name: "restore", landedGone: true, olderInTree: older},
		)
	}
	for _, w := range rtWriters {
		for _, c := range cases {
			where := "no branch copy"
			if c.olderInTree {
				where = "older branch copy"
			}
			t.Run(w.name+"/"+c.name+"/"+where, func(t *testing.T) {
				f := newLiveFixture(t)
				j := memstore.NewData().OpenJournaled(store.OpenOptions{})
				f.b.inner, f.inner = j, j // WriteAudited needs a journal inner
				dir := f.b.top
				older := ltRow(t, schema.TableMemories, ltMemory, 1, "first", false)
				landed := ltRow(t, schema.TableMemories, ltMemory, 2, "second", c.landedGone)
				if c.olderInTree {
					// origin/main holds the newer copy; HEAD and the work tree the older one.
					rtCommitFile(t, dir, landed)
					gitT(t, dir, "add", "-A")
					gitT(t, dir, "commit", "-q", "-m", "landed")
					gitT(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
					gitT(t, dir, "reset", "-q", "--hard", "HEAD~1")
					rtCommitFile(t, dir, older)
					gitT(t, dir, "add", "-A")
					gitT(t, dir, "commit", "-q", "-m", "older")
					if err := j.Write(f.ctx, []store.Change{{Row: older}}); err != nil {
						t.Fatal(err)
					}
				} else {
					// origin/main holds the row; this checkout committed its delete.
					rel := rtCommitFile(t, dir, landed)
					gitT(t, dir, "add", "-A")
					gitT(t, dir, "commit", "-q", "-m", "landed")
					gitT(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
					gitT(t, dir, "rm", "-q", rel)
					gitT(t, dir, "commit", "-q", "-m", "rm")
				}
				got, err := f.b.Get(f.ctx, landed.Table, landed.ID)
				if err != nil || got.HLC.Compare(landed.HLC) != 0 || got.Deleted != c.landedGone {
					t.Fatalf("Get reads the landed copy through: %+v %v", got.HLC, err)
				}
				next := ltRow(t, schema.TableMemories, ltMemory, 3, "edited", c.deleteWrite)
				other := ltRow(t, schema.TableMemories, ltMemory, 9, "x", false)
				if err = w.write(f.ctx, f.b, []store.Change{{Row: next, Expect: other.HLC}}); !errors.Is(err, store.ErrStale) {
					t.Fatalf("an Expect naming another version stays E_STALE: %v", err)
				}
				if c.olderInTree {
					if err = w.write(f.ctx, f.b, []store.Change{{Row: next, Expect: older.HLC}}); !errors.Is(err, store.ErrStale) {
						t.Fatalf("an Expect naming the older work-tree copy Get did not serve stays E_STALE: %v", err)
					}
				}
				if err = w.write(f.ctx, f.b, []store.Change{{Row: next, Expect: got.HLC}}); err != nil {
					t.Fatalf("a write of the read-through copy lands, not E_STALE: %v", err)
				}
				br, err := f.inner.Get(f.ctx, next.Table, next.ID)
				if err != nil || br.HLC.Compare(next.HLC) != 0 || br.Deleted != c.deleteWrite {
					t.Fatalf("the write materialises in the branch data: %+v %v", br.HLC, err)
				}
				// The next write is an ordinary one over the branch copy.
				after := ltRow(t, schema.TableMemories, ltMemory, 4, "again", !c.deleteWrite)
				if err = w.write(f.ctx, f.b, []store.Change{{Row: after, Expect: next.HLC}}); err != nil {
					t.Fatalf("the following write expects the materialised copy: %v", err)
				}
			})
		}
	}
}
