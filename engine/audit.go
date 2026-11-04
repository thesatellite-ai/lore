package engine

import (
	"context"
	"slices"

	"github.com/thesatellite-ai/lore/model/errs"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// Action is what a write did to a row.
type Action string

// Actions.
const (
	ActionCreate Action = "create"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete" // a tombstone was written
	ActionPurge  Action = "purge"  // a tombstone was written and its id listed in _purged.json

	// Secret actions (SECRETS_AND_REFERENCES 16): no row is written; the
	// event carries a SecretAudit, never a value.
	ActionSecretRevealed     Action = "secret.revealed"
	ActionSecretRevealFailed Action = "secret.reveal_failed"
	ActionSecretVerified     Action = "secret.verified"
	ActionSecretRefsMoved    Action = "secret.refs_moved"
	ActionSecretEntryRenamed Action = "secret.entry_renamed"
	ActionSecretLinked       Action = "secret.linked"
	ActionSecretRelabeled    Action = "secret.relabeled"
)

// SecretActions lists the secret.* actions (16).
func SecretActions() []Action {
	return []Action{ActionSecretRevealed, ActionSecretRevealFailed, ActionSecretVerified, ActionSecretRefsMoved,
		ActionSecretEntryRenamed, ActionSecretLinked, ActionSecretRelabeled}
}

// ForcedBy says why a reveal had no person's answer (16, SQ10).
type ForcedBy string

// ForcedBy values.
const (
	ForcedNone   ForcedBy = ""
	ForcedFlag   ForcedBy = "flag"   // --force
	ForcedPolicy ForcedBy = "policy" // secrets.agent_reveal: allow
)

// SecretAudit is the detail of a secret.* event (16). Every field is a
// name, an id, a count or a result word: the value, stderr and any preview
// are never recorded.
type SecretAudit struct {
	// Ref is the reference text (manager://locator?holder…), never a value.
	Ref string
	// Command is the lore command ("secrets show", "show").
	Command string
	// Row is the row the reference sits in (zero when none).
	Row ids.AnyID
	// Result: present | missing | not_checked (verified); the error class
	// (reveal_failed).
	Result string
	// Forced and ForcedBy: a reveal without a person's answer (loud).
	Forced   bool
	ForcedBy ForcedBy
	// Count, From, To: bulk rewrites (refs_moved, entry_renamed, relabeled,
	// linked): how many references, from and to (ids and labels).
	Count int
	From  string
	To    string
}

// AuditSecret records one secret.* event through the audit sink (16). The
// event names the repo store, the running op, the caller's actor and its
// confirm flag; a.Ref is never resolved. It is an error for any non-secret
// action.
func (e *Engine) AuditSecret(ctx context.Context, action Action, a SecretAudit) error {
	if !slices.Contains(SecretActions(), action) {
		return errs.New(errs.Internal, "engine: "+string(action)+" is not a secret audit action")
	}
	cl := e.callOf(ctx)
	ev := Event{Op: cl.op, Action: action, Store: store.SourceRepo, ID: a.Row, Actor: e.actorOf(cl),
		Confirmed: cl.caller.Yes, Secret: a}
	if b, ok := BatchOf(ctx); ok {
		ev.Batch = b
	}
	if err := e.audit.Record(ctx, ev); err != nil {
		return errs.Wrap(errs.Internal, "engine: secret audit record failed", err)
	}
	return nil
}

// Event is one audit record. It never carries a field value: only names,
// ids and masked findings, so the audit log can be shown to anyone who may
// see the row's existence.
type Event struct {
	Op     string // the operation name ("memory.add"); "" outside Run
	Action Action
	Store  store.Source
	Table  row.Table
	ID     ids.AnyID
	HLC    hlc.HLC
	Actor  ids.ID[ids.Actor]
	// Merged: the write came back stale and was merged field by field
	// (spec 5.3) before it was stored.
	Merged bool
	// SecretsAccepted are findings the caller confirmed as not secrets
	// (secrets.on_detect: ask, answered yes; SQ2 audits it). Masked only.
	SecretsAccepted []scan.Finding
	// SecretsRedacted are findings replaced with ${secret:unstored}.
	SecretsRedacted []scan.Finding
	// Cascade names the row whose delete caused this write ("" otherwise).
	Cascade ids.AnyID
	// Batch is the bulk action this write belongs to (XQ1: one bat_ id per
	// bulk action, read back by `lore batch list|show`); zero otherwise.
	Batch Batch
	// Session is lore's ses_ id of the agent session that made the write
	// (AGENT_IDENTITY_DESIGN 6.6; never the tool's own id). Machine-local:
	// sinks keep it in local logs only (OQ14). "" outside a session; the
	// sink fills it when the engine does not know it.
	Session string
	// Confirmed is true when the write followed --yes (8.1, 6.6).
	Confirmed bool
	// Secret is the detail of a secret.* action (zero for row writes).
	Secret SecretAudit
}

// AuditSink stores audit events (the audit table, phase 2). Record is
// called after the backend accepted the write; an error is returned to the
// caller (the row IS stored; the missing audit record must not be silent).
type AuditSink interface {
	Record(ctx context.Context, ev Event) error
}

// WarnCode names a warning kind.
type WarnCode string

// Warning codes.
const (
	// WarnScanDegraded: the scanner skipped part of its rules on a field
	// (the SQ7 size gate); the field may hold a secret it did not look for.
	WarnScanDegraded WarnCode = "W_SCAN_DEGRADED"
)

// Warning is something a write did that the person must hear about but
// that did not stop it. Operations copy warnings into their output.
type Warning struct {
	Code    WarnCode
	Field   string
	Message string
}

// Written is the result of one write: the stored row(s) and any warnings.
type Written struct {
	// Row is the stored row (a tombstone for Delete).
	Row row.Row
	// Also lists other rows the write changed (cascade tombstones, cleared
	// set_null refs), each tagged with its store.
	Also     []Changed
	Warnings []Warning
}

// Changed is one row a write changed besides its main row.
type Changed struct {
	Store store.Source
	Row   row.Row
}
