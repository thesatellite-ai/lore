package wire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/auditfill"
	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// v0.1.17 kept a code repo's open work (open tasks, claims in progress,
// their comments…) on a git ref of its own, the live ref, and landed rows
// into .lore/data only later (on done, on commit). The 0.1.18 core reads
// .lore/data only, so until `lore migrate run` brings those rows in they are
// invisible to it. This file reads the live ref (read-only: it never writes
// or moves a ref) so migrate can bring the rows in and every command can say
// when some are still outside.
//
// Live ref layout (saas/pkg/aicoder/live/layout.go, frozen v0.1.17
// format): <folder>/<table>/<id>.json, folder `_root` for the project at the
// repo top or the project's path; `_live/` holds the ref's own files. Each
// row is a `_v: 1` document plus a `_live` block whose `hlc` is the row's
// last-edit stamp (same text form as the core's `_hlc`).
const (
	// liveRefLocal is the code repo's live ref.
	liveRefLocal = "refs/lore/live"
	// liveRefTracking is its tracking copy after a fetch (v0.1.17 fetches
	// refs/lore/* into refs/lore-remote/<remote>/*; origin is its default).
	liveRefTracking = "refs/lore-remote/origin/live"
	// liveReservedPrefix marks the live ref's own folders (`_live`), except
	// liveRootFolder.
	liveReservedPrefix = "_"
	// liveRootFolder is the folder of the project at the repo top.
	liveRootFolder = "_root"
	// liveBlockKey is the per-row live block; liveHLCKey its stamp.
	liveBlockKey = "_live"
	liveHLCKey   = "hlc"
	// liveRowExt is the row file extension.
	liveRowExt = ".json"
	// liveRowParts is folder/table/id.json; a nested project folder adds
	// segments in front.
	liveRowParts = 3
)

// liveRow is one row of a core-owned table found on the live ref.
type liveRow struct {
	ref  string // the ref it was read from
	path string // its path in the ref's tree
	row  row.Row
}

// liveGit runs one read-only git command in top.
func liveGit(ctx context.Context, top string, args ...string) ([]byte, error) {
	return gitRun{args: append([]string{"-C", top}, args...)}.output(ctx)
}

// liveRefExists reports whether ref names a commit in top.
func liveRefExists(ctx context.Context, top, ref string) bool {
	_, err := liveGit(ctx, top, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// readLiveRows returns the live rows of the tables in want from refs (in
// git dir dir), keeping only rows under folder when folder is not "". A
// row on several refs is merged (logic/merge, no common base: the newer
// side wins per field), so a clone that fetched but never integrated still
// sees the remote's newer edit. A ref that does not exist adds nothing.
func readLiveRows(ctx context.Context, dir string, refs []string, folder string, want map[row.Table]bool) ([]liveRow, error) {
	byKey := map[store.Key]liveRow{}
	for _, ref := range refs {
		if !liveRefExists(ctx, dir, ref) {
			continue
		}
		got, err := readLiveRef(ctx, dir, ref, folder, want)
		if err != nil {
			return nil, err
		}
		for _, lr := range got {
			k := store.Key{Table: lr.row.Table, ID: lr.row.ID}
			prev, seen := byKey[k]
			if !seen {
				byKey[k] = lr
				continue
			}
			m, err := mergeRow(prev.row, lr.row)
			if err != nil {
				return nil, err
			}
			prev.row = m
			byKey[k] = prev
		}
	}
	out := make([]liveRow, 0, len(byKey))
	for _, lr := range byKey {
		out = append(out, lr)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].row.Table != out[j].row.Table {
			return out[i].row.Table < out[j].row.Table
		}
		return out[i].row.ID.String() < out[j].row.ID.String()
	})
	return out, nil
}

// readLiveRef lists one ref's rows of the wanted tables.
func readLiveRef(ctx context.Context, top, ref, folder string, want map[row.Table]bool) ([]liveRow, error) {
	listing, err := liveGit(ctx, top, "ls-tree", "-r", "-z", "--full-tree", ref)
	if err != nil {
		return nil, err
	}
	var out []liveRow
	for _, entry := range bytes.Split(listing, []byte{0}) {
		meta, path, ok := strings.Cut(string(entry), "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta) // mode type oid
		if len(fields) != 3 || fields[1] != "blob" {
			continue
		}
		table, ok := liveRowTable(path)
		if !ok || !want[table] || (folder != "" && liveRowFolder(path) != folder) {
			continue
		}
		b, err := liveGit(ctx, top, "cat-file", "blob", fields[2])
		if err != nil {
			return nil, err
		}
		r, err := decodeLiveRow(b)
		if err != nil {
			return nil, errs.Wrap(errs.CodeOf(err), "wire: live row "+path, err)
		}
		if r.Table != table {
			continue // a row filed under another table's folder: not ours to guess
		}
		out = append(out, liveRow{ref: ref, path: path, row: r})
	}
	return out, nil
}

// liveRowTable is the table of a live-tree row path, ok=false for the ref's
// own files and anything that is not <folder…>/<table>/<id>.json.
func liveRowTable(path string) (row.Table, bool) {
	parts := strings.Split(path, "/")
	if len(parts) < liveRowParts || !strings.HasSuffix(path, liveRowExt) {
		return "", false
	}
	if strings.HasPrefix(parts[0], liveReservedPrefix) && parts[0] != liveRootFolder {
		return "", false
	}
	return row.Table(parts[len(parts)-2]), true
}

// liveRowFolder is the project folder of a live-tree row path (everything
// before <table>/<id>.json).
func liveRowFolder(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) < liveRowParts {
		return ""
	}
	return strings.Join(parts[:len(parts)-2], "/")
}

// decodeLiveRow turns a live document into a row: the `_live` block's hlc
// becomes the row's `_hlc` (the stamp v0.1.17 compared edits by) and the
// block is dropped; the rest is the `_v: 1` document model/row upcasts.
func decodeLiveRow(b []byte) (row.Row, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var doc map[string]json.RawMessage
	if err := dec.Decode(&doc); err != nil {
		return row.Row{}, errs.Wrap(errs.InvalidInput, "wire: live row is not a JSON object", err)
	}
	if raw, ok := doc[liveBlockKey]; ok {
		var block map[string]json.RawMessage
		if err := json.Unmarshal(raw, &block); err == nil {
			if h, ok := block[liveHLCKey]; ok {
				doc[row.KeyHLC] = h
			}
		}
		delete(doc, liveBlockKey)
	}
	clean, err := json.Marshal(doc)
	if err != nil {
		return row.Row{}, errs.Wrap(errs.Internal, "wire: re-encode a live row", err)
	}
	return row.Decode(clean)
}

// mergeRow merges two copies of one row with no common base: the newer
// side wins per field (per-field strategies such as list-by-id still
// apply), a field only one side holds is kept, and a tombstone is never
// undone (merge.Rows; DQ4).
//
// Why no conflict markers here, unlike a git merge: the copies are
// versions of one row along one v0.1.17 history (the live ref held the
// working copy that v0.1.17 later landed into .lore/data; a tracking ref
// is the same ref fetched later), not concurrent edits, and with no base
// every edited text would look like a clash. Markers would also be written
// straight to the backend, past the engine's refusal of marker text, and
// leave a row nobody can re-save without hand-editing it.
func mergeRow(a, b row.Row) (row.Row, error) {
	def, ok := builtinDef(a.Table)
	p := merge.UnknownTable()
	if ok {
		p = merge.PolicyFor(def)
	}
	p.IsText = func(string) bool { return false }
	res, err := merge.Rows(p, merge.Absent, a, b)
	if err != nil {
		return row.Row{}, err
	}
	return res.Row, nil
}

// liveTables is the core-owned tables as a set (the tables a live row can
// be brought in for).
//
// RQ2: resources travel with the branch only and never go to the live ref,
// so a resources file found there is never brought in (liveResourceNote
// names it instead).
func liveTables() map[row.Table]bool {
	out := map[row.Table]bool{}
	for _, t := range CoreTables() {
		out[t] = true
	}
	delete(out, schema.TableResources)
	return out
}

// liveResourceNote warns about resources files on any live ref ("" when
// none or unreadable): they are refused, never imported (RQ2), and the
// paths say which files to remove from the ref.
func liveResourceNote(ctx context.Context, st *state) string {
	var paths []string
	srcs, serr := liveSources(ctx, st)
	if serr != nil {
		return ""
	}
	for _, src := range srcs {
		rows, err := readLiveRows(ctx, src.dir, src.refs, src.folder, map[row.Table]bool{schema.TableResources: true})
		if err != nil {
			return ""
		}
		for _, lr := range rows {
			paths = append(paths, lr.path)
		}
	}
	if len(paths) == 0 {
		return ""
	}
	sort.Strings(paths)
	return "warning: " + strconv.Itoa(len(paths)) + " resources files on the live ref are ignored (resources travel with the branch only, RQ2): " + strings.Join(paths, ", ")
}

// pendingLive is a live row the repo store does not hold yet, or holds an
// older copy of (cur is that copy; zero ID when absent).
type pendingLive struct {
	source store.Source
	live   liveRow
	cur    row.Row
	has    bool
}

// liveSource is one place v0.1.17 kept live rows for an attached store:
// the code repo's own live ref (repo store), or a v0.1.17 store's
// per-branch live ref inside the store's lore clone, limited to the
// attached folder's rows.
type liveSource struct {
	name   store.Source
	dir    string   // where git runs (the repo top, the store worktree)
	refs   []string // local ref and tracking copy
	folder string   // the project's live folder (repoLiveFolder) or the attached store folder
	branch string   // store branch ("" for the repo store)
	v017   bool     // the store's marker is still v0.1.17 (lore-store.yaml v1)
}

// repoLiveFolder is the live folder of the project rooted at dir: `_root`
// for the project at the git top level, else dir's path relative to it (a
// monorepo sub-project, OWNERQ-1). Every project of a repo shares the one
// refs/lore/live, so reading only this folder keeps one project's live rows
// out of another's .lore/data. It is the rule the live writer files rows by
// (wire/host livePlace, v0.1.17 liveRepoPlace), asked of git itself
// (`rev-parse --show-prefix`), so a symlinked path names the same folder.
func repoLiveFolder(ctx context.Context, dir string) (string, error) {
	out, err := liveGit(ctx, dir, "rev-parse", "--show-prefix")
	if err != nil {
		return "", errs.Wrap(errs.CodeOf(err), "wire: the live folder of "+dir, err)
	}
	return live.FolderFor(strings.TrimSpace(string(out))), nil
}

// gitStoreBackend is a git store backend: its worktree and branch.
type gitStoreBackend interface {
	Dir() string
	Branch() string
}

// liveSources lists the live refs of the repo and of every attached git
// store. The repo reads only its project's folder (repoLiveFolder), a
// store attached at its root reads v0.1.17's `_root` folder.
func liveSources(ctx context.Context, st *state) ([]liveSource, error) {
	folder, err := repoLiveFolder(ctx, st.repoTop)
	if err != nil {
		return nil, err
	}
	out := []liveSource{{name: store.SourceRepo, dir: st.repoTop, folder: folder, refs: []string{liveRefLocal, liveRefTracking}}}
	for _, name := range st.eng.Stores() {
		if name == store.SourceRepo {
			continue
		}
		gb, ok := concrete(st.byName[name]).(gitStoreBackend)
		if !ok || gb.Branch() == "" {
			continue
		}
		f := strings.Trim(st.folders[name], "/")
		if f == "" || f == "." {
			f = liveRootFolder
		}
		v017 := false
		if m, ok := concrete(st.byName[name]).(markerOf); ok {
			v017 = m.Marker().Version < rowfs.MarkerVersion
		}
		b := gb.Branch()
		out = append(out, liveSource{name: name, dir: gb.Dir(), folder: f, branch: b, v017: v017,
			refs: []string{liveRefLocal + "/" + b, liveTrackingPrefix + b}})
	}
	return out, nil
}

// liveTrackingPrefix + <branch> is where fetchStoreLive copies a store
// branch's live ref inside the store clone.
const liveTrackingPrefix = "refs/lore-remote/" + git.RemoteName + "/live/"

// fetchStoreLive copies the store's live ref of src.branch from its remote
// into the store clone (liveTrackingPrefix). Best effort: offline, or a
// store with no live ref, leaves what is there.
func fetchStoreLive(ctx context.Context, src liveSource) {
	ref := liveRefLocal + "/" + src.branch
	_, _ = liveGit(ctx, src.dir, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--", git.RemoteName, "+"+ref+":"+liveTrackingPrefix+src.branch)
}

// pendingLiveRows lists the live rows of core-owned tables that their
// store is missing or holds older (by HLC). A store's tombstone is never
// undone by a live row (rows are never resurrected), nor a trashed row
// (deleted_at set) edited. fetch: refresh each
// store's live ref from its remote first (fetchAll: every git store;
// otherwise only stores still marked v0.1.17, so a migrated setup pays
// nothing).
func pendingLiveRows(ctx context.Context, st *state, fetchAll bool) ([]pendingLive, error) {
	var out []pendingLive
	srcs, serr := liveSources(ctx, st)
	if serr != nil {
		return nil, serr
	}
	for _, src := range srcs {
		if src.branch != "" && (fetchAll || src.v017) {
			fetchStoreLive(ctx, src)
		}
		rows, err := readLiveRows(ctx, src.dir, src.refs, src.folder, liveTables())
		if err != nil {
			return nil, err
		}
		b := st.byName[src.name]
		for _, lr := range rows {
			sr, err := b.Get(ctx, lr.row.Table, lr.row.ID)
			if errors.Is(err, store.ErrNotFound) {
				out = append(out, pendingLive{source: src.name, live: lr})
				continue
			}
			if err != nil {
				return nil, err
			}
			if sr.Deleted || sr.HLC.Compare(lr.row.HLC) >= 0 {
				continue
			}
			cur, err := sr.Decode()
			if err != nil {
				return nil, err
			}
			if at, trashed := cur.Fields[auditfill.FieldDeletedAt]; trashed && at != nil && at.String() != "" {
				continue // a trashed row is left in the trash as it is, like a tombstone (never resurrected or edited by an old live copy)
			}
			out = append(out, pendingLive{source: src.name, live: lr, cur: cur, has: true})
		}
	}
	return out, nil
}

// LiveNote is a one-line notice for a repo whose v0.1.17 live ref holds
// rows of core-owned tables the core cannot see yet ("" when none, or when
// the live ref cannot be read: a note must never break a command). The
// surface prints it on stderr before a core command; `lore migrate run`
// brings the rows in and the note goes away.
func (a *App) LiveNote(ctx context.Context) string {
	st, err := a.state(ctx)
	if err != nil {
		return ""
	}
	return liveNote(ctx, st)
}

// LiveOverlay is implemented by a repo-store backend wrapper that serves
// the repo's live ref itself (cmd/lore's wire/host liveBackend): the
// repo's live rows are then 0.1.18 live rows, never rows waiting for
// `lore migrate run`.
type LiveOverlay interface {
	ServesLiveRef() bool
}

// servesLiveRef reports whether b, or any wrapper on the way to its
// concrete backend, is a LiveOverlay that serves the live ref.
func servesLiveRef(b store.Backend) bool {
	for b != nil {
		if o, ok := b.(LiveOverlay); ok && o.ServesLiveRef() {
			return true
		}
		u, ok := b.(Unwrapper)
		if !ok {
			return false
		}
		b = u.Unwrap()
	}
	return false
}

// liveNote is LiveNote for an opened state (App.state calls it while it
// holds the App's lock, so it must not call back into the App).
func liveNote(ctx context.Context, st *state) string {
	warn := liveResourceNote(ctx, st)
	p, err := pendingLiveRows(ctx, st, false)
	if servesLiveRef(st.byName[store.SourceRepo]) {
		// The repo store's live overlay (cmd/lore) shows the repo's live
		// rows to every command that opens a live session; a run without
		// one (a git hook, a skipped-live op such as task heartbeat) only
		// misses them for itself. Not "not visible yet": no note.
		p = slices.DeleteFunc(p, func(x pendingLive) bool { return x.source == store.SourceRepo })
	}
	if err != nil || len(p) == 0 {
		return warn
	}
	counts := map[row.Table]int{}
	for _, x := range p {
		counts[x.live.row.Table]++
	}
	names := make([]string, 0, len(counts))
	for t := range counts {
		names = append(names, t.String())
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, strconv.Itoa(counts[row.Table(n)])+" "+n)
	}
	note := "note: lore 0.1.17 live rows not visible yet (" + strings.Join(parts, ", ") + "); run `" + MigrateCommand + "` to bring them in"
	if warn != "" {
		note += "\n" + warn
	}
	return note
}

// MigrateLive implements opsmigrate.Rewriter: every live-ref row of a
// core-owned table that its store is missing (or holds older) is written
// into that store as a `_v: 2` row with the same id and the live row's
// HLC, merged with the store's copy when there is one (logic/merge, newer
// side wins per field). Sources: the code repo's live ref (repo store) and
// each attached git store's branch live ref, fetched first (only the
// attached folder's rows; a folder no repo here attaches is migrated from
// a repo that does). Written straight to the backend with Expect, like
// RewriteV1: a format move, not an edit, so no new HLC. A store's batch is
// one commit, pushed at once (Sync). Returns "table/id" per row, with
// " (<store>)" for a store row (made, or that would be under dryRun). The
// live refs themselves are left as they are; once their rows are in, the
// store copy is at least as new and LiveNote stays quiet.
func (a *App) MigrateLive(ctx context.Context, dryRun bool) ([]string, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	p, err := pendingLiveRows(ctx, st, true)
	if err != nil || len(p) == 0 {
		return nil, err
	}
	out := make([]string, 0, len(p))
	bySource := map[store.Source][]store.Change{}
	var order []store.Source
	for _, x := range p {
		line := x.live.row.Table.String() + "/" + x.live.row.ID.String()
		if x.source != store.SourceRepo {
			line += " (" + string(x.source) + ")"
		}
		out = append(out, line)
		r := x.live.row
		var expect hlc.HLC
		if x.has {
			if r, err = mergeRow(x.cur, r); err != nil {
				return nil, err
			}
			expect = x.cur.HLC
		}
		// audit_fields (step 9) for a live row, as the branch rewrite does
		// for a v1 row: written here as _v: 2, the audit_fields step (run
		// before this one) would only fill it on the next run (F-MIGRATE-1).
		r, _ = auditfill.Defaults(r)
		sr, err := store.FromRow(r)
		if err != nil {
			return nil, errs.Wrap(errs.CodeOf(err), "migrate: live row "+x.live.path, err)
		}
		if _, seen := bySource[x.source]; !seen {
			order = append(order, x.source)
		}
		bySource[x.source] = append(bySource[x.source], store.Change{Row: sr, Expect: expect})
	}
	if dryRun {
		return out, nil
	}
	for _, name := range order {
		if err := st.byName[name].Write(ctx, bySource[name]); err != nil {
			return nil, errs.Wrap(errs.CodeOf(err), "migrate: live rows into "+string(name), err)
		}
		if name == store.SourceRepo {
			a.wrote(nil)
			if _, err := st.eng.Refresh(ctx, name); err != nil {
				return nil, err
			}
			continue
		}
		if _, err := st.eng.Sync(ctx, name); err != nil {
			return nil, errs.Wrap(errs.CodeOf(err), "migrate: push the live rows of "+string(name), err)
		}
	}
	return out, nil
}

// builtinDef is the built-in definition of table (ok=false when the core
// does not define it).
func builtinDef(table row.Table) (schema.TableDef, bool) {
	for _, d := range schema.Builtin() {
		if d.Name == table {
			return d, true
		}
	}
	return schema.TableDef{}, false
}
