// Package conflictpick settles conflicts by keeping one side: the marker
// blocks a text merge leaves in a string (git's shape: "<<<<<<< ",
// "=======", ">>>>>>> "; logic/merge3 MarkConflict writes the same) and
// the structured meta conflict entries of METADATA 3.12
// ({"conflict": {base, ours, theirs}}). `lore sync resolve --take
// kept|other` uses it on live rows and on row files a git merge left
// marked (G-SYNC-1, G-SYNC-2). It only ever chooses between texts that are
// already there; a caller re-checks the result for markers it could not
// settle (nested or unfinished blocks) and refuses then.
package conflictpick

import (
	"strings"

	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/logic/merge3"
	"github.com/thesatellite-ai/lore/model/row"
)

// Object is body with every conflict settled to one side (other:
// the second section, else the first): marker blocks in any string at any
// depth, and structured meta conflict entries (METADATA 3.12).
func Object(body canonjson.Object, other bool) canonjson.Object {
	out := make(canonjson.Object, len(body))
	for k, v := range body {
		out[k] = value(v, other)
	}
	if meta, ok := out[row.KeyMeta].AsObject(); ok {
		out[row.KeyMeta] = canonjson.ObjectValue(metaEntries(meta, other))
	}
	return out
}

// value settles the marker blocks of every string inside v.
func value(v canonjson.Value, other bool) canonjson.Value {
	if str, ok := v.AsString(); ok {
		return canonjson.String(Text(str, other))
	}
	if arr, ok := v.AsArray(); ok {
		out := make([]canonjson.Value, len(arr))
		for i, it := range arr {
			out[i] = value(it, other)
		}
		return canonjson.Array(out...)
	}
	if obj, ok := v.AsObject(); ok {
		return canonjson.ObjectValue(Object(obj, other))
	}
	return v
}

// Structured meta conflict members (logic/merge entries.go, METADATA 3.12).
const (
	metaConflictKey = "conflict"
	metaOursKey     = "ours"
	metaTheirsKey   = "theirs"
	metaValueKey    = "v"
)

// metaEntries replaces each entry's {"conflict": {base, ours,
// theirs}} with the chosen side's value; a side that removed the value
// (null) removes the entry.
func metaEntries(meta canonjson.Object, other bool) canonjson.Object {
	side := metaOursKey
	if other {
		side = metaTheirsKey
	}
	out := make(canonjson.Object, len(meta))
	for id, e := range meta {
		entry, ok := e.AsObject()
		if !ok {
			out[id] = e
			continue
		}
		c, has := entry[metaConflictKey]
		if !has {
			out[id] = e
			continue
		}
		sides, _ := c.AsObject()
		chosen, present := sides[side]
		if !present || chosen.Kind() == canonjson.KindNull {
			continue
		}
		next := make(canonjson.Object, len(entry))
		for k, v := range entry {
			if k != metaConflictKey {
				next[k] = v
			}
		}
		next[metaValueKey] = chosen
		out[id] = canonjson.ObjectValue(next)
	}
	return out
}

// Text keeps one section of every complete marker block of s
// (open "<<<<<<< ", split "=======", close ">>>>>>> ", git's shape);
// an unfinished block is left as written.
func Text(s string, other bool) string {
	if !merge3.ContainsConflictMarkers(s) {
		return s
	}
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	const (
		outside = iota
		first
		second
	)
	state, start := outside, 0
	var firstLines, secondLines []string
	for i, line := range lines {
		switch {
		case state == outside && strings.HasPrefix(line, merge3.MarkerOurs[:8]):
			state, start, firstLines, secondLines = first, i, nil, nil
		case state == first && line == merge3.MarkerSplit:
			state = second
		case state == second && strings.HasPrefix(line, merge3.MarkerTheirs[:8]):
			if other {
				out = append(out, secondLines...)
			} else {
				out = append(out, firstLines...)
			}
			state = outside
		case state == first:
			firstLines = append(firstLines, line)
		case state == second:
			secondLines = append(secondLines, line)
		default:
			out = append(out, line)
		}
	}
	if state != outside {
		out = append(out, lines[start:]...) // an unfinished block stays as written
	}
	return strings.Join(out, "\n")
}

// HasMarkers reports whether any string of body (object keys included, at
// any depth) holds a complete conflict block, or meta holds a structured
// conflict entry: what Object would settle.
func HasMarkers(body canonjson.Object) bool {
	for k, v := range body {
		if merge3.ContainsConflictMarkers(k) || valueHasMarkers(v) {
			return true
		}
	}
	meta, ok := body[row.KeyMeta].AsObject()
	if !ok {
		return false
	}
	for _, e := range meta {
		if entry, ok := e.AsObject(); ok {
			if _, has := entry[metaConflictKey]; has {
				return true
			}
		}
	}
	return false
}

// valueHasMarkers walks v for a string holding a conflict block.
func valueHasMarkers(v canonjson.Value) bool {
	if s, ok := v.AsString(); ok {
		return merge3.ContainsConflictMarkers(s)
	}
	if arr, ok := v.AsArray(); ok {
		for _, it := range arr {
			if valueHasMarkers(it) {
				return true
			}
		}
		return false
	}
	if obj, ok := v.AsObject(); ok {
		for k, it := range obj {
			if merge3.ContainsConflictMarkers(k) || valueHasMarkers(it) {
				return true
			}
		}
	}
	return false
}
