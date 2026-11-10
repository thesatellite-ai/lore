package meta

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
)

// Name limits (MQ3, 3.5, 3.13): a dotted key is at most MaxKeyLen
// characters and MaxDepth segments.
const (
	MaxKeyLen = 64
	MaxDepth  = 4
)

// Level words of the grammar (3.6).
const (
	LevelRepo    = "repo"
	LevelProject = "project"
	LevelOrg     = "org"
)

// levelWords are the reserved level words.
var levelWords = map[string]bool{LevelRepo: true, LevelProject: true, LevelOrg: true}

// IsLevel reports whether s is a level word.
func IsLevel(s string) bool { return levelWords[s] }

// TypeObject is the grammar's object word. B7 stores an object as a json
// value (DECISION META-B7-OBJ: per-field key rows need a parent column the
// meta_keys table does not have yet).
const TypeObject = "object"

// typeWords are the reserved type words (3.5): every value kind lore
// stores in meta, plus list and object.
var typeWords = map[string]bool{
	"text": true, "number": true, "bool": true, "date": true, "datetime": true, "duration": true,
	"dns": true, "ip": true, "cidr": true, "url": true, "email": true, "list": true, TypeObject: true, "json": true,
}

// IsTypeWord reports whether s is a type word (list<elem> included).
func IsTypeWord(s string) bool {
	if typeWords[s] {
		return true
	}
	_, err := ParseType(s)
	return err == nil && strings.HasPrefix(s, "list<")
}

// tagLike names are keys that duplicate lore tags (MQ5): a warning, not a
// refusal ("label" is a column, so it is reserved anyway).
var tagLike = map[string]bool{"tag": true, "tags": true, "labels": true}

// TagLike reports whether a key name should warn and point to lore tag.
func TagLike(name string) bool { return tagLike[TopSegment(name)] }

// TopSegment is the first dot segment of a key name.
func TopSegment(name string) string {
	top, _, _ := strings.Cut(name, ".")
	return top
}

// Segments splits a dotted key name.
func Segments(name string) []string { return strings.Split(name, ".") }

// CheckName refuses a key name that is not dotted snake_case ASCII (3.5):
// E_META_KEY with the converter's suggestion, E_META_DEPTH past maxDepth
// segments (maxDepth <= 0 means MaxDepth).
func CheckName(name string, maxDepth int) error {
	if maxDepth <= 0 {
		maxDepth = MaxDepth
	}
	if name == "" {
		return errs.New(errs.MetaKey, "meta: a key name is empty")
	}
	for i, r := range name {
		if r >= utf8.RuneSelf {
			return errs.New(errs.MetaKey, fmt.Sprintf("meta: %q contains a character that is not ASCII (%U at position %d). Use: %s",
				name, r, i, suggestKey(name)))
		}
	}
	segs := Segments(name)
	if len(segs) > maxDepth {
		return errs.New(errs.MetaDepth, fmt.Sprintf("meta: %q has %d dot segments; at most %d", name, len(segs), maxDepth)).
			WithHint("flatten the key, or use a json value")
	}
	if len(name) > MaxKeyLen {
		return errs.New(errs.MetaKey, fmt.Sprintf("meta: %q is longer than %d characters", name, MaxKeyLen))
	}
	for _, s := range segs {
		if !snakeSegment(s) {
			msg := fmt.Sprintf("meta: %q is not a snake_case key name. Use: %s", name, suggestKey(name))
			if len(segs) > 1 {
				msg = fmt.Sprintf("meta: segment %q of %q is not snake_case. Use: %s", s, name, suggestKey(name))
			}
			if s != "" && s[0] >= '0' && s[0] <= '9' {
				msg = fmt.Sprintf("meta: %q must start with a letter. Use: %s", name, suggestKey(name))
			}
			return errs.New(errs.MetaKey, msg)
		}
	}
	return nil
}

// snakeSegment is [a-z][a-z0-9]*(_[a-z0-9]+)*, checked byte by byte.
func snakeSegment(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' || s[len(s)-1] == '_' {
		return false
	}
	prevUnderscore := false
	for i := range len(s) {
		c := s[i]
		switch {
		case c == '_':
			if prevUnderscore {
				return false
			}
			prevUnderscore = true
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			prevUnderscore = false
		default:
			return false
		}
	}
	return true
}

// suggestKey converts every segment (the shown suggestion only).
func suggestKey(name string) string {
	segs := Segments(name)
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		if c := ToSnake(s); c != "" {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return "(no usable letters)"
	}
	return strings.Join(out, ".")
}

// ToSnake is the one name converter (3.5, OQ1): NFKD without marks, split
// at separators and case changes, lowercase, drop non [a-z0-9], join with
// "_", prefix n_ before a digit. "" when nothing is left.
func ToSnake(s string) string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = cur[:0]
		}
	}
	rs := []rune(norm.NFKD.String(strings.TrimSpace(s)))
	for i, r := range rs {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		switch {
		case r == ' ' || r == '-' || r == '.' || r == '_' || unicode.IsSpace(r):
			flush()
			continue
		case unicode.IsUpper(r) && len(cur) > 0:
			prev := cur[len(cur)-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if unicode.IsLower(prev) || (unicode.IsUpper(prev) && nextLower) { // "2FA" stays one word (TestNameConvert table)
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	var kept []string
	for _, w := range words {
		var b strings.Builder
		for _, r := range strings.ToLower(w) {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
		if b.Len() > 0 {
			kept = append(kept, b.String())
		}
	}
	out := strings.Join(kept, "_")
	if out != "" && out[0] >= '0' && out[0] <= '9' {
		out = "n_" + out
	}
	if len(out) > MaxKeyLen {
		return ""
	}
	return out
}

// Reserved refuses a key whose first segment is lore's own word (3.5):
// a column of any table (columns), id, meta, _*, mky, val, mky_*, val_*, a
// type word or a level word. E_META_RESERVED.
func Reserved(name string, columns map[string]bool) error {
	top := TopSegment(name)
	why := ""
	switch {
	case top == "id" || top == "meta":
		why = "is a word lore uses for every row"
	case strings.HasPrefix(top, "_"):
		why = "starts with _ (lore's own fields)"
	case top == "mky" || top == "val" || strings.HasPrefix(top, "mky_") || strings.HasPrefix(top, "val_"):
		why = "looks like a key or value id"
	case typeWords[top] || top == string(value.KindRef):
		why = "is a type word"
	case levelWords[top]:
		why = "is a level word"
	case columns[top]:
		why = "is a column of a lore table"
	}
	if why == "" {
		return nil
	}
	return errs.New(errs.MetaReserved, fmt.Sprintf("meta: %q %s, so it cannot be a metadata key", top, why)).
		WithHint("use another name")
}
