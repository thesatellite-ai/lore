package store

import (
	"context"

	"github.com/thesatellite-ai/lore/model/hlc"
)

// Audit records in the row's own batch (owner decision CLEANUP Qs): the
// audit record of a write is written in the SAME backend batch as the row,
// so both land or neither does, and a stored row always has its audit
// record. The final audit trail (the checkout's chain, the machine-local
// store trail) is still the engine's AuditSink; the backend keeps each
// record in a machine-local pending journal (never committed, never
// synced) until the engine acknowledges that the sink recorded it, and a
// record the sink could not take stays pending and is replayed later.
//
//	engine.put ── AuditJournal.WriteAudited(changes, records) ── one batch:
//	                journal append + row files (+ one commit in a git clone)
//	engine.finish ─ AuditSink.Record(ev) ok ── AckAudit(records)
//	              └ failed ── record stays pending ── replayed by a later write

// AuditRecord is one audit event as a backend keeps it: the row it is about
// (Key and the HLC of the write) and the event encoded by the engine, one
// JSON object that never holds a field value. Line is opaque to the
// backend, like Row.Doc.
type AuditRecord struct {
	Key  Key
	HLC  hlc.HLC
	Line []byte
}

// AuditJournal is an optional capability: a backend that can write audit
// records all-or-none with the rows. A backend without it gets a plain
// Write, and the engine records the audit after it as before.
type AuditJournal interface {
	// WriteAudited is Write plus recs in the same batch: on success the
	// rows are stored and every record is pending; on any error neither
	// the rows nor the records are kept.
	WriteAudited(ctx context.Context, changes []Change, recs []AuditRecord) error
	// PendingAudit lists the records not yet acknowledged, oldest first.
	PendingAudit(ctx context.Context) ([]AuditRecord, error)
	// AckAudit drops the pending records matching recs (by Key and HLC);
	// a record that is not pending is ignored.
	AckAudit(ctx context.Context, recs []AuditRecord) error
}
