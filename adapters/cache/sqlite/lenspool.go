package sqlite

// lenspool.go — a long-lived process keeps a narrowed lens open between
// commands (devco:D-833, the local read server).
//
// Why: building a lens is most of a warm read's time (ATTACH parses the
// schema of every cache file, then the column lists and views are built).
// A one-shot process cannot avoid it; the read server (app/readserver)
// runs many commands in one process, so it turns reuse on and a command
// over the same files gets the lens the last one built:
//
//	OpenLensWith ─▶ pooled key?  (reuse on, narrowed lens, no trash rows)
//	   ├─ idle lens with the key, every file still the one attached and
//	   │  unchanged (same device+inode, same PRAGMA data_version and
//	   │  schema_version as right after its ATTACH) ─▶ reused
//	   ├─ same files and schemas, only rows written since (data_version
//	   │  moved) and the views those rows decide (kind arms, mask) come
//	   │  out the same ─▶ reused, its kept answers dropped (D-848)
//	   └─ else build a new one; record each file's identity and versions
//	Lens.Close ─▶ pooled lens back to idle (closed for real when reuse is
//	              off, evicted, or a file changed)
//
// Correctness: the views, the mask and the kind arms are built from the
// files' schemas and rows, so ANY committed change to an attached file
// (data_version moves for every commit by another connection) or a file
// replaced on disk (new inode) either rebuilds the lens or, when only rows
// moved, re-reads every row-decided input of the views on the open
// connection (rowShape) and keeps the lens only when they name the same
// views (lensShape). Why (devco:D-848): a sync that adds one row to one
// folder rebuilt the whole lens, ATTACH parsing every folder's schema
// again: ~30 ms of each served read right after a sync. Kept answers are
// dropped either way: they were read from the old rows. Only setup inputs are in
// the key; the per-command inputs (expander, query, clock) are set on
// every reuse. Off by default: a one-shot process never pools.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/model/value"
)

// lensPoolMax is how many idle lenses the pool keeps (one per store set
// and table set a reader asks over); the oldest is closed past it.
const lensPoolMax = 4

// lensPool is the process's idle lenses; on is false unless
// EnableLensReuse turned it on.
var lensPool struct {
	mu   sync.Mutex
	on   bool
	idle []*Lens
}

// pooledFile is what one attached file was when its lens was built.
type pooledFile struct {
	identity      string // device:inode (fileIdentity)
	data, schemaV int64  // PRAGMA data_version / schema_version right after ATTACH
}

// EnableLensReuse turns lens reuse on for this process (the read server
// only) and returns the func that turns it off and closes every idle
// lens. A one-shot command never calls it.
func EnableLensReuse() func() {
	lensPool.mu.Lock()
	lensPool.on = true
	lensPool.mu.Unlock()
	return func() {
		lensPool.mu.Lock()
		idle := lensPool.idle
		lensPool.idle, lensPool.on = nil, false
		lensPool.mu.Unlock()
		for _, l := range idle {
			_ = l.closeConn()
		}
	}
}

// lensReuseOn reports whether EnableLensReuse is in force.
func lensReuseOn() bool {
	lensPool.mu.Lock()
	defer lensPool.mu.Unlock()
	return lensPool.on
}

// poolKey is the key of a lens over sources with o, and whether such a
// lens may be pooled at all: only a narrowed lens (o.Tables) with no trash
// rows handed in, the one whose setup reads nothing but the files (a full
// lens also builds time views from the clock and the trash from the
// stores). Every setup input is in the key.
func poolKey(sources []LensSource, o LensOptions) (string, bool) {
	if len(o.Tables) == 0 || len(o.Trash) > 0 {
		return "", false
	}
	var b strings.Builder
	for _, s := range sources {
		abs, err := filepath.Abs(s.Path)
		if err != nil {
			return "", false
		}
		b.WriteString("src\x00" + s.Name + "\x00" + abs + "\x00" + strconv.FormatBool(s.Combined) + strconv.FormatBool(s.Deleted) + strconv.FormatBool(s.Shared) + "\x01")
	}
	b.WriteString("mask\x00" + strconv.FormatBool(o.MaskSensitive) + "\x01only\x00" + o.Only + "\x01client\x00" + o.TimeClientKey + "\x01")
	tables := slices.Clone(o.Tables)
	slices.Sort(tables)
	b.WriteString("tables\x00" + strings.Join(tables, "\x00") + "\x01")
	places := make([]string, 0, len(o.Places))
	for k, v := range o.Places {
		places = append(places, k+"\x00"+v)
	}
	slices.Sort(places)
	b.WriteString("places\x00" + strings.Join(places, "\x02"))
	return b.String(), true
}

// takePooled returns an idle lens with key whose files are unchanged, set
// up for this command's o; nil when there is none. A lens found stale is
// closed.
func takePooled(ctx context.Context, key string, o LensOptions) *Lens {
	lensPool.mu.Lock()
	var hit *Lens
	for i, l := range lensPool.idle {
		if l.poolKey == key {
			hit = l
			lensPool.idle = slices.Delete(lensPool.idle, i, i+1)
			break
		}
	}
	lensPool.mu.Unlock()
	if hit == nil {
		return nil
	}
	switch hit.state(ctx) {
	case fileSame:
	case fileRowsMoved:
		if !hit.sameShape(ctx) {
			_ = hit.closeConn()
			return nil
		}
	default:
		_ = hit.closeConn()
		return nil
	}
	hit.mu.Lock()
	hit.closed = false
	hit.expand, hit.query, hit.loadTrash, hit.now = o.Expand, o.Query, o.LoadTrash, o.Now
	hit.mu.Unlock()
	return hit
}

// recordFile notes what attached source i is right after its ATTACH, for
// a lens that may be pooled; a file whose identity cannot be read makes
// the lens unpooled.
func (l *Lens) recordFile(ctx context.Context, i int, abs string) {
	if l.poolKey == "" {
		return
	}
	fi, err := os.Stat(abs)
	id := ""
	if err == nil {
		id = fileIdentity(fi)
	}
	data, derr := l.pragmaOf(ctx, i, "data_version")
	sv, serr := l.pragmaOf(ctx, i, "schema_version")
	if id == "" || derr != nil || serr != nil {
		l.poolKey = "" // closed for real on Close
		return
	}
	l.files = append(l.files, pooledFile{identity: id, data: data, schemaV: sv})
}

// pragmaOf reads one integer PRAGMA of attached source i.
func (l *Lens) pragmaOf(ctx context.Context, i int, name string) (int64, error) {
	var v int64
	err := l.conn.QueryRowContext(ctx, "PRAGMA "+attachedName(i)+"."+name).Scan(&v)
	return v, err
}

// pooledState is what became of a pooled lens's files since it was built.
type pooledState int

const (
	// fileSame: every file is the one attached, with no commit since.
	fileSame pooledState = iota
	// fileRowsMoved: every file is the one attached with the schema it
	// had, and at least one commit since (rows written or removed).
	fileRowsMoved
	// fileReplaced: a file is gone, replaced, unreadable or its schema
	// changed: only a new lens is right.
	fileReplaced
)

// state reports what became of the lens's files since its build (or its
// last sameShape). It records each file's new data_version when rows
// moved, before sameShape reads them: a commit landing after this look
// moves the version again, so the next command looks again.
func (l *Lens) state(ctx context.Context) pooledState {
	if len(l.files) != len(l.sources) {
		return fileReplaced
	}
	moved := false
	data := make([]int64, len(l.sources))
	for i, s := range l.sources {
		abs, err := filepath.Abs(s.Path)
		if err != nil {
			return fileReplaced
		}
		fi, err := os.Stat(abs)
		if err != nil || fileIdentity(fi) != l.files[i].identity {
			return fileReplaced
		}
		d, derr := l.pragmaOf(ctx, i, "data_version")
		sv, serr := l.pragmaOf(ctx, i, "schema_version")
		if derr != nil || serr != nil || sv != l.files[i].schemaV {
			return fileReplaced
		}
		data[i] = d
		moved = moved || d != l.files[i].data
	}
	if !moved {
		return fileSame
	}
	for i := range l.files {
		l.files[i].data = data[i]
	}
	return fileRowsMoved
}

// sameShape reports whether the lens's views are still the ones its files
// call for after rows moved: the row-decided inputs (kind arms, mask) are
// read again from base on the open connection and must name the same views
// (lensShape). True drops the kept answers (read from the old rows); false
// leaves the lens to be closed.
func (l *Lens) sameShape(ctx context.Context) bool {
	if l.base == nil || l.shape == "" {
		return false
	}
	tables := cloneTables(l.base)
	m, err := l.rowShape(ctx, tables)
	if err != nil || lensShape(tables, m) != l.shape {
		return false
	}
	l.mu.Lock()
	l.kept = nil
	l.mu.Unlock()
	return true
}

// cloneTables copies tables so rowShape may add arms and drop tables
// without touching the original.
func cloneTables(tables map[string][]srcTable) map[string][]srcTable {
	out := make(map[string][]srcTable, len(tables))
	for n, parts := range tables {
		out[n] = slices.Clone(parts)
	}
	return out
}

// lensShape names every input createViews builds the views from: each
// exposed table's sources (index, columns, the arm SQL of a kind arm) and
// the mask. Two equal shapes build the same views over the same files.
func lensShape(tables map[string][]srcTable, m mask) string {
	names := make([]string, 0, len(tables))
	for n := range tables {
		names = append(names, n)
	}
	slices.Sort(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString("t\x00" + n + "\x01")
		for _, st := range tables[n] {
			b.WriteString(strconv.Itoa(st.src) + "\x00" + strings.Join(st.cols, "\x00") + "\x00" + st.from + "\x01")
		}
	}
	b.WriteString("mask\x00" + strconv.FormatBool(m.on) + "\x01")
	cols := make([]string, 0, len(m.cols))
	for t, cs := range m.cols {
		cs = slices.Clone(cs)
		slices.Sort(cs)
		cols = append(cols, t+"\x00"+strings.Join(cs, "\x00"))
	}
	slices.Sort(cols)
	b.WriteString(strings.Join(cols, "\x02") + "\x01" + strings.Join(m.fieldIDs, "\x00") + "\x01" + strings.Join(m.metaKeyIDs, "\x00"))
	return b.String()
}

// release puts a pooled lens back to idle; false when it is not pooled
// (reuse off, or a lens that may not be pooled), and the caller closes it.
func (l *Lens) release() bool {
	if l.poolKey == "" {
		return false
	}
	lensPool.mu.Lock()
	if !lensPool.on {
		lensPool.mu.Unlock()
		return false
	}
	lensPool.idle = append(lensPool.idle, l)
	var evict *Lens
	if len(lensPool.idle) > lensPoolMax {
		evict = lensPool.idle[0]
		lensPool.idle = lensPool.idle[1:]
	}
	lensPool.mu.Unlock()
	if evict != nil {
		_ = evict.closeConn()
	}
	return true
}

// Keeps reports whether QueryKept keeps answers on this lens: a pooled,
// open lens (the read server, devco:D-842). Why: a caller with a choice
// between one statement naming its input and one that names none reads
// the second only when it is kept (lore ask's text rows, devco:D-846);
// read once, the statement filtering in SQL is cheaper.
func (l *Lens) Keeps() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.poolKey != "" && !l.closed
}

// QueryKept is QueryTimeout for a statement whose answer depends only on
// the attached files: no clock, no per-command input, no lore_expand. A
// pooled lens (the read server, devco:D-842) keeps the answer and hands
// it out again until the lens goes, and any commit to an attached file or
// a file replaced on disk makes takePooled build a new lens, so a kept
// answer never outlives the rows it was read from. Why: every lore ask
// reads the same resources-and-open-tasks statement, whose compile over
// the per-file views and run cost ~10 ms of each warm served call. A lens
// that is not pooled (every one-shot command) runs it every time.
//
// The answer is a copy: a caller may change its slices without changing
// what the next caller gets.
func (l *Lens) QueryKept(ctx context.Context, query string, timeout time.Duration) (Result, error) {
	l.mu.Lock()
	res, ok := l.kept[query]
	pooled := l.poolKey != "" && !l.closed
	l.mu.Unlock()
	if ok && pooled {
		return copyResult(res), nil
	}
	res, err := l.QueryTimeout(ctx, query, timeout)
	if err != nil || !pooled {
		return res, err
	}
	l.mu.Lock()
	if l.kept == nil {
		l.kept = map[string]Result{}
	}
	l.kept[query] = copyResult(res)
	l.mu.Unlock()
	return res, nil
}

// copyResult copies r's column list and every row slice (the values are
// immutable).
func copyResult(r Result) Result {
	out := Result{Columns: slices.Clone(r.Columns), Rows: make([][]value.Value, len(r.Rows))}
	for i, row := range r.Rows {
		out.Rows[i] = slices.Clone(row)
	}
	return out
}
