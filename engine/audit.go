package engine

import (
	"context"
	"encoding/json"
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
	// ActionVaultRefsMoved keeps the wire value secret.refs_moved; the Go name
	// avoids gosec G101 reading it as a credential (CLEANUP Qs).
	ActionVaultRefsMoved     Action = "secret.refs_moved"
	ActionSecretEntryRenamed Action = "secret.entry_renamed"
	ActionSecretLinked       Action = "secret.linked"
	ActionSecretRelabeled    Action = "secret.relabeled"
	// Allow entries and rule rows (16, FA-AGENT-4): emitted by the ops
	// after their row write, carrying names and ids only.
	ActionSecretAllowAdded   Action = "secret.allow_added"
	ActionSecretAllowRemoved Action = "secret.allow_removed"
	ActionSecretRuleChanged  Action = "secret.rule_changed"
	// Detection and URL cleanup (16, FA-AGENT-7): secret.detected is the
	// write path's choice on a finding (refused, redacted, unstored,
	// not_a_secret); secret.hook_refused a commit/push hook refusal;
	// url.credentials_stripped a URL that lost its userinfo (14.4).
	ActionSecretDetected         Action = "secret.detected"
	ActionLeakHookRefused        Action = "secret.hook_refused"
	ActionURLCredentialsStripped Action = "url.credentials_stripped"
)

// SecretActions lists the secret.* actions (16).
func SecretActions() []Action {
	return []Action{ActionSecretRevealed, ActionSecretRevealFailed, ActionSecretVerified, ActionVaultRefsMoved,
		ActionSecretEntryRenamed, ActionSecretLinked, ActionSecretRelabeled,
		ActionSecretAllowAdded, ActionSecretAllowRemoved, ActionSecretRuleChanged,
		ActionSecretDetected, ActionLeakHookRefused, ActionURLCredentialsStripped}
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
		return errs.New(errs.Internal, ""+string(action)+" is not a secret audit action")
	}
	cl := e.callOf(ctx)
	ev := Event{Op: cl.op, Action: action, Store: store.SourceRepo, ID: a.Row, Actor: e.actorOf(cl),
		Confirmed: cl.caller.Yes, Secret: a}
	if def, ok := e.reg.ByPrefix(a.Row.Prefix()); !a.Row.IsZero() && ok {
		ev.Table = def.Name // a batch log line needs its table (FB-RES-2)
	}
	if b, ok := BatchOf(ctx); ok {
		ev.Batch = b
	}
	// recorded now, mid-write: never part of a staged batch (unstaged)
	if err := e.audit.Record(unstaged(ctx), ev); err != nil {
		return errs.Wrap(errs.Internal, "secret audit record failed", err)
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
// called after the backend accepted the write. When the backend kept the
// record in the row's own batch (store.AuditJournal, CLEANUP Qs) a failed
// Record becomes W_AUDIT_DEFERRED and the record is replayed later;
// otherwise the error is returned to the caller (the row IS stored; the
// missing audit record must not be silent).
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
	// WarnAuditDeferred: the row is stored and its audit record was
	// written with it (the backend's journal, CLEANUP Qs), but the audit
	// log could not take it yet; a later write adds it. Not an error, so
	// a retry never stores the row twice.
	WarnAuditDeferred WarnCode = "W_AUDIT_DEFERRED"
)

// journalEvent is the JSON form of an Event in a backend's pending audit
// journal (store.AuditRecord.Line): names and ids only, never a field
// value, a finding or the machine-local session id. Machine is the writing
// machine's id (Provenance.Writer, "" when unknown): a folder store on a
// shared folder can hold another machine's pending records, and only the
// machine that wrote a record replays it (G-DECIDE-1).
type journalEvent struct {
	Machine   string `json:"machine,omitempty"`
	Op        string `json:"op,omitempty"`
	Action    string `json:"action"`
	Store     string `json:"store"`
	Table     string `json:"table"`
	ID        string `json:"id"`
	HLC       string `json:"hlc"`
	Actor     string `json:"actor,omitempty"`
	Merged    bool   `json:"merged,omitempty"`
	Cascade   string `json:"cascade,omitempty"`
	Batch     string `json:"batch,omitempty"`
	BatchKind string `json:"batch_kind,omitempty"`
}

// journalRecord encodes ev, written by machine, for the backend's journal
// (CLEANUP Qs, G-DECIDE-1).
func journalRecord(ev Event, machine string) (store.AuditRecord, error) {
	line, err := json.Marshal(journalEvent{
		Machine: machine, Op: ev.Op, Action: string(ev.Action), Store: string(ev.Store), Table: ev.Table.String(), ID: ev.ID.String(),
		HLC: ev.HLC.String(), Actor: actorText(ev.Actor), Merged: ev.Merged, Cascade: anyText(ev.Cascade),
		Batch: batchText(ev.Batch.ID), BatchKind: ev.Batch.Kind,
	})
	if err != nil {
		return store.AuditRecord{}, errs.Wrap(errs.Internal, "encode an audit record", err)
	}
	return store.AuditRecord{Key: store.Key{Table: ev.Table, ID: ev.ID}, HLC: ev.HLC, Line: line}, nil
}

// actorText, anyText and batchText print an id, "" for the zero id.
func actorText(id ids.ID[ids.Actor]) string {
	if id.IsZero() {
		return ""
	}
	return id.String()
}

func anyText(id ids.AnyID) string {
	if id.IsZero() {
		return ""
	}
	return id.String()
}

func batchText(id ids.ID[ids.Batch]) string {
	if id.IsZero() {
		return ""
	}
	return id.String()
}

// thisMachine is the id journal records of this engine carry: the
// Provenance writer's machine id, "" without one (G-DECIDE-1).
func (e *Engine) thisMachine() string {
	if e.prov == nil {
		return ""
	}
	if _, m := e.prov.Writer(); !m.IsZero() {
		return m.String()
	}
	return ""
}

// eventOfRecord decodes a journal record back into the Event it was made
// from and the machine that wrote it (the replay of a deferred record;
// CLEANUP Qs, G-DECIDE-1).
func eventOfRecord(rc store.AuditRecord) (Event, string, error) {
	var j journalEvent
	if err := json.Unmarshal(rc.Line, &j); err != nil {
		return Event{}, "", errs.Wrap(errs.Internal, "decode an audit record", err)
	}
	ev := Event{Op: j.Op, Action: Action(j.Action), Store: store.Source(j.Store), Table: rc.Key.Table, ID: rc.Key.ID,
		HLC: rc.HLC, Merged: j.Merged, Batch: Batch{Kind: j.BatchKind}}
	var err error
	if j.Actor != "" {
		if ev.Actor, err = ids.Parse[ids.Actor](j.Actor); err != nil {
			return Event{}, "", err
		}
	}
	if j.Cascade != "" {
		if ev.Cascade, err = ids.ParseAny(j.Cascade); err != nil {
			return Event{}, "", err
		}
	}
	if j.Batch != "" {
		if ev.Batch.ID, err = ids.Parse[ids.Batch](j.Batch); err != nil {
			return Event{}, "", err
		}
	}
	return ev, j.Machine, nil
}

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
