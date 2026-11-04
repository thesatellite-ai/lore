package merge

import (
	"bytes"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// Provenance bookkeeping keys the merge treats specially
// (PROVENANCE_DESIGN 5.4, 7).
const (
	// fieldSharedAs is the v0.1.17 list of copy ids a `lore share` wrote on
	// the original row; merged as a union on every table.
	fieldSharedAs = "shared_as"
	// originMoves is the `moves` list inside _o (FQ4: the one exception
	// to write-once).
	originMoves = "moves"
	moveAt      = "at"
	moveFrom    = "moved_from"
	moveTo      = "moved_to"
)

// mergeOrigin settles _o (PROVENANCE_DESIGN 7): write-once, so equal sides
// agree and a side without _o (an old binary dropped it) yields to the one
// that has it. The `moves` list is a union keyed by (at, moved_from,
// moved_to), sorted by at. The rest of _o merges three-way (so a `lore repo
// merge-ids` rewrite on one side wins over an untouched other side); two
// different rewrites keep the side with the earlier created_at, ties by
// the byte-smaller canonical JSON (lore lint reports origin_mismatch).
func mergeOrigin(base, ours, theirs row.Row) value.JSON {
	o, t := ours.Origin, theirs.Origin
	switch {
	case o.IsZero():
		return t
	case t.IsZero():
		return o
	case bytes.Equal(o.Raw(), t.Raw()):
		return o
	}
	bRest, bMoves := splitMoves(base.Origin)
	oRest, oMoves := splitMoves(o)
	tRest, tMoves := splitMoves(t)
	var rest []byte
	switch {
	case bytes.Equal(oRest, tRest):
		rest = oRest
	case bRest != nil && bytes.Equal(oRest, bRest):
		rest = tRest
	case bRest != nil && bytes.Equal(tRest, bRest):
		rest = oRest
	case earlierSide(ours, theirs, oRest, tRest):
		rest = oRest
	default:
		rest = tRest
	}
	ms, err := canon.Members(rest)
	if err != nil {
		return o // a hand-broken _o: keep ours whole rather than lose it
	}
	if moves := unionMoves(bMoves, oMoves, tMoves); moves != nil {
		ms = append(ms, canon.Member{Key: originMoves, Raw: moves})
	}
	obj, err := canon.Object(ms)
	if err != nil {
		return o
	}
	j, err := value.JSONOf(obj)
	if err != nil {
		return o
	}
	return j
}

// earlierSide reports whether ours' _o is the one to keep: earlier
// created_at, ties by the byte-smaller canonical JSON.
func earlierSide(ours, theirs row.Row, oRest, tRest []byte) bool {
	switch {
	case ours.CreatedAt.Before(theirs.CreatedAt):
		return true
	case theirs.CreatedAt.Before(ours.CreatedAt):
		return false
	}
	return bytes.Compare(oRest, tRest) <= 0
}

// splitMoves returns _o without its moves (canonical bytes; nil when _o is
// absent) and the moves elements.
func splitMoves(o value.JSON) (rest []byte, moves [][]byte) {
	if o.IsZero() {
		return nil, nil
	}
	ms, err := canon.Members(o.Raw())
	if err != nil {
		return o.Raw(), nil
	}
	keep := make([]canon.Member, 0, len(ms))
	for _, m := range ms {
		if m.Key == originMoves {
			if els, eerr := canon.Elements(m.Raw); eerr == nil {
				moves = els
			}
			continue
		}
		keep = append(keep, m)
	}
	obj, err := canon.Object(keep)
	if err != nil {
		return o.Raw(), moves
	}
	return obj, moves
}

// unionMoves is the union of the moves lists keyed by (at, moved_from,
// moved_to), sorted by at then key; the same key with different bytes keeps
// the byte-larger entry so the outcome never depends on the side. nil when
// no side has a move.
func unionMoves(lists ...[][]byte) []byte {
	byKey := map[string][]byte{}
	for _, l := range lists {
		for _, e := range l {
			c, err := canon.Compact(e)
			if err != nil {
				continue
			}
			k := moveKey(c)
			if old, ok := byKey[k]; !ok || bytes.Compare(c, old) > 0 {
				byKey[k] = c
			}
		}
	}
	if len(byKey) == 0 {
		return nil
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	slices.Sort(keys) // the key starts with at, so this is time order
	parts := make([][]byte, len(keys))
	for i, k := range keys {
		parts[i] = byKey[k]
	}
	return append(append([]byte("["), bytes.Join(parts, []byte(","))...), ']')
}

// moveKey is a move entry's identity: at, moved_from, moved_to.
func moveKey(e []byte) string {
	return strings.Join([]string{memberText(e, moveAt), memberText(e, moveFrom), memberText(e, moveTo)}, "\x00")
}
