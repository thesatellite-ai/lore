package main

import (
	"context"
	"testing"
	"time"
)

// A wall clock that jumps backwards must not produce an earlier tx_at:
// the high-water mark wins and later writes stay strictly later.
func TestTxAtMonotonicAcrossClockRewind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := openTestClient(t)
	c.Use(txAtMonotonicHook())
	future := time.Now().UTC().Add(time.Hour)
	if err := c.DBConfig.Create().SetKey(txClockKey).SetValue(future.Format(time.RFC3339Nano)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	const project = "prj_01a10237c9a279ee94a32b8450dfb6f9"
	var prev time.Time
	for i := range 3 {
		m, err := c.Memory.Create().SetProjectID(project).SetBody("m").SetSourceKind("manual").Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !m.TxAt.After(future) {
			t.Fatalf("write %d: tx_at %v not after the high-water mark %v", i, m.TxAt, future)
		}
		if i > 0 && !m.TxAt.After(prev) {
			t.Fatalf("tx_at went backwards or stood still: %v then %v", prev, m.TxAt)
		}
		prev = m.TxAt
	}
	// Normal clock: tx_at is simply now.
	fresh := openTestClient(t)
	fresh.Use(txAtMonotonicHook())
	before := time.Now().UTC()
	m, err := fresh.Memory.Create().SetProjectID(project).SetBody("m").SetSourceKind("manual").Save(ctx)
	if err != nil || m.TxAt.Before(before.Add(-time.Second)) {
		t.Fatalf("tx_at = %v err %v", m.TxAt, err)
	}
}
