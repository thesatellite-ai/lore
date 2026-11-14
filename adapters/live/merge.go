package live

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/logic/merge3"

	rowmerge "github.com/thesatellite-ai/lore/logic/merge"
	lorerow "github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// StrategySource returns the field strategy for rows of a table (lsync's
// StrategyFor). nil, or a nil result for a table, means every field is a
// loud conflict when both sides change it.
type StrategySource func(table string) func(key string) merge3.Strategy

// MergeNote records one file the merge had to decide (both sides changed
// it), so commands can say what happened ("deleted by dev2 but edited by
// dev1; kept") and `lore explain --merge` can show it.
type MergeNote struct {
	Path      string
	Key       RowKey
	Folder    string
	Decision  merge3.FileDecision
	DeletedBy merge3.Side
	// Conflict is true when a text field kept both edits with markers.
	Conflict bool
	// ConflictKeys names the clashing fields.
	ConflictKeys []string
	// OursHLC and TheirsHLC are the two stamps compared.
	OursHLC, TheirsHLC HLC
	// Trash names the soft-delete rule that settled a v2 row (deleted on
	// one side, edited on the other: kept alive), "" when none applied.
	Trash rowmerge.TrashOutcome
	// Replaced lists the fields both sides changed where the newer edit
	// (by HLC) was kept and the other one replaced, sorted by field. A
	// live merge never writes conflict markers (L-19): the live ref is
	// read by every clone, so markers there broke everyone's lists and
	// failed the sync of people who never touched the row.
	Replaced []ReplacedField
}

// ReplacedField is one field a live merge settled by keeping the newer
// edit: Older is the replaced value as text (a string as itself, anything
// else as compact JSON), so the sync line can show what was dropped.
type ReplacedField struct {
	Field string
	Older string
}

// MergeTrees three-way merges two live trees (F4): base is their merge
// base (empty when unrelated), ours the local tree, theirs the remote one.
//
// Rules per path:
//   - row files: merge3.MergeFile on the bodies (file level first: add,
//     delete, modify-beats-delete; then field level with the hlc-newer side
//     winning every clash, text too, never markers (L-19); the older
//     value is named in the note); the `_live`
//     blocks combine (earliest live_since, highest hlc, latest
//     last_copy_at);
//   - _live/epoch: the higher number;
//   - _live/purged.json: set union, and every purged id is removed from the
//     result so a stale side can never bring it back (its tombstone stays,
//     dropsPurged);
//   - _live/meta.json: features as a set and min_lore_version the higher
//     version when both sides hold one (FORMAT_EVOLUTION rule 2), so a
//     raised floor is never lost to a side that did not raise it;
//   - any other file: the side that changed it, theirs when both did.
//
// The result is a whole tree; notes list every row both sides changed.
func MergeTrees(base, ours, theirs Tree, strategies StrategySource) (Tree, []MergeNote, error) {
	// v2 row bodies (the 0.1.18 core's `_v: 2` documents) merge through
	// logic/merge, the one row merge every backend uses (STORE_SERVER_SPEC
	// 11 rule 5): ordered by the row's _hlc, envelope keys (_hlc, updated_*)
	// settled by the row rule, per-field strategies from the schema, soft
	// deletes by its trash rules. A registry that fails to build only
	// costs the per-table policy (UnknownTable keeps clashes loud).
	reg, regErr := schema.NewRegistry(schema.Builtin()...)
	if regErr != nil {
		reg = nil
	}
	out := Tree{}
	var notes []MergeNote
	for _, p := range unionPaths(base, ours, theirs) {
		b, inB := base[p]
		o, inO := ours[p]
		t, inT := theirs[p]
		switch p {
		case EpochPath:
			out[p] = encodeEpoch(max(Tree{p: o}.Epoch(), Tree{p: t}.Epoch()))
			continue
		case PurgedPath:
			continue // unioned below
		case MetaPath:
			if inO && inT && string(o) != string(t) {
				// An unreadable side falls back to the plain rule below: the
				// write gate (checkFloor) refuses on it anyway, and a read
				// (fetch, integrate) must not fail on a hand-mangled file.
				if m, err := mergeMetaFiles(o, t); err == nil {
					out[p] = m
					continue
				}
			}
		}
		folder, key, isRow := ParseRowPath(p)
		if !isRow {
			if v, ok := pickPlain(b, inB, o, inO, t, inT); ok {
				out[p] = v
			}
			continue
		}
		merged, note, keep, err := mergeRow(p, b, inB, o, inO, t, inT, strategies, reg, key.Table)
		if err != nil {
			return nil, nil, err
		}
		if note != nil {
			note.Folder, note.Key = folder, key
			notes = append(notes, *note)
		}
		if keep {
			out[p] = merged
		}
	}
	purged, err := unionPurged(ours, theirs)
	if err != nil {
		return nil, nil, err
	}
	if len(purged) > 0 {
		b, err := encodePurged(purged)
		if err != nil {
			return nil, nil, err
		}
		out[PurgedPath] = b
		dropPurged(out, purged)
	}
	return out, notes, nil
}

// unionPaths lists every path present on any side, sorted (deterministic
// output and notes).
func unionPaths(trees ...Tree) []string {
	set := map[string]bool{}
	for _, t := range trees {
		for p := range t {
			set[p] = true
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// pickPlain settles a non-row file by "whoever changed it", theirs when
// both did. ok=false means the file is absent from the result.
func pickPlain(b []byte, inB bool, o []byte, inO bool, t []byte, inT bool) ([]byte, bool) {
	oChanged := inO != inB || string(o) != string(b)
	tChanged := inT != inB || string(t) != string(b)
	switch {
	case tChanged:
		return t, inT
	case oChanged:
		return o, inO
	}
	return b, inB
}

// mergeRow merges one row file. keep=false deletes the path.
func mergeRow(p string, b []byte, inB bool, o []byte, inO bool, t []byte, inT bool, strategies StrategySource, reg *schema.Registry, table string) ([]byte, *MergeNote, bool, error) {
	bf, err := sideFile(b, inB)
	if err != nil {
		return nil, nil, false, fmt.Errorf("live: merge %s (base): %w", p, err)
	}
	of, err := sideFile(o, inO)
	if err != nil {
		// Our copy is unreadable: never guess, keep theirs if present.
		return t, nil, inT, nil
	}
	tf, err := sideFile(t, inT)
	if err != nil {
		// Their copy is unreadable: never guess, keep ours if present.
		return o, nil, inO, nil
	}
	if !inO && !inT {
		return nil, nil, false, nil
	}
	if inO && inT && string(o) == string(t) {
		return o, nil, true, nil
	}
	v2out, v2note, v2ok, err := mergeV2Row(p, bf, of, tf, reg)
	if v2ok || err != nil {
		return v2out, v2note, v2ok, err
	}
	newer := merge3.SideTheirs
	if of.block.HLC.Compare(tf.block.HLC) > 0 {
		newer = merge3.SideOurs
	}
	pol := merge3.Policy{Newer: newer}
	if strategies != nil {
		pol.StrategyFor = strategies(table)
	}
	res, err := merge3.MergeFile(bf.m3(), of.m3(), tf.m3(), pol)
	if err != nil {
		return nil, nil, false, fmt.Errorf("live: merge %s: %w", p, err)
	}
	var note *MergeNote
	switch res.Decision {
	case merge3.DecisionFieldMerge, merge3.DecisionModifyBeatsDelete, merge3.DecisionBothDeleted:
		// both sides changed the row: say how it was settled
		note = &MergeNote{Path: p, Decision: res.Decision, DeletedBy: res.DeletedBy, OursHLC: of.block.HLC, TheirsHLC: tf.block.HLC}
	case merge3.DecisionSame, merge3.DecisionOursOnly, merge3.DecisionTheirsOnly:
		// one side (or neither) changed the row: nothing to report
	}
	if res.File.Kind == merge3.FileAbsent {
		return nil, note, false, nil
	}
	f := File{Body: res.File.Doc, Block: combineBlocks(of, tf)}
	if res.File.Kind == merge3.FileTombstone {
		f = Tombstone(idOf(res.File.Doc), f.Block)
	} else {
		f.Block.Deleted = false
		newerBody := tf.file.Body
		if newer == merge3.SideOurs {
			newerBody = of.file.Body
		}
		replaced := takeNewerOnClash(f.Body, res.Fields.Conflicts, newer, newerBody)
		f.Block.Conflict = f.HasConflictMarkers()
		if note != nil {
			note.Replaced = replaced
		}
	}
	out, err := f.Encode()
	if err != nil {
		return nil, nil, false, err
	}
	return out, note, true, nil
}

// mergeV2Row merges a row both sides hold alive when both bodies are v2
// row documents, through logic/merge (see MergeTrees). ok=false (and no
// error) means the row is not a v2 row on both sides, or one side is a
// live tombstone: the caller's merge3 path settles it (file-level rules,
// modify beats delete).
func mergeV2Row(p string, bf, of, tf side, reg *schema.Registry) ([]byte, *MergeNote, bool, error) {
	if !of.present || !tf.present || of.file.Deleted() || tf.file.Deleted() {
		return nil, nil, false, nil
	}
	or, ok := v2Row(of)
	if !ok {
		return nil, nil, false, nil
	}
	tr, ok := v2Row(tf)
	if !ok {
		return nil, nil, false, nil
	}
	br := rowmerge.Absent
	if bf.present && !bf.file.Deleted() {
		if r, ok := v2Row(bf); ok && r.Table == or.Table && r.ID == or.ID {
			br = r
		}
	}
	pol := rowmerge.UnknownTable()
	if reg != nil {
		if def, ok := reg.Table(or.Table); ok {
			pol = rowmerge.PolicyFor(def)
		}
	}
	pol.TextNewerWins = true // never markers on the shared live ref (L-19)
	res, err := rowmerge.Rows(pol, br, or, tr)
	if err != nil {
		return nil, nil, false, fmt.Errorf("live: merge %s: %w", p, err)
	}
	enc, err := lorerow.Encode(res.Row)
	if err != nil {
		return nil, nil, false, fmt.Errorf("live: merge %s: %w", p, err)
	}
	body, err := canonjson.Decode(enc, canonjson.DefaultMaxDepth)
	if err != nil {
		return nil, nil, false, fmt.Errorf("live: merge %s: %w", p, err)
	}
	f := File{Body: body, Block: combineBlocks(of, tf)}
	f.Block.Deleted = false
	f.Block.Conflict = f.HasConflictMarkers()
	note := &MergeNote{Path: p, Decision: merge3.DecisionFieldMerge, OursHLC: of.block.HLC, TheirsHLC: tf.block.HLC, Trash: res.Trash}
	for _, c := range res.Replaced {
		older := "(none)"
		if c.Older != nil {
			older = c.Older.String()
		}
		note.Replaced = append(note.Replaced, ReplacedField{Field: c.Field, Older: older})
	}
	if !res.Clean() {
		note.Conflict = true
		for _, c := range res.Conflicts {
			note.ConflictKeys = append(note.ConflictKeys, metaKeyName(f.Body, c.Field))
		}
	}
	out, err := f.Encode()
	if err != nil {
		return nil, nil, false, err
	}
	return out, note, true, nil
}

// v2Row decodes a side's body as a v2 row document; ok=false for any body
// that is not one (a v0.1.17 row, a hand-written file).
func v2Row(s side) (lorerow.Row, bool) {
	if _, has := s.file.Body[lorerow.KeyVersion]; !has {
		return lorerow.Row{}, false
	}
	b, err := s.file.EncodeBody()
	if err != nil {
		return lorerow.Row{}, false
	}
	r, err := lorerow.Decode(b)
	if err != nil {
		return lorerow.Row{}, false
	}
	return r, true
}

// side is one side of a row merge, decoded.
type side struct {
	present bool
	file    File
	block   Block
}

func sideFile(b []byte, present bool) (side, error) {
	if !present {
		return side{}, nil
	}
	f, err := DecodeFile(b)
	if err != nil {
		return side{}, err
	}
	return side{present: true, file: f, block: f.Block}, nil
}

// m3 converts a side for merge3. Bodies are compared without their block,
// so a block-only change (a new last_copy_at) is never a field conflict.
func (s side) m3() merge3.File {
	switch {
	case !s.present:
		return merge3.Absent()
	case s.file.Deleted():
		return merge3.Tombstone(s.file.Body)
	}
	return merge3.Row(s.file.Body)
}

// combineBlocks merges two blocks: earliest live_since, highest hlc,
// latest last_copy_at. Deleted and Conflict are set by the caller from the
// merged body.
func combineBlocks(a, b side) Block {
	var out Block
	for _, s := range []side{a, b} {
		if !s.present {
			continue
		}
		out.LiveSince = earliest(out.LiveSince, s.block.LiveSince)
		out.HLC = MaxHLC(out.HLC, s.block.HLC)
		if s.block.LastCopyAt.After(out.LastCopyAt) {
			out.LastCopyAt = s.block.LastCopyAt
		}
	}
	// A companion stays one only while every side says so: once anyone
	// wrote the row on purpose it lands on its own.
	out.Companion = (!a.present || a.block.Companion) && (!b.present || b.block.Companion)
	return out
}

func earliest(a, b time.Time) time.Time {
	switch {
	case a.IsZero():
		return b
	case b.IsZero():
		return a
	case b.Before(a):
		return b
	}
	return a
}

func idOf(doc canonjson.Object) string { return doc.Str(idKey) }

// takeNewerOnClash settles every key merge3 left as a clash (text or not)
// by the newer side (by the rows' HLC), and returns what it replaced. Why
// not markers, as the branch merge driver writes: a live row is shared by
// every clone through refs/lore/live, so markers there were pushed to
// everyone and failed the sync of people who never touched the row (L-19).
//
// newerBody is the newer side's whole body: it says whether the key is
// absent there (removed) or holds a JSON null, which a merge3.Conflict
// cannot tell apart.
func takeNewerOnClash(body canonjson.Object, cs []merge3.Conflict, newer merge3.Side, newerBody canonjson.Object) []ReplacedField {
	var out []ReplacedField
	for _, c := range cs {
		keep, older := c.Theirs, c.Ours
		if newer == merge3.SideOurs {
			keep, older = c.Ours, c.Theirs
		}
		if s, ok := keep.AsString(); ok && merge3.ContainsConflictMarkers(s) {
			// the newer side already held markers (written before L-19):
			// keep them as they are, the row stays a conflict to settle
			body[c.Key] = keep
			continue
		}
		if v, ok := newerBody[c.Key]; ok {
			body[c.Key] = v
		} else {
			delete(body, c.Key) // the newer side removed the key
		}
		out = append(out, ReplacedField{Field: c.Key, Older: compactOrNull(older)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out
}

// compactOrNull renders one side of a clash for the conflict markers: a
// string as itself, anything else as compact JSON. CompactValue fails only
// on a malformed number literal, which is then shown as written so the
// clash stays visible.
func compactOrNull(v canonjson.Value) string {
	if v.IsNull() {
		return "null"
	}
	if s, ok := v.AsString(); ok {
		return s
	}
	s, err := canonjson.CompactValue(v)
	if err != nil {
		lit, _ := v.AsNumber()
		return lit
	}
	return s
}

func unionPurged(trees ...Tree) (map[string]bool, error) {
	out := map[string]bool{}
	for _, t := range trees {
		set, err := t.Purged()
		if err != nil {
			return nil, err
		}
		for id := range set {
			out[id] = true
		}
	}
	return out, nil
}

// dropPurged removes every row file whose id is purged, except a
// tombstone (dropsPurged).
func dropPurged(t Tree, purged map[string]bool) {
	for p, b := range t {
		if dropsPurged(p, b, purged) {
			delete(t, p)
		}
	}
}

// dropsPurged reports whether the live file at p (bytes b) goes because
// its id is purged. A tombstone stays: it holds no content (§3.3), and a
// `lore trash purge` lists its ids on _live/purged.json while its
// tombstone must stay in live until it lands as a file delete plus a
// _purged.json id on main (LORE_TEAM_SPEC side effect 11, DQ4: purge
// leaves a tombstone; F-TOMB-1). Every other file of a purged id goes, so
// a stale clone can never bring content back (§6.6). An undecodable file
// goes too: nothing shows it is a tombstone.
func dropsPurged(p string, b []byte, purged map[string]bool) bool {
	_, k, ok := ParseRowPath(p)
	if !ok || !purged[k.ID] {
		return false
	}
	f, err := DecodeFile(b)
	return err != nil || !f.Deleted()
}

// Describe renders a note the way commands print it.
func (n MergeNote) Describe() string {
	id := n.Key.ID
	switch n.Decision {
	case merge3.DecisionModifyBeatsDelete:
		by := "you"
		if n.DeletedBy == merge3.SideTheirs {
			by = "a teammate"
		}
		return fmt.Sprintf("%s was deleted by %s but edited on the other side; kept", id, by)
	case merge3.DecisionFieldMerge:
		if n.Trash != "" {
			return fmt.Sprintf("%s: %s", id, n.Trash)
		}
		if n.Conflict && metaOnly(n.ConflictKeys) {
			// D-220: a strict metadata key, not a text clash
			return fmt.Sprintf("%s: %s was set to different values on two machines (a strict key); both values are kept in the entry and the row does not land until one is chosen, see `lore sync conflicts`", id, strings.Join(n.ConflictKeys, ", "))
		}
		if n.Conflict {
			return fmt.Sprintf("%s: both sides edited %s; both kept with conflict markers, see `lore sync conflicts`", id, strings.Join(n.ConflictKeys, ", "))
		}
		if len(n.Replaced) > 0 {
			return n.describeReplaced()
		}
		return fmt.Sprintf("%s: merged both edits (newer by hlc: %s)", id, MaxHLC(n.OursHLC, n.TheirsHLC))
	case merge3.DecisionBothDeleted:
		return fmt.Sprintf("%s: deleted on both sides", id)
	case merge3.DecisionSame, merge3.DecisionOursOnly, merge3.DecisionTheirsOnly:
		// no note is made for these (merge3Row); the fallback names them
	}
	return fmt.Sprintf("%s: %s", id, n.Decision)
}

// metaKeyName turns a merge conflict field "meta.<key id>" into
// "meta.<key name>" (the name `lore meta set --pair` takes), read from the
// merged body's entry; any other field, or an entry with no name, is
// returned as it is.
func metaKeyName(body canonjson.Object, field string) string {
	id, ok := strings.CutPrefix(field, "meta.")
	if !ok {
		return field
	}
	entries, ok := body["meta"].AsObject()
	if !ok {
		return field
	}
	e, ok := entries[id].AsObject()
	if !ok || e.Str("name") == "" {
		return field
	}
	return "meta." + e.Str("name")
}

// metaOnly reports whether every key is a metadata key ("meta.…").
func metaOnly(keys []string) bool {
	for _, k := range keys {
		if !strings.HasPrefix(k, "meta.") {
			return false
		}
	}
	return len(keys) > 0
}

// replacedShown is how many runes of a replaced value a sync line shows.
const replacedShown = 60

// describeReplaced is the line of a row both sides edited where the newer
// edit was kept: which fields, whose edit stayed, and what was replaced
// (so the person whose text lost sees it, and can put it back with
// `lore task edit`).
func (n MergeNote) describeReplaced() string {
	fields := make([]string, 0, len(n.Replaced))
	was := make([]string, 0, len(n.Replaced))
	for _, r := range n.Replaced {
		fields = append(fields, r.Field)
		v := []rune(r.Older)
		short := r.Older
		if len(v) > replacedShown {
			short = string(v[:replacedShown]) + "…"
		}
		was = append(was, fmt.Sprintf("%s was %q", r.Field, short))
	}
	return fmt.Sprintf("%s: both sides edited %s; kept the newer edit (hlc %s), replaced: %s", n.Key.ID, strings.Join(fields, ", "), MaxHLC(n.OursHLC, n.TheirsHLC), strings.Join(was, "; "))
}
