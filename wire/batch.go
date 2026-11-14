package wire

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opsbatch "github.com/thesatellite-ai/lore/ops/batch"
	"github.com/thesatellite-ai/lore/store"
)

// BatchDir is where the batch log lives, relative to the lore home
// (Options.Home, ~/.lore): one append-only JSON-lines file per batch,
// <BatchDir>/<bat_id>.jsonl.
//
// XQ1: every event of a bulk action carries its bat_ id and kind, and
// `lore batch list|show` read them from this ONE machine-local batch log
// (Q-R3-8: batches are visible on the machine that ran them in 0.1.18). It
// is never in the checkout and never committed (the AUDIT ATTACHED STORES
// rule), so a repo write is not recorded in two checkout audit sources, and
// the v0.1.17 audit_logs chain, which has no batch column, stays as it is.
const BatchDir = "state/batches"

// Modes of the batch log (owner only, like StoreAuditDir).
const (
	batchDirMode = 0o700
	batchExt     = ".jsonl"
)

// batchLine is the JSON shape of one batch event: names and ids only,
// never a field value (engine.Event's rule).
type batchLine struct {
	Batch  string `json:"batch"`
	Kind   string `json:"kind"`
	Op     string `json:"op,omitempty"`
	Action string `json:"action"`
	Store  string `json:"store"`
	Table  string `json:"table"`
	ID     string `json:"id"`
	Actor  string `json:"actor,omitempty"`
	At     string `json:"at"`
}

// batchDirOf is the batch log folder beside the store audit folder: both
// live under <home>/state (routedAudit knows only its StoreAuditDir path).
func batchDirOf(storeAuditDir string) string {
	return filepath.Join(filepath.Dir(storeAuditDir), filepath.Base(filepath.FromSlash(BatchDir)))
}

// recordBatch appends ev to its batch's file when ev carries a batch; an
// event outside a batch is not written.
func recordBatch(ctx context.Context, dir string, ev engine.Event) error {
	if ev.Batch.IsZero() {
		return nil
	}
	line, err := json.Marshal(batchLine{
		Batch: ev.Batch.ID.String(), Kind: ev.Batch.Kind, Op: ev.Op, Action: string(ev.Action), Store: string(ev.Store),
		Table: ev.Table.String(), ID: ev.ID.String(), Actor: ev.Actor.String(), At: ev.HLC.Wall.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: encode a batch event", err)
	}
	// The file name is the parsed bat_ id (prefix + hex), never row data.
	return appendAuditLineCtx(ctx, filepath.Join(dir, ev.Batch.ID.String()+batchExt), batchDirMode, line)
}

// BatchEvents implements opsbatch.Env: every event of every batch file
// under <home>/BatchDir, files in id order (bat_ ids are UUIDv7, so that is
// time order), lines in written order. A missing folder is no batches. A
// line that does not parse is E_CONFIG_INVALID naming the file: a batch
// shown with rows missing would mislead.
func (a *App) BatchEvents(_ context.Context) ([]opsbatch.Event, error) {
	dir := filepath.Join(a.opts.Home, filepath.FromSlash(BatchDir))
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "wire: read the batch log", err)
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), batchExt) {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	var out []opsbatch.Event
	for _, n := range names {
		evs, rerr := readBatchFile(filepath.Join(dir, n))
		if rerr != nil {
			return nil, rerr
		}
		out = append(out, evs...)
	}
	return out, nil
}

// readBatchFile parses one batch file.
func readBatchFile(path string) ([]opsbatch.Event, error) {
	fh, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "wire: open a batch file", err)
	}
	defer func() { _ = fh.Close() }() // read only: a close error loses nothing
	var out []opsbatch.Event
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		if len(strings.TrimSpace(sc.Text())) == 0 {
			continue
		}
		ev, perr := parseBatchLine(sc.Bytes())
		if perr != nil {
			return nil, errs.New(errs.ConfigInvalid, "batch log "+filepath.Base(path)+": "+perr.Error())
		}
		out = append(out, ev)
	}
	if err = sc.Err(); err != nil {
		return nil, errs.Wrap(errs.Internal, "wire: read a batch file", err)
	}
	return out, nil
}

// parseBatchLine decodes and types one line.
func parseBatchLine(b []byte) (opsbatch.Event, error) {
	var l batchLine
	if err := json.Unmarshal(b, &l); err != nil {
		return opsbatch.Event{}, err
	}
	bid, err := ids.Parse[ids.Batch](l.Batch)
	if err != nil {
		return opsbatch.Event{}, err
	}
	rid, err := ids.ParseAny(l.ID)
	if err != nil {
		return opsbatch.Event{}, err
	}
	var actor ids.ID[ids.Actor]
	if l.Actor != "" {
		if actor, err = ids.Parse[ids.Actor](l.Actor); err != nil {
			return opsbatch.Event{}, err
		}
	}
	table, err := row.ParseTable(l.Table)
	if err != nil {
		return opsbatch.Event{}, err
	}
	at, err := time.Parse(time.RFC3339Nano, l.At)
	if err != nil {
		return opsbatch.Event{}, err
	}
	return opsbatch.Event{Batch: bid, Kind: opsbatch.Kind(l.Kind), Op: l.Op, Action: l.Action, Store: store.Source(l.Store),
		Table: table, ID: rid, Actor: actor, At: at}, nil
}

// Policy is the effective policy set a write to target sees (the
// bulk.Policier upgrade: XQ2's batch.preview_rows is read through it).
func (a *App) Policy(ctx context.Context, target store.Source) (policy.Set, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return policy.Set{}, err
	}
	return eng.Policy(ctx, target)
}

// batchPriorExt is the file of a batch's before-images, beside its event
// file: <BatchDir>/<bat_id>.prior (JSON lines). It is not a ".jsonl" file,
// so BatchEvents never reads it as events.
const batchPriorExt = ".prior"

// priorLine is one before-image of an edit batch (R14, PASS3-A-6): the
// whole row document as it was just before one Update of the batch, and
// the hlc that Update left. `lore batch undo` puts the row back to the
// first image of the batch when its hlc still is the last one.
//
// Unlike batchLine it holds field values. It stays in the owner-only,
// machine-local batch log (never in a checkout, never synced), and the
// values are the ones the row file already held, past the secret checks
// (P3-BATCH-2).
type priorLine struct {
	Store  string          `json:"store"`
	ID     string          `json:"id"`
	After  string          `json:"after"`
	Before json.RawMessage `json:"before"`
}

// recordPrior appends before (the row as read before an Update of batch
// b) and after (the hlc that Update stored) to b's prior file.
func (a *App) recordPrior(b engine.Batch, src store.Source, before row.Row, after hlc.HLC) error {
	doc, err := row.Encode(before)
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: encode a batch before-image", err)
	}
	line, err := json.Marshal(priorLine{Store: string(src), ID: before.ID.String(), After: after.String(), Before: doc})
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: encode a batch before-image", err)
	}
	dir := filepath.Join(a.opts.Home, filepath.FromSlash(BatchDir))
	// The file name is the parsed bat_ id, never row data.
	return appendAuditLine(filepath.Join(dir, b.ID.String()+batchPriorExt), batchDirMode, line)
}

// BatchPriors implements opsbatch.Reverter: per row the batch edited, the
// first before-image and the last hlc (a row edited twice in one batch is
// put back to its state before the first edit, P3-BATCH-1). A batch with
// no prior file has none (it edited nothing, or ran before 0.1.18 kept
// them). A line that does not parse is E_CONFIG_INVALID naming the file.
func (a *App) BatchPriors(_ context.Context, id ids.ID[ids.Batch]) (map[ids.AnyID]opsbatch.Prior, error) {
	path := filepath.Join(a.opts.Home, filepath.FromSlash(BatchDir), id.String()+batchPriorExt)
	fh, err := os.Open(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return map[ids.AnyID]opsbatch.Prior{}, nil
	}
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "wire: open a batch before-image file", err)
	}
	defer func() { _ = fh.Close() }() // read only: a close error loses nothing
	out := map[ids.AnyID]opsbatch.Prior{}
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, priorScanStart), priorScanMax) // a whole row document per line
	for sc.Scan() {
		if len(strings.TrimSpace(sc.Text())) == 0 {
			continue
		}
		p, perr := parsePriorLine(sc.Bytes())
		if perr != nil {
			return nil, errs.New(errs.ConfigInvalid, "batch log "+filepath.Base(path)+": "+perr.Error())
		}
		if first, ok := out[p.Before.ID]; ok {
			first.After = p.After // keep the first image, the last hlc
			out[p.Before.ID] = first
			continue
		}
		out[p.Before.ID] = p
	}
	if err = sc.Err(); err != nil {
		return nil, errs.Wrap(errs.Internal, "wire: read a batch before-image file", err)
	}
	return out, nil
}

// Scanner sizes of a prior file: a line holds a whole row document.
const (
	priorScanStart = 64 << 10
	priorScanMax   = 64 << 20
)

// parsePriorLine decodes and types one prior line.
func parsePriorLine(b []byte) (opsbatch.Prior, error) {
	var l priorLine
	if err := json.Unmarshal(b, &l); err != nil {
		return opsbatch.Prior{}, err
	}
	after, err := hlc.Parse(l.After)
	if err != nil {
		return opsbatch.Prior{}, err
	}
	before, err := row.Decode(l.Before)
	if err != nil {
		return opsbatch.Prior{}, err
	}
	return opsbatch.Prior{Store: store.Source(l.Store), Before: before, After: after}, nil
}

// Revert implements opsbatch.Reverter: engine.Revert behind the same write
// guards as Update (the target is writable and reachable, the actor row
// exists).
func (a *App) Revert(ctx context.Context, ref engine.Ref, want hlc.HLC, before row.Row) (engine.Written, error) {
	st, err := a.writeState(ctx)
	if err != nil {
		return engine.Written{}, err
	}
	src := refStore(ctx, st, ref)
	if uerr := a.unreachableGuard(ctx, st, explicitTarget(src)); uerr != nil {
		return engine.Written{}, uerr
	}
	if ferr := a.checkTarget(src); ferr != nil {
		return engine.Written{}, ferr
	}
	at, was, gerr := st.eng.Get(ctx, ref)
	// batch leftover-3: the actor row and the revert as one batch (a
	// stale revert writes neither)
	w, err := a.withActorRows(ctx, st, func(ctx context.Context) (engine.Written, error) {
		if aerr := a.ensureActorFor(ctx, st, ref); aerr != nil {
			return engine.Written{}, aerr
		}
		w, err := st.eng.Revert(ctx, ref, want, before)
		a.wroteTo(src, err)
		return w, err
	})
	if err == nil && gerr == nil {
		w = a.keepPrior(ctx, at, was, w) // the undo's own batch can be undone too
	}
	return w, err
}

// priorOf reads the before-image of an Update about to run inside a batch
// (R14): ok is false outside a batch or when the row cannot be read (the
// Update then fails or is not undoable, and keepPrior says so).
func priorOf(ctx context.Context, eng *engine.Engine, ref engine.Ref) (store.Source, row.Row, bool) {
	if _, ok := engine.BatchOf(ctx); !ok {
		return "", row.Row{}, false
	}
	at, was, err := eng.Get(ctx, ref)
	return at, was, err == nil
}

// keepPrior records was (the row before an Update of ctx's batch) once the
// Update stored a change. The row is already written, so a failed record
// is a warning naming the row undo will not cover, never an error.
func (a *App) keepPrior(ctx context.Context, at store.Source, was row.Row, w engine.Written) engine.Written {
	b, ok := engine.BatchOf(ctx)
	if !ok || w.Row.HLC.Compare(was.HLC) == 0 { // a no-op update writes no event
		return w
	}
	if err := a.recordPrior(b, at, was, w.Row.HLC); err != nil {
		w.Warnings = append(w.Warnings, engine.Warning{Code: engine.WarnUndoUncovered,
			Message: "lore batch undo --id " + b.ID.String() + " cannot put " + was.ID.String() + " back: " + err.Error()})
	}
	return w
}

var _ opsbatch.Reverter = (*App)(nil)
