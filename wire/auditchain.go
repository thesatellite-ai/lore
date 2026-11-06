package wire

// auditchain.go — the hash-chained repo audit log in this machine's machine
// store (P3-LOCAL-7): the v0.1.17 lore.db audit_logs chain moved unchanged,
// one chain per project (keyed by its lore.db path, as v0.1.17 kept one
// chain per lore.db), never synced.
//
//	App.AuditChain          ─▶ a handle on <home>/state/machine.db (caller closes)
//	ImportLoreDBAudit       ─▶ the chain's one import of the lore.db rows (marker; run twice = no-op)
//	ChainRecordOf(ev, ses)  ─▶ the chain entry of one core event (both binaries name it alike)
//	NewChainAudit(home,dir) ─▶ cmd/lore's repo audit sink (StandaloneOptions): AuditFile + the chain,
//	                           and `lore audit log|verify` over the chain
//
// The old binary's sink (saas/cmd/cli/core_audit.go) appends to the same
// chain through the same three functions, so both binaries in one checkout
// extend one chain.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/auditchain"
	"github.com/thesatellite-ai/lore/model/errs"
	opsaudit "github.com/thesatellite-ai/lore/ops/audit"
	opssession "github.com/thesatellite-ai/lore/ops/session"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// AuditChain opens the hash-chained audit log in this machine's machine
// store file (<home>/state/machine.db). A fresh handle per call; the
// caller closes it.
func (a *App) AuditChain(ctx context.Context) (*sqlite.AuditChain, error) {
	return openAuditChainAt(ctx, a.opts.Home)
}

// openAuditChainAt opens the chain of the lore home home.
func openAuditChainAt(ctx context.Context, home string) (*sqlite.AuditChain, error) {
	return sqlite.OpenAuditChain(ctx, filepath.Join(home, filepath.FromSlash(MachineStoreFile)))
}

// legacyLoreDB is a project's v0.1.17 database file, relative to the
// project folder: the chain's key and its one import source.
const legacyLoreDB = repoMarkerDir + "/lore.db"

// auditKindWrite is the chain action kind of a core create or update (the
// v0.1.17 chain named every local change "<table>.write"; a delete, trash,
// restore or purge is named by the engine action, OV9).
const auditKindWrite = "write"

// AuditChainKey is the chain name of the project whose lore.db path is
// dbPath: absolute, its folder's symlinks resolved, so every spelling of
// one checkout names one chain.
func AuditChainKey(dbPath string) string {
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return dbPath
	}
	if dir, derr := filepath.EvalSymlinks(filepath.Dir(abs)); derr == nil {
		return filepath.Join(dir, filepath.Base(abs))
	}
	return abs
}

// ImportLoreDBAudit copies dbPath's v0.1.17 audit_logs into chain key once
// (sqlite.AuditChain.Import's marker). No lore.db, or a symlink (never
// followed), imports nothing but still sets the marker, so a later lore.db
// is never spliced in front of entries already chained.
func ImportLoreDBAudit(ctx context.Context, ch *sqlite.AuditChain, key, dbPath string, at time.Time) error {
	done, err := ch.Imported(ctx, key)
	if err != nil || done {
		return err
	}
	var recs []auditchain.Record
	if fi, lerr := os.Lstat(dbPath); lerr == nil && fi.Mode().IsRegular() {
		if recs, err = sqlite.ReadLegacyAuditLog(ctx, dbPath); err != nil {
			return errs.Wrap(errs.Internal, "audit: read lore.db audit_logs for the move to the machine store", err)
		}
	}
	if _, err := ch.Import(ctx, key, dbPath, recs, at); err != nil {
		return errs.Wrap(errs.Internal, "audit: move lore.db audit_logs to the machine store", err)
	}
	return nil
}

// ChainRecordOf is the chain entry of one core event, session the ses_ id
// to link ("" = none): action "<table>.<kind>" (a secret.* event keeps its
// own action and puts the command, result and FORCED mark in the reason).
// The entry holds names and ids only: an engine.Event never carries a
// field value, so no secret reaches the log.
func ChainRecordOf(ctx context.Context, ev engine.Event, session string) auditchain.Record {
	kind := auditKindWrite
	switch ev.Action {
	case engine.ActionDelete, engine.ActionTrash, engine.ActionRestore, engine.ActionPurge:
		kind = string(ev.Action)
	default: // create, update and every secret.* event are a write; a secret event's own action is set below
	}
	reason := ev.Op
	if !ev.Cascade.IsZero() {
		reason += " (cascade of " + ev.Cascade.String() + ")"
	}
	table := ev.Table.String()
	action := table + "." + kind
	if ev.Secret != (engine.SecretAudit{}) {
		action = string(ev.Action)
		reason += " " + ev.Secret.Command + " " + ev.Secret.Result
		if ev.Secret.Forced {
			reason += " FORCED (" + string(ev.Secret.ForcedBy) + ")"
		}
	}
	return auditchain.Record{ActorID: ev.Actor.String(), Action: action, TargetTable: table, TargetID: ev.ID.String(),
		Reason: reason, SessionID: session, Confirmed: ev.Confirmed || engine.Confirmed(ctx)}
}

// chainAudit is cmd/lore's repo audit sink: every repo-store event goes to
// AuditFile (the append-only record --who and the batch tooling read) and
// to the project's chain in the machine store. It writes no engine row,
// so it can never re-enter the engine or itself from inside Record.
type chainAudit struct {
	home, dir string
	now       func() time.Time

	// app is the App this sink records for (bindApp, from New): its clock
	// stamps created_at (LORE_NOW under loretest, as the old binary's
	// cliClock) and its CurrentSession links the session. nil before New.
	app *App
	// sesOnce/ses/sesErr: this process's session, found or created once.
	sesOnce sync.Once
	ses     string
	sesErr  error

	once   sync.Once
	top    string // git top (AuditFile's checkout)
	dbPath string // <project>/.lore/lore.db (the chain key and import source)
	file   *fileAudit
}

var (
	_ engine.AuditSink = (*chainAudit)(nil)
	_ opsaudit.Env     = (*chainAudit)(nil)
	_ WhoSource        = (*chainAudit)(nil)
	_ SessionSource    = (*chainAudit)(nil)
	_ appBinder        = (*chainAudit)(nil)
)

// appBinder is a repo audit sink that needs the App it records for; New
// hands it the App before anything opens.
type appBinder interface {
	bindApp(a *App)
}

// bindApp implements appBinder: the App's clock replaces the one given to
// NewChainAudit.
func (c *chainAudit) bindApp(a *App) {
	c.app = a
	c.now = a.opts.Wall
}

// boundAppSource is a sink that hands out the App New bound it to (nil
// before New): cmd/lore's whoami and agent start hosts reach the machine
// store's session rows through it (wire/session_hook.go).
type boundAppSource interface {
	boundApp() *App
}

var _ boundAppSource = (*chainAudit)(nil)

// boundApp implements boundAppSource.
func (c *chainAudit) boundApp() *App { return c.app }

// Session implements SessionSource: this process's ses_ id (App.CurrentSession),
// "" before New bound an App or when no session applies.
func (c *chainAudit) Session(ctx context.Context) (string, error) {
	if c.app == nil {
		return "", nil
	}
	c.sesOnce.Do(func() { c.ses, c.sesErr = c.app.CurrentSession(ctx) })
	return c.ses, c.sesErr
}

// ToolSessionIDs implements ToolSessionSource (OV11, later 434): the raw
// tool session ids this machine's session rows know, so cmd/lore's write
// scanner refuses one in row text at write time (RuleSessionID), as the
// old binary's lore.db sink did; nil before New bound an App.
func (c *chainAudit) ToolSessionIDs(ctx context.Context) ([]string, error) {
	if c.app == nil {
		return nil, nil
	}
	return c.app.KnownToolSessionIDs(ctx)
}

var _ ToolSessionSource = (*chainAudit)(nil)

// NewChainAudit is the repo audit sink of a binary with no chain of its
// own (cmd/lore, StandaloneOptions): the project is the nearest folder at
// or above dir holding .lore, never above the git top.
func NewChainAudit(home, dir string, now func() time.Time) engine.AuditSink {
	return &chainAudit{home: home, dir: dir, now: now}
}

// resolve finds the checkout and the project folder once.
func (c *chainAudit) resolve(ctx context.Context) {
	c.once.Do(func() {
		dir := c.dir
		if dir == "" {
			if wd, err := os.Getwd(); err == nil {
				dir = wd
			}
		}
		c.top = gitTop(ctx, dir)
		if c.top == "" {
			c.top = dir
		}
		project := projectFolder(dir, c.top)
		c.dbPath = filepath.Join(project, filepath.FromSlash(legacyLoreDB))
		// the project's own .lore/state (OWNERQ-1): a monorepo sub-project's
		// trail is not written into the repo top, which may have no project
		// (and no .gitignore line for it)
		c.file = &fileAudit{path: filepath.Join(project, filepath.FromSlash(AuditFile))}
	})
}

// projectFolder is the nearest folder from dir up to top (inclusive) that
// holds .lore, else top (the old binary's nearestMarkerDir rule).
func projectFolder(dir, top string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	if real, err := filepath.EvalSymlinks(top); err == nil {
		top = real
	}
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if fi, err := os.Stat(filepath.Join(d, repoMarkerDir)); err == nil && fi.IsDir() {
			return d
		}
		rel, err := filepath.Rel(top, d)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.Dir(d) == d {
			return top
		}
	}
}

// Record implements engine.AuditSink: only the repo store's own writes
// (a checkout's chain never names an attached store's row, 54a6669).
func (c *chainAudit) Record(ctx context.Context, ev engine.Event) error {
	if ev.Store != store.SourceRepo {
		return nil
	}
	c.resolve(ctx)
	if err := c.file.Record(ctx, ev); err != nil {
		return err
	}
	return c.withChain(ctx, func(ch *sqlite.AuditChain, key string) error {
		_, err := ch.Append(ctx, key, ChainRecordOf(ctx, ev, ev.Session), c.now())
		return err
	})
}

// withChain opens the chain, makes sure the project's import ran, runs fn
// and closes the handle.
func (c *chainAudit) withChain(ctx context.Context, fn func(ch *sqlite.AuditChain, key string) error) (err error) {
	c.resolve(ctx)
	ch, err := openAuditChainAt(ctx, c.home)
	if err != nil {
		return errs.Wrap(errs.Internal, "audit: open the chain", err)
	}
	defer func() { err = errors.Join(err, ch.Close()) }()
	key := AuditChainKey(c.dbPath)
	if err = ImportLoreDBAudit(ctx, ch, key, c.dbPath, c.now()); err != nil {
		return err
	}
	return fn(ch, key)
}

// records is the project's chain, oldest first.
func (c *chainAudit) records(ctx context.Context) (out []auditchain.Record, err error) {
	err = c.withChain(ctx, func(ch *sqlite.AuditChain, key string) error {
		out, err = ch.Records(ctx, key)
		return err
	})
	return out, err
}

// AuditLog implements opsaudit.Env: newest first, limit <= 0 = all.
func (c *chainAudit) AuditLog(ctx context.Context, limit int) ([]opsaudit.Entry, error) {
	recs, err := c.records(ctx)
	if err != nil {
		return nil, err
	}
	slices.Reverse(recs)
	if limit > 0 && len(recs) > limit {
		recs = recs[:limit]
	}
	out := make([]opsaudit.Entry, 0, len(recs))
	for _, r := range recs {
		out = append(out, AuditEntryOf(r))
	}
	return out, nil
}

// AuditVerify implements opsaudit.Env: the chain check (cmd/lore keeps no
// lore.db rows, so there is no "changed outside lore" check here).
func (c *chainAudit) AuditVerify(ctx context.Context) (opsaudit.Report, error) {
	var rep opsaudit.Report
	var recs []auditchain.Record
	err := c.withChain(ctx, func(ch *sqlite.AuditChain, key string) error {
		var verr error
		if rep.BrokenAt, rep.Entries, verr = ch.Verify(ctx, key); verr != nil { // links + head anchor (P3-AUDIT-3)
			return verr
		}
		recs, verr = ch.Records(ctx, key)
		return verr
	})
	if err != nil {
		return opsaudit.Report{}, err
	}
	rep.ExternalWrites = ExternalWrites(recs)
	return rep, nil
}

// WhoLines implements WhoSource as the old binary's sink does: one line
// per session that wrote rowID, in first-write order, from the chain, with
// the session's tool, tool session (redacted off-terminal / in CI) and
// model from the machine store. Before New bound an App it reads the
// fallback file's lines.
func (c *chainAudit) WhoLines(ctx context.Context, rowID string) ([]string, error) {
	c.resolve(ctx)
	if c.app == nil {
		return whoFromAuditFile(c.file.path, rowID)
	}
	recs, err := c.records(ctx)
	if err != nil {
		return nil, err
	}
	type tally struct {
		confirmed bool
		writes    int
	}
	var order []string
	per := map[string]*tally{}
	for _, r := range recs {
		if r.TargetID != rowID || strings.TrimSpace(r.SessionID) == "" {
			continue
		}
		t, ok := per[r.SessionID]
		if !ok {
			t = &tally{}
			per[r.SessionID] = t
			order = append(order, r.SessionID)
		}
		t.writes++
		t.confirmed = t.confirmed || r.Confirmed
	}
	if len(order) == 0 {
		return nil, nil
	}
	rows, err := c.app.Sessions(ctx, 0)
	if err != nil {
		return nil, err
	}
	known := map[string]SessionDetails{}
	for _, r := range rows {
		known[r.ID] = r.SessionDetails
	}
	out := make([]string, 0, len(order))
	for _, ses := range order {
		d, t := known[ses], per[ses]
		out = append(out, fmt.Sprintf("%s  tool %s  session %s  model %s  confirmed %s  (%d writes)",
			ses, whoCell(d.Tool), whoCell(redactSession(d.ToolSessionID)), whoCell(d.Model), yesNo(t.confirmed), t.writes))
	}
	return out, nil
}

// whoCell shows an unknown --who field as "-" (OQ8: never guessed).
func whoCell(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}

// SessionCounts fills each session row's Writes (its chain entries) and
// NewKeys (distinct meta_keys rows it wrote) from recs (P3-RUNS-5).
func SessionCounts(recs []auditchain.Record, rows []opssession.Row) {
	writes := map[string]int{}
	keys := map[string]map[string]bool{}
	for _, r := range recs {
		if r.SessionID == "" {
			continue
		}
		writes[r.SessionID]++
		if r.TargetTable == schema.TableMetaKeys && r.TargetID != "" {
			if keys[r.SessionID] == nil {
				keys[r.SessionID] = map[string]bool{}
			}
			keys[r.SessionID][r.TargetID] = true
		}
	}
	for i := range rows {
		rows[i].Writes, rows[i].NewKeys = writes[rows[i].ID], len(keys[rows[i].ID])
	}
}

// ExternalWrites counts the entries the sync bridge recorded as changes
// lore did not make ("<table>" + opsaudit.ExternalWriteSuffix).
func ExternalWrites(recs []auditchain.Record) int {
	n := 0
	for _, r := range recs {
		if strings.HasSuffix(r.Action, opsaudit.ExternalWriteSuffix) {
			n++
		}
	}
	return n
}

// AuditEntryOf is one chain record in the operation's shape (same JSON keys).
func AuditEntryOf(r auditchain.Record) opsaudit.Entry {
	return opsaudit.Entry{
		ActingProjectID: r.ActingProjectID, TargetProjectID: r.TargetProjectID, ActorID: r.ActorID, Action: r.Action,
		TargetTable: r.TargetTable, TargetID: r.TargetID, Override: r.Override, BeforeHash: r.BeforeHash, AfterHash: r.AfterHash,
		Reason: r.Reason, SessionID: r.SessionID, Confirmed: r.Confirmed, ID: r.ID, CreatedAt: r.CreatedAt, PrevLogHash: r.PrevLogHash,
	}
}
