package live

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/merge3"

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
}

// MergeTrees three-way merges two live trees (F4): base is their merge
// base (empty when unrelated), ours the local tree, theirs the remote one.
//
// Rules per path:
//   - row files: merge3.MergeFile on the bodies (file level first: add,
//     delete, modify-beats-delete; then field level with the hlc-newer side
//     winning scalars and text clashes kept with markers); the `_live`
//     blocks combine (earliest live_since, highest hlc, latest
//     last_copy_at);
//   - _live/epoch: the higher number;
//   - _live/purged.json: set union, and every purged id is removed from the
//     result so a stale side can never bring it back;
//   - _live/meta.json and any other file: the side that changed it, theirs
//     when both did.
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
	if out, note, ok, err := mergeV2Row(p, bf, of, tf, reg); ok || err != nil {
		return out, note, ok, err
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
	}
	if res.File.Kind == merge3.FileAbsent {
		return nil, note, false, nil
	}
	f := File{Body: res.File.Doc, Block: combineBlocks(of, tf)}
	if res.File.Kind == merge3.FileTombstone {
		f = Tombstone(idOf(res.File.Doc), f.Block)
	} else {
		f.Block.Deleted = false
		markNonTextConflicts(f.Body, res.Fields.Conflicts)
		f.Block.Conflict = f.HasConflictMarkers()
		if note != nil && res.Conflicted() {
			note.Conflict = true
			for _, c := range res.Fields.Conflicts {
				note.ConflictKeys = append(note.ConflictKeys, c.Key)
			}
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
	if !res.Clean() {
		note.Conflict = true
		for _, c := range res.Conflicts {
			note.ConflictKeys = append(note.ConflictKeys, c.Field)
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

func idOf(doc map[string]any) string {
	s, _ := doc[idKey].(string)
	return s
}

// markNonTextConflicts makes a clash on a non-string field visible as
// marked text, exactly like the branch merge driver does, so a conflict can
// never hide inside a valid-looking value.
func markNonTextConflicts(body map[string]any, cs []merge3.Conflict) {
	for _, c := range cs {
		if s, ok := body[c.Key].(string); ok && merge3.ContainsConflictMarkers(s) {
			continue
		}
		body[c.Key] = merge3.MarkConflict(compactOrNull(c.Ours), compactOrNull(c.Theirs))
	}
}

func compactOrNull(v any) string {
	if v == nil {
		return "null"
	}
	if s, ok := v.(string); ok {
		return s
	}
	s, err := canonjson.CompactValue(v)
	if err != nil {
		return fmt.Sprint(v)
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

// dropPurged removes every row file whose id is purged.
func dropPurged(t Tree, purged map[string]bool) {
	for p := range t {
		if _, k, ok := ParseRowPath(p); ok && purged[k.ID] {
			delete(t, p)
		}
	}
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
		if n.Conflict {
			return fmt.Sprintf("%s: both sides edited %s; both kept with conflict markers, see `lore sync conflicts`", id, strings.Join(n.ConflictKeys, ", "))
		}
		return fmt.Sprintf("%s: merged both edits (newer by hlc: %s)", id, MaxHLC(n.OursHLC, n.TheirsHLC))
	case merge3.DecisionBothDeleted:
		return fmt.Sprintf("%s: deleted on both sides", id)
	}
	return fmt.Sprintf("%s: %s", id, n.Decision)
}
