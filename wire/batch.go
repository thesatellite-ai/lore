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
func recordBatch(dir string, ev engine.Event) error {
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
	return appendAuditLine(filepath.Join(dir, ev.Batch.ID.String()+batchExt), batchDirMode, line)
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
	return eng.Policy(target)
}
