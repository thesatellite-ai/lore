package rowfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// The pending audit journal (store.AuditJournal; CLEANUP Qs): audit
// records written in the same batch as the rows, kept until the engine
// acknowledges them. It sits beside the store lock, in a state folder:
// <repo>/.lore/state for the code repo (git-ignored by `lore init`),
// <store>/.lore/state for a folder store, and the clone folder outside the
// worktree for a git clone, so it is never committed and never synced.
//
//	ApplyAudited: journal (old + new lines) → Apply rows; a row failure
//	              restores the journal; Applied.Undo restores both.
//	AckJournal:   under the lock, rewrite the journal without the acked lines.
const (
	// JournalDir is the state folder beside the lock file.
	JournalDir = "state"
	// JournalFileName is the pending audit journal, one JSON object a line.
	JournalFileName = "audit-pending.jsonl"
)

// journalLine is one pending record on disk.
type journalLine struct {
	Table string          `json:"table"`
	ID    string          `json:"id"`
	HLC   string          `json:"hlc"`
	Event json.RawMessage `json:"event"`
}

// JournalPath is the absolute path of the pending audit journal.
func (r *Root) JournalPath() string {
	return filepath.Join(filepath.Dir(r.lockPath), JournalDir, JournalFileName)
}

// readJournal returns the journal's bytes (nil when absent) and records.
func (r *Root) readJournal() ([]byte, []store.AuditRecord, error) {
	raw, err := os.ReadFile(filepath.Clean(r.JournalPath()))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, errs.Wrap(errs.Internal, "rowfs: read the audit journal", err)
	}
	var out []store.AuditRecord
	for ln := range bytes.SplitSeq(raw, []byte("\n")) {
		if len(bytes.TrimSpace(ln)) == 0 {
			continue
		}
		rec, ok := decodeJournalLine(ln)
		if !ok {
			continue // a torn or foreign line is never a record; a rewrite drops it
		}
		out = append(out, rec)
	}
	return raw, out, nil
}

// decodeJournalLine parses one line; ok is false for anything unusable.
func decodeJournalLine(ln []byte) (store.AuditRecord, bool) {
	var j journalLine
	if json.Unmarshal(ln, &j) != nil {
		return store.AuditRecord{}, false
	}
	table, err := row.ParseTable(j.Table)
	if err != nil {
		return store.AuditRecord{}, false
	}
	id, err := ids.ParseAny(j.ID)
	if err != nil {
		return store.AuditRecord{}, false
	}
	h, err := hlc.Parse(j.HLC)
	if err != nil {
		return store.AuditRecord{}, false
	}
	return store.AuditRecord{Key: store.Key{Table: table, ID: id}, HLC: h, Line: []byte(j.Event)}, true
}

// encodeJournal renders recs as journal lines.
func encodeJournal(recs []store.AuditRecord) ([]byte, error) {
	var buf bytes.Buffer
	for _, rc := range recs {
		if !json.Valid(rc.Line) {
			return nil, errs.New(errs.InvalidInput, "rowfs: an audit record is not one JSON value")
		}
		var ev bytes.Buffer
		if err := json.Compact(&ev, rc.Line); err != nil {
			return nil, errs.Wrap(errs.InvalidInput, "rowfs: compact an audit record", err)
		}
		b, err := json.Marshal(journalLine{Table: rc.Key.Table.String(), ID: rc.Key.ID.String(), HLC: rc.HLC.String(), Event: ev.Bytes()})
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "rowfs: encode an audit record", err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// ApplyAudited is Apply with recs appended to the pending journal in the
// same batch (store.AuditJournal.WriteAudited). The caller holds the lock.
// The journal is written first: a crash between the two leaves a pending
// record of a write that may not have landed (the engine's replay records
// it), never a stored row without its record.
func (f *Folder) ApplyAudited(ctx context.Context, changes []store.Change, recs []store.AuditRecord) (*Applied, error) {
	if len(recs) == 0 {
		return f.Apply(ctx, changes)
	}
	if err := ValidateBatch(changes); err != nil {
		return nil, err
	}
	add, err := encodeJournal(recs)
	if err != nil {
		return nil, err
	}
	prev, _, err := f.root.readJournal()
	if err != nil {
		return nil, err
	}
	full := f.root.JournalPath()
	next := append(bytes.Clone(prev), add...)
	if err := writeAtomic(full, next); err != nil {
		return nil, errs.Wrap(errs.Internal, "rowfs: write the audit journal", err)
	}
	journal := undoStep{full: full, prev: prev}
	a, err := f.Apply(ctx, changes)
	if err != nil {
		return nil, errors.Join(err, (&Applied{undo: []undoStep{journal}}).Undo())
	}
	a.undo = append([]undoStep{journal}, a.undo...) // restored last by Undo
	return a, nil
}

// PendingJournal lists the pending records, oldest first.
func (r *Root) PendingJournal() ([]store.AuditRecord, error) {
	_, recs, err := r.readJournal()
	return recs, err
}

// AckJournal removes the pending records matching acked (Key and HLC)
// under the store lock; nothing pending is a no-op.
func (r *Root) AckJournal(ctx context.Context, acked []store.AuditRecord) error {
	if len(acked) == 0 {
		return nil
	}
	lock, err := r.Lock(ctx)
	if err != nil {
		return err
	}
	err = r.ackLocked(acked)
	if rerr := lock.Release(); rerr != nil && err == nil {
		err = errs.Wrap(errs.Internal, "rowfs: release the store lock", rerr)
	}
	return err
}

// ackLocked rewrites the journal without acked; removes it when empty.
func (r *Root) ackLocked(acked []store.AuditRecord) error {
	raw, recs, err := r.readJournal()
	if err != nil || raw == nil {
		return err
	}
	keep := recs[:0]
	for _, rc := range recs {
		drop := false
		for _, a := range acked {
			if a.Key == rc.Key && a.HLC.Compare(rc.HLC) == 0 {
				drop = true
				break
			}
		}
		if !drop {
			keep = append(keep, rc)
		}
	}
	if len(keep) == 0 {
		if err := removeIfExists(r.JournalPath()); err != nil {
			return errs.Wrap(errs.Internal, "rowfs: remove the audit journal", err)
		}
		return nil
	}
	b, err := encodeJournal(keep)
	if err != nil {
		return err
	}
	return writeAtomic(r.JournalPath(), b)
}
