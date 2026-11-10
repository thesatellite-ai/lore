package meta

import (
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
)

// Entry keys of one row-file meta entry (3.1).
const (
	EntryName      = "name"
	EntryScope     = "scope"
	EntryStrict    = "strict"
	EntrySensitive = "sensitive"
	EntryV         = "v"
	EntryVName     = "v_name"
)

// Entry is one value in a row's meta, keyed by its key id in the row.
// Extra keeps entry fields a newer lore wrote (FORMAT_EVOLUTION rule 1).
type Entry struct {
	Name, Scope       string
	Strict, Sensitive bool
	// V and VName are canonical JSON (VName nil when absent).
	V, VName []byte
	Extra    []canon.Member
}

// DecodeEntry reads a stored entry.
func DecodeEntry(j value.JSON) (Entry, error) {
	ms, err := canon.Members(j.Raw())
	if err != nil {
		return Entry{}, errs.Wrap(errs.InvalidInput, "meta: an entry is not a JSON object", err)
	}
	var e Entry
	for _, m := range ms {
		switch m.Key {
		case EntryName:
			e.Name, err = unquote(m.Raw)
		case EntryScope:
			e.Scope, err = unquote(m.Raw)
		case EntryStrict:
			e.Strict = string(m.Raw) == "true"
		case EntrySensitive:
			e.Sensitive = string(m.Raw) == "true"
		case EntryV:
			e.V = m.Raw
		case EntryVName:
			e.VName = m.Raw
		default:
			e.Extra = append(e.Extra, m)
		}
		if err != nil {
			return Entry{}, errs.Wrap(errs.InvalidInput, "meta: entry field "+m.Key, err)
		}
	}
	if len(e.V) == 0 {
		return Entry{}, errs.New(errs.InvalidInput, "meta: an entry has no v")
	}
	return e, nil
}

// Encode writes the entry as canonical JSON.
func (e Entry) Encode() (value.JSON, error) {
	ms := []canon.Member{{Key: EntryName, Raw: canon.String(e.Name)}, {Key: EntryScope, Raw: canon.String(e.Scope)}, {Key: EntryV, Raw: e.V}}
	if e.Strict {
		ms = append(ms, canon.Member{Key: EntryStrict, Raw: []byte("true")})
	}
	if e.Sensitive {
		ms = append(ms, canon.Member{Key: EntrySensitive, Raw: []byte("true")})
	}
	if len(e.VName) > 0 {
		ms = append(ms, canon.Member{Key: EntryVName, Raw: e.VName})
	}
	ms = append(ms, e.Extra...)
	doc, err := canon.Object(ms)
	if err != nil {
		return value.JSON{}, errs.Wrap(errs.Internal, "meta: encode entry", err)
	}
	return value.JSONOf(doc)
}

// SetPath puts v at the dotted field path inside obj (nil obj = a new
// object), for dotted keys stored as one object value (META-B7-OBJ).
func SetPath(obj []byte, path []string, v []byte) ([]byte, error) {
	if len(path) == 0 {
		return v, nil
	}
	first, rest := path[0], path[1:]
	var ms []canon.Member
	if len(obj) > 0 {
		var err error
		if ms, err = canon.Members(obj); err != nil {
			return nil, errs.New(errs.MetaShape, "meta: the key holds a value that is not an object; unset it before setting fields")
		}
	}
	for i, m := range ms {
		if m.Key == first {
			inner, err := SetPath(m.Raw, rest, v)
			if err != nil {
				return nil, err
			}
			ms[i].Raw = inner
			return compactObject(ms)
		}
	}
	inner, err := SetPath(nil, rest, v)
	if err != nil {
		return nil, err
	}
	return compactObject(append(ms, canon.Member{Key: first, Raw: inner}))
}

// UnsetPath removes the dotted field path from obj; empty reports that
// nothing is left (the key goes), found that the path existed.
func UnsetPath(obj []byte, path []string) (out []byte, empty, found bool, err error) {
	if len(path) == 0 {
		return obj, false, false, nil
	}
	first, rest := path[0], path[1:]
	ms, err := canon.Members(obj)
	if err != nil {
		return nil, false, false, nil
	}
	for i, m := range ms {
		if m.Key != first {
			continue
		}
		if len(rest) == 0 {
			ms = append(ms[:i], ms[i+1:]...)
		} else {
			inner, e, f, ierr := UnsetPath(m.Raw, rest)
			if ierr != nil || !f {
				return obj, false, f, ierr
			}
			if e {
				ms = append(ms[:i], ms[i+1:]...)
			} else {
				ms[i].Raw = inner
			}
		}
		if len(ms) == 0 {
			return nil, true, true, nil
		}
		o, cerr := compactObject(ms)
		return o, false, true, cerr
	}
	return obj, false, false, nil
}

// compactObject builds a canonical compact object.
func compactObject(ms []canon.Member) ([]byte, error) {
	doc, err := canon.Object(ms)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "meta: build object", err)
	}
	return canon.Compact(doc)
}

// Limits (3.13): WarnBytes is the "looks like content" warning; DefaultMaxBytes
// applies when no policy sets meta.max_bytes.
const (
	WarnBytes       = 4096
	DefaultMaxBytes = 16384
)

// Size measures a row's metadata (D22): the canonical JSON of {name: v}
// over every entry; ids, scope and strict labels do not count.
func Size(entries []Entry) (int, error) {
	ms := make([]canon.Member, 0, len(entries))
	seen := map[string]int{}
	for _, e := range entries {
		k := e.Name
		if n := seen[k]; n > 0 {
			k += "#" + strconv.Itoa(n) // two keys of one name from two places
		}
		seen[e.Name]++
		ms = append(ms, canon.Member{Key: k, Raw: e.V})
	}
	b, err := compactObject(ms)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

// CheckSize refuses past max (E_META_SIZE) and warns past WarnBytes.
func CheckSize(entries []Entry, maxBytes int64) (warning string, err error) {
	return CheckSizeWarn(entries, maxBytes, WarnBytes)
}

// CheckSizeWarn is CheckSize with the team's meta.warn_bytes (OV56;
// warnBytes <= 0 means WarnBytes).
func CheckSizeWarn(entries []Entry, maxBytes, warnBytes int64) (warning string, err error) {
	if warnBytes <= 0 {
		warnBytes = WarnBytes
	}
	n, err := Size(entries)
	if err != nil {
		return "", err
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if int64(n) > maxBytes {
		return "", errs.New(errs.MetaSize, "meta: the row's metadata would be "+strconv.Itoa(n)+" bytes; the limit is "+
			strconv.FormatInt(maxBytes, 10)+" (meta.max_bytes)").WithHint("move the content to the body or a memory")
	}
	if int64(n) > warnBytes {
		return "the row's metadata is " + strconv.Itoa(n) + " bytes; past " + strconv.FormatInt(warnBytes, 10) + " it looks like content (put long text in the body or a memory)", nil
	}
	return "", nil
}

// Show renders a value for a line of output: text unquoted, a list joined
// with " | ", sensitive masked.
func Show(e Entry) string {
	if e.Sensitive {
		return "•••"
	}
	raw := e.V
	if len(e.VName) > 0 {
		raw = e.VName
	}
	switch canon.KindOf(raw) {
	case canon.KindString:
		s, err := unquote(raw)
		if err == nil {
			return s
		}
	case canon.KindArray:
		items, err := canon.Elements(raw)
		if err == nil {
			parts := make([]string, len(items))
			for i, it := range items {
				parts[i] = Show(Entry{V: it})
			}
			return strings.Join(parts, " | ")
		}
	case canon.KindInvalid, canon.KindNull, canon.KindBool, canon.KindNumber, canon.KindObject:
	}
	return string(raw)
}
