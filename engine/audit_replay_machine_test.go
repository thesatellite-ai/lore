package engine

import (
	"context"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// machineProv is a Provenance whose only fact is the writing machine.
type machineProv struct{ machine ids.MachineID }

func (machineProv) Place(context.Context) origin.Facts    { return origin.Facts{} }
func (machineProv) Upstream(context.Context, string) bool { return false }
func (p machineProv) Writer() (string, ids.MachineID)     { return "0.1.18", p.machine }

func machineOf(t *testing.T, hex byte) ids.MachineID {
	t.Helper()
	s := "mch_"
	for range 64 {
		s += string(hex)
	}
	m, err := ids.ParseMachine(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestJournalReplayOnlyOwnMachine (G-DECIDE-1, later 544): a folder store
// on a shared folder holds every machine's pending audit records; a write
// replays only the records its own machine wrote. The writer is the
// Provenance machine id; no id ("") matches only records with no id.
func TestJournalReplayOnlyOwnMachine(t *testing.T) {
	t.Parallel()
	a, b := machineOf(t, 'a'), machineOf(t, 'b')
	cases := []struct {
		name          string
		writer, other ids.MachineID
		otherReplays  bool
	}{
		{"another machine leaves it pending", a, b, false},
		{"the same machine replays it", a, a, true},
		{"a machine with an id leaves a record with none", ids.MachineID{}, a, false},
		{"no id leaves a machine's record", a, ids.MachineID{}, false},
		{"no id replays a record with none", ids.MachineID{}, ids.MachineID{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			sid, err := ids.New[ids.Store]()
			if err != nil {
				t.Fatal(err)
			}
			if err = folder.Init(dir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: rowfs.VisibilityInternal, Created: "2026-11-05"}); err != nil {
				t.Fatal(err)
			}
			open := func() *folder.Backend {
				fb, oerr := folder.Open(dir, rowfs.FolderRoot, store.OpenOptions{})
				if oerr != nil {
					t.Fatal(oerr)
				}
				t.Cleanup(func() { _ = fb.Close() }) // test teardown
				return fb
			}
			clk := &testClock{now: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)}
			engineOn := func(m ids.MachineID, sink AuditSink) *fixture {
				return setup(t, WithStore(Attached{Name: kbStore, Backend: open(), KeyNamespace: sid.String()}),
					WithAudit(sink), WithWall(clk.wall), WithProvenance(machineProv{machine: m}))
			}
			downSink := &flakySink{}
			writer := engineOn(c.writer, downSink)
			downSink.setFail(errs.New(errs.Internal, "audit log is read-only"))
			first := kbKey(t, writer.eng, "client")
			if !hasWarn(first.Warnings, WarnAuditDeferred) {
				t.Fatalf("warnings = %+v, want W_AUDIT_DEFERRED", first.Warnings)
			}
			otherSink := &flakySink{}
			other := engineOn(c.other, otherSink)
			clk.advance(journalReplayAge + time.Second)
			kbKey(t, other.eng, "env")
			j := open()
			replayed := otherSink.recorded(first.Row.ID) == 1
			pending := pendingIDs(t, j)[first.Row.ID] == 1
			if replayed != c.otherReplays || pending == c.otherReplays {
				t.Fatalf("replayed by the other machine %v, still pending %v; want replayed %v", replayed, pending, c.otherReplays)
			}
			if c.otherReplays {
				return
			}
			// the writing machine replays its own record on its next write
			downSink.setFail(nil)
			kbKey(t, writer.eng, "region")
			if n := downSink.recorded(first.Row.ID); n != 1 {
				t.Fatalf("own record replayed %d times, want 1", n)
			}
			if p := pendingIDs(t, j)[first.Row.ID]; p != 0 {
				t.Fatalf("own record still pending after its replay")
			}
		})
	}
}
