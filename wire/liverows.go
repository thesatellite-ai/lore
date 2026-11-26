package wire

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/auditfill"
	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
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
	// oid is the blob id the row was read from: two refs holding the same
	// blob hold the same row, so readLiveRows merges nothing for it (T-12).
	oid string
	row row.Row
	// writer is the bare writer name the live block's hlc carried in
	// place of an act_ id ("agent"), "" when it named an actor; the row's
	// HLC then names LiveWriterActor(writer) instead (LiveStamp).
	writer string
}

// liveWriterKey prefixes the stable key a bare live-hlc writer name maps
// to (identity.ActorID): the same name gives the same act_ id on every
// machine, so two clones migrating one row write the same _hlc.
const liveWriterKey = "lore-live-writer:"

// LiveWriterActor is the act_ id a live block's bare writer name stands
// for in a row's _hlc (LiveStamp).
func LiveWriterActor(writer string) (ids.ID[ids.Actor], error) {
	return identity.ActorID(liveWriterKey + writer)
}

// LiveStamp reads the hlc of a live block as the core's HLC. The live
// format keeps any writer text in its third part: v0.1.17 and this
// core's live writer stamp an agent's edit as the bare word "agent" (D8:
// an agent is never named in a live hlc; the stamp only breaks ties),
// which is no act_ id, so the core's own parser refuses it ("hlc: …:
// actor"). Such a writer is mapped to the stable LiveWriterActor id and
// returned in writer so the caller can report it; an act_ writer, or
// none, is kept as it is. A wall time or counter that does not parse is
// still an error.
func LiveStamp(s string) (h hlc.HLC, writer string, err error) {
	if h, err = hlc.Parse(s); err == nil {
		return h, "", nil
	}
	lh, lerr := live.ParseHLC(s)
	if lerr != nil {
		return hlc.HLC{}, "", err // the core parser's reason: wall, counter or form
	}
	if lh.Actor == "" || strings.HasPrefix(lh.Actor, string(ids.PrefixActor)+"_") {
		return hlc.HLC{}, "", err // none, or a damaged act_ id: the failure is the form, never guessed
	}
	actor, aerr := LiveWriterActor(lh.Actor)
	if aerr != nil {
		return hlc.HLC{}, "", aerr
	}
	h = hlc.HLC{Wall: lh.Wall, Counter: lh.Counter, Actor: actor}
	if _, perr := hlc.Parse(h.String()); perr != nil {
		return hlc.HLC{}, "", err
	}
	return h, lh.Actor, nil
}

// liveGit runs one read-only git command in top.
func liveGit(ctx context.Context, top string, args ...string) ([]byte, error) {
	return gitRun{args: append([]string{"-C", top}, args...)}.output(ctx)
}

// liveRefExists reports whether ref names a commit in top.
func liveRefExists(ctx context.Context, top, ref string) bool {
	_, ok := liveRefCommit(ctx, top, ref)
	return ok
}

// liveRefCommit is the commit ref names in top (ok=false when it names none).
func liveRefCommit(ctx context.Context, top, ref string) (string, bool) {
	out, err := liveGit(ctx, top, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// readLiveRows returns the live rows of the tables in want from refs (in
// git dir dir), keeping only rows under folder when folder is not "". A
// ref that does not exist adds nothing.
//
// D-737: the result is the refs' current rows, as the live ref resolves
// them. A ref and its tracking copy (the usual pair: refs/lore/live and
// refs/lore-remote/origin/live) are merged three-way over their merge base
// with live.MergeTrees, the merge `lore sync` integrates them with: a row
// taken off one side since the base stays off, a trash or a tombstone is
// never overruled by the other side's stale copy, and an id either side
// lists on _live/purged.json (`lore live purge`, `lore trash purge`) is
// gone except its tombstone. Before, the pair was merged with no base and
// purged.json unread: until the next push the tracking copy handed back
// every row the owner had deleted, trashed or purged, and `lore store
// adopt` counted and copied them until `lore live compact`. Refs that are
// not such a pair (one ref, or more) merge with no base (logic/merge: the
// newer side wins per field, a tombstone is never undone) after the same
// purge rule.
//
// oldOnly keeps only rows older than the current format (the ones a
// migration brings in); a current-format row is then recognised from its
// bytes and never decoded (liveRowCurrent).
func readLiveRows(ctx context.Context, dir string, refs []string, folder string, want map[row.Table]bool, oldOnly bool) ([]liveRow, error) {
	var present, commits []string
	read := map[string]bool{} // commits already read: a tracking ref at the local ref's commit adds nothing (T-12)
	for _, ref := range refs {
		commit, ok := liveRefCommit(ctx, dir, ref)
		if !ok || read[commit] {
			continue
		}
		read[commit] = true
		present, commits = append(present, ref), append(commits, commit)
	}
	var got []liveRow
	var purged map[string]bool // set by either reader below
	var err error
	if len(present) == 2 {
		got, purged, err = readLivePair(ctx, dir, present, commits, folder, want, oldOnly)
	} else {
		got, purged, err = readLiveNoBase(ctx, dir, present, folder, want, oldOnly)
	}
	if err != nil {
		return nil, err
	}
	out := make([]liveRow, 0, len(got))
	for _, lr := range got {
		if purged[lr.row.ID.String()] && !lr.row.Deleted {
			continue // purged: gone from every ref, whichever still held it (live.dropsPurged's rule)
		}
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

// readLiveNoBase reads refs one by one and merges a row several hold with
// no common base (the newer side wins per field), with the union of their
// purged ids.
func readLiveNoBase(ctx context.Context, dir string, refs []string, folder string, want map[row.Table]bool, oldOnly bool) ([]liveRow, map[string]bool, error) {
	byKey := map[store.Key]liveRow{}
	purged := map[string]bool{}
	for _, ref := range refs {
		got, gone, err := readLiveRef(ctx, dir, ref, folder, want, oldOnly)
		if err != nil {
			return nil, nil, err
		}
		for id := range gone {
			purged[id] = true
		}
		for _, lr := range got {
			k := store.Key{Table: lr.row.Table, ID: lr.row.ID}
			prev, seen := byKey[k]
			if !seen {
				byKey[k] = lr
				continue
			}
			if prev.oid == lr.oid {
				continue // the same blob on both refs: the same row, nothing to merge (T-12)
			}
			m, err := mergeRow(prev.row, lr.row)
			if err != nil {
				return nil, nil, err
			}
			// The merge is built in memory (SourceVersion 0); it keeps the
			// oldest format of its copies, so a current-format row stays
			// recognisable (MigrateLive) and a v1 copy still counts as old.
			m.SourceVersion = min(prev.row.SourceVersion, lr.row.SourceVersion)
			prev.row = m
			byKey[k] = prev
		}
	}
	out := make([]liveRow, 0, len(byKey))
	for _, lr := range byKey {
		out = append(out, lr)
	}
	return out, purged, nil
}

// liveEntry is one wanted file of a live tree listing: its blob and, for a
// row file, its table.
type liveEntry struct {
	oid   string
	table row.Table
}

// listLiveTree lists rev's wanted row files (tables in want, under folder
// when not "") by path, and the blob of its _live/purged.json ("" when
// none). rev "" lists nothing.
func listLiveTree(ctx context.Context, top, rev, folder string, want map[row.Table]bool) (map[string]liveEntry, string, error) {
	out := map[string]liveEntry{}
	if rev == "" {
		return out, "", nil
	}
	listing, err := liveGit(ctx, top, "ls-tree", "-r", "-z", "--full-tree", rev)
	if err != nil {
		return nil, "", err
	}
	purgedOID := ""
	for _, entry := range bytes.Split(listing, []byte{0}) {
		meta, path, ok := strings.Cut(string(entry), "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta) // mode type oid
		if len(fields) != 3 || fields[1] != "blob" {
			continue
		}
		if path == live.PurgedPath {
			purgedOID = fields[2]
			continue
		}
		table, ok := liveRowTable(path)
		if !ok || !want[table] || (folder != "" && liveRowFolder(path) != folder) {
			continue
		}
		out[path] = liveEntry{oid: fields[2], table: table}
	}
	return out, purgedOID, nil
}

// readLiveRef lists one ref's rows of the wanted tables, and the ids its
// _live/purged.json lists (purged; readLiveRows drops them from every ref).
func readLiveRef(ctx context.Context, top, ref, folder string, want map[row.Table]bool, oldOnly bool) ([]liveRow, map[string]bool, error) {
	entries, purgedOID, err := listLiveTree(ctx, top, ref, folder, want)
	if err != nil {
		return nil, nil, err
	}
	paths := slices.Sorted(maps.Keys(entries))
	// One git cat-file --batch for every row (B1-16: a git process per
	// live row made each command start hundreds of them in a big repo), by
	// blob id: "<ref>:<path>" rescanned the folder per row (T-12).
	oids := make([]string, len(paths), len(paths)+1)
	for i, p := range paths {
		oids[i] = entries[p].oid
	}
	if purgedOID != "" {
		oids = append(oids, purgedOID) // read in the same batch, last
	}
	blobs, err := catFileBatch(ctx, top, nil, oids)
	if err != nil {
		return nil, nil, err
	}
	purged := map[string]bool{}
	if purgedOID != "" {
		if purged, err = livePurgedSet(blobs[len(paths)], ref); err != nil {
			return nil, nil, err
		}
	}
	var out []liveRow
	for i, p := range paths {
		if blobs[i] == nil {
			return nil, nil, errs.New(errs.Internal, "wire: live row "+p+" could not be read from "+ref)
		}
		lr, keep, err := decodeLiveEntry(ref, p, entries[p].oid, entries[p].table, blobs[i], oldOnly)
		if err != nil {
			return nil, nil, err
		}
		if keep {
			out = append(out, lr)
		}
	}
	return out, purged, nil
}

// livePurgedSet decodes a _live/purged.json read from ref.
func livePurgedSet(b []byte, ref string) (map[string]bool, error) {
	set, err := (live.Tree{live.PurgedPath: b}).Purged()
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "wire: "+live.PurgedPath+" on "+ref, err)
	}
	return set, nil
}

// decodeLiveEntry decodes the live file b at path (blob oid, "" when
// merged in memory) as a row of table; keep false when oldOnly skips it or
// it is filed under another table's folder.
func decodeLiveEntry(ref, path, oid string, table row.Table, b []byte, oldOnly bool) (liveRow, bool, error) {
	if oldOnly && liveRowCurrent(b) {
		return liveRow{}, false, nil
	}
	r, writer, err := decodeLiveRow(b)
	if err != nil {
		return liveRow{}, false, errs.Prefix("wire: live row "+path, err)
	}
	if r.Table != table {
		return liveRow{}, false, nil // a row filed under another table's folder: not ours to guess
	}
	if oldOnly && r.SourceVersion >= row.FormatVersion {
		return liveRow{}, false, nil
	}
	return liveRow{ref: ref, path: path, oid: oid, row: r, writer: writer}, true, nil
}

// readLivePair resolves a live ref and its tracking copy (refs[0], refs[1]
// at commits) as `lore sync` would integrate them, over the pair's merge
// base (none when unrelated): a row file both hold as one blob is read
// once; a file only one side changed since the base stands as that side
// has it (its removal too); a file both changed goes through
// live.MergeTrees. Only the files that differ are read beyond the shared
// ones (T-20: a big repo whose sides agree pays one listing per ref). It
// returns the resolved rows and the union of both sides' purged ids.
func readLivePair(ctx context.Context, dir string, refs, commits []string, folder string, want map[row.Table]bool, oldOnly bool) ([]liveRow, map[string]bool, error) {
	base := ""
	if b, err := liveGit(ctx, dir, "merge-base", commits[0], commits[1]); err == nil {
		base = strings.TrimSpace(string(b)) // unrelated (a rewritten remote): no base, every row is an add
	}
	ours, oursPurged, err := listLiveTree(ctx, dir, commits[0], folder, want)
	if err != nil {
		return nil, nil, err
	}
	theirs, theirsPurged, err := listLiveTree(ctx, dir, commits[1], folder, want)
	if err != nil {
		return nil, nil, err
	}
	same := map[string]liveEntry{} // both sides hold the blob: no merge
	var differ []string
	for _, p := range slices.Sorted(maps.Keys(ours)) {
		if t, ok := theirs[p]; ok && t.oid == ours[p].oid {
			same[p] = ours[p]
			continue
		}
		differ = append(differ, p)
	}
	for _, p := range slices.Sorted(maps.Keys(theirs)) {
		if _, ok := ours[p]; !ok {
			differ = append(differ, p)
		}
	}
	baseEntries := map[string]liveEntry{}
	if len(differ) > 0 {
		if baseEntries, _, err = listLiveTree(ctx, dir, base, folder, want); err != nil {
			return nil, nil, err
		}
	}
	// The trivial three-way cases take a side as it is (one side kept
	// the base's blob: the other side's file, or its absence, stands);
	// only a file both sides changed goes through live.MergeTrees.
	taken := map[string]liveEntry{} // path → the side's entry that stands
	takenRef := map[string]string{}
	var both []string
	for _, p := range differ {
		o, t, b := ours[p], theirs[p], baseEntries[p]
		switch {
		case o.oid == b.oid: // ours unchanged since the base (or absent from both)
			if t.oid != "" {
				taken[p], takenRef[p] = t, refs[1]
			}
		case t.oid == b.oid:
			if o.oid != "" {
				taken[p], takenRef[p] = o, refs[0]
			}
		default:
			both = append(both, p)
		}
	}
	// one batch: the rows that stand as they are, each side's files of the
	// rows both changed, and both purged lists
	var oids []string
	at := map[string]int{} // oid → index in oids (a blob is read once)
	need := func(oid string) {
		if oid == "" {
			return
		}
		if _, ok := at[oid]; !ok {
			at[oid] = len(oids)
			oids = append(oids, oid)
		}
	}
	for _, e := range same {
		need(e.oid)
	}
	for _, e := range taken {
		need(e.oid)
	}
	for _, p := range both {
		need(ours[p].oid)
		need(theirs[p].oid)
		need(baseEntries[p].oid)
	}
	need(oursPurged)
	need(theirsPurged)
	blobs, err := catFileBatch(ctx, dir, nil, oids)
	if err != nil {
		return nil, nil, err
	}
	blob := func(oid string) ([]byte, error) {
		b := blobs[at[oid]]
		if b == nil {
			return nil, errs.New(errs.Internal, "wire: live blob "+oid+" could not be read from "+refs[0]+" or "+refs[1])
		}
		return b, nil
	}
	purged := map[string]bool{} // set union: a purge on either side holds (live.MergeTrees' rule)
	for _, po := range []struct{ oid, ref string }{{oursPurged, refs[0]}, {theirsPurged, refs[1]}} {
		if po.oid == "" {
			continue
		}
		b, err := blob(po.oid)
		if err != nil {
			return nil, nil, err
		}
		set, err := livePurgedSet(b, po.ref)
		if err != nil {
			return nil, nil, err
		}
		for id := range set {
			purged[id] = true
		}
	}
	var out []liveRow
	add := func(ref, p, oid string, table row.Table, b []byte) error {
		lr, keep, err := decodeLiveEntry(ref, p, oid, table, b, oldOnly)
		if err != nil {
			return err
		}
		if keep {
			out = append(out, lr)
		}
		return nil
	}
	for _, p := range slices.Sorted(maps.Keys(same)) {
		b, err := blob(same[p].oid)
		if err != nil {
			return nil, nil, err
		}
		if err := add(refs[0], p, same[p].oid, same[p].table, b); err != nil {
			return nil, nil, err
		}
	}
	for _, p := range slices.Sorted(maps.Keys(taken)) {
		b, err := blob(taken[p].oid)
		if err != nil {
			return nil, nil, err
		}
		if err := add(takenRef[p], p, taken[p].oid, taken[p].table, b); err != nil {
			return nil, nil, err
		}
	}
	if len(both) == 0 {
		return out, purged, nil
	}
	tree := func(entries map[string]liveEntry) (live.Tree, error) {
		t := live.Tree{}
		for _, p := range both {
			if e, ok := entries[p]; ok {
				b, err := blob(e.oid)
				if err != nil {
					return nil, err
				}
				t[p] = b
			}
		}
		return t, nil
	}
	bt, err := tree(baseEntries)
	if err != nil {
		return nil, nil, err
	}
	ot, err := tree(ours)
	if err != nil {
		return nil, nil, err
	}
	tt, err := tree(theirs)
	if err != nil {
		return nil, nil, err
	}
	merged, _, err := live.MergeTrees(bt, ot, tt, nil)
	if err != nil {
		return nil, nil, errs.Prefix("wire: resolve "+refs[0]+" with "+refs[1], err)
	}
	for _, p := range both {
		b, ok := merged[p]
		if !ok {
			continue // deleted on one side, unchanged on the other's terms (merge3's file rule)
		}
		table := cmp.Or(ours[p].table, theirs[p].table)
		if err := add(refs[0], p, "", table, b); err != nil {
			return nil, nil, err
		}
	}
	return out, purged, nil
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

// liveTopVersion is how lore writes a row's format at the top level of a
// live document (two-space indented JSON, the `_v` key on its own line).
var liveTopVersion = []byte("\n  \"" + row.KeyVersion + "\": ")

// liveRowCurrent reports whether live document b is surely in the current
// row format, read from its bytes without decoding it: the top-level `_v`
// line names row.FormatVersion or newer. Anything else (another layout, a
// v0.1.17 document) is false, and the caller decodes the row to know. A
// repo with 5,000 open tasks decoded every one on each command only to
// find none was old (T-20).
func liveRowCurrent(b []byte) bool {
	i := bytes.Index(b, liveTopVersion)
	if i < 0 {
		return false
	}
	rest := b[i+len(liveTopVersion):]
	n := 0
	for n < len(rest) && rest[n] >= '0' && rest[n] <= '9' {
		n++
	}
	if n == 0 || n > 4 || (n < len(rest) && rest[n] != ',' && rest[n] != '\n') {
		return false
	}
	v, err := strconv.Atoi(string(rest[:n]))
	return err == nil && v >= row.FormatVersion
}

// decodeLiveRow turns a live document into a row: the `_live` block's hlc
// becomes the row's `_hlc` (the stamp v0.1.17 compared edits by; a bare
// writer name such as "agent" read through LiveStamp, and returned) and
// the block is dropped; the rest is the `_v: 1` document model/row
// upcasts.
func decodeLiveRow(b []byte) (row.Row, string, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var doc map[string]json.RawMessage
	if err := dec.Decode(&doc); err != nil {
		return row.Row{}, "", errs.Wrap(errs.InvalidInput, "wire: live row is not a JSON object", err)
	}
	writer := ""
	if raw, ok := doc[liveBlockKey]; ok {
		var block map[string]json.RawMessage
		if err := json.Unmarshal(raw, &block); err == nil {
			if h, ok := block[liveHLCKey]; ok {
				doc[row.KeyHLC] = h
				var text string
				if json.Unmarshal(h, &text) == nil {
					stamp, w, serr := LiveStamp(text)
					if serr != nil {
						return row.Row{}, "", serr
					}
					if w != "" {
						writer = w
						doc[row.KeyHLC] = canonString(stamp.String())
					}
				}
			}
		}
		delete(doc, liveBlockKey)
	}
	clean, err := json.Marshal(doc)
	if err != nil {
		return row.Row{}, "", errs.Wrap(errs.Internal, "wire: re-encode a live row", err)
	}
	r, err := row.Decode(clean)
	return r, writer, err
}

// canonString is s as a JSON string.
func canonString(s string) json.RawMessage {
	b, _ := json.Marshal(s) // a Go string always encodes
	return b
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
		rows, err := readLiveRows(ctx, src.dir, src.refs, src.folder, map[row.Table]bool{schema.TableResources: true}, false)
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
	// moved is the step line of a repo live row a newer lore moved to
	// another store (migrate_moved.go): source is then that store, and
	// leftOut says the live copy is older and nothing is written.
	moved   string
	leftOut bool
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
		return "", errs.Prefix("wire: the live folder of "+dir, err)
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
			v017 = m.Marker().Version < rowfs.MarkerFirstStores
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
	if gitenv.NoStoreFetch(os.Environ()) {
		return // the --all parent fetched every store clone's live refs just now (git.RefreshClone)
	}
	ref := liveRefLocal + "/" + src.branch
	args := git.FetchArgsIn(src.dir, []string{"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--", git.RemoteName, "+" + ref + ":" + liveTrackingPrefix + src.branch})
	_, _ = liveGit(ctx, src.dir, args...)
}

// openFetched reports whether b's first open already fetched its live refs
// inside ctx's git.FetchScope (the startup schema update), so
// fetchStoreLive would only open a second connection for the same refs.
func openFetched(ctx context.Context, b store.Backend) bool {
	f, ok := concrete(b).(interface{ OpenFetchFresh(context.Context) bool })
	return ok && f.OpenFetchFresh(ctx)
}

// pendingLiveRows lists the live rows of core-owned tables that their
// store is missing or holds older (by HLC). A store's tombstone is never
// undone by a live row (rows are never resurrected), nor a trashed row
// (deleted_at set) edited. fetch: refresh each
// store's live ref from its remote first (fetchAll: every git store;
// otherwise only stores still marked v0.1.17, so a migrated setup pays
// nothing).
func pendingLiveRows(ctx context.Context, st *state, fetchAll bool) ([]pendingLive, error) {
	return pendingLiveRowsOf(ctx, st, fetchAll, false)
}

// pendingLiveRowsOf is pendingLiveRows, without the repo's own live ref when
// skipRepo (liveNote: a list nobody would show).
func pendingLiveRowsOf(ctx context.Context, st *state, fetchAll, skipRepo bool) ([]pendingLive, error) {
	return pendingLiveRowsWhere(ctx, st, fetchAll, skipRepo, nil)
}

// pendingOldLiveRows is pendingLiveRows for the live rows older than the
// current row format only: the ones `lore migrate status` counts (D-217).
// A current-format live row is never looked up in its store, so a repo
// with 2,400 open live tasks no longer pays a store read per row on every
// lore doctor and lore fix.
func pendingOldLiveRows(ctx context.Context, st *state) ([]pendingLive, error) {
	return pendingLiveRowsWhere(ctx, st, false, false, func(store.Source) bool { return true })
}

// pendingLiveRowsWhere is pendingLiveRowsOf that, for each source oldOnly
// names (nil: none), keeps only the live rows older than the current
// format, filtered before any decode or store lookup.
func pendingLiveRowsWhere(ctx context.Context, st *state, fetchAll, skipRepo bool, oldOnly func(store.Source) bool) ([]pendingLive, error) {
	var out []pendingLive
	srcs, serr := liveSources(ctx, st)
	if serr != nil {
		return nil, serr
	}
	for _, src := range srcs {
		if skipRepo && src.name == store.SourceRepo {
			continue
		}
		if src.branch != "" && (fetchAll || src.v017) && !openFetched(ctx, st.byName[src.name]) {
			fetchStoreLive(ctx, src)
		}
		rows, err := readLiveRows(ctx, src.dir, src.refs, src.folder, liveTables(), oldOnly != nil && oldOnly(src.name))
		if err != nil {
			return nil, err
		}
		b := st.byName[src.name]
		for _, lr := range rows {
			sr, err := b.Get(ctx, lr.row.Table, lr.row.ID)
			if errors.Is(err, store.ErrNotFound) {
				if src.name == store.SourceRepo {
					if own, ok := movedOwnerOf(ctx, st, lr.row.Table, lr.row.ID); ok {
						// B1-5: moved away by a newer lore; never a second copy here
						newer := lr.row.HLC.Compare(own.cur.HLC) > 0
						out = append(out, pendingLive{source: own.src, live: lr, cur: own.cur, has: true, leftOut: !newer,
							moved: movedLiveLine(lr.row.Table, lr.row.ID, own.src, newer)})
						continue
					}
				}
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

// hasNonRepoStore reports whether a store other than the repo's is
// attached (where movedOwnerOf looks for a moved row).
func hasNonRepoStore(st *state) bool {
	for _, name := range st.eng.Stores() {
		if name != store.SourceRepo && st.byName[name] != nil {
			return true
		}
	}
	return false
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
	serves := servesLiveRef(st.byName[store.SourceRepo])
	// The repo's live rows are dropped below when its store serves them,
	// all but those moved to another store (movedOwnerOf); with no other
	// store attached none can be, so they are not read at all: decoding the
	// whole live ref a second time was a third of every agent command under
	// the live lock (D-159).
	//
	// With a store that serves the live ref, only the repo's rows older than
	// the current format can be a 0.1.17 copy a newer lore moved away: a
	// current-format live row is this lore's own (MigrateLive leaves it out
	// for the same reason). Looking every open task up in every attached
	// store (a git process each) made whoami and every write grow with the
	// open tasks: 10-30 s at 5,000 (T-12, T-20).
	oldOnly := func(src store.Source) bool { return serves && src == store.SourceRepo }
	p, err := pendingLiveRowsWhere(ctx, st, false, serves && !hasNonRepoStore(st), oldOnly)
	p = slices.DeleteFunc(p, func(x pendingLive) bool { return x.leftOut }) // nothing to bring in (migrate_moved.go)
	if serves {
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
	if servesLiveRef(st.byName[store.SourceRepo]) {
		// A current-format live row of the code repo is 0.1.18's own:
		// the live overlay serves it to every command with a live session
		// and it reaches .lore/data only by landing. migrate run opens no
		// session, so the repo store looked "missing" it and the run (and
		// its dry run) copied every open live row into .lore/data as an
		// "upgrade" of a row that was never old. Only rows older than
		// _v: 2 (v0.1.17's live ref) are brought in.
		p = slices.DeleteFunc(p, func(x pendingLive) bool {
			return x.source == store.SourceRepo && x.live.row.SourceVersion >= row.FormatVersion
		})
		if len(p) == 0 {
			return nil, nil
		}
	}
	out := make([]string, 0, len(p))
	bySource := map[store.Source][]store.Change{}
	var order []store.Source
	for _, x := range p {
		line := x.live.row.Table.String() + "/" + x.live.row.ID.String()
		if x.source != store.SourceRepo {
			line += " (" + string(x.source) + ")"
		}
		if x.live.writer != "" {
			// the migration report names the mapping (LiveStamp), so a
			// reader of the row's _hlc knows where the actor came from
			line += " (live hlc writer " + strconv.Quote(x.live.writer) + " recorded as " + x.live.row.HLC.Actor.String() + ")"
		}
		if x.moved != "" {
			line = x.moved // a repo live row a newer lore moved away (migrate_moved.go)
		}
		out = append(out, line)
		if x.leftOut {
			continue // the moved copy is newer: nothing to write
		}
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
			return nil, errs.Prefix("migrate: live row "+x.live.path, err)
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
			return nil, errs.Prefix("migrate: live rows into "+string(name), err)
		}
		if name == store.SourceRepo {
			a.wrote(nil)
			if _, err := st.eng.Refresh(ctx, name); err != nil {
				return nil, err
			}
			continue
		}
		if _, err := st.eng.Sync(ctx, name); err != nil {
			return nil, errs.Prefix("migrate: push the live rows of "+string(name), err)
		}
	}
	return out, nil
}

// builtinDef is the built-in definition of table (ok=false when the core
// does not define it).
func builtinDef(table row.Table) (schema.TableDef, bool) {
	d, ok := builtinDefs()[table]
	return d, ok
}

// builtinDefs is schema.Builtin by table name, built once per process:
// mergeRow runs per live row and rebuilding every definition each time was
// a tenth of a big repo's command (T-20).
var builtinDefs = sync.OnceValue(func() map[row.Table]schema.TableDef {
	defs := schema.Builtin()
	m := make(map[row.Table]schema.TableDef, len(defs))
	for _, d := range defs {
		m[d.Name] = d
	}
	return m
})
