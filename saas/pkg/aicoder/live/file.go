package live

import (
	"fmt"
	"maps"
	"strings"
	"time"

	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/merge3"
)

// BlockKey is the key of the `_live` block inside a live file (§3.3). Lore
// columns never start with "_", so it can never clash with a row field.
const BlockKey = "_live"

// Keys inside the `_live` block (§1 "Files and folders").
const (
	blockLiveSince  = "live_since"
	blockHLC        = "hlc"
	blockDeleted    = "deleted"
	blockLastCopyAt = "last_copy_at"
	blockConflict   = "conflict"
	blockCompanion  = "companion"
)

// idKey is the row id key every row document carries.
const idKey = "id"

// blockTimeLayout is how block times are written: UTC, whole seconds, the
// form §3.3 shows. Block times only drive max-age decisions measured in
// days, so sub-second precision would only add noise to diffs.
const blockTimeLayout = time.RFC3339

// Block is a live file's `_live` block. It is removed when the row lands
// (the landed file is the plain row).
type Block struct {
	// LiveSince is when the row first became live; it never moves forward
	// on later edits, so max age is measured from the first live write.
	LiveSince time.Time
	// HLC orders edits (F4).
	HLC HLC
	// Deleted marks a tombstone: the body is gone, only the id remains, so
	// lookup stops here instead of falling through to an older branch copy.
	Deleted bool
	// LastCopyAt is the time of the last max-age copy to main (F5 rule 6c).
	LastCopyAt time.Time
	// Conflict is set when a merge left conflict markers in a text field;
	// such a row is shown with a warning and never lands (F4).
	Conflict bool
	// Companion marks a supporting row that went live only because rows of
	// the same command did (rowroute.DecideAll: the writer's actor row on
	// their first write). It lands only together with a content row, so a
	// plain live write never moves main on its own (F2).
	Companion bool
}

// File is one decoded live file: the row body (exactly what lands on main)
// and its block.
//
// Invariant: Body never contains BlockKey; a tombstone's Body is {"id": id}.
type File struct {
	Body  map[string]any
	Block Block
}

// ID returns the row id the file carries ("" when missing).
func (f File) ID() string {
	s, _ := f.Body[idKey].(string)
	return s
}

// Deleted reports a tombstone.
func (f File) Deleted() bool { return f.Block.Deleted }

// Tombstone returns the tombstone for a row: only the id is kept, never the
// body (§3.3: a deleted secret must not survive in the live tree).
func Tombstone(id string, b Block) File {
	b.Deleted = true
	b.Conflict = false
	return File{Body: map[string]any{idKey: id}, Block: b}
}

// DecodeFile parses a live file. A file without a block (written by hand,
// or a plain row file) decodes with a zero block.
func DecodeFile(b []byte) (File, error) {
	doc, err := canonjson.Decode(b, canonjson.DefaultMaxDepth)
	if err != nil {
		return File{}, fmt.Errorf("live: decode file: %w", err)
	}
	f := File{Body: doc}
	raw, has := doc[BlockKey]
	if !has {
		return f, nil
	}
	delete(doc, BlockKey)
	m, ok := raw.(map[string]any)
	if !ok {
		return File{}, fmt.Errorf("live: %s must be an object", BlockKey)
	}
	if f.Block, err = decodeBlock(m); err != nil {
		return File{}, err
	}
	return f, nil
}

func decodeBlock(m map[string]any) (Block, error) {
	var b Block
	var err error
	if b.LiveSince, err = blockTime(m, blockLiveSince); err != nil {
		return b, err
	}
	if b.LastCopyAt, err = blockTime(m, blockLastCopyAt); err != nil {
		return b, err
	}
	if s, ok := m[blockHLC].(string); ok {
		if b.HLC, err = ParseHLC(s); err != nil {
			return b, err
		}
	}
	b.Deleted, _ = m[blockDeleted].(bool)
	b.Conflict, _ = m[blockConflict].(bool)
	b.Companion, _ = m[blockCompanion].(bool)
	return b, nil
}

func blockTime(m map[string]any, key string) (time.Time, error) {
	s, ok := m[key].(string)
	if !ok || s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("live: %s.%s: %w", BlockKey, key, err)
	}
	return t.UTC(), nil
}

// Encode renders the live file canonically (body plus block).
func (f File) Encode() ([]byte, error) {
	doc := maps.Clone(f.Body)
	if doc == nil {
		doc = map[string]any{}
	}
	doc[BlockKey] = f.Block.toMap()
	return canonjson.Encode(doc)
}

// EncodeBody renders the body alone, canonically: the bytes that land on
// main and that the content comparisons (Confirm, ride snapshots) use.
func (f File) EncodeBody() ([]byte, error) {
	return canonjson.Encode(f.Body)
}

func (b Block) toMap() map[string]any {
	m := map[string]any{}
	if !b.LiveSince.IsZero() {
		m[blockLiveSince] = b.LiveSince.UTC().Format(blockTimeLayout)
	}
	if !b.HLC.IsZero() {
		m[blockHLC] = b.HLC.String()
	}
	if b.Deleted {
		m[blockDeleted] = true
	}
	if !b.LastCopyAt.IsZero() {
		m[blockLastCopyAt] = b.LastCopyAt.UTC().Format(blockTimeLayout)
	}
	if b.Conflict {
		m[blockConflict] = true
	}
	if b.Companion {
		m[blockCompanion] = true
	}
	return m
}

// HasConflictMarkers reports whether any string field of the body carries
// merge conflict markers (a text clash, F4).
func (f File) HasConflictMarkers() bool {
	for _, v := range f.Body {
		if s, ok := v.(string); ok && merge3.ContainsConflictMarkers(s) {
			return true
		}
	}
	return false
}

// SameBody reports whether two files hold the same row body (block
// ignored). Tombstones compare by id only.
func SameBody(a, b File) (bool, error) {
	if a.Deleted() != b.Deleted() {
		return false, nil
	}
	ab, err := a.EncodeBody()
	if err != nil {
		return false, err
	}
	bb, err := b.EncodeBody()
	if err != nil {
		return false, err
	}
	return string(ab) == string(bb), nil
}

// stripKey returns doc without key; comparisons use it to ignore a bare
// updated_at bump (an edit that changed nothing else is not a change).
func stripKey(doc map[string]any, key string) map[string]any {
	out := maps.Clone(doc)
	delete(out, key)
	return out
}

// isReservedFolder reports a folder name lore keeps for itself inside the
// live tree (fixed rule 3: names beginning with "_" other than a project's
// own "_root").
func isReservedFolder(name string) bool {
	return strings.HasPrefix(name, reservedPrefix) && name != RootFolder
}
