package folder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// Journal lines of two machines sharing one folder store (G-DECIDE-1).
const (
	journalMachineA = `{"machine":"mch_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","action":"create"}`
	journalMachineB = `{"machine":"mch_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","action":"create"}`
)

// TestJournalKeepsAnotherMachinesRecords (G-DECIDE-1, later 544): two
// machines on one shared folder store write pending audit records into
// the store's one journal (<store>/.lore-state, beside the lock), each
// record carrying its writer's machine id (the key lives in the record:
// engine journalRecord, replayJournal skips other machines' records). The
// backend half pinned here: a write by one machine keeps the other's
// pending records, an ack of one machine's records leaves the other's
// pending for their owner (byte for byte, machine id intact), and the
// journal goes away only when the last record is acked.
func TestJournalKeepsAnotherMachinesRecords(t *testing.T) {
	t.Parallel()
	dir := newStore(t)
	open := func() *Backend {
		b, err := Open(dir, rowfs.FolderRoot, store.OpenOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = b.Close() }) // test teardown
		return b
	}
	machA, machB := open(), open()
	actor, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	clock := hlc.NewClock(actor, time.Now)
	ctx := context.Background()
	write := func(b *Backend, line string) store.AuditRecord {
		t.Helper()
		r := holdRow(t, clock)
		rec := store.AuditRecord{Key: r.Key(), HLC: r.HLC, Line: []byte(line)}
		if err := b.WriteAudited(ctx, []store.Change{{Row: r}}, []store.AuditRecord{rec}); err != nil {
			t.Fatal(err)
		}
		return rec
	}
	recA1 := write(machA, journalMachineA)
	recB := write(machB, journalMachineB)
	recA2 := write(machA, journalMachineA)

	pending := func(b *Backend) []string {
		t.Helper()
		recs, err := b.PendingAudit(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(recs))
		for _, r := range recs {
			out = append(out, string(r.Line))
		}
		return out
	}
	if got := pending(machB); strings.Join(got, "|") != strings.Join([]string{journalMachineA, journalMachineB, journalMachineA}, "|") {
		t.Fatalf("pending after both machines wrote: %q", got)
	}
	if err := machA.AckAudit(ctx, []store.AuditRecord{recA1, recA2}); err != nil {
		t.Fatal(err)
	}
	if got := pending(machB); len(got) != 1 || got[0] != journalMachineB {
		t.Fatalf("machine A's ack touched machine B's records: %q", got)
	}
	journal := filepath.Join(dir, rowfs.StoreStateDir, rowfs.JournalFileName)
	if _, err := os.Stat(journal); err != nil {
		t.Fatalf("journal with B's record pending: %v", err)
	}
	if err := machB.AckAudit(ctx, []store.AuditRecord{recB}); err != nil {
		t.Fatal(err)
	}
	if got := pending(machA); len(got) != 0 {
		t.Fatalf("pending after both acks: %q", got)
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatalf("empty journal kept: %v", err)
	}
}
