package lsync

import (
	"context"
	"fmt"
	"testing"
	"time"

	"saas/pkg/aicoder/ids"
)

// seedMemories inserts n memories in one transaction (fast path for
// benchmarks; ent per-row saves would dominate the measurement).
func seedMemories(b *testing.B, e *env, n int) {
	b.Helper()
	tx, err := e.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	now := time.Now().UTC()
	for i := range n {
		if _, err := tx.Exec(`INSERT INTO memories(id, created_at, updated_at, trust_score, source_kind, kind, body, tx_at, project_id)
			VALUES (?, ?, ?, 0.5, 'manual', 'retrieved', ?, ?, ?)`,
			ids.MustNew(ids.PrefixMemory), now, now, fmt.Sprintf("memory body number %d with some realistic length text", i), now, e.project); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
}

func benchEnv(b *testing.B, n int) *env {
	b.Helper()
	t := &testing.T{}
	e := newEnv(t)
	e.t = t
	seedMemories(b, e, n)
	return e
}

// BenchmarkReconcileNoop is the per-command overhead once everything is in
// sync: a directory walk with the stat cache plus two small queries.
func BenchmarkReconcileNoop(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			e := benchEnv(b, n)
			if _, err := Reconcile(context.Background(), e.opts()); err != nil {
				b.Fatal(err)
			}
			// Let the racy window pass so the steady state is measured.
			time.Sleep(racyWindow + 100*time.Millisecond)
			if _, err := Reconcile(context.Background(), e.opts()); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for b.Loop() {
				if _, err := Reconcile(context.Background(), e.opts()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkBootstrap measures the one-time export of an existing DB.
func BenchmarkBootstrap(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			for b.Loop() {
				b.StopTimer()
				e := benchEnv(b, n)
				b.StartTimer()
				if _, err := Reconcile(context.Background(), e.opts()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkAdoptFreshClone measures building a cache from files (fresh clone).
func BenchmarkAdoptFreshClone(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			src := benchEnv(b, n)
			if _, err := Reconcile(context.Background(), src.opts()); err != nil {
				b.Fatal(err)
			}
			for b.Loop() {
				b.StopTimer()
				t := &testing.T{}
				dst := newEmptyEnv(t)
				dst.t = t
				src.copyDataTo(dst)
				b.StartTimer()
				if _, err := Reconcile(context.Background(), dst.opts()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
