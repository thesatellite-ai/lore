// live_core.go — v0.1.17 live mode on the 0.1.18 core (LORE_TEAM_SPEC F1,
// F2, F5).
//
// The core writes row files into the repo store's .lore/data and never
// commits, so without this file an open task added in one checkout reached
// another checkout only after a commit and a push. v0.1.17 instead kept the
// open rows of its live kinds (lorecfg.DefaultLiveKinds: tasks, missions,
// handoffs, comments) on refs/lore/live, pushed at the end of every command,
// and landed them on the default branch once finished.
//
// liveBackend restores that, strangler-style, at the one seam the core has
// for where rows sit: it wraps the repo store's store.Backend (wire's
// Options.WrapBackend), so the engine, the cache and every op see live rows
// without knowing about them:
//
//   - Get / Read: a row of a live table is read from the local live ref
//     (after the command's fetch + integrate, live_wire.go) and from the
//     branch copy; when both exist the newer HLC wins (a landed copy equals
//     the live one; a later edit is always written where the row already
//     is). On the default branch, a row that landed on the fetched
//     origin/<default> and is neither live nor checked out yet is still
//     found (v0.1.17's default-branch read-through, F1).
//   - Write: rowroute decides as in v0.1.17 — a live row stays live, a
//     branch row's edit stays with the code, a new row of a live kind goes
//     live. Live rows are committed to the local live ref (live.Ref.Write,
//     compare-and-swap); the end-of-command pass pushes the ref and lands
//     finished rows (live_wire.go finish / live_land.go). A push that fails
//     leaves the row on the local live ref, which this backend keeps
//     serving, and the command says it is waiting (live.PushOutcome): the
//     same partial-write path as v0.1.17.
//
// Live mode off (LORE_LIVE=0 or LORE_SYNC=0, no git repo) makes the wrapper a
// no-op: every row lives in .lore/data, as v0.1.17 with LORE_LIVE=0.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"

	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/live"
	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/rowroute"
	"saas/pkg/aicoder/style"
)

// liveCursorPrefix marks a cursor issued by liveBackend: prefix, the live
// ref's commit, separator, then the wrapped backend's own cursor. A cursor
// without it (issued before live mode applied) reads from the beginning.
const (
	liveCursorPrefix = "lv1:"
	liveCursorSep    = "|"
)

// dirBackend is a backend that knows its repository folder (the repo
// store's *git.Backend).
type dirBackend interface {
	store.Backend
	Dir() string
}

// coreWrapBackend is wire.Options.WrapBackend: the repo store gets the live
// overlay when live mode applies; a store clone with a remote pushes after
// each write (store_push_core.go); every other store is returned as is.
func coreWrapBackend(_ context.Context, name store.Source, _ string, b store.Backend) store.Backend {
	if name != store.SourceRepo {
		return wrapStorePush(name, b)
	}
	if liveDisabled() {
		return b
	}
	d, ok := b.(dirBackend)
	if !ok {
		return b
	}
	return &liveBackend{inner: d, top: d.Dir(), folder: live.RootFolder}
}

// liveBackend is the repo store's backend with v0.1.17's live ref in front
// of it for the live kinds (see the file comment).
type liveBackend struct {
	inner store.Backend
	// top is the repository's top level (the live session's key).
	top string
	// folder is the live folder of the repo store (`_root`: the repo store
	// is always the repository's top-level .lore/data).
	folder string
}

var (
	_ store.Backend  = (*liveBackend)(nil)
	_ wire.Unwrapper = (*liveBackend)(nil)
)

// session is the live session of the repository, prepared (fetched and
// integrated) once per command; nil when live mode does not apply here.
func (b *liveBackend) session(ctx context.Context) *liveSession {
	s := liveFor(ctx, b.top)
	if s != nil && !s.prepared {
		s.prepare(ctx, liveCmdState.fetchKind())
	}
	return s
}

// liveKinds are the tables whose new rows go live (config live.defaults,
// lorecfg.DefaultLiveKinds when no session gives one).
func liveKinds(s *liveSession) map[string]bool {
	if s != nil && s.cfg.Defaults != nil {
		return s.cfg.Defaults
	}
	return lorecfg.DefaultLiveKinds()
}

// isLiveKind reports whether table can hold live rows at all: a live kind
// by default, or one config turned off (an existing live row of it is still
// read and edited live, rule 4).
func isLiveKind(s *liveSession, table row.Table) bool {
	_, ok := liveKinds(s)[string(table)]
	return ok
}

// liveFiles is the live folder of the local live ref: its commit ("" when
// the ref does not exist yet) and its rows. A row that cannot be decoded is
// an error, never "absent": an absent task looks free to `task next`.
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
	if err := badRowsError(bad); err != nil {
		return "", nil, err
	}
	return oid, rows, nil
}

// liveStoreRow turns a live file into the backend row the core reads. The
// body of a row the core wrote is its `_v: 2` document; a v0.1.17 body gets
// the block's stamp as `_hlc` when it has none (wire/liverows.go does the
// same), else row.Decode upcasts it from updated_at.
func liveStoreRow(k live.RowKey, f live.File) (store.Row, error) {
	body := maps.Clone(f.Body)
	if body == nil {
		body = map[string]any{}
	}
	if _, has := body[row.KeyHLC]; !has {
		if h, err := hlc.Parse(f.Block.HLC.String()); err == nil && !h.IsZero() {
			body[row.KeyHLC] = h.String()
		}
	}
	if _, has := body[row.KeyTable]; !has {
		body[row.KeyTable] = k.Table
	}
	if f.Deleted() {
		body[row.KeyDeleted] = true
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

// newer is the copy a reader sees when a row is both live and in the
// branch data: the higher HLC; a tie is the live copy (live wins, F1).
func newer(liveRow, branchRow store.Row) store.Row {
	if branchRow.HLC.Compare(liveRow.HLC) > 0 {
		return branchRow
	}
	return liveRow
}

// Get implements store.Backend.
func (b *liveBackend) Get(ctx context.Context, table row.Table, id ids.AnyID) (store.Row, error) {
	s := b.session(ctx)
	if s == nil || !isLiveKind(s, table) {
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
	k := live.RowKey{Table: string(table), ID: id.String()}
	if f, ok := files[k]; ok {
		lr, err := liveStoreRow(k, f)
		if err != nil {
			return store.Row{}, err
		}
		if berr == nil {
			return newer(lr, branchRow), nil
		}
		return lr, nil
	}
	if berr == nil {
		return branchRow, nil
	}
	if r, ok := b.landedOnDefault(ctx, s, k); ok {
		return r, nil
	}
	return store.Row{}, berr
}

// landedOnDefault is v0.1.17's default-branch read-through (F1, coordinator
// 2026-11-02): on the default branch only, a row that landed on the fetched
// origin/<default> and left live is found before this checkout pulls it.
// Other branches keep "not found" (with live_flags.go's peek hint).
func (b *liveBackend) landedOnDefault(ctx context.Context, s *liveSession, k live.RowKey) (store.Row, bool) {
	branch := s.defaultBranch(ctx)
	head, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}})
	if err != nil || strings.TrimSpace(string(head)) != branch {
		return store.Row{}, false
	}
	raw, ok, err := s.repo.ReadFile(ctx, "refs/remotes/"+s.ref.RemoteName+"/"+branch, mainDataPath(b.folder, k.Table+"/"+k.ID+storeRowFileExt))
	if err != nil || !ok {
		return store.Row{}, false
	}
	r, err := row.Decode(raw)
	if err != nil {
		return store.Row{}, false
	}
	sr, err := store.FromRow(r)
	return sr, err == nil
}

// Read implements store.Backend. While the live ref has not moved since
// cursor, it is the wrapped backend's changes with live copies merged in;
// once it moved, every branch row and every live row is returned (an
// idempotent upsert for the cache), because a row that left live must be
// re-read from the branch.
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
		if !isLiveKind(s, row.Table(k.Table)) {
			continue
		}
		lr, err := liveStoreRow(k, f)
		if err != nil {
			return nil, "", err
		}
		lives[lr.Key()] = lr
	}
	out := make([]store.Row, 0, len(rows)+len(lives))
	for _, r := range rows {
		if lr, ok := lives[r.Key()]; ok {
			out = append(out, newer(lr, r))
			delete(lives, r.Key())
			continue
		}
		out = append(out, r)
	}
	// While the live ref did not move, only live rows whose branch copy
	// changed matter (merged above); the rest are what the cache holds.
	if innerCur == "" {
		for _, lr := range lives {
			out = append(out, lr)
		}
	}
	return out, store.Cursor(liveCursorPrefix + oid + liveCursorSep + string(next)), nil
}

// Write implements store.Backend: every change's Expect is checked against
// the copy a reader sees (live or branch), then each row goes where
// rowroute places it. Branch rows are written first (the wrapped backend is
// all-or-none and refuses a stale batch); the live rows then go to the
// local live ref in one commit.
func (b *liveBackend) Write(ctx context.Context, changes []store.Change) error {
	s := b.session(ctx)
	if s == nil {
		return b.inner.Write(ctx, changes)
	}
	_, files, err := b.liveFiles(ctx, s)
	if err != nil {
		return err
	}
	req := rowroute.Request{Defaults: liveKinds(s)}
	col := liveCollectorFrom(ctx)
	var branch []store.Change
	liveRows := map[live.RowKey]live.File{}
	now := s.clk.Now()
	for _, ch := range changes {
		if !isLiveKind(s, ch.Row.Table) {
			branch = append(branch, ch)
			continue
		}
		k := live.RowKey{Table: string(ch.Row.Table), ID: ch.Row.ID.String()}
		prev, inLive := files[k]
		if col != nil {
			if f, staged := col.rows[k]; staged {
				prev, inLive = f, true
			}
		}
		branchRow, berr := b.inner.Get(ctx, ch.Row.Table, ch.Row.ID)
		if berr != nil && !errors.Is(berr, store.ErrNotFound) {
			return berr
		}
		inBranch := berr == nil
		place := rowroute.Decide(req, rowroute.Row{Table: k.Table, ID: k.ID, InLive: inLive, InBranch: inBranch, Deleted: ch.Row.Deleted}).Place
		if col != nil {
			// A fenced coordination write takes its row live, as v0.1.17's
			// stagedLiveRows did: only the live ref can join the atomic
			// push with the signals ref.
			place = rowroute.PlaceLive
		}
		if place != rowroute.PlaceLive {
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
		if cur.HLC.Compare(ch.Expect) != 0 {
			return errs.Wrap(errs.Stale, "live row "+k.Table+"/"+k.ID+" changed since it was read", store.ErrStale)
		}
		f, ferr := liveFileFor(ctx, ch.Row, prevPtr(prev, inLive), now)
		if ferr != nil {
			return ferr
		}
		liveRows[k] = f
	}
	if len(branch) > 0 {
		if err = b.inner.Write(ctx, branch); err != nil {
			return err
		}
	}
	if len(liveRows) == 0 {
		return nil
	}
	if s.blocked != nil {
		return errs.Wrap(errs.StoreReadOnly, "the remote live ref was rewritten; lore stopped writing to it", s.blocked)
	}
	if col != nil {
		col.s, col.folder = s, b.folder
		maps.Copy(col.rows, liveRows)
		return nil
	}
	res, err := s.ref.Write(ctx, live.WriteRequest{Folder: b.folder, Rows: liveRows, BinaryVersion: version})
	if err != nil {
		if len(branch) > 0 {
			fmt.Fprintln(os.Stderr, style.Error(liveLogPrefix+"rows with the code were written, the live ones were not: "+err.Error()))
		}
		return errs.Wrap(errs.Internal, "live: write the local live ref", err)
	}
	s.wrote += len(res.Written)
	return nil
}

// prevPtr is the live file a new version edits (nil = first live write).
func prevPtr(f live.File, ok bool) *live.File {
	if !ok {
		return nil
	}
	return &f
}

// liveFileFor builds the live file of one core row: its document as the
// body, with a block stamp strictly above the previous live version. A
// tombstone keeps only the envelope (§3.3: no field survives a delete on
// the live tree).
func liveFileFor(ctx context.Context, r store.Row, prev *live.File, now time.Time) (live.File, error) {
	var body map[string]any
	dec := json.NewDecoder(strings.NewReader(string(r.Doc)))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		return live.File{}, fmt.Errorf("live: decode %s/%s: %w", r.Table, r.ID, err)
	}
	canon, err := canonjson.Encode(body)
	if err != nil {
		return live.File{}, err
	}
	doc, err := canonjson.Decode(canon, canonjson.DefaultMaxDepth)
	if err != nil {
		return live.File{}, err
	}
	actor := liveCoreHLCActor(ctx, r)
	var f live.File
	if r.Deleted {
		f = live.NewVersion(prev, envelopeOnly(doc), now, actor)
		f.Block.Deleted = true
		f.Block.Conflict = false
	} else {
		f = live.NewVersion(prev, doc, now, actor)
	}
	// The block stamp is the row's own `_hlc` whenever that is above the
	// version it edits (always, for a row the core keeps live): one stamp
	// per row, so every reader (this backend, wire's pending-live check,
	// v0.1.17's live merge) orders the copies alike.
	if h, err := live.ParseHLC(r.HLC.String()); err == nil && (prev == nil || h.Compare(prev.Block.HLC) > 0) {
		f.Block.HLC = h
	}
	return f, nil
}

// envelopeOnly keeps a tombstone document's envelope keys and id.
func envelopeOnly(doc map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range doc {
		if strings.HasPrefix(k, "_") || k == row.KeyID || k == row.KeyUpdatedAt || k == row.KeyUpdatedBy {
			out[k] = v
		}
	}
	return out
}

// liveCoreHLCActor is the writer named in the live block's stamp: an agent
// never appears there (D8, Q11 — liveHLCActor's rule; it reads no lore.db
// for an agent), a person is the row's updated_by.
func liveCoreHLCActor(ctx context.Context, r store.Row) string {
	who := identityBestEffort(ctx)
	if who.Kind == identityKindAgent || strings.HasPrefix(who.StableKey, agentKeyPrefix) {
		return liveHLCActor(ctx, nil)
	}
	if !r.HLC.Actor.IsZero() {
		return r.HLC.Actor.String()
	}
	return liveAnonymousWriter
}

// Sync implements store.Backend: the repo store has no remote of its own;
// the live ref is pushed by the end-of-command pass.
func (b *liveBackend) Sync(ctx context.Context) (store.SyncReport, error) { return b.inner.Sync(ctx) }

// Capabilities implements store.Backend.
func (b *liveBackend) Capabilities() store.Capabilities { return b.inner.Capabilities() }

// Close implements store.Backend.
func (b *liveBackend) Close() error { return b.inner.Close() }

// Unwrap implements wire.Unwrapper: wire reads the store's marker, branch
// and clone through the concrete backend.
func (b *liveBackend) Unwrap() store.Backend { return b.inner }

// liveCollector gathers the live rows of one fenced coordination write
// (coreLiveCompanion) instead of committing them: they become one staged
// commit pushed atomically with the signals ref, applied only once the
// remote confirmed both (F7, TEAM-036), so a holder that lost its lease
// never publishes its row change.
type liveCollector struct {
	s      *liveSession
	folder string
	rows   map[live.RowKey]live.File
}

// liveCollectorKey carries a *liveCollector in a context.
type liveCollectorKey struct{}

// withLiveCollector makes liveBackend.Write collect into c.
func withLiveCollector(ctx context.Context, c *liveCollector) context.Context {
	return context.WithValue(ctx, liveCollectorKey{}, c)
}

// liveCollectorFrom is the context's collector (nil = commit as usual).
func liveCollectorFrom(ctx context.Context) *liveCollector {
	c, _ := ctx.Value(liveCollectorKey{}).(*liveCollector)
	return c
}
