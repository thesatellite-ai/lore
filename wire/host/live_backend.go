package host

// live_backend.go — the repo store's backend with refs/lore/live in front
// of it for the live kinds (LORE_TEAM_SPEC F1, F2; ported from saas/cmd/cli
// live_core.go):
//
//   - Get / Read: a row of a live-readable table is read from the local live
//     ref (fetched and integrated by the run's prepare) and from the branch
//     copy; when both exist the higher HLC wins, a tie is the live copy.
//   - Write: logic/rowroute decides as in v0.1.17 — a live row stays live, a
//     branch row's edit stays with the code, a new row of a live kind goes
//     live. Live rows are one compare-and-swap commit on the local live ref;
//     the run's finish pushes it.
//
// The default-branch read-through of rows that landed on origin/<default>
// before this checkout pulled is live_landed.go (Get and Write's expect;
// not the list Read). Not ported yet: the fenced coordination collector
// (coord host).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/logic/rowroute"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// A cursor issued by liveBackend: prefix, the live ref's commit, separator,
// then the wrapped backend's cursor. Any other cursor reads from the start.
const (
	liveCursorPrefix = "lv1:"
	liveCursorSep    = "|"
)

// liveAnonymousWriter stamps an agent's live block (D8: an agent never
// appears in a live hlc; the stamp only breaks ties).
const liveAnonymousWriter = "agent"

// liveFlagTables take --live / --no-live (PENDING-60): their rows may sit
// on the live ref though the table is no live kind, so they are read there.
var liveFlagTables = map[row.Table]bool{
	schema.TableTasks: true, schema.TableRules: true, schema.TableMemories: true,
}

// companionTables are supporting tables whose NEW rows ride live with the
// content row that needs them (FLAG6, PASS3-A-1).
var companionTables = map[row.Table]bool{
	schema.TableActors: true, schema.TableMetaKeys: true, schema.TableMetaValues: true,
}

// liveBackend wraps the repo store's backend.
type liveBackend struct {
	inner store.Backend
	// top is the git top level (the session's key); folder the project's
	// live folder (`_root` or a monorepo subproject path).
	top    string
	folder string
	// h is the host (its LeakCheck runs on writes with no live session).
	h *liveHost
	// off: live rows are disabled (LORE_LIVE / LORE_SYNC off). The backend
	// then has no live overlay at all, but still checks F16 on every
	// branch write (noSessionLeakCheck).
	off bool
}

var (
	_ store.Backend      = (*liveBackend)(nil)
	_ store.AuditJournal = (*liveBackend)(nil)
	_ wire.Unwrapper     = (*liveBackend)(nil)
)

// session is the run's prepared session (nil: no live overlay).
func (b *liveBackend) session(ctx context.Context) *liveSession {
	if b.off {
		return nil
	}
	r := runFrom(ctx)
	if r == nil {
		return nil
	}
	return r.session(ctx, b.top)
}

// isLiveKind: a live kind by default, or one config turned off (an existing
// live row of it is still read and edited live, rule 4).
func isLiveKind(s *liveSession, t row.Table) bool {
	_, ok := s.cfg.Defaults[string(t)]
	return ok
}

// liveReadable: rows of t are read from the live ref too.
func liveReadable(s *liveSession, t row.Table) bool {
	return isLiveKind(s, t) || companionTables[t] || liveFlagTables[t]
}

// liveChosen: the operation's --live names t (store.LiveChoice on ctx).
func liveChosen(ctx context.Context, t row.Table) bool {
	return store.LiveChoiceFrom(ctx).For(t) == store.LiveOn
}

// liveRequest is the routing input: --live / --no-live plus live.defaults.
func liveRequest(ctx context.Context, s *liveSession) rowroute.Request {
	req := rowroute.Request{Defaults: s.cfg.Defaults}
	c := store.LiveChoiceFrom(ctx)
	switch c.Mode {
	case store.LiveOn:
		req.Choice, req.ChoiceTable = rowroute.ChoiceLive, string(c.Table)
	case store.LiveOff:
		req.Choice, req.ChoiceTable = rowroute.ChoiceNoLive, string(c.Table)
	case store.LiveDefault:
	}
	return req
}

// liveFiles is the local live ref's commit ("" before the first write) and
// the folder's rows. An undecodable row is an error, never "absent".
func (b *liveBackend) liveFiles(ctx context.Context, s *liveSession) (string, map[live.RowKey]live.File, error) {
	oid, ok, err := s.ref.Repo.ResolveRef(ctx, s.ref.Name)
	if err != nil {
		return "", nil, err
	}
	if !ok {
		return "", map[live.RowKey]live.File{}, nil
	}
	tree, err := s.ref.ReadTree(ctx, oid)
	if err != nil {
		return "", nil, err
	}
	rows, bad := tree.Rows(b.folder)
	if len(bad) > 0 {
		paths := make([]string, 0, len(bad))
		for p := range bad {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		return "", nil, fmt.Errorf("unreadable live rows: %s (first: %w)", strings.Join(paths, ", "), bad[paths[0]])
	}
	return oid, rows, nil
}

// liveStoreRow is the backend row of a live file: its `_v: 2` body, with the
// block's stamp as `_hlc` for a v0.1.17 body that has none.
func liveStoreRow(k live.RowKey, f live.File) (store.Row, error) {
	body := f.Body.Clone()
	if body == nil {
		body = canonjson.Object{}
	}
	if !body.Has(row.KeyHLC) {
		if h, err := hlc.Parse(f.Block.HLC.String()); err == nil && !h.IsZero() {
			body[row.KeyHLC] = canonjson.String(h.String())
		}
	}
	if !body.Has(row.KeyTable) {
		body[row.KeyTable] = canonjson.String(k.Table)
	}
	if f.Deleted() {
		body[row.KeyDeleted] = canonjson.Bool(true)
		for key := range body {
			if !strings.HasPrefix(key, "_") && key != row.KeyID && key != row.KeyUpdatedAt && key != row.KeyUpdatedBy {
				delete(body, key) // a tombstone has no fields
			}
		}
	}
	raw, err := canonjson.Encode(body)
	if err != nil {
		return store.Row{}, fmt.Errorf("live row %s/%s: %w", k.Table, k.ID, err)
	}
	r, err := row.Decode(raw)
	if err != nil {
		return store.Row{}, errs.Wrap(errs.CodeOf(err), "live row "+k.Table+"/"+k.ID, err)
	}
	return store.FromRow(r)
}

// newer is what a reader sees when a row is live and on the branch.
func newer(liveRow, branchRow store.Row) store.Row {
	if branchRow.HLC.Compare(liveRow.HLC) > 0 {
		return branchRow
	}
	return liveRow
}

func keyOf(r store.Row) live.RowKey {
	return live.RowKey{Table: string(r.Table), ID: r.ID.String()}
}

// Get implements store.Backend.
func (b *liveBackend) Get(ctx context.Context, table row.Table, id ids.AnyID) (store.Row, error) {
	s := b.session(ctx)
	k := live.RowKey{Table: string(table), ID: id.String()}
	if s != nil {
		if ch, held := s.companions[k]; held {
			return ch.Row, nil
		}
	}
	if s == nil || !liveReadable(s, table) {
		return b.inner.Get(ctx, table, id)
	}
	_, files, err := b.liveFiles(ctx, s)
	if err != nil {
		return store.Row{}, err
	}
	branchRow, berr := b.inner.Get(ctx, table, id)
	if berr != nil && !errors.Is(berr, store.ErrNotFound) {
		return store.Row{}, berr
	}
	f, ok := files[k]
	if !ok {
		if r, landed := b.landedCopy(ctx, s, k, branchRow, berr == nil); landed {
			return r, nil // F1 read-through (live_landed.go)
		}
		return branchRow, berr
	}
	lr, err := liveStoreRow(k, f)
	if err != nil {
		return store.Row{}, err
	}
	if berr == nil {
		return newer(lr, branchRow), nil
	}
	return lr, nil
}

// Read implements store.Backend. While the live ref has not moved since
// cursor, it is the wrapped backend's changes with live copies merged in;
// once it moved, every branch row and every live row (an idempotent upsert
// for the cache), because a row that left live must be re-read.
func (b *liveBackend) Read(ctx context.Context, cursor store.Cursor) ([]store.Row, store.Cursor, error) {
	s := b.session(ctx)
	if s == nil {
		return b.inner.Read(ctx, cursor)
	}
	oid, files, err := b.liveFiles(ctx, s)
	if err != nil {
		return nil, "", err
	}
	innerCur := store.Cursor("")
	if rest, ok := strings.CutPrefix(string(cursor), liveCursorPrefix); ok {
		if seen, ic, ok := strings.Cut(rest, liveCursorSep); ok && seen == oid {
			innerCur = store.Cursor(ic)
		}
	}
	rows, next, err := b.inner.Read(ctx, innerCur)
	if err != nil {
		return nil, "", err
	}
	lives := map[store.Key]store.Row{}
	for k, f := range files {
		if !liveReadable(s, row.Table(k.Table)) {
			continue
		}
		lr, err := liveStoreRow(k, f)
		if err != nil {
			return nil, "", err
		}
		lives[lr.Key()] = lr
	}
	out := make([]store.Row, 0, len(rows)+len(lives))
	branchOnly := map[live.RowKey]int{} // index in out of rows with no live copy
	for _, r := range rows {
		if lr, ok := lives[r.Key()]; ok {
			out = append(out, newer(lr, r))
			delete(lives, r.Key())
			continue
		}
		branchOnly[keyOf(r)] = len(out)
		out = append(out, r)
	}
	if innerCur == "" {
		// The default-branch read-through for lists (live_landed.go): branch
		// rows origin holds newer copies of are swapped in place, and rows
		// that landed and are not pulled yet are added after the live ones.
		have := make(map[store.Key]bool, len(out)+len(lives))
		for _, r := range out {
			have[r.Key()] = true
		}
		for k := range lives {
			have[k] = true
		}
		b.landedOverRows(ctx, s, out, branchOnly)
		keys := make([]store.Key, 0, len(lives))
		for k := range lives {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].Table != keys[j].Table {
				return keys[i].Table < keys[j].Table
			}
			return keys[i].ID.String() < keys[j].ID.String()
		})
		for _, k := range keys {
			out = append(out, lives[k])
		}
		out = append(out, b.landedRows(ctx, s, have)...)
	}
	return out, store.Cursor(liveCursorPrefix + oid + liveCursorSep + string(next)), nil
}

// Write implements store.Backend: each change's Expect is checked against
// the copy a reader sees, then each row goes where rowroute places it.
// Branch rows first (all-or-none), then the live rows in one commit.
func (b *liveBackend) Write(ctx context.Context, changes []store.Change) error {
	s := b.session(ctx)
	if s == nil {
		if err := b.noSessionLeakCheck(ctx, changes); err != nil {
			return err
		}
		return b.inner.Write(ctx, changes)
	}
	_, files, err := b.liveFiles(ctx, s)
	if err != nil {
		return err
	}
	if changes, err = b.holdCompanions(ctx, s, files, changes); err != nil || len(changes) == 0 {
		return err
	}
	req := liveRequest(ctx, s)
	var branch []store.Change
	liveRows := map[live.RowKey]live.File{}
	now := s.clk.Now()
	for _, ch := range changes {
		k := keyOf(ch.Row)
		prev, inLive := files[k]
		chosen := liveChosen(ctx, ch.Row.Table) && liveReadable(s, ch.Row.Table)
		if !chosen && !isLiveKind(s, ch.Row.Table) && (!inLive || !liveReadable(s, ch.Row.Table)) {
			if ch, err = b.landedExpect(ctx, s, files, ch); err != nil {
				return err
			}
			branch = append(branch, ch)
			continue
		}
		branchRow, berr := b.inner.Get(ctx, ch.Row.Table, ch.Row.ID)
		if berr != nil && !errors.Is(berr, store.ErrNotFound) {
			return berr
		}
		inBranch := berr == nil
		place := rowroute.Decide(req, rowroute.Row{Table: k.Table, ID: k.ID, InLive: inLive, InBranch: inBranch, Deleted: ch.Row.Deleted}).Place
		if chosen {
			place = rowroute.PlaceLive
		}
		if place != rowroute.PlaceLive {
			if !inLive {
				ch = b.overLanded(ctx, s, ch, branchRow, inBranch)
			}
			branch = append(branch, ch)
			continue
		}
		cur := store.Row{}
		switch {
		case inLive:
			lr, lerr := liveStoreRow(k, prev)
			if lerr != nil {
				return lerr
			}
			cur = lr
			if inBranch {
				cur = newer(lr, branchRow)
			}
		case inBranch:
			cur = branchRow
		}
		if !inLive {
			if r, landed := b.landedCopy(ctx, s, k, branchRow, inBranch); landed {
				cur = r // the copy Get showed the reader (live_landed.go)
			}
		}
		if cur.HLC.Compare(ch.Expect) != 0 {
			return errs.Wrap(errs.Stale, "live row "+k.String()+" changed since it was read", store.ErrStale)
		}
		var prevPtr *live.File
		if inLive {
			prevPtr = &prev
		}
		f, ferr := liveFileFor(ch.Row, prevPtr, now, s)
		if ferr != nil {
			return ferr
		}
		liveRows[k] = f
	}
	// F16: the live ref and the branch of a client repo are readable by the
	// client. Checked before any row of the batch is written, so a refusal
	// writes nothing.
	if err = s.h.leakCheck(ctx, leakWhereLive, b.folder, liveRows); err != nil {
		return err
	}
	if err = s.h.leakCheckBranch(ctx, b.folder, branch); err != nil {
		return err
	}
	var companions map[live.RowKey]bool
	if len(liveRows) > 0 {
		if companions, err = s.companionFiles(now, liveRows); err != nil {
			return err
		}
	} else if len(s.companions) > 0 {
		branch, companions = s.companionChanges(branch)
	}
	if len(branch) > 0 {
		if err = b.inner.Write(ctx, branch); err != nil {
			return err
		}
		if len(liveRows) == 0 {
			s.dropCompanions(companions)
		}
	}
	if len(liveRows) == 0 {
		return nil
	}
	if s.blocked != nil {
		return rewrittenError(errs.StoreReadOnly, s.blocked)
	}
	res, err := s.ref.Write(ctx, live.WriteRequest{Folder: b.folder, Rows: liveRows, BinaryVersion: s.h.cfg.Version})
	if err != nil {
		if len(branch) > 0 {
			s.warn("rows with the code were written, the live ones were not: " + err.Error())
		}
		return errs.Wrap(errs.Internal, "live: write the local live ref", err)
	}
	s.dropCompanions(companions)
	s.wrote += len(res.Written)
	s.markFolder(b.folder)
	return nil
}

// liveFileFor is the live file of one core row: its document as the body,
// a block stamp strictly above the previous version (the row's own `_hlc`
// whenever that is above it). A tombstone keeps only the envelope (§3.3).
func liveFileFor(r store.Row, prev *live.File, now time.Time, s *liveSession) (live.File, error) {
	doc, err := canonjson.Decode(r.Doc, canonjson.DefaultMaxDepth)
	if err != nil {
		return live.File{}, fmt.Errorf("live: decode %s/%s: %w", r.Table, r.ID, err)
	}
	actor := liveAnonymousWriter
	if !s.runAgent() && !r.HLC.Actor.IsZero() {
		actor = r.HLC.Actor.String()
	}
	var f live.File
	if r.Deleted {
		f = live.NewVersion(prev, envelopeOnly(doc), now, actor)
		f.Block.Deleted = true
		f.Block.Conflict = false
	} else {
		f = live.NewVersion(prev, doc, now, actor)
	}
	if h, err := live.ParseHLC(r.HLC.String()); err == nil && (prev == nil || h.Compare(prev.Block.HLC) > 0) {
		f.Block.HLC = h
	}
	return f, nil
}

// envelopeOnly keeps a tombstone document's envelope keys and id.
func envelopeOnly(doc canonjson.Object) canonjson.Object {
	out := canonjson.Object{}
	for k, v := range doc {
		if strings.HasPrefix(k, "_") || k == row.KeyID || k == row.KeyUpdatedAt || k == row.KeyUpdatedBy {
			out[k] = v
		}
	}
	return out
}

// ---------------------------------------------------------------- companions (FLAG6)

// holdCompanions takes every NEW row of a companion table out of changes and
// holds it on the session until the run's next content write carries it
// (the same live commit, or the same branch write); finish writes what no
// content row took. An explicitly live supporting row is written on its own.
func (b *liveBackend) holdCompanions(ctx context.Context, s *liveSession, files map[live.RowKey]live.File, changes []store.Change) ([]store.Change, error) {
	kept := changes[:0:0]
	for _, ch := range changes {
		if !companionTables[ch.Row.Table] || ch.Row.Deleted || liveChosen(ctx, ch.Row.Table) {
			kept = append(kept, ch)
			continue
		}
		k := keyOf(ch.Row)
		if prev, held := s.companions[k]; held {
			ch.Expect = prev.Expect // still a create: the held row has no file yet
			s.companions[k] = ch
			continue
		}
		if _, inLive := files[k]; inLive {
			kept = append(kept, ch)
			continue
		}
		if _, err := b.inner.Get(ctx, ch.Row.Table, ch.Row.ID); err == nil {
			kept = append(kept, ch)
			continue
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		if s.companions == nil {
			s.companions = map[live.RowKey]store.Change{}
		}
		s.companions[k] = ch
		s.companionInner = b.inner
	}
	return kept, nil
}

// companionFiles adds every held row to a live write, marked Companion (it
// lands only together with a content row, F2), and returns their keys.
func (s *liveSession) companionFiles(now time.Time, rows map[live.RowKey]live.File) (map[live.RowKey]bool, error) {
	keys := map[live.RowKey]bool{}
	for k, ch := range s.companions {
		if _, own := rows[k]; own {
			continue
		}
		f, err := liveFileFor(ch.Row, nil, now, s)
		if err != nil {
			return nil, err
		}
		f.Block.Companion = true
		rows[k] = f
		keys[k] = true
	}
	return keys, nil
}

// companionChanges appends every held row to a branch write.
func (s *liveSession) companionChanges(branch []store.Change) ([]store.Change, map[live.RowKey]bool) {
	keys := map[live.RowKey]bool{}
	for k, ch := range s.companions {
		branch = append(branch, ch)
		keys[k] = true
	}
	return branch, keys
}

// dropCompanions forgets held rows that were written.
func (s *liveSession) dropCompanions(keys map[live.RowKey]bool) {
	for k := range keys {
		delete(s.companions, k)
	}
}

// flushCompanions writes the held rows no content row carried to the
// branch's .lore/data; an identical file already there is a no-op.
func (s *liveSession) flushCompanions(ctx context.Context) error {
	if len(s.companions) == 0 || s.companionInner == nil {
		return nil
	}
	branch, keys := s.companionChanges(nil)
	pending := branch[:0:0]
	for _, ch := range branch {
		cur, err := s.companionInner.Get(ctx, ch.Row.Table, ch.Row.ID)
		switch {
		case err == nil && ch.Expect.IsZero() && sameFile(cur, ch.Row):
			continue
		case err != nil && !errors.Is(err, store.ErrNotFound):
			return err
		}
		pending = append(pending, ch)
	}
	if len(pending) > 0 {
		if err := s.companionInner.Write(ctx, pending); err != nil {
			return err
		}
	}
	s.dropCompanions(keys)
	return nil
}

// sameFile reports whether cur already holds what r would write.
func sameFile(cur, r store.Row) bool {
	if cur.Deleted != r.Deleted {
		return false
	}
	a, errA := canonjson.Canonicalize(cur.Doc)
	c, errC := canonjson.Canonicalize(r.Doc)
	return errA == nil && errC == nil && bytes.Equal(a, c)
}

// runAgent reports whether an agent runs the operation (D8).
func (s *liveSession) runAgent() bool { return s.agent }

// ---------------------------------------------------------------- pass-through

// WriteAudited implements store.AuditJournal: with a live session it is a
// plain Write when any row may go live (the engine records the audit after
// it), else the inner journal's one batch.
func (b *liveBackend) WriteAudited(ctx context.Context, changes []store.Change, recs []store.AuditRecord) error {
	j, ok := b.inner.(store.AuditJournal)
	if !ok {
		return b.Write(ctx, changes)
	}
	if s := b.session(ctx); s != nil {
		branch, err := b.wholeBatchToBranch(ctx, s, changes)
		if err != nil {
			return err
		}
		if !branch {
			return b.Write(ctx, changes)
		}
		// The journal writes the batch as given: a read-through edit must
		// expect the branch copy first, as Write's branch rows do.
		_, files, err := b.liveFiles(ctx, s)
		if err != nil {
			return err
		}
		out := make([]store.Change, len(changes))
		for i, ch := range changes {
			if out[i], err = b.landedExpect(ctx, s, files, ch); err != nil {
				return err
			}
		}
		changes = out
		if err := s.h.leakCheckBranch(ctx, b.folder, changes); err != nil {
			return err // F16 before the branch write, as Write does
		}
	} else if err := b.noSessionLeakCheck(ctx, changes); err != nil {
		return err
	}
	return j.WriteAudited(ctx, changes, recs)
}

// wholeBatchToBranch reports whether Write would send every change to the
// branch (Write's first routing rule, checked up front).
func (b *liveBackend) wholeBatchToBranch(ctx context.Context, s *liveSession, changes []store.Change) (bool, error) {
	if len(s.companions) > 0 {
		return false, nil
	}
	_, files, err := b.liveFiles(ctx, s)
	if err != nil {
		return false, err
	}
	req := liveRequest(ctx, s)
	for _, ch := range changes {
		if companionTables[ch.Row.Table] {
			return false, nil
		}
		k := keyOf(ch.Row)
		if _, inLive := files[k]; inLive {
			return false, nil
		}
		if !isLiveKind(s, ch.Row.Table) && !liveChosen(ctx, ch.Row.Table) {
			continue
		}
		_, berr := b.inner.Get(ctx, ch.Row.Table, ch.Row.ID)
		if berr != nil && !errors.Is(berr, store.ErrNotFound) {
			return false, berr
		}
		if rowroute.Decide(req, rowroute.Row{Table: k.Table, ID: k.ID, InBranch: berr == nil, Deleted: ch.Row.Deleted}).Place == rowroute.PlaceLive {
			return false, nil
		}
	}
	return true, nil
}

// PendingAudit implements store.AuditJournal.
func (b *liveBackend) PendingAudit(ctx context.Context) ([]store.AuditRecord, error) {
	if j, ok := b.inner.(store.AuditJournal); ok {
		return j.PendingAudit(ctx)
	}
	return nil, nil
}

// AckAudit implements store.AuditJournal.
func (b *liveBackend) AckAudit(ctx context.Context, recs []store.AuditRecord) error {
	if j, ok := b.inner.(store.AuditJournal); ok {
		return j.AckAudit(ctx, recs)
	}
	return nil
}

// Sync implements store.Backend.
func (b *liveBackend) Sync(ctx context.Context) (store.SyncReport, error) { return b.inner.Sync(ctx) }

// Capabilities implements store.Backend.
func (b *liveBackend) Capabilities() store.Capabilities { return b.inner.Capabilities() }

// Close implements store.Backend.
func (b *liveBackend) Close() error { return b.inner.Close() }

// Unwrap implements wire.Unwrapper.
func (b *liveBackend) Unwrap() store.Backend { return b.inner }

var _ wire.LiveOverlay = (*liveBackend)(nil)

// ServesLiveRef implements wire.LiveOverlay: this wrapper serves the repo's
// live ref, so its rows are never reported as v0.1.17 rows waiting for
// `lore migrate run` (wire liveNote), also in a run without a live session.
func (b *liveBackend) ServesLiveRef() bool { return true }

// ---------------------------------------------------------------- purge

var _ engine.Purger = (*liveBackend)(nil)

// WritePurge implements engine.Purger: changes go through the live routing
// (Write), then purged is listed in the inner backend's _purged.json.
// Without it every purge behind the wrapper was E_UNSUPPORTED.
func (b *liveBackend) WritePurge(ctx context.Context, changes []store.Change, purged []ids.AnyID) error {
	p, ok := b.inner.(engine.Purger)
	if !ok {
		return errs.New(errs.Unsupported, "this store cannot record purged ids")
	}
	if len(changes) > 0 {
		if err := b.Write(ctx, changes); err != nil {
			return err
		}
	}
	return p.WritePurge(ctx, nil, purged)
}

// Purged implements engine.Purger (the inner backend's list).
func (b *liveBackend) Purged(ctx context.Context) (map[string]bool, error) {
	if p, ok := b.inner.(engine.Purger); ok {
		return p.Purged(ctx)
	}
	return map[string]bool{}, nil
}

// landedExpect is overLanded for a branch-bound change whose branch copy
// is not known yet; a live row keeps its Expect (Get served the live copy).
func (b *liveBackend) landedExpect(ctx context.Context, s *liveSession, files map[live.RowKey]live.File, ch store.Change) (store.Change, error) {
	if ch.Expect.IsZero() || !liveReadable(s, ch.Row.Table) {
		return ch, nil // Get reads through only live-readable tables
	}
	if _, inLive := files[keyOf(ch.Row)]; inLive {
		return ch, nil
	}
	branchRow, berr := b.inner.Get(ctx, ch.Row.Table, ch.Row.ID)
	if berr != nil && !errors.Is(berr, store.ErrNotFound) {
		return ch, berr
	}
	return b.overLanded(ctx, s, ch, branchRow, berr == nil), nil
}

// overLanded rewrites the Expect of a branch-bound edit of a row Get read
// through from origin/<default> (landedCopy, F1): the caller expects the
// landed copy, but the branch file the edit replaces is the work tree's
// older copy (found) or none, so the branch write would refuse it as
// stale (t-old2: `memory archive` after a committed local delete of a
// row still on origin/main was E_STALE). Only an Expect that names exactly
// the landed copy is rewritten; a stale read stays stale. v0.1.17
// live_core.go materialiseLanded / overLanded.
func (b *liveBackend) overLanded(ctx context.Context, s *liveSession, ch store.Change, branchRow store.Row, found bool) store.Change {
	if ch.Expect.IsZero() {
		return ch
	}
	landed, ok := b.landedCopy(ctx, s, keyOf(ch.Row), branchRow, found)
	if !ok || landed.HLC.Compare(ch.Expect) != 0 {
		return ch
	}
	if found {
		ch.Expect = branchRow.HLC
	} else {
		ch.Expect = hlc.HLC{}
	}
	return ch
}

// noSessionLeakCheck is F16 on a branch write that has no live session: live
// rows off, an operation the live hook skips (config, identity, org, stores,
// init), or a caller outside the host runner. The client reads the branch
// rows once committed, so they get the same write-time check as a live
// run's branch rows (leakCheckBranch); before this, only the commit and
// push hooks caught them. migrate is exempt (leakExempt): it rewrites rows
// the repo already holds, it does not add text.
func (b *liveBackend) noSessionLeakCheck(ctx context.Context, changes []store.Change) error {
	if b.h == nil || leakExempt(ctx) {
		return nil
	}
	return b.h.leakCheckBranch(ctx, b.folder, changes)
}
