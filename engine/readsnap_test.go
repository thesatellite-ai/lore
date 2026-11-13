package engine

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// countingBackend counts full reads (cursor "").
type countingBackend struct {
	store.Backend
	full *atomic.Int64
}

func (b countingBackend) Read(ctx context.Context, c store.Cursor) ([]store.Row, store.Cursor, error) {
	if c == "" {
		b.full.Add(1)
	}
	return b.Backend.Read(ctx, c)
}

// TestReadSnapshot (item 41): under WithReadSnapshot a store is read once
// however many reads ask for it; a write through the engine drops the
// snapshot so the next read goes to the backend again; without a snapshot
// every read goes to the backend; two snapshots never share reads.
func TestReadSnapshot(t *testing.T) {
	t.Parallel()
	var n atomic.Int64
	st := Attached{Name: "kb", Backend: countingBackend{memstore.NewData().Open(store.OpenOptions{}), &n}}
	e := &Engine{stores: map[store.Source]Attached{"kb": st}}
	base := context.Background()

	for range 3 {
		if _, _, err := readAll(base, st); err != nil {
			t.Fatal(err)
		}
	}
	if got := n.Load(); got != 3 {
		t.Fatalf("no snapshot: %d full reads, want 3", got)
	}

	n.Store(0)
	ctx := WithReadSnapshot(base)
	for range 5 {
		if _, _, err := readAll(ctx, st); err != nil {
			t.Fatal(err)
		}
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("snapshot: %d full reads, want 1", got)
	}
	if err := e.writeChanges(ctx, st, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readAll(ctx, st); err != nil {
		t.Fatal(err)
	}
	if got := n.Load(); got != 2 {
		t.Fatalf("after a write: %d full reads, want 2 (the write drops the snapshot)", got)
	}

	if _, _, err := readAll(WithReadSnapshot(base), st); err != nil {
		t.Fatal(err)
	}
	if got := n.Load(); got != 3 {
		t.Fatalf("a new snapshot: %d full reads, want 3", got)
	}
}

// slowBackend is countingBackend whose full read waits for release, so a
// test can have many readers ask while the first read is still running.
type slowBackend struct {
	countingBackend
	started chan struct{}
	release chan struct{}
}

func (b slowBackend) Read(ctx context.Context, c store.Cursor) ([]store.Row, store.Cursor, error) {
	if c == "" {
		select {
		case b.started <- struct{}{}:
		default:
		}
		<-b.release
	}
	return b.countingBackend.Read(ctx, c)
}

// TestReadSnapshotSharesAConcurrentFill: doctor's checks run in parallel
// under one snapshot; readers that ask while the first full read runs wait
// for it instead of each reading the whole store (a 5,500-row repo was read
// ~20 times at once per doctor run).
func TestReadSnapshotSharesAConcurrentFill(t *testing.T) {
	t.Parallel()
	var n atomic.Int64
	b := slowBackend{
		countingBackend: countingBackend{memstore.NewData().Open(store.OpenOptions{}), &n},
		started:         make(chan struct{}, 1),
		release:         make(chan struct{}),
	}
	st := Attached{Name: "kb", Backend: b}
	ctx := WithReadSnapshot(context.Background())
	const readers = 8
	errc := make(chan error, readers)
	go func() { _, _, err := readAll(ctx, st); errc <- err }()
	<-b.started // the first reader is filling
	for range readers - 1 {
		go func() { _, _, err := readAll(ctx, st); errc <- err }()
	}
	close(b.release)
	for range readers {
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("%d readers under one snapshot made %d full reads, want 1", readers, got)
	}
}
