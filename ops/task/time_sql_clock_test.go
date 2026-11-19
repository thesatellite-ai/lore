package task_test

import (
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/wire"
)

// TestTimeSpentOpenSpanEndsOnTheAppClock (devco:D-460, the clock class):
// a running timer's span in `lore sql`'s time_spent view ends at the app
// clock (LORE_NOW in a test build, the harness's Wall here), as `lore task
// list` measures it, never the machine's real clock: the same rows under
// one fixed clock print the same seconds on any real day.
func TestTimeSpentOpenSpanEndsOnTheAppClock(t *testing.T) {
	base := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	var off atomic.Int64
	h := opstest.New(t, repoClient(false), func(o *wire.Options) { // time data lives in internal stores only (PC-LEAK-1)
		o.Wall = func() time.Time { return base.Add(time.Duration(off.Load())) }
	})
	id := add(h, "running work", "")
	h.Must(opstask.OpStart, `{"id":"`+id+`"}`)
	off.Store(int64(3 * time.Hour))
	got := timeSQL(t, h, `SELECT seconds, end_at FROM time_spent WHERE is_open = 1`)
	if want := []string{"10800|2026-11-03T11:00:00.000Z"}; !slices.Equal(got, want) {
		t.Errorf("open span: %q, want %q (3h on the app clock)", got, want)
	}
}
