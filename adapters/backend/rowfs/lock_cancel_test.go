package rowfs

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// D-45: TestLock flaked under load because a cancelled wait answered
// ErrLocked: Lock passes acquireFile the wait left after the gate, which a
// loaded machine can have spent already, and the deadline was checked
// before ctx. A cancelled caller is told it was cancelled, whatever the
// wait left. Break-verified: the deadline check first fails this test.
func TestLockCancelledBeatsSpentWait(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), LockFileName)
	held, err := acquireFile(context.Background(), path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.release() }()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, wait := range []time.Duration{0, -time.Millisecond, time.Second} {
		if _, err := acquireFile(cancelled, path, wait); !errors.Is(err, context.Canceled) {
			t.Errorf("busy lock, cancelled ctx, wait %v: err = %v, want context.Canceled", wait, err)
		}
	}
}
