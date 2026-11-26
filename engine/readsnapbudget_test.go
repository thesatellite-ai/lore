package engine

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// countedRows is a backend whose full read is rows, counted.
type countedRows struct {
	store.Backend
	rows  []store.Row
	reads atomic.Int64
}

func (b *countedRows) Read(context.Context, store.Cursor) ([]store.Row, store.Cursor, error) {
	b.reads.Add(1)
	return b.rows, "c1", nil
}

// TestReadSnapshotKeepsABoundedShare (bug hunt H-280, devco:D-1018): a
// read snapshot keeps its stores' reads up to snapshotBudget bytes; past
// it the oldest read is dropped (its next reader reads that store again)
// and the newest is kept. Before, a doctor run's snapshot kept every
// folder's read for the whole run: every row of a 1.26M-row store.
func TestReadSnapshotKeepsABoundedShare(t *testing.T) {
	doc := []byte(`{"_table":"memories","body":"` + strings.Repeat("x", 1000) + `"}`)
	mk := func() *countedRows {
		b := &countedRows{}
		for range 10 {
			b.rows = append(b.rows, store.Row{Table: schema.TableMemories, Doc: doc})
		}
		return b
	}
	backs := []*countedRows{mk(), mk(), mk()}
	one := snapRowsBytes(backs[0].rows)
	was := snapshotBudget
	snapshotBudget = one + one/2 // one store's read fits, two do not
	t.Cleanup(func() { snapshotBudget = was })
	ctx := WithReadSnapshot(context.Background())
	for _, b := range backs {
		if _, _, err := readAll(ctx, Attached{Name: "kb", Backend: b}); err != nil {
			t.Fatal(err)
		}
	}
	s := snapshotOf(ctx)
	s.mu.Lock()
	kept, bytes := len(s.full), s.keptBytes
	s.mu.Unlock()
	if kept != 1 || bytes > snapshotBudget {
		t.Errorf("the snapshot keeps %d store reads, %d bytes: want the newest only, within %d", kept, bytes, snapshotBudget)
	}
	for i := len(backs) - 1; i >= 0; i-- { // newest first: each read again evicts the one before it
		b := backs[i]
		if _, _, err := readAll(ctx, Attached{Name: "kb", Backend: b}); err != nil {
			t.Fatal(err)
		}
		want := int64(2)
		if i == len(backs)-1 {
			want = 1 // the newest read is still kept
		}
		if got := b.reads.Load(); got != want {
			t.Errorf("store %d was read %d times, want %d", i, got, want)
		}
	}
}

// TestReadMemosStayWithinTheirBudget (bug hunt H-280, devco:D-1018):
// the snapshot's decode memo and the read-mask scan memo stop taking rows
// once they hold their budget, instead of keeping every row a run read.
func TestReadMemosStayWithinTheirBudget(t *testing.T) {
	wasDecode, wasScan := decodeMemoBudget, scanMemoBudget
	decodeMemoBudget, scanMemoBudget = 4096, 4096
	t.Cleanup(func() { decodeMemoBudget, scanMemoBudget = wasDecode, wasScan })
	ctx := WithReadSnapshot(context.Background())
	for i := range 200 {
		id, err := ids.New[ids.Memory]()
		if err != nil {
			t.Fatal(err)
		}
		r := row.Row{Table: schema.TableMemories, ID: id.Any(), HLC: hlc.HLC{Wall: time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC), Actor: testActor}, Fields: map[string]value.Value{"body": txt(strings.Repeat("y", 100))}}
		sr, err := store.FromRow(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decode(ctx, sr); err != nil {
			t.Fatal(err)
		}
	}
	s := snapshotOf(ctx)
	s.mu.Lock()
	memo, memoBytes := len(s.decoded), s.decodedBytes
	s.mu.Unlock()
	if memoBytes > decodeMemoBudget || memo >= 200 {
		t.Errorf("the decode memo holds %d rows, %d bytes: want at most %d bytes", memo, memoBytes, decodeMemoBudget)
	}
	e := &Engine{readScanner: noFindings{}}
	for i := range 200 {
		e.scanRead("body", strings.Repeat("z", 100)+string(rune('a'+i%26))+strings.Repeat("q", i))
	}
	m := e.masks()
	m.mu.Lock()
	scanned, scanBytes := len(m.memo), m.memoBytes
	m.mu.Unlock()
	if scanBytes > scanMemoBudget || scanned >= 200 {
		t.Errorf("the scan memo holds %d texts, %d bytes: want at most %d bytes", scanned, scanBytes, scanMemoBudget)
	}
}

// noFindings is a scanner that finds nothing.
type noFindings struct{}

func (noFindings) Scan(string, string) []scan.Finding { return nil }

// TestReadSnapshotDropsTheReadUsedLongestAgo (bug hunt H-280,
// devco:D-1018): past the budget the snapshot drops the read used
// longest ago, not the one kept first: a store every check reads stays,
// and is not read again from git over and over (a 100,000-row folder
// read that way made a first doctor slower than before the budget).
func TestReadSnapshotDropsTheReadUsedLongestAgo(t *testing.T) {
	doc := []byte(`{"_table":"memories","body":"` + strings.Repeat("x", 1000) + `"}`)
	mk := func() *countedRows {
		b := &countedRows{}
		for range 10 {
			b.rows = append(b.rows, store.Row{Table: schema.TableMemories, Doc: doc})
		}
		return b
	}
	hot, cold, late := mk(), mk(), mk()
	one := snapRowsBytes(hot.rows)
	was := snapshotBudget
	snapshotBudget = 2*one + one/2 // two stores' reads fit, three do not
	t.Cleanup(func() { snapshotBudget = was })
	ctx := WithReadSnapshot(context.Background())
	read := func(b *countedRows) {
		if _, _, err := readAll(ctx, Attached{Name: "kb", Backend: b}); err != nil {
			t.Fatal(err)
		}
	}
	read(hot)
	read(cold)
	read(hot) // used again: now the most recent
	read(late)
	read(hot)
	if got := hot.reads.Load(); got != 1 {
		t.Errorf("the store read most recently was read %d times, want 1: the snapshot dropped it before one used longer ago", got)
	}
	read(cold)
	if got := cold.reads.Load(); got != 2 {
		t.Errorf("the store used longest ago was read %d times, want 2 (dropped, read again)", got)
	}
}
