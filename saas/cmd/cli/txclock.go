// txclock.go — monotonic tx_at for memories (R27 #29, chaos CH-2).
//
// tx_at is the transaction time of a memory: the order in which lore
// recorded facts. Wall clocks jump backwards (NTP corrections, VMs resumed
// from snapshots, a dev who changes the date), and a later write must
// never get an earlier tx_at than an earlier write, or "what did we know
// at time T" queries lie. Each DB keeps a high-water mark in its config
// table; a new memory gets max(now, high-water + txClockStep).
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"dbent/gen/ent"
	entConfig "dbent/gen/ent/dbconfig"
)

// txClockKey is the config row holding the DB's tx_at high-water mark
// (RFC3339Nano, UTC).
const txClockKey = "tx_clock_high_water"

// txClockStep is the smallest increment between two tx_at values, so
// "later" stays strictly later even when the clock stands still or rewinds.
const txClockStep = time.Microsecond

// memoryMutationType is ent's type name for memory mutations.
const memoryMutationType = "Memory"

// txAtSetter is implemented by the memory create mutation.
type txAtSetter interface {
	SetTxAt(t time.Time)
	Client() *ent.Client
}

// txAtMonotonicHook stamps every memory CREATE with a monotonic tx_at and
// advances the high-water mark.
func txAtMonotonicHook() ent.Hook {
	return func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if !m.Op().Is(ent.OpCreate) || m.Type() != memoryMutationType {
				return next.Mutate(ctx, m)
			}
			mm, ok := m.(txAtSetter)
			if !ok {
				return next.Mutate(ctx, m)
			}
			tx, err := nextTxAt(ctx, mm.Client(), time.Now().UTC())
			if err != nil {
				return nil, fmt.Errorf("tx_at clock: %w", err)
			}
			mm.SetTxAt(tx)
			return next.Mutate(ctx, m)
		})
	}
}

// nextTxAt returns max(now, high-water + step) and stores it as the new
// high-water mark, atomically: the read and the write run in ONE transaction,
// which this DSN begins IMMEDIATE (dbent.InitDB), so concurrent lore processes
// queue on the write lock instead of both reading the same mark (two equal
// tx_at values) or both inserting the first one (UNIQUE config.key failure,
// which made SC-5 lose a memory 1 run in 100). When the memory is being
// created inside a caller's transaction, that transaction already holds the
// write lock and is used as is.
func nextTxAt(ctx context.Context, client *ent.Client, now time.Time) (time.Time, error) {
	tx, err := client.Tx(ctx)
	if errors.Is(err, ent.ErrTxStarted) {
		return advanceTxClock(ctx, client, now) // already inside a transaction
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("begin tx_at clock transaction: %w", err)
	}
	next, err := advanceTxClock(ctx, tx.Client(), now)
	if err != nil {
		_ = tx.Rollback() // the clock error is the one to report
		return time.Time{}, err
	}
	if err := tx.Commit(); err != nil {
		return time.Time{}, fmt.Errorf("commit tx_at clock: %w", err)
	}
	return next, nil
}

// advanceTxClock is nextTxAt's read-modify-write; the caller provides the
// transaction that makes it atomic.
func advanceTxClock(ctx context.Context, client *ent.Client, now time.Time) (time.Time, error) {
	next := now
	row, err := client.DBConfig.Query().Where(entConfig.Key(txClockKey)).Only(ctx)
	switch {
	case ent.IsNotFound(err):
	case err != nil:
		return time.Time{}, err
	default:
		if row.Value == nil {
			break // key present without a value: treat as no high-water mark
		}
		if hw, perr := time.Parse(time.RFC3339Nano, *row.Value); perr == nil && !next.After(hw) {
			next = hw.Add(txClockStep)
		}
	}
	value := next.Format(time.RFC3339Nano)
	if row != nil {
		if err := client.DBConfig.UpdateOne(row).SetValue(value).Exec(ctx); err != nil {
			return time.Time{}, err
		}
		return next, nil
	}
	if err := client.DBConfig.Create().SetKey(txClockKey).SetValue(value).Exec(ctx); err != nil {
		return time.Time{}, err
	}
	return next, nil
}
