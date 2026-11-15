package engine

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// fullCache is a cache whose Apply fails with ENOSPC once armed: the disk
// filled up between the row file write and the cache commit.
type fullCache struct {
	*fakeCache
	armed atomic.Bool
}

func (c *fullCache) Apply(ctx context.Context, src store.Source, rows []store.Row, next store.Cursor) error {
	if c.armed.Load() {
		return fmt.Errorf("sqlite cache: commit: %w", syscall.ENOSPC)
	}
	return c.fakeCache.Apply(ctx, src, rows, next)
}

// TestCacheFailAfterWriteSaysWritten (devco:U-7): a write whose row is
// stored but whose cache update fails on a full disk must say the row is
// written and not to rerun, so the retry a person makes never stores a
// duplicate row. Before, the bare cache error reached the disk-full hook
// as "nothing was changed ... run the command again".
func TestCacheFailAfterWriteSaysWritten(t *testing.T) {
	fc := &fullCache{fakeCache: newFakeCache()}
	f := setup(t, WithCache(fc))
	fc.armed.Store(true)
	before := storedMemories(t, f)
	_, err := f.eng.Create(context.Background(), "", schema.TableMemories, f.memFields("disk fills up now"))
	if err == nil {
		t.Fatal("create with a failing cache returned no error")
	}
	w := errs.WrittenOf(err)
	if w == nil {
		t.Fatalf("error is not marked written: %v", err)
	}
	if !strings.Contains(w.Message, " is written") || !strings.Contains(w.Hint, "do not rerun") {
		t.Fatalf("message/hint must say the row is written and not to rerun: %q / %q", w.Message, w.Hint)
	}
	if got := storedMemories(t, f); got != before+1 {
		t.Fatalf("stored memories = %d, want %d (the row file is written)", got, before+1)
	}
}

// storedMemories counts the memory rows the repo backend holds.
func storedMemories(t *testing.T, f *fixture) int {
	t.Helper()
	rows, _, err := f.repo.Open(store.OpenOptions{}).Read(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range rows {
		if r.Table == schema.TableMemories {
			n++
		}
	}
	return n
}
