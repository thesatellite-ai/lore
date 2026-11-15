package engine

import (
	"bytes"
	"context"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Read-side secret masking (devco:R-14). A write is scanned before it is
// stored, but a row can reach a store without lore: a teammate hand-writes
// a row file and commits it with the hooks off, or a rule added later
// matches an old row. Such a row was printed raw by show, search and
// list and written into render files and agent context. So every read
// (List, Get, and the cache Refresh that search and sql read) scans the
// row's text fields with the engine's rules and replaces each match no
// secret_allows entry accepts with ReadRedacted plus the rule; the row
// file is never changed. The rows masked in this process are kept so
// render can leave them out and say which row and author it skipped.
//
// The secret checks themselves (`lore check leaks --rows`, lint, `lore
// secrets allow`) must see the stored text to report and allow a
// finding: they read with WithRawRead.

// ReadRedacted is the text a secret match is replaced with on read; the
// rule id follows it ("[redacted github_token]").
const ReadRedacted = "[redacted "

// rawReadKey marks a ctx whose reads return the stored text unmasked.
type rawReadKey struct{}

// WithRawRead returns ctx whose reads skip read-side masking: only the
// secret checks that must report or allow the stored match use it.
func WithRawRead(ctx context.Context) context.Context {
	return context.WithValue(ctx, rawReadKey{}, true)
}

// rawRead reports whether ctx asks for unmasked reads.
func rawRead(ctx context.Context) bool {
	v, _ := ctx.Value(rawReadKey{}).(bool)
	return v
}

// MaskedRead names one row whose text a read masked: which row, in which
// store, who wrote it, and which fields and rules matched (never the
// text).
type MaskedRead struct {
	Source store.Source
	Table  row.Table
	ID     ids.AnyID
	Author ids.ID[ids.Actor] // updated_by, else created_by; zero when unknown
	Fields []string
	Rules  []string
}

// readMasks is the per-engine state of read masking: a memo of scan
// results by (field, text), so a row read many times in one process is
// scanned once, and the rows masked so far.
type readMasks struct {
	mu     sync.Mutex
	memo   map[[2]string][]scan.Finding
	masked map[string]MaskedRead
}

// readMaskSkip are the tables never masked on read: the secret settings
// themselves (an allow entry names a rule and a hash, never the text, and
// reading them happens inside the mask).
var readMaskSkip = map[row.Table]bool{schema.TableSecretAllows: true, schema.TableSecretRules: true}

// WithReadScanner sets the scanner reads are masked with (devco:R-14).
// It must not call back into the code that opens the engine: reads run
// while it opens. Unset: reads are not masked.
func WithReadScanner(s scan.Scanner) Option {
	return func(e *Engine) error { e.readScanner = s; return nil }
}

// masks returns e's read-mask state, made on first use.
func (e *Engine) masks() *readMasks {
	e.readMaskOnce.Do(func() {
		e.readMask = &readMasks{memo: map[[2]string][]scan.Finding{}, masked: map[string]MaskedRead{}}
	})
	return e.readMask
}

// scanRead is scanText through the read memo.
func (e *Engine) scanRead(field, text string) []scan.Finding {
	m := e.masks()
	k := [2]string{field, text}
	m.mu.Lock()
	got, ok := m.memo[k]
	m.mu.Unlock()
	if ok {
		return got
	}
	found := e.readScanner.Scan(field, text)
	m.mu.Lock()
	m.memo[k] = found
	m.mu.Unlock()
	return found
}

// maskRead returns r with every unaccepted secret match in its text
// fields replaced (see the package note above); r itself is not changed.
// A row with no match is returned as it is.
func (e *Engine) maskRead(ctx context.Context, src store.Source, r row.Row) row.Row {
	if r.Deleted || readMaskSkip[r.Table] || rawRead(ctx) || e.readScanner == nil {
		return r
	}
	r = e.maskOrigin(r)
	if len(r.Fields) == 0 {
		return r
	}
	names := slices.Sorted(maps.Keys(r.Fields))
	loreRec := e.loreRecordFields(r.ID)
	var hits []*textJob
	for _, j := range scanJobs(r.Fields, names) {
		for _, f := range e.scanRead(j.field, j.text) {
			// BUG-T7: a session id in lore's own structured record is
			// bookkeeping, not a leak (the write path's rule).
			if f.RuleID == scan.RuleSessionID && loreRec[j.field] {
				continue
			}
			j.found = append(j.found, f)
		}
		if len(j.found) > 0 {
			hits = append(hits, j)
		}
	}
	if len(hits) == 0 {
		return r
	}
	// Allow entries are read only when something matched (rare): a match
	// someone accepted as not a secret stays readable.
	allows, err := e.settings.Allows(ctx, src)
	if err != nil {
		allows = nil // fail closed: mask every match
	}
	fields := maps.Clone(r.Fields)
	mr := MaskedRead{Source: src, Table: r.Table, ID: r.ID, Author: cmpActor(r.UpdatedBy, r.CreatedBy)}
	nFields := 0
	for _, j := range hits {
		kept := scan.Filter(r.ID, j.found, allows)
		if len(kept) == 0 {
			continue
		}
		repl := make([]string, len(kept))
		for i, f := range kept {
			repl[i] = ReadRedacted + f.RuleID + "]"
			mr.Rules = append(mr.Rules, f.RuleID)
		}
		fields[j.field] = maskValue(fields[j.field], j.item, kept, repl)
		mr.Fields = append(mr.Fields, j.field)
		nFields++
	}
	if nFields == 0 {
		return r
	}
	m := e.masks()
	m.mu.Lock()
	if prev, ok := m.masked[r.ID.String()]; ok { // its `_o` was masked too
		mr.Fields = append(mr.Fields, prev.Fields...)
		mr.Rules = append(mr.Rules, prev.Rules...)
	}
	slices.Sort(mr.Rules)
	mr.Rules = slices.Compact(mr.Rules)
	slices.Sort(mr.Fields)
	mr.Fields = slices.Compact(mr.Fields)
	m.masked[r.ID.String()] = mr
	m.mu.Unlock()
	r.Fields = fields
	return r
}

// maskOrigin masks a secret match in r's `_o` block (devco:R-10: a row
// made before the branch scrub, or hand-written, can hold a token in
// `_o.git.branch`). Allow entries do not apply: `_o` is lore's record,
// never text a person meant to keep.
func (e *Engine) maskOrigin(r row.Row) row.Row {
	o := r.Origin
	if o.IsZero() {
		return r
	}
	found := e.scanRead(row.KeyOrigin, o.String())
	if len(found) == 0 {
		return r
	}
	repl := make([]string, len(found))
	for i, f := range found {
		repl[i] = ReadRedacted + f.RuleID + "]"
	}
	nv, err := redactJSON(o, found, repl)
	if err != nil {
		nv = value.JSON{} // cannot cut it out cleanly: the block reads as absent
	}
	r.Origin = value.JSON{}
	if j, ok := nv.(value.JSON); ok {
		r.Origin = j
	}
	m := e.masks()
	m.mu.Lock()
	mr := m.masked[r.ID.String()]
	mr.Source, mr.Table, mr.ID, mr.Author = "", r.Table, r.ID, cmpActor(r.UpdatedBy, r.CreatedBy)
	mr.Fields = append(mr.Fields, row.KeyOrigin)
	for _, f := range found {
		mr.Rules = append(mr.Rules, f.RuleID)
	}
	m.masked[r.ID.String()] = mr
	m.mu.Unlock()
	return r
}

// maskValue replaces the matches fs in v (item: a list item, -1 the whole
// value). When a json match cannot be replaced inside one string value,
// the whole value reads as the redaction text: never the match.
func maskValue(v value.Value, item int, fs []scan.Finding, repl []string) value.Value {
	if l, ok := v.(value.List); ok && item >= 0 {
		items := slices.Clone(l.Items())
		items[item] = maskValue(items[item], -1, fs, repl)
		if nl, err := value.ListOf(l.Elem(), items...); err == nil {
			return nl
		}
		return wholeRedacted(fs)
	}
	nv, err := redactValueWith(v, item, fs, repl)
	if err == nil {
		return nv
	}
	return wholeRedacted(fs)
}

// wholeRedacted is the text a value reads as when its matches cannot be
// cut out one by one.
func wholeRedacted(fs []scan.Finding) value.Value {
	t, _ := value.TextOf(ReadRedacted + fs[0].RuleID + "]")
	return t
}

// cmpActor is the first non-zero actor id.
func cmpActor(a, b ids.ID[ids.Actor]) ids.ID[ids.Actor] {
	if !a.IsZero() {
		return a
	}
	return b
}

// MaskedReads lists the rows this engine masked on read so far, by id.
func (e *Engine) MaskedReads() []MaskedRead {
	m := e.masks()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := slices.Collect(maps.Values(m.masked))
	slices.SortFunc(out, func(a, b MaskedRead) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	return out
}

// WasMasked reports whether a read in this process masked row id; render
// leaves such a row out of every file an agent reads.
func (e *Engine) WasMasked(id ids.AnyID) bool {
	m := e.masks()
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.masked[id.String()]
	return ok
}

// maskStoreRows masks the rows a cache refresh applies, so search and sql
// read what show reads. Rows with no match keep their bytes.
func (e *Engine) maskStoreRows(ctx context.Context, src store.Source, rows []store.Row) []store.Row {
	if rawRead(ctx) {
		return rows
	}
	var out []store.Row
	for i, sr := range rows {
		if sr.Deleted || readMaskSkip[sr.Table] {
			continue
		}
		r, err := sr.Decode()
		if err != nil {
			continue // the cache refuses it as before
		}
		m := e.maskRead(ctx, src, r)
		if !e.WasMasked(r.ID) {
			continue // no match in this row: its bytes stay
		}
		nr, err := store.FromRow(m)
		if err != nil {
			continue
		}
		if was, err := store.FromRow(r); err == nil && bytes.Equal(was.Doc, nr.Doc) {
			continue // nothing masked
		}
		if out == nil {
			out = slices.Clone(rows)
		}
		out[i] = nr
	}
	if out == nil {
		return rows
	}
	return out
}
