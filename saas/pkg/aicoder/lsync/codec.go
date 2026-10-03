package lsync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"saas/pkg/aicoder/canonjson"

	"entgo.io/ent/schema/field"
)

// FormatVersion is the on-disk file format version written as "_v" in every
// row file. Bump it only together with an upcaster in upcast(); readers
// refuse files whose _v is newer than they understand (E24) instead of
// rewriting data they cannot fully represent.
// ds:def id=sync-formatversion-h8e7vzn7 owner=@khanakia stability=stable desc="row file format version"
const FormatVersion = 1

// Reserved document keys (never column names: lore columns are snake_case
// without a leading underscore).
const (
	keyVersion = "_v"
	keyTable   = "_table"
)

// timeLayout is the canonical timestamp form in files: UTC, fixed nine
// fractional digits. Fixed width matters — merge3.StrategyMax compares
// timestamps as strings, which is only correct when every value has the same
// length ("…05.1Z" would otherwise sort after "…05.12Z").
// ds:def id=sync-timelayout-ykcphmfb owner=@khanakia stability=stable desc="canonical timestamp layout in row files"
const timeLayout = "2006-01-02T15:04:05.000000000Z"

// looseTimeLayouts are the forms a timestamp may already have in lore.db:
// ent via the modernc driver writes Go's time.String() form (with a zone
// name and sometimes a " m=+…" monotonic suffix), raw SQL writes
// CURRENT_TIMESTAMP, and files carry timeLayout / RFC3339.
var looseTimeLayouts = []string{
	timeLayout,
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05.999999999 -0700 -0700",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// errNewerFormat marks a file written by a newer lore binary.
var errNewerFormat = errors.New("lsync: file written by a newer lore format")

// parseLooseTime accepts every timestamp form lore.db may hold.
func parseLooseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, " m="); i >= 0 {
		s = s[:i] // drop Go's monotonic clock reading
	}
	for _, l := range looseTimeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("lsync: unrecognised timestamp %q", s)
}

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// docFromRow converts DB column values (as returned by the sqlite driver)
// into a file document. vals[i] belongs to cols[i].
//
// Driver value shapes handled per column type:
//   - time:  time.Time (declared datetime columns), string (raw SQL), nil
//   - bool:  int64 0/1 (SQLite has no bool), bool, nil
//   - ints:  int64, float64 (integral), nil
//   - float: float64, int64, nil
//   - json:  string / []byte holding JSON text, nil — embedded as a parsed
//     value so diffs and merges see structure, not one escaped line
//   - other: string / []byte, nil
func docFromRow(t *Table, cols []Column, vals []any) (map[string]any, error) {
	doc := map[string]any{keyVersion: int64(FormatVersion), keyTable: t.Name}
	for i, c := range cols {
		v, err := toDocValue(c, vals[i])
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", t.Name, c.Name, err)
		}
		doc[c.Name] = v
	}
	return doc, nil
}

// toDocValue converts one scanned SQL value into its JSON document form.
//
// Boundary: database/sql scans untyped columns into `any`, and the document
// is arbitrary JSON (`any` as encoding/json decodes it). The switch on the
// column's ent type is where the value is narrowed; an unexpected Go type
// for that column is an error, never passed through.
func toDocValue(c Column, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch c.Type {
	case field.TypeTime:
		switch x := v.(type) {
		case time.Time:
			return formatTime(x), nil
		case string:
			t, err := parseLooseTime(x)
			if err != nil {
				return nil, err
			}
			return formatTime(t), nil
		case []byte:
			t, err := parseLooseTime(string(x))
			if err != nil {
				return nil, err
			}
			return formatTime(t), nil
		}
	case field.TypeBool:
		switch x := v.(type) {
		case bool:
			return x, nil
		case int64:
			return x != 0, nil
		}
	case field.TypeInt, field.TypeInt8, field.TypeInt16, field.TypeInt32, field.TypeInt64,
		field.TypeUint, field.TypeUint8, field.TypeUint16, field.TypeUint32, field.TypeUint64:
		switch x := v.(type) {
		case int64:
			return x, nil
		case float64:
			if x == float64(int64(x)) {
				return int64(x), nil
			}
		}
	case field.TypeFloat32, field.TypeFloat64:
		switch x := v.(type) {
		case float64:
			return x, nil
		case int64:
			return float64(x), nil
		}
	case field.TypeJSON:
		var s string
		switch x := v.(type) {
		case string:
			s = x
		case []byte:
			s = string(x)
		default:
			return nil, fmt.Errorf("json column holds %T", v)
		}
		parsed, err := canonjson.ParseValue(s)
		if err != nil {
			return nil, err
		}
		return parsed, nil
	default:
		switch x := v.(type) {
		case string:
			return x, nil
		case []byte:
			return string(x), nil
		case int64, float64, bool:
			// A text column holding a number: SQLite's dynamic typing allows
			// it (raw SQL). Keep the text form so it round-trips as text.
			return fmt.Sprint(x), nil
		}
	}
	return nil, fmt.Errorf("unexpected %T for %s column", v, c.Type)
}

// rowArgs is the parsed, typed form of a document ready for SQL binding.
type rowArgs struct {
	// values per column name; only columns PRESENT in the document.
	values map[string]any
	// extra holds keys this binary does not know (written by a newer
	// version); they are preserved verbatim and merged back on export (E24).
	extra map[string]any
}

// argsFromDoc validates and converts a document into SQL arguments.
//
// Documents come from two places with different value types: decoded files
// (numbers are json.Number) and docFromRow (int64 / float64). toSQLValue
// accepts both.
func argsFromDoc(t *Table, doc map[string]any) (rowArgs, error) {
	ra := rowArgs{values: map[string]any{}, extra: map[string]any{}}
	known := map[string]Column{}
	for _, c := range t.Columns {
		known[c.Name] = c
	}
	for k, v := range doc {
		if k == keyVersion || k == keyTable {
			continue
		}
		c, ok := known[k]
		if !ok {
			if _, vol := volatileColumns[k]; vol {
				continue // a file must never carry these; ignore if hand-added
			}
			ra.extra[k] = v
			continue
		}
		sv, err := toSQLValue(c, v)
		if err != nil {
			return rowArgs{}, fmt.Errorf("%s.%s: %w", t.Name, k, err)
		}
		ra.values[k] = sv
	}
	id, ok := ra.values[idColumn].(string)
	if !ok || id == "" {
		return rowArgs{}, fmt.Errorf("%s: document has no string id", t.Name)
	}
	return ra, nil
}

// toSQLValue is the inverse of toDocValue: it narrows a decoded JSON value to
// the Go type the column's ent type expects, for use as a database/sql
// argument (which the stdlib types as `any`). A value of the wrong JSON kind
// for the column is an error.
func toSQLValue(c Column, v any) (any, error) {
	if v == nil {
		if !c.Nullable && c.Name == idColumn {
			return nil, fmt.Errorf("id must not be null")
		}
		return nil, nil
	}
	switch c.Type {
	case field.TypeTime:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("timestamp must be a string, got %T", v)
		}
		t, err := parseLooseTime(s)
		if err != nil {
			return nil, err
		}
		return t.UTC(), nil
	case field.TypeBool:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("bool expected, got %T", v)
		}
		return b, nil
	case field.TypeInt, field.TypeInt8, field.TypeInt16, field.TypeInt32, field.TypeInt64,
		field.TypeUint, field.TypeUint8, field.TypeUint16, field.TypeUint32, field.TypeUint64:
		switch x := v.(type) {
		case json.Number:
			i, err := x.Int64()
			if err != nil {
				return nil, fmt.Errorf("integer expected: %w", err)
			}
			return i, nil
		case int64:
			return x, nil
		case float64:
			if x == float64(int64(x)) {
				return int64(x), nil
			}
		}
		return nil, fmt.Errorf("integer expected, got %T", v)
	case field.TypeFloat32, field.TypeFloat64:
		switch x := v.(type) {
		case json.Number:
			f, err := x.Float64()
			if err != nil {
				return nil, fmt.Errorf("number expected: %w", err)
			}
			return f, nil
		case float64:
			return x, nil
		case int64:
			return float64(x), nil
		}
		return nil, fmt.Errorf("number expected, got %T", v)
	case field.TypeJSON:
		return canonjson.CompactValue(v)
	default:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("string expected, got %T", v)
		}
		return s, nil
	}
}

// encodeDoc renders a document (plus preserved extras) canonically.
func encodeDoc(doc map[string]any, extra map[string]any) ([]byte, error) {
	if len(extra) == 0 {
		return canonjson.Encode(doc)
	}
	merged := maps.Clone(doc)
	for k, v := range extra {
		if _, clash := merged[k]; !clash {
			merged[k] = v
		}
	}
	return canonjson.Encode(merged)
}

// decodeDoc parses a row file and checks its envelope.
func decodeDoc(b []byte) (map[string]any, error) {
	doc, err := canonjson.Decode(b, canonjson.DefaultMaxDepth)
	if err != nil {
		return nil, err
	}
	v, ok := doc[keyVersion].(json.Number)
	if !ok {
		return nil, fmt.Errorf("lsync: missing %s", keyVersion)
	}
	n, err := v.Int64()
	if err != nil {
		return nil, fmt.Errorf("lsync: bad %s: %w", keyVersion, err)
	}
	if n > FormatVersion {
		return nil, fmt.Errorf("%w (file _v=%d, this lore understands %d): upgrade lore", errNewerFormat, n, FormatVersion)
	}
	if n < 1 {
		return nil, fmt.Errorf("lsync: bad %s %d", keyVersion, n)
	}
	return upcast(doc, n)
}

// upcast converts older file versions to FormatVersion in memory. There is
// only version 1 today; this is the single place future migrations hook in
// (E25), so branches still carrying old-format files keep importing.
func upcast(doc map[string]any, from int64) (map[string]any, error) {
	if from == FormatVersion {
		return doc, nil
	}
	return nil, fmt.Errorf("lsync: no upcaster from _v=%d", from)
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// sameIgnoringUpdatedAt reports whether two documents are equal once the
// updated_at key is ignored. A change that ONLY bumps updated_at (ent does
// this even when only a volatile column changed) is not a real change.
func sameIgnoringUpdatedAt(a, b map[string]any) (bool, error) {
	ac, bc := maps.Clone(a), maps.Clone(b)
	delete(ac, updatedAtColumn)
	delete(bc, updatedAtColumn)
	ab, err := canonjson.Encode(ac)
	if err != nil {
		return false, err
	}
	bb, err := canonjson.Encode(bc)
	if err != nil {
		return false, err
	}
	return string(ab) == string(bb), nil
}

// docUpdatedAt returns the document's updated_at instant (zero if absent or
// unparseable — a hand-edited file may carry any layout, so this parses
// instead of comparing strings).
func docUpdatedAt(doc map[string]any) time.Time {
	s, _ := doc[updatedAtColumn].(string)
	if s == "" {
		return time.Time{}
	}
	t, err := parseLooseTime(s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// firstIsNewer reports whether a's updated_at is strictly later than b's.
// Ties go to b, so callers pass the side that should win ties second.
func firstIsNewer(a, b map[string]any) bool {
	return docUpdatedAt(a).After(docUpdatedAt(b))
}
