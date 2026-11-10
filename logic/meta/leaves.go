package meta

// Leaves of a metadata value for the secret scan (SECRETS_AND_REFERENCES
// 19.1, METADATA 3.15/3.21): a value is scanned leaf by leaf, so a
// finding is redacted inside the one string that holds it, never across
// JSON syntax.
//
//	raw (canonical JSON) ─► Leaves ─► every decoded string, at any depth,
//	                                  plus every object member name (Key)
//	scan each Text ─► redact one leaf ─► ReplaceLeaf(raw, path, new text)

import (
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
)

// Step is one step of a leaf's path from the value's root: an object
// member (Member) or an array item (Index ≥ 0, Member empty).
type Step struct {
	Member string
	Index  int
}

// IsIndex reports an array-item step.
func (s Step) IsIndex() bool { return s.Index >= 0 }

// Leaf is one decoded string inside a metadata value: the value itself
// when it is a string, a string item of a list, a string member of an
// object at any depth, or (Key) an object member's name. A key name is
// an identifier: it is scanned but can never hold a reference (19.1).
type Leaf struct {
	Path []Step
	Text string
	Key  bool
}

// PathString is the leaf's path in the --meta key grammar: members
// joined with ".", items as "[i]" ("smtp.url", "hosts[2]"); "" for the
// value's root.
func (l Leaf) PathString() string {
	var b strings.Builder
	for _, s := range l.Path {
		if s.IsIndex() {
			b.WriteString("[" + strconv.Itoa(s.Index) + "]")
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(s.Member)
	}
	return b.String()
}

// Leaves returns every string leaf and member name of raw, members in
// canonical (sorted) order, depth first. Numbers, bools and null have no
// text to scan.
func Leaves(raw []byte) ([]Leaf, error) {
	var out []Leaf
	err := walkLeaves(raw, nil, func(l Leaf) { out = append(out, l) })
	return out, err
}

// walkLeaves visits the leaves of raw below path.
func walkLeaves(raw []byte, path []Step, visit func(Leaf)) error {
	switch canon.KindOf(raw) {
	case canon.KindString:
		s, err := unquote(raw)
		if err != nil {
			return leafErr(err)
		}
		visit(Leaf{Path: path, Text: s})
	case canon.KindObject:
		ms, err := canon.Members(raw)
		if err != nil {
			return leafErr(err)
		}
		for _, m := range ms {
			at := append(clonePath(path), Step{Member: m.Key, Index: -1})
			visit(Leaf{Path: at, Text: m.Key, Key: true})
			if err := walkLeaves(m.Raw, at, visit); err != nil {
				return err
			}
		}
	case canon.KindArray:
		items, err := canon.Elements(raw)
		if err != nil {
			return leafErr(err)
		}
		for i, it := range items {
			if err := walkLeaves(it, append(clonePath(path), Step{Index: i}), visit); err != nil {
				return err
			}
		}
	case canon.KindInvalid:
		return leafErr(canon.ErrInvalid)
	default: // null, bool and number hold no text to scan
	}
	return nil
}

// ReplaceLeaf returns raw with the string leaf at path set to text, in
// canonical compact form. Only a string value can be replaced: a member
// name or a non-string leaf is an error (a key cannot hold a reference).
func ReplaceLeaf(raw []byte, path []Step, text string) ([]byte, error) {
	out, err := replaceAt(raw, path, text)
	if err != nil {
		return nil, err
	}
	return canon.Compact(out)
}

// replaceAt rebuilds raw with the leaf at path replaced.
func replaceAt(raw []byte, path []Step, text string) ([]byte, error) {
	if len(path) == 0 {
		if canon.KindOf(raw) != canon.KindString {
			return nil, errs.New(errs.InvalidInput, "meta: only a text leaf can be replaced")
		}
		return canon.String(text), nil
	}
	step, rest := path[0], path[1:]
	switch canon.KindOf(raw) {
	case canon.KindObject:
		ms, err := canon.Members(raw)
		if err != nil {
			return nil, leafErr(err)
		}
		found := false
		for i := range ms {
			if step.IsIndex() || ms[i].Key != step.Member {
				continue
			}
			if ms[i].Raw, err = replaceAt(ms[i].Raw, rest, text); err != nil {
				return nil, err
			}
			found = true
		}
		if !found {
			return nil, errs.New(errs.InvalidInput, "meta: no member "+strconv.Quote(step.Member)+" in the value")
		}
		doc, err := canon.Object(ms)
		if err != nil {
			return nil, leafErr(err)
		}
		return doc, nil
	case canon.KindArray:
		items, err := canon.Elements(raw)
		if err != nil {
			return nil, leafErr(err)
		}
		if !step.IsIndex() || step.Index >= len(items) {
			return nil, errs.New(errs.InvalidInput, "meta: no item "+strconv.Itoa(step.Index)+" in the value")
		}
		if items[step.Index], err = replaceAt(items[step.Index], rest, text); err != nil {
			return nil, err
		}
		var b strings.Builder
		b.WriteByte('[')
		for i, it := range items {
			if i > 0 {
				b.WriteByte(',')
			}
			b.Write(it)
		}
		b.WriteByte(']')
		return []byte(b.String()), nil
	default: // a scalar (or invalid value) has no member or item to step into
	}
	return nil, errs.New(errs.InvalidInput, "meta: the path does not reach a text leaf")
}

// clonePath copies a path so sibling leaves never share a backing array.
func clonePath(p []Step) []Step { return append([]Step(nil), p...) }

// leafErr wraps a parse failure of a stored value.
func leafErr(err error) error {
	return errs.Wrap(errs.InvalidInput, "meta: a value is not valid JSON", err)
}
