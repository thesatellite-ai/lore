package wire

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// AuditFile is where the 0.1.18 write path records its audit events,
// relative to the repo top: one JSON object per line, append only. It sits
// in .lore/state/, which `lore init` git-ignores (the audit log is local
// history, not shared knowledge).
//
// It is the fallback sink: when Options.Audit is set (the old binary's
// sink appending the v0.1.17 audit_logs hash chain) nothing is written here.
const AuditFile = ".lore/state/audit-v2.jsonl"

// Modes of the audit file and its folder (owner only).
const (
	auditFileMode = 0o600
	auditDirMode  = 0o750
)

// auditLine is the JSON shape of one event. It never carries a field value
// or a finding's text: only names, ids and counts (engine.Event's rule).
type auditLine struct {
	Op       string `json:"op,omitempty"`
	Action   string `json:"action"`
	Store    string `json:"store"`
	Table    string `json:"table"`
	ID       string `json:"id"`
	HLC      string `json:"hlc"`
	Actor    string `json:"actor"`
	Merged   bool   `json:"merged,omitempty"`
	Accepted int    `json:"secrets_accepted,omitempty"`
	Redacted int    `json:"secrets_redacted,omitempty"`
	Cascade  string `json:"cascade,omitempty"`
}

// fileAudit is the engine.AuditSink writing AuditFile.
type fileAudit struct {
	mu   sync.Mutex
	path string
}

var _ engine.AuditSink = (*fileAudit)(nil)

// Record implements engine.AuditSink: one fsynced line per event.
func (f *fileAudit) Record(_ context.Context, ev engine.Event) error {
	// The file lives inside the checkout, so it records only the repo
	// store's own writes: an event for an attached store would leave that
	// store's row id in the checkout (CONFIG_GUIDE: a checkout never holds
	// store rows; same rule as the CLI's audit-chain sink, 54a6669).
	if ev.Store != store.SourceRepo {
		return nil
	}
	line, err := json.Marshal(auditLine{
		Op: ev.Op, Action: string(ev.Action), Store: string(ev.Store), Table: ev.Table.String(), ID: ev.ID.String(),
		HLC: ev.HLC.String(), Actor: ev.Actor.String(), Merged: ev.Merged,
		Accepted: len(ev.SecretsAccepted), Redacted: len(ev.SecretsRedacted), Cascade: ev.Cascade.String(),
	})
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: encode an audit event", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if mkErr := os.MkdirAll(filepath.Dir(f.path), auditDirMode); mkErr != nil {
		return errs.Wrap(errs.Internal, "wire: create the audit folder", mkErr)
	}
	fh, err := os.OpenFile(f.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, auditFileMode)
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: open the audit file", err)
	}
	_, werr := fh.Write(append(line, '\n'))
	serr := fh.Sync()
	cerr := fh.Close()
	if werr != nil || serr != nil || cerr != nil {
		return errs.Wrap(errs.Internal, "wire: write the audit file", firstErr(werr, serr, cerr))
	}
	return nil
}

// firstErr returns the first non-nil error.
func firstErr(es ...error) error {
	for _, e := range es {
		if e != nil {
			return e
		}
	}
	return nil
}
