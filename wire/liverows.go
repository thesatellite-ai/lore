package wire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"sort"
	"strconv"
	"strings"

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
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", top}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "wire: git "+args[0]+": "+strings.TrimSpace(stderr.String()), err)
	}
	return out, nil
}

// liveRefExists reports whether ref names a commit in top.
func liveRefExists(ctx context.Context, top, ref string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", top, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return cmd.Run() == nil
}

// readLiveRows returns the live rows of the tables in want, from the local
// live ref and its tracking copy. A row on both refs is merged (logic/merge,
// no common base: the newer side wins per field), so a clone that fetched
// but never integrated still sees the remote's newer edit. No live ref (a
// repo v0.1.17 never shared work in, or not a git repo) is no rows.
func readLiveRows(ctx context.Context, top string, want map[row.Table]bool) ([]liveRow, error) {
	byKey := map[store.Key]liveRow{}
	for _, ref := range []string{liveRefLocal, liveRefTracking} {
		if !liveRefExists(ctx, top, ref) {
			continue
		}
		got, err := readLiveRef(ctx, top, ref, want)
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
func readLiveRef(ctx context.Context, top, ref string, want map[row.Table]bool) ([]liveRow, error) {
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
		if !ok || !want[table] {
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

// mergeRow merges two copies of one row with no common base (the newer side
// wins per field; free text both changed gets conflict markers).
func mergeRow(a, b row.Row) (row.Row, error) {
	def, ok := builtinDef(a.Table)
	p := merge.UnknownTable()
	if ok {
		p = merge.PolicyFor(def)
	}
	res, err := merge.Rows(p, merge.Absent, a, b)
	if err != nil {
		return row.Row{}, err
	}
	return res.Row, nil
}

// liveTables is the core-owned tables as a set (the tables a live row can
// be brought in for).
func liveTables() map[row.Table]bool {
	out := map[row.Table]bool{}
	for _, t := range CoreTables() {
		out[t] = true
	}
	return out
}

// pendingLive is a live row the repo store does not hold yet, or holds an
// older copy of (cur is that copy; zero ID when absent).
type pendingLive struct {
	live liveRow
	cur  row.Row
	has  bool
}

// pendingLiveRows lists the live rows of core-owned tables that the repo
// store is missing or holds older (by HLC). A repo-store tombstone is never
// undone by a live row (rows are never resurrected).
func pendingLiveRows(ctx context.Context, st *state) ([]pendingLive, error) {
	rows, err := readLiveRows(ctx, st.repoTop, liveTables())
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	repo := st.byName[store.SourceRepo]
	if repo == nil {
		return nil, nil
	}
	var out []pendingLive
	for _, lr := range rows {
		sr, err := repo.Get(ctx, lr.row.Table, lr.row.ID)
		if errors.Is(err, store.ErrNotFound) {
			out = append(out, pendingLive{live: lr})
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
		out = append(out, pendingLive{live: lr, cur: cur, has: true})
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

// liveNote is LiveNote for an opened state (App.state calls it while it
// holds the App's lock, so it must not call back into the App).
func liveNote(ctx context.Context, st *state) string {
	p, err := pendingLiveRows(ctx, st)
	if err != nil || len(p) == 0 {
		return ""
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
	return "note: lore 0.1.17 live rows not visible yet (" + strings.Join(parts, ", ") + "); run `" + MigrateCommand + "` to bring them in"
}

// MigrateLive implements opsmigrate.Rewriter: every live-ref row of a
// core-owned table the repo store is missing (or holds older) is written
// into the repo store as a `_v: 2` row with the same id and the live row's
// HLC, merged with the store's copy when there is one (logic/merge, newer
// side wins per field). Written straight to the backend with Expect, like
// RewriteV1: this is a format move, not an edit, so it must not stamp a
// new HLC. Returns "table/id" per row (made, or that would be under dryRun).
// The live ref itself is left as it is (read-only here); once its rows are
// in, the repo store's copy is at least as new and LiveNote stays quiet.
func (a *App) MigrateLive(ctx context.Context, dryRun bool) ([]string, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	p, err := pendingLiveRows(ctx, st)
	if err != nil || len(p) == 0 {
		return nil, err
	}
	out := make([]string, 0, len(p))
	changes := make([]store.Change, 0, len(p))
	for _, x := range p {
		out = append(out, x.live.row.Table.String()+"/"+x.live.row.ID.String())
		r := x.live.row
		var expect hlc.HLC
		if x.has {
			if r, err = mergeRow(x.cur, r); err != nil {
				return nil, err
			}
			expect = x.cur.HLC
		}
		sr, err := store.FromRow(r)
		if err != nil {
			return nil, errs.Wrap(errs.CodeOf(err), "migrate: live row "+x.live.path, err)
		}
		changes = append(changes, store.Change{Row: sr, Expect: expect})
	}
	if dryRun {
		return out, nil
	}
	if err := st.byName[store.SourceRepo].Write(ctx, changes); err != nil {
		return nil, err
	}
	a.wrote(nil)
	if _, err := st.eng.Refresh(ctx, store.SourceRepo); err != nil {
		return nil, err
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
