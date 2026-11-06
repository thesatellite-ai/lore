package wire

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// AuditFile is where the 0.1.18 write path records its audit events,
// relative to the repo top: one JSON object per line, append only. It sits
// in .lore/state/, which `lore init` git-ignores (the audit log is local
// history, not shared knowledge).
//
// It is the fallback sink: when Options.Audit is set (the old binary's
// sink appending the v0.1.17 hash chain, in the machine store since
// P3-LOCAL-7) nothing is written here.
// Writes to attached stores never land here (AUDIT ATTACHED STORES: the
// checkout's chain keeps only repo-store writes): their trail is the
// machine-local StoreAuditDir file (routedAudit).
const AuditFile = ".lore/state/audit-v2.jsonl"

// Modes of the audit file and its folder (owner only).
const (
	auditFileMode = 0o600
	auditDirMode  = 0o750
)

// StoreAuditDir is where the trail of writes to ATTACHED stores (a company
// store, the personal "me" store) is kept, relative to the lore home
// (Options.Home, ~/.lore): one append-only JSON-lines file per store,
// <StoreAuditDir>/<stem>.jsonl. Such a write must not land in the
// checkout (CONFIG_GUIDE: lore.db and .lore/state hold only the repo's
// rows, F20/E11), yet must not lose its trail (owner decision AUDIT
// ATTACHED STORES), so it is machine-local: never in a repo, never
// committed, never synced.
const StoreAuditDir = "state/audit"

// Store audit file details. The stem is the store's permanent sto_ id, so
// a store renamed in stores.yaml keeps one file and an org store's two
// attached folders share it; a store without one (a v0.1.17 marker) gets
// storeAuditNoIDPrefix + its sanitised source name, the same stem the
// private render uses (render_private.go privateNoIDPrefix).
const (
	storeAuditExt        = ".jsonl"
	storeAuditNoIDPrefix = "name-"
	storeAuditDirMode    = 0o700
)

// storeAuditUnsafe matches every character a source name may not carry
// into a file stem (keeps the stem one path element).
var storeAuditUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

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
	// Batch and BatchKind name the bulk action the write belongs to (XQ1).
	Batch     string `json:"batch,omitempty"`
	BatchKind string `json:"batch_kind,omitempty"`
	// Session is lore's ses_ id (6.6); Confirmed marks a --yes write.
	// Both machine-local: the audit files never leave this machine.
	Session   string `json:"session_id,omitempty"`
	Confirmed bool   `json:"confirmed,omitempty"`
	// Secret detail of a secret.* action (SECRETS_AND_REFERENCES 16):
	// names, ids, counts and result words only, never a value.
	SecretRef      string `json:"secret_ref,omitempty"`
	SecretCommand  string `json:"secret_command,omitempty"`
	SecretResult   string `json:"secret_result,omitempty"`
	SecretForced   bool   `json:"secret_forced,omitempty"`
	SecretForcedBy string `json:"secret_forced_by,omitempty"`
	SecretCount    int    `json:"secret_count,omitempty"`
	SecretFrom     string `json:"secret_from,omitempty"`
	SecretTo       string `json:"secret_to,omitempty"`
}

// SessionSource is an optional capability of the repo audit sink
// (Options.Audit): it knows this process's session row (the old binary's
// machine store's sessions table, P3-LOCAL-5). routedAudit asks it
// once per event that lacks a session, so the attached-store trail and
// the batch log carry the same ses_ id as the repo chain.
type SessionSource interface {
	Session(ctx context.Context) (string, error)
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
	line, err := encodeAuditLine(ev)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return appendAuditLine(f.path, auditDirMode, line)
}

// encodeAuditLine is the one JSON shape both audit files share.
func encodeAuditLine(ev engine.Event) ([]byte, error) {
	line, err := json.Marshal(auditLine{
		Op: ev.Op, Action: string(ev.Action), Store: string(ev.Store), Table: ev.Table.String(), ID: ev.ID.String(),
		HLC: ev.HLC.String(), Actor: ev.Actor.String(), Merged: ev.Merged,
		Accepted: len(ev.SecretsAccepted), Redacted: len(ev.SecretsRedacted), Cascade: ev.Cascade.String(),
		Batch: ev.Batch.ID.String(), BatchKind: ev.Batch.Kind, Session: ev.Session, Confirmed: ev.Confirmed,
		SecretRef: ev.Secret.Ref, SecretCommand: ev.Secret.Command, SecretResult: ev.Secret.Result, SecretForced: ev.Secret.Forced,
		SecretForcedBy: string(ev.Secret.ForcedBy), SecretCount: ev.Secret.Count, SecretFrom: ev.Secret.From, SecretTo: ev.Secret.To,
	})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "wire: encode an audit event", err)
	}
	return line, nil
}

// appendAuditLine appends one fsynced line to path (mode auditFileMode),
// creating its folder with dirMode.
func appendAuditLine(path string, dirMode os.FileMode, line []byte) error {
	if mkErr := os.MkdirAll(filepath.Dir(path), dirMode); mkErr != nil {
		return errs.Wrap(errs.Internal, "wire: create the audit folder", mkErr)
	}
	// path is built by the caller from the lore home and a store id, never
	// from row data; Clean normalises it before the open.
	fh, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_WRONLY|os.O_APPEND, auditFileMode)
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

// routedAudit is the engine.AuditSink the engine actually gets: it hands
// every event to the repo sink (Options.Audit, else fileAudit; both chain
// only store.SourceRepo events into the checkout) and additionally
// appends every attached-store event to that store's machine-local file
// under <home>/StoreAuditDir. So a repo write is chained exactly as
// before, and an attached-store write keeps a trail without its row id
// ever reaching the checkout.
type routedAudit struct {
	repo  engine.AuditSink
	dir   string                  // <home>/StoreAuditDir
	stems map[store.Source]string // attached source -> file stem
	mu    sync.Mutex
}

var _ engine.AuditSink = (*routedAudit)(nil)

// newRoutedAudit wraps repo with the attached stores' machine-local trail.
func newRoutedAudit(repo engine.AuditSink, home string, attached []attach.Opened) *routedAudit {
	r := &routedAudit{repo: repo, dir: filepath.Join(home, filepath.FromSlash(StoreAuditDir)), stems: map[store.Source]string{}}
	for _, o := range attached {
		stem := ""
		if m, ok := concrete(o.Backend).(markerOf); ok && !m.Marker().ID.IsZero() {
			stem = m.Marker().ID.String()
		}
		r.stems[o.Name] = stem
	}
	return r
}

// stem is the file stem of an attached source: its sto_ id, else
// storeAuditNoIDPrefix + the sanitised name.
func (r *routedAudit) stem(name store.Source) string {
	if s := r.stems[name]; s != "" {
		return s
	}
	return storeAuditNoIDPrefix + storeAuditUnsafe.ReplaceAllString(string(name), "_")
}

// Record implements engine.AuditSink.
func (r *routedAudit) Record(ctx context.Context, ev engine.Event) error {
	// 6.6: the session link and the --yes mark, filled once here so every
	// sink below records the same values.
	ev.Confirmed = ev.Confirmed || engine.Confirmed(ctx)
	// P3-LOCAL-5: a session row's own event is never linked: the session
	// source writes that row from inside this call (the first write of a
	// process), and asking it again would re-enter it.
	// OV3: a `lore api` request (RemoteRequest) is not this process's
	// session; its writes carry no session link.
	if src, ok := r.repo.(SessionSource); ok && ev.Session == "" && ev.Table != schema.TableSessions && !IsRemoteRequest(ctx) {
		ses, err := src.Session(ctx)
		if err != nil {
			return err
		}
		ev.Session = ses
	}
	// XQ1: every event of a bulk action, of every store, also goes to the
	// one machine-local batch log `lore batch list|show` read (Q-R3-8:
	// batches are visible on the machine that ran them in 0.1.18).
	r.mu.Lock()
	berr := recordBatch(batchDirOf(r.dir), ev)
	r.mu.Unlock()
	if berr != nil {
		return berr
	}
	if ev.Store != store.SourceRepo {
		line, err := encodeAuditLine(ev)
		if err != nil {
			return err
		}
		r.mu.Lock()
		err = appendAuditLine(filepath.Join(r.dir, r.stem(ev.Store)+storeAuditExt), storeAuditDirMode, line)
		r.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return r.repo.Record(ctx, ev)
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
